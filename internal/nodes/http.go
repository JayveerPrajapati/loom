package nodes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"loom/internal/egress"
	"loom/internal/expr"
	"loom/internal/flow"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type httpNode struct{}

func (httpNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	method := strings.ToUpper(strings.TrimSpace(expr.RenderWithScope(str(in.Config["method"]), scope)))
	if method == "" {
		method = "GET"
	}
	rawURL := expr.RenderWithScope(str(in.Config["url"]), scope)
	pathParams := renderMap(in.Config["pathParams"], scope)
	for key, value := range pathParams {
		rawURL = strings.ReplaceAll(rawURL, "{"+key+"}", url.PathEscape(fmt.Sprint(value)))
		rawURL = strings.ReplaceAll(rawURL, ":"+key, url.PathEscape(fmt.Sprint(value)))
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("http.request: invalid url: %w", err)
	}
	query := parsed.Query()
	for key, value := range renderMap(in.Config["query"], scope) {
		query.Set(key, fmt.Sprint(value))
	}
	parsed.RawQuery = query.Encode()

	headers := renderStringMap(in.Config["headers"], scope)
	authMode := strings.ToLower(strings.TrimSpace(str(in.Config["auth"])))
	var authHeaders map[string]string
	if authMode == "oauth2" {
		// The oauth2 credential provides the bearer token via the host,
		// not a static header.
		authHeaders = map[string]string{}
	} else {
		authHeaders, err = httpAuthHeaders(authMode, in.CredentialData["http"])
		if err != nil {
			return nil, err
		}
	}
	outbound, err := httpOutboundAuth(in, authMode)
	if err != nil {
		return nil, err
	}
	for key, value := range authHeaders {
		if _, exists := headers[key]; !exists {
			headers[key] = value
		}
	}

	// Retries with backoff are the engine's per-node retry policy
	// (node.Retry: maxAttempts, backoffMs, maxDelayMs); each attempt gets
	// its own effect tape, so replays verify every retry. The node layer
	// stays single-shot per HTTP call.
	if pag := parsePagination(in.Config["pagination"]); pag != nil {
		return paginatedExecute(ctx, in, scope, method, parsed, query, headers, pag)
	}

	body, bodyRaw, contentType, err := httpBody(in, scope)
	if err != nil {
		return nil, err
	}

	var timeout time.Duration
	if seconds := num(in.Config["timeout"]); seconds > 0 {
		timeout = time.Duration(seconds * float64(time.Second))
	}

	trace(in, "request", map[string]any{
		"method": method, "url": parsed.String(), "pathParams": pathParams,
		"query": query, "headers": redactHeaders(headers), "body": body, "rawBody": string(bodyRaw),
	})
	// Credential egress restriction (D7-1, credential layer): enforced in
	// the node where the credential is parsed — the host never sees
	// credential data. Denial happens before any network I/O.
	if err := checkCredentialEgress(outbound, parsed); err != nil {
		return nil, err
	}
	started := time.Now()
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: method, URL: parsed.String(), Headers: headers, Body: body,
		BodyRaw: bodyRaw, ContentType: contentType, Timeout: timeout,
		IdempotencyKey: in.IdempotencyKey(),
		OAuth2:         outbound.oauth2, Proxy: outbound.proxy,
	})
	if err != nil {
		return nil, err
	}
	duration := time.Since(started)
	success := statusMatches(resp.Status, str(in.Config["successCodes"]))
	responseTrace := map[string]any{
		"status": resp.Status, "statusText": resp.StatusText,
		"headers": redactHeaders(resp.Headers), "body": resp.Body,
		"durationMs": duration.Milliseconds(), "ok": success,
	}
	trace(in, "response", responseTrace)
	failOnError, _ := in.Config["failOnError"].(bool)
	if failOnError && !success {
		return nil, fmt.Errorf("http.request: status %d is not in success codes %q: %s",
			resp.Status, str(in.Config["successCodes"]), previewValue(resp.Body))
	}
	// GraphQL responses can carry errors with a 200 status; failOnError
	// must not miss them, and without it the errors array stays in the
	// body for downstream handling.
	if failOnError && isGraphQL(in.Config) {
		if message := firstGraphQLError(resp.Body); message != "" {
			return nil, fmt.Errorf("http.request: graphql error: %s", message)
		}
	}

	// The parsed response body wins over the envelope: a body field named
	// "ok", "status" or "headers" must not be destroyed. statusCode and
	// statusText are collision-free aliases that are always present.
	item := resp.Body
	if item == nil {
		item = flow.Item{}
	}
	setIfAbsent(item, "status", float64(resp.Status))
	setIfAbsent(item, "headers", resp.Headers)
	setIfAbsent(item, "url", parsed.String())
	setIfAbsent(item, "method", method)
	setIfAbsent(item, "durationMs", float64(duration.Milliseconds()))
	setIfAbsent(item, "ok", success)
	item["statusCode"] = resp.Status
	item["statusText"] = resp.StatusText
	if err := applyResponseMode(in, resp, item); err != nil {
		return nil, err
	}
	return flow.Output{"main": item}, nil
}

// parsePagination reads the pagination config object. A nil result means no
// pagination (the common case) and must keep the single-request behavior.
func parsePagination(raw any) *paginationConfig {
	config, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	pag := &paginationConfig{
		mode:        strings.ToLower(strings.TrimSpace(str(config["mode"]))),
		itemsPath:   str(config["itemsPath"]),
		pageParam:   str(config["pageParam"]),
		offsetParam: str(config["offsetParam"]),
		limitParam:  str(config["limitParam"]),
		cursorParam: str(config["cursorParam"]),
		cursorPath:  str(config["cursorPath"]),
		until:       str(config["until"]),
		startPage:   int(num(config["startPage"])),
		limit:       int(num(config["limit"])),
		maxPages:    int(num(config["maxPages"])),
		maxItems:    int(num(config["maxItems"])),
	}
	if pag.pageParam == "" {
		pag.pageParam = "page"
	}
	if pag.offsetParam == "" {
		pag.offsetParam = "offset"
	}
	if pag.cursorParam == "" {
		pag.cursorParam = "cursor"
	}
	if pag.startPage <= 0 {
		pag.startPage = 1
	}
	if pag.maxPages <= 0 {
		pag.maxPages = defaultMaxPages
	}
	if pag.maxItems <= 0 {
		pag.maxItems = defaultMaxItems
	}
	return pag
}

type paginationConfig struct {
	mode                    string
	itemsPath               string
	pageParam, offsetParam  string
	limitParam, cursorParam string
	cursorPath, until       string
	startPage, limit        int
	maxPages, maxItems      int
}

const (
	defaultMaxPages  = 100
	defaultMaxItems  = 10000
	hardMaxPages     = 1000
	hardMaxItems     = 100000
	defaultThreshold = 1 << 20
)

// paginatedExecute walks pages, one guarded HTTP call per page so every
// page is a recorded effect replays verify individually.
func paginatedExecute(ctx context.Context, in flow.Input, scope expr.Scope, method string, parsed *url.URL, query url.Values, headers map[string]string, pag *paginationConfig) (flow.Output, error) {
	if pag.maxPages > hardMaxPages || pag.maxItems > hardMaxItems {
		return nil, fmt.Errorf("http.request: pagination caps are maxPages<=%d, maxItems<=%d", hardMaxPages, hardMaxItems)
	}
	var timeout time.Duration
	if seconds := num(in.Config["timeout"]); seconds > 0 {
		timeout = time.Duration(seconds * float64(time.Second))
	}
	outbound, err := httpOutboundAuth(in, strings.ToLower(strings.TrimSpace(str(in.Config["auth"]))))
	if err != nil {
		return nil, err
	}
	records := []any{}
	pageBlobs := []any{}
	var last flow.HTTPResponse
	var lastDuration time.Duration
	pages := 0
	cursor := ""
	for {
		if pages >= pag.maxPages {
			return nil, fmt.Errorf("http.request: pagination hit the maxPages cap (%d) with more data available; raise pagination.maxPages or fix pagination.until",
				pag.maxPages)
		}
		pageQuery := cloneQuery(query)
		switch pag.mode {
		case "page":
			pageQuery.Set(pag.pageParam, strconv.Itoa(pag.startPage+pages))
			if pag.limit > 0 && pag.limitParam != "" {
				pageQuery.Set(pag.limitParam, strconv.Itoa(pag.limit))
			}
		case "offset":
			if pag.limit <= 0 {
				return nil, fmt.Errorf("http.request: pagination mode offset requires a positive limit (the offset step)")
			}
			pageQuery.Set(pag.offsetParam, strconv.Itoa(pages*pag.limit))
			if pag.limitParam != "" {
				pageQuery.Set(pag.limitParam, strconv.Itoa(pag.limit))
			}
		case "cursor":
			if cursor != "" {
				pageQuery.Set(pag.cursorParam, cursor)
			}
		default:
			return nil, fmt.Errorf("http.request: pagination.mode must be page, offset or cursor (got %q)", pag.mode)
		}
		pageURL := *parsed
		pageURL.RawQuery = pageQuery.Encode()

		// Credential egress restriction applies to every paginated hop
		// too — the follow URL is derived config, not operator intent.
		if err := checkCredentialEgress(outbound, &pageURL); err != nil {
			return nil, fmt.Errorf("http.request: page %d: %w", pages+1, err)
		}
		started := time.Now()
		resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
			Method: method, URL: pageURL.String(), Headers: headers,
			Timeout: timeout, IdempotencyKey: in.IdempotencyKey() + fmt.Sprintf("-p%d", pages),
			OAuth2: outbound.oauth2, Proxy: outbound.proxy,
		})
		if err != nil {
			return nil, fmt.Errorf("http.request: page %d: %w", pages+1, err)
		}
		last = resp
		lastDuration = time.Since(started)
		success := statusMatches(resp.Status, str(in.Config["successCodes"]))
		trace(in, "response", map[string]any{
			"status": resp.Status, "statusText": resp.StatusText,
			"headers": redactHeaders(resp.Headers), "page": pages + 1,
			"durationMs": lastDuration.Milliseconds(), "ok": success,
		})
		if !success {
			// A failed page mid-loop must fail the node: continuing would
			// silently truncate the result set.
			return nil, fmt.Errorf("http.request: page %d status %d is not in success codes %q: %s",
				pages+1, resp.Status, str(in.Config["successCodes"]), previewValue(resp.Body))
		}

		pageRecords, err := pageItems(resp.Body, pag)
		if err != nil {
			return nil, err
		}
		pages++

		// Per-page blob spill keeps large pages out of the item stream.
		if hash, size, spill, err := spillResponse(in, resp); err != nil {
			return nil, err
		} else if spill {
			pageBlobs = append(pageBlobs, flow.Item{
				"page": float64(pages), "blob": hash, "size": float64(size),
				"contentType": responseContentType(resp),
			})
		}

		records = append(records, pageRecords...)
		if len(records) > pag.maxItems {
			return nil, fmt.Errorf("http.request: pagination hit the maxItems cap (%d); raise pagination.maxItems or narrow itemsPath",
				pag.maxItems)
		}

		if pag.until != "" {
			pageScope := expr.Scope{Item: resp.Body}
			done, err := expr.EvalBoolWithScope(ctx, pag.until, pageScope)
			if err != nil {
				return nil, fmt.Errorf("http.request: pagination.until: %w", err)
			}
			if done {
				return paginationOutput(method, parsed, last, lastDuration, records, pageBlobs, pages), nil
			}
		}

		switch pag.mode {
		case "cursor":
			next, ok := flow.LookupPath(resp.Body, pag.cursorPath)
			if !ok {
				return nil, fmt.Errorf("http.request: pagination cursorPath %q not found in page %d", pag.cursorPath, pages)
			}
			text := strings.TrimSpace(fmt.Sprint(next))
			if text == "" || text == "<nil>" {
				return paginationOutput(method, parsed, last, lastDuration, records, pageBlobs, pages), nil
			}
			cursor = text
		default:
			if len(pageRecords) == 0 {
				return paginationOutput(method, parsed, last, lastDuration, records, pageBlobs, pages), nil
			}
		}
	}
}

func paginationOutput(method string, parsed *url.URL, last flow.HTTPResponse, duration time.Duration, records []any, pageBlobs []any, pages int) flow.Output {
	item := flow.Item{
		"items":      records,
		"itemsCount": float64(len(records)),
		"pages":      float64(pages),
		"statusCode": last.Status,
		"statusText": last.StatusText,
		"headers":    last.Headers,
		"url":        parsed.String(),
		"method":     method,
		"durationMs": float64(duration.Milliseconds()),
		"ok":         true,
	}
	if len(pageBlobs) > 0 {
		item["pageBlobs"] = pageBlobs
	}
	return flow.Output{"main": item}
}

// pageItems extracts one page's records: itemsPath wins, then the host's
// items convention (JSON arrays decode to {"items": [...]}), else the whole
// body is a single record.
func pageItems(body flow.Item, pag *paginationConfig) ([]any, error) {
	if pag.itemsPath != "" {
		value, ok := flow.LookupPath(body, pag.itemsPath)
		if !ok {
			return nil, fmt.Errorf("http.request: pagination itemsPath %q not found in page", pag.itemsPath)
		}
		arr, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("http.request: pagination itemsPath %q is not an array", pag.itemsPath)
		}
		return arr, nil
	}
	if items, ok := body["items"].([]any); ok {
		return items, nil
	}
	return []any{body}, nil
}

func cloneQuery(src url.Values) url.Values {
	dst := make(url.Values, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

// applyResponseMode spills a single response into the blob store when the
// node asks for it, replacing the inline body with a reference the blob API
// can serve (GET /api/blobs/{hash}).
func applyResponseMode(in flow.Input, resp flow.HTTPResponse, item flow.Item) error {
	mode := strings.ToLower(strings.TrimSpace(str(in.Config["responseMode"])))
	if mode == "" || mode == "inline" {
		return nil
	}
	threshold := defaultThreshold
	if set := int(num(in.Config["inlineThresholdBytes"])); set > 0 {
		threshold = set
	}
	raw := responseRaw(resp)
	if mode == "auto" && len(raw) <= threshold {
		return nil
	}
	saver, ok := in.Host.(flow.BlobSaver)
	if !ok {
		return fmt.Errorf("http.request: responseMode %q requires a host with blob storage", mode)
	}
	hash, size, err := saver.SaveBlob(raw)
	if err != nil {
		return fmt.Errorf("http.request: save response blob: %w", err)
	}
	contentType := responseContentType(resp)
	// Drop the parsed body and keep the envelope plus the blob reference.
	for _, key := range bodyKeys(item) {
		delete(item, key)
	}
	item["blob"] = hash
	item["blobSize"] = float64(size)
	item["contentType"] = contentType
	return nil
}

// spillResponse saves a page body when the config asks for blob spill and
// reports whether it did.
func spillResponse(in flow.Input, resp flow.HTTPResponse) (string, int, bool, error) {
	mode := strings.ToLower(strings.TrimSpace(str(in.Config["responseMode"])))
	if mode == "" || mode == "inline" {
		return "", 0, false, nil
	}
	threshold := defaultThreshold
	if set := int(num(in.Config["inlineThresholdBytes"])); set > 0 {
		threshold = set
	}
	raw := responseRaw(resp)
	if mode == "auto" && len(raw) <= threshold {
		return "", 0, false, nil
	}
	saver, ok := in.Host.(flow.BlobSaver)
	if !ok {
		return "", 0, false, fmt.Errorf("http.request: responseMode %q requires a host with blob storage", mode)
	}
	hash, size, err := saver.SaveBlob(raw)
	if err != nil {
		return "", 0, false, fmt.Errorf("http.request: save response blob: %w", err)
	}
	return hash, size, true, nil
}

// envelopeKeys are the keys the output envelope owns; everything else came
// from the parsed body and is dropped on a blob spill.
var envelopeKeys = map[string]bool{
	"status": true, "statusCode": true, "statusText": true, "headers": true,
	"url": true, "method": true, "durationMs": true, "ok": true,
	"items": true, "itemsCount": true, "pages": true, "pageBlobs": true,
	"blob": true, "blobSize": true, "contentType": true,
}

func bodyKeys(item flow.Item) []string {
	var keys []string
	for key := range item {
		if !envelopeKeys[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// responseRaw returns faithful response bytes. Live hosts set Raw; replays
// reconstruct from the parsed body (SaveBlob on replay returns the taped
// hash, so the reconstruction only feeds determinism, not verification).
func responseRaw(resp flow.HTTPResponse) []byte {
	if resp.Raw != nil {
		return resp.Raw
	}
	if text, ok := resp.Body["body"].(string); ok && len(resp.Body) <= 2 {
		return []byte(text)
	}
	if data, err := json.Marshal(resp.Body); err == nil {
		return data
	}
	return nil
}

func responseContentType(resp flow.HTTPResponse) string {
	if resp.Headers != nil {
		if ct, ok := resp.Headers["content-type"]; ok {
			return ct
		}
	}
	return "application/octet-stream"
}

func httpBody(in flow.Input, scope expr.Scope) (any, []byte, string, error) {
	bodyType := strings.ToLower(strings.TrimSpace(str(in.Config["bodyType"])))
	if bodyType == "multipart" {
		return multipartBody(in, scope)
	}
	config := in.Config
	switch bodyType {
	case "none":
		return nil, nil, "", nil
	case "raw":
		raw := expr.RenderWithScope(str(config["rawBody"]), scope)
		if raw == "" {
			if body, ok := config["body"]; ok && body != nil {
				rendered := expr.RenderValueWithScope(body, scope)
				encoded, err := json.Marshal(rendered)
				if err != nil {
					return nil, nil, "", fmt.Errorf("http.request: encode body: %w", err)
				}
				raw = string(encoded)
			}
		}
		contentType := str(config["contentType"])
		if contentType == "" {
			contentType = "text/plain; charset=utf-8"
		}
		return nil, []byte(raw), contentType, nil
	case "form":
		values := url.Values{}
		for key, value := range renderMap(config["body"], scope) {
			values.Set(key, fmt.Sprint(value))
		}
		return nil, []byte(values.Encode()), "application/x-www-form-urlencoded", nil
	case "json":
		return expr.RenderValueWithScope(config["body"], scope), nil, "application/json", nil
	case "graphql":
		// POST {query, variables} as JSON. The query text and variables
		// are rendered like every other templated field.
		query := expr.RenderWithScope(str(config["graphqlQuery"]), scope)
		if query == "" {
			return nil, nil, "", fmt.Errorf("http.request: bodyType graphql requires graphqlQuery")
		}
		payload := flow.Item{"query": query}
		if raw, ok := config["graphqlVariables"]; ok && raw != nil {
			payload["variables"] = expr.RenderValueWithScope(raw, scope)
		}
		return payload, nil, "application/json", nil
	default:
		raw, ok := config["body"]
		if !ok {
			return nil, nil, "", nil
		}
		return expr.RenderValueWithScope(raw, scope), nil, str(config["contentType"]), nil
	}
}

// multipartBody builds a multipart/form-data body from config parts. The
// boundary is derived from the part content, so the same inputs always
// produce the same request — a replayed node rebuilds a byte-identical
// body and the tape verifies it by hash.
func multipartBody(in flow.Input, scope expr.Scope) (any, []byte, string, error) {
	parts, ok := in.Config["parts"].([]any)
	if !ok || len(parts) == 0 {
		return nil, nil, "", fmt.Errorf("http.request: bodyType multipart requires a non-empty parts array")
	}

	type builtPart struct {
		name, filename, contentType string
		data                        []byte
	}
	built := make([]builtPart, 0, len(parts))
	digest := sha256.New()
	for i, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, "", fmt.Errorf("http.request: parts[%d] must be an object", i)
		}
		name := expr.RenderWithScope(str(part["name"]), scope)
		if name == "" {
			return nil, nil, "", fmt.Errorf("http.request: parts[%d].name is required", i)
		}
		var data []byte
		switch strings.ToLower(strings.TrimSpace(str(part["type"]))) {
		case "", "text":
			data = []byte(expr.RenderWithScope(str(part["value"]), scope))
		case "json":
			rendered := expr.RenderValueWithScope(part["value"], scope)
			encoded, err := json.Marshal(rendered)
			if err != nil {
				return nil, nil, "", fmt.Errorf("http.request: parts[%d] encode json: %w", i, err)
			}
			data = encoded
		case "blob":
			loader, ok := in.Host.(flow.BlobLoader)
			if !ok {
				return nil, nil, "", fmt.Errorf("http.request: parts[%d] blob reference requires a host with blob storage", i)
			}
			hash := strings.TrimSpace(str(part["value"]))
			if hash == "" {
				return nil, nil, "", fmt.Errorf("http.request: parts[%d].value must be a blob hash", i)
			}
			loaded, err := loader.LoadBlob(hash)
			if err != nil {
				return nil, nil, "", fmt.Errorf("http.request: parts[%d] load blob: %w", i, err)
			}
			data = loaded
		default:
			return nil, nil, "", fmt.Errorf("http.request: parts[%d].type must be text, json or blob", i)
		}
		digest.Write([]byte(name))
		digest.Write([]byte{0})
		digest.Write(data)
		built = append(built, builtPart{
			name:        name,
			filename:    expr.RenderWithScope(str(part["filename"]), scope),
			contentType: str(part["contentType"]),
			data:        data,
		})
	}
	boundary := "loom-" + hex.EncodeToString(digest.Sum(nil))[:32]

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, nil, "", fmt.Errorf("http.request: multipart boundary: %w", err)
	}
	for _, part := range built {
		header := textproto.MIMEHeader{}
		if part.filename != "" {
			header.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q; filename=%q", part.name, part.filename))
		} else {
			header.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q", part.name))
		}
		if part.contentType != "" {
			header.Set("Content-Type", part.contentType)
		}
		field, err := writer.CreatePart(header)
		if err != nil {
			return nil, nil, "", fmt.Errorf("http.request: multipart part %q: %w", part.name, err)
		}
		if _, err := field.Write(part.data); err != nil {
			return nil, nil, "", fmt.Errorf("http.request: multipart part %q: %w", part.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, nil, "", fmt.Errorf("http.request: multipart close: %w", err)
	}
	return nil, buf.Bytes(), "multipart/form-data; boundary=" + boundary, nil
}

// checkCredentialEgress enforces the http credential's allowedHosts
// restriction against the request target. Empty restrictions pass.
func checkCredentialEgress(out outboundAuth, target *url.URL) error {
	if len(out.allowedHosts) == 0 {
		return nil
	}
	hostPort := target.Hostname()
	if strings.Contains(hostPort, ":") {
		hostPort = "[" + hostPort + "]"
	}
	if p := target.Port(); p != "" {
		hostPort += ":" + p
	}
	return egress.CredentialAllows(out.credentialName, out.allowedHosts, hostPort)
}

// httpOutboundAuth captures host-level auth and proxy settings applied to
// every outbound request of the node. OAuth2 params carry the client
// secret, so they deliberately never enter the recorded effect payload.
// It also carries the http credential's egress restriction (allowedHosts,
// D7-1's credential layer): the node enforces it against every target it
// dials so a stolen or mis-scoped credential cannot be pointed at an
// arbitrary destination.
func httpOutboundAuth(in flow.Input, authMode string) (outboundAuth, error) {
	var out outboundAuth
	if authMode == "oauth2" {
		params, err := httpOAuth2(in.CredentialData["oauth2"])
		if err != nil {
			return out, err
		}
		out.oauth2 = params
	}
	httpCred := in.CredentialData["http"]
	if proxy := credentialString(httpCred.Data["proxyUrl"]); proxy != "" {
		out.proxy = strings.TrimSpace(proxy)
	}
	out.allowedHosts = credentialStringList(httpCred.Data["allowedHosts"])
	out.credentialName = in.Credentials["http"]
	return out, nil
}

type outboundAuth struct {
	oauth2 *flow.OAuth2Params
	proxy  string
	// allowedHosts is the http credential's destination restriction
	// ("host" / "*.host" entries, optionally port-bearing). Empty means
	// the credential imposes no restriction of its own.
	allowedHosts   []string
	credentialName string
}

// httpOAuth2 builds the client-credentials token request from an oauth2
// credential (tokenUrl, clientId, clientSecret, scopes).
func httpOAuth2(cred flow.Credential) (*flow.OAuth2Params, error) {
	params := &flow.OAuth2Params{
		TokenURL:     strings.TrimSpace(credentialString(cred.Data["tokenUrl"])),
		ClientID:     strings.TrimSpace(credentialString(cred.Data["clientId"])),
		ClientSecret: credentialString(cred.Data["clientSecret"]),
		Scope:        strings.TrimSpace(credentialString(cred.Data["scopes"])),
	}
	if cred.Type == "" {
		return nil, fmt.Errorf("http.request: auth \"oauth2\" requires an oauth2 credential (tokenUrl, clientId, clientSecret)")
	}
	if params.TokenURL == "" || params.ClientID == "" {
		return nil, fmt.Errorf("http.request: oauth2 credential needs tokenUrl and clientId")
	}
	return params, nil
}

func isGraphQL(config map[string]any) bool {
	return strings.ToLower(strings.TrimSpace(str(config["bodyType"]))) == "graphql"
}

// firstGraphQLError returns the first error message from a GraphQL response
// body, or "" when the response has no errors array.
func firstGraphQLError(body flow.Item) string {
	errs, ok := body["errors"].([]any)
	if !ok || len(errs) == 0 {
		return ""
	}
	if first, ok := errs[0].(map[string]any); ok {
		if message := credentialString(first["message"]); message != "" {
			return message
		}
	}
	return "graphql response contains errors"
}

func httpAuthHeaders(mode string, cred flow.Credential) (map[string]string, error) {
	if mode == "none" {
		return map[string]string{}, nil
	}
	if cred.Type == "" {
		if mode == "" || mode == "credential" {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("http.request: auth %q requires a credential", mode)
	}
	headers := map[string]string{}
	if raw, ok := cred.Data["headers"].(map[string]any); ok {
		for key, value := range raw {
			headers[key] = fmt.Sprint(value)
		}
	}
	if token := credentialString(cred.Data["token"]); token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if apiKey := credentialString(cred.Data["apiKey"]); apiKey != "" {
		name := credentialString(cred.Data["apiKeyHeader"])
		if name == "" {
			name = "X-API-Key"
		}
		headers[name] = apiKey
	}
	if username := credentialString(cred.Data["username"]); username != "" {
		password := credentialString(cred.Data["password"])
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	}
	if len(headers) == 0 && strings.TrimSpace(cred.Raw) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(cred.Raw)
	}
	if mode == "bearer" && !strings.HasPrefix(headers["Authorization"], "Bearer ") {
		return nil, fmt.Errorf("http.request: credential %q has no bearer token", cred.Type)
	}
	if mode == "basic" && !strings.HasPrefix(headers["Authorization"], "Basic ") {
		return nil, fmt.Errorf("http.request: credential %q has no basic username/password", cred.Type)
	}
	if mode == "apiKey" && credentialString(cred.Data["apiKey"]) == "" {
		return nil, fmt.Errorf("http.request: credential %q has no API key", cred.Type)
	}
	return headers, nil
}

func statusMatches(status int, spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return status >= 200 && status < 300
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if strings.HasSuffix(part, "xx") {
			if n, err := strconv.Atoi(strings.TrimSuffix(part, "xx")); err == nil && status/100 == n {
				return true
			}
			continue
		}
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			low, err1 := strconv.Atoi(strings.TrimSpace(bounds[0]))
			high, err2 := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err1 == nil && err2 == nil && status >= low && status <= high {
				return true
			}
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && status == n {
			return true
		}
	}
	return false
}
