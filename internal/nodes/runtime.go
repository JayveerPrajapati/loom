package nodes

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"loom/internal/expr"
	"loom/internal/flow"
)

// Registry returns one stateless implementation per catalog type.
func Registry() map[string]flow.Node {
	return map[string]flow.Node{
		"trigger.manual":  triggerNode{typ: "trigger.manual"},
		"trigger.webhook": triggerNode{typ: "trigger.webhook"},
		"trigger.cron":    triggerNode{typ: "trigger.cron"},
		"trigger.subflow": triggerNode{typ: "trigger.subflow"},
		"http.request":    httpNode{},
		"code.run":        codeNode{},
		"data.set":        setNode{},
		"flow.if":         ifNode{},
		"flow.switch":     switchNode{},
		"flow.merge":      mergeNode{},
		"flow.loop":       loopNode{},
		"flow.wait":       waitNode{},
		"flow.approval":   approvalNode{},
		"flow.subflow":    subflowNode{},
		"db.postgres":     postgresNode{},
		"chat.slack":      slackNode{},
		"sheets.append":   sheetsNode{},
		"chat.discord":    discordNode{},
		"chat.teams":      teamsNode{},
		"email.resend":    resendNode{},
		"crm.hubspot":     hubspotNode{},
		"ticket.jira":     jiraNode{},
		"ai.chat":         aiChatNode{},
		"ai.classify":     aiClassifyNode{},
		"ai.extract":      aiExtractNode{},
		"ai.embed":        aiEmbedNode{},
		"data.json.parse": jsonParseNode{},
		"data.csv.parse":  csvParseNode{},
		"text.template":   templateNode{},
		"crypto.hmac":     hmacNode{},
		"random.uuid":     uuidNode{},
		"date.format":     dateFormatNode{},
	}
}

type triggerNode struct{ typ string }

func (n triggerNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	item := flow.CloneItem(in.Item)
	if n.typ == "trigger.cron" {
		now := in.Now
		if now.IsZero() {
			now = time.Now()
		}
		if _, ok := item["now"]; !ok {
			item["now"] = now.UTC().Format(time.RFC3339)
		}
	}
	return flow.Output{"main": item}, nil
}

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
	authHeaders, err := httpAuthHeaders(authMode, in.CredentialData["http"])
	if err != nil {
		return nil, err
	}
	for key, value := range authHeaders {
		if _, exists := headers[key]; !exists {
			headers[key] = value
		}
	}

	body, bodyRaw, contentType, err := httpBody(in.Config, scope)
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
	started := time.Now()
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: method, URL: parsed.String(), Headers: headers, Body: body,
		BodyRaw: bodyRaw, ContentType: contentType, Timeout: timeout,
		IdempotencyKey: in.IdempotencyKey(),
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

	item := resp.Body
	if item == nil {
		item = flow.Item{}
	}
	item["status"] = float64(resp.Status)
	item["statusText"] = resp.StatusText
	item["headers"] = resp.Headers
	item["url"] = parsed.String()
	item["method"] = method
	item["durationMs"] = float64(duration.Milliseconds())
	item["ok"] = success
	return flow.Output{"main": item}, nil
}

func renderMap(value any, scope expr.Scope) map[string]any {
	out := map[string]any{}
	raw, ok := value.(map[string]any)
	if !ok {
		return out
	}
	for key, inner := range raw {
		out[key] = expr.RenderValueWithScope(inner, scope)
	}
	return out
}

func renderStringMap(value any, scope expr.Scope) map[string]string {
	out := map[string]string{}
	for key, inner := range renderMap(value, scope) {
		out[key] = fmt.Sprint(inner)
	}
	return out
}

func httpBody(config map[string]any, scope expr.Scope) (any, []byte, string, error) {
	bodyType := strings.ToLower(strings.TrimSpace(str(config["bodyType"])))
	switch bodyType {
	case "none":
		return nil, nil, "", nil
	case "raw":
		raw := expr.RenderWithScope(str(config["rawBody"]), scope)
		if raw == "" {
			raw = expr.RenderWithScope(str(config["body"]), scope)
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
	default:
		raw, ok := config["body"]
		if !ok {
			return nil, nil, "", nil
		}
		return expr.RenderValueWithScope(raw, scope), nil, str(config["contentType"]), nil
	}
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

func credentialString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func statusMatches(status int, spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return status >= 200 && status < 300
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
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

func previewValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	if len(data) > 2000 {
		return string(data[:2000]) + "…"
	}
	return string(data)
}

type codeNode struct{}

func (codeNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	item, err := expr.EvalObjectWithScope(ctx, str(in.Config["source"]), expr.FromInput(in))
	if err != nil {
		return nil, err
	}
	return flow.Output{"main": item}, nil
}

type setNode struct{}

func (setNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	out := flow.CloneItem(in.Item)
	if keepOnly, _ := in.Config["keepOnlySet"].(bool); keepOnly {
		out = flow.Item{}
	}
	if values, ok := in.Config["values"].(map[string]any); ok {
		for k, v := range values {
			out[k] = expr.RenderValueWithScope(v, scope)
		}
	}
	return flow.Output{"main": out}, nil
}

type ifNode struct{}

func (ifNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	ok, err := expr.EvalBoolWithScope(ctx, str(in.Config["condition"]), expr.FromInput(in))
	if err != nil {
		return nil, err
	}
	port := "false"
	if ok {
		port = "true"
	}
	return flow.Output{port: in.Item}, nil
}

type switchNode struct{}

func (switchNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	cases, _ := in.Config["cases"].([]any)
	for i, raw := range cases {
		expression, ok := raw.(string)
		if !ok {
			continue
		}
		match, err := expr.EvalBoolWithScope(ctx, expression, scope)
		if err != nil {
			return nil, err
		}
		if match {
			port := fmt.Sprintf("case%d", i)
			if i > 2 {
				port = "default"
			}
			return flow.Output{port: in.Item}, nil
		}
	}
	return flow.Output{"default": in.Item}, nil
}

type mergeNode struct{}

func (mergeNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	return flow.Output{"main": in.Item}, nil
}

type loopNode struct{}

func (loopNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	workflowID := expr.RenderWithScope(str(in.Config["workflowId"]), scope)
	if workflowID == "" {
		return nil, fmt.Errorf("flow.loop: workflowId is required")
	}

	var raw any
	if path := expr.RenderWithScope(str(in.Config["itemsPath"]), scope); path != "" {
		value, ok := flow.LookupPath(in.Item, path)
		if !ok {
			return nil, fmt.Errorf("flow.loop: itemsPath %q not found", path)
		}
		raw = value
	} else if value, ok := in.Item["items"]; ok {
		raw = value
	} else {
		raw = in.Item
	}

	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("flow.loop: expected an array, got %T", raw)
	}

	max := int(num(in.Config["maxIterations"]))
	if max <= 0 || max > len(arr) {
		max = len(arr)
	}

	concurrency := int(num(in.Config["concurrency"]))
	if concurrency <= 0 {
		concurrency = 1
	}

	results := make([]any, max)
	if concurrency == 1 {
		for i := 0; i < max; i++ {
			item := flow.Item{"value": arr[i]}
			switch v := arr[i].(type) {
			case map[string]any:
				item = flow.Item(v)
			case flow.Item:
				item = v
			}
			child, err := in.Subflow.RunSubflow(ctx, workflowID, item)
			if err != nil {
				return nil, fmt.Errorf("flow.loop iteration %d: %w", i, err)
			}
			results[i] = map[string]any(child)
		}
	} else {
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		var firstErr error
		var errMu sync.Mutex

		for i := 0; i < max; i++ {
			errMu.Lock()
			stop := firstErr != nil
			errMu.Unlock()
			if stop {
				break
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				defer func() { <-sem }()

				item := flow.Item{"value": arr[idx]}
				switch v := arr[idx].(type) {
				case map[string]any:
					item = flow.Item(v)
				case flow.Item:
					item = v
				}
				child, err := in.Subflow.RunSubflow(ctx, workflowID, item)
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("flow.loop iteration %d: %w", idx, err)
					}
					errMu.Unlock()
					return
				}
				results[idx] = map[string]any(child)
			}(i)
		}
		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}
	}
	return flow.Output{"done": flow.Item{"items": results}}, nil
}

type waitNode struct{}

func (waitNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	seconds := num(in.Config["seconds"])
	if seconds < 0 {
		seconds = 0
	}
	if err := in.Host.Sleep(ctx, time.Duration(seconds*float64(time.Second))); err != nil {
		return nil, err
	}
	return flow.Output{"main": in.Item}, nil
}

type approvalNode struct{}

func (approvalNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	channel := expr.RenderWithScope(str(in.Config["channel"]), scope)
	message := expr.RenderWithScope(str(in.Config["message"]), scope)

	if in.Decision == "" {
		return nil, &flow.Suspend{
			Reason: "approval",
			Data:   map[string]any{"channel": channel, "message": message},
		}
	}
	if in.Decision != "approved" && in.Decision != "rejected" {
		return nil, fmt.Errorf("flow.approval: decision must be approved or rejected, got %q", in.Decision)
	}

	out := flow.CloneItem(in.Item)
	out["approved"] = in.Decision == "approved"
	return flow.Output{in.Decision: out}, nil
}

type subflowNode struct{}

func (subflowNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	workflowID := expr.RenderWithScope(str(in.Config["workflowId"]), expr.FromInput(in))
	if workflowID == "" {
		return nil, fmt.Errorf("flow.subflow: workflowId is required")
	}
	item, err := in.Subflow.RunSubflow(ctx, workflowID, in.Item)
	if err != nil {
		return nil, err
	}
	return flow.Output{"main": item}, nil
}

type postgresNode struct{}

func (postgresNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	query := expr.RenderWithScope(str(in.Config["query"]), expr.FromInput(in))
	trace(in, "request", map[string]any{
		"operation": str(in.Config["operation"]), "query": query,
	})
	rows, err := in.Host.Query(ctx, in.Credentials["postgres"], query)
	if err != nil {
		return nil, err
	}
	trace(in, "response", map[string]any{"rows": len(rows)})
	arr := make([]any, len(rows))
	for i := range rows {
		arr[i] = map[string]any(rows[i])
	}
	return flow.Output{"main": flow.Item{"rows": arr}}, nil
}

type slackNode struct{}

func (slackNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	channel := expr.RenderWithScope(str(in.Config["channel"]), scope)
	text := expr.RenderWithScope(str(in.Config["text"]), scope)
	trace(in, "request", map[string]any{"channel": channel, "text": text})
	if err := in.Host.Slack(ctx, in.Credentials["slack"], channel, text); err != nil {
		return nil, err
	}
	trace(in, "response", map[string]any{"ok": true, "channel": channel})
	return flow.Output{"main": flow.Item{"ok": true, "channel": channel}}, nil
}

type sheetsNode struct{}

func (sheetsNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	id := expr.RenderWithScope(str(in.Config["spreadsheetId"]), scope)
	cellRange := expr.RenderWithScope(str(in.Config["range"]), scope)
	token := credentialField(in, "sheets", "token")
	if token == "" {
		token = credentialField(in, "sheets", "accessToken")
	}
	if token == "" {
		return nil, fmt.Errorf("sheets.append: credential has no token")
	}
	rows := renderRows(in.Config["rows"], scope)
	body := map[string]any{"values": rows}
	trace(in, "request", map[string]any{"spreadsheetId": id, "range": cellRange, "rows": len(rows)})
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: "POST",
		URL: fmt.Sprintf("https://sheets.googleapis.com/v4/spreadsheets/%s/values/%s:append?valueInputOption=USER_ENTERED",
			url.PathEscape(id), url.PathEscape(cellRange)),
		Headers:        map[string]string{"Authorization": "Bearer " + token},
		Body:           body,
		ContentType:    "application/json",
		IdempotencyKey: in.IdempotencyKey(),
	})
	if err != nil {
		return nil, err
	}
	trace(in, "response", map[string]any{"status": resp.Status, "body": resp.Body})
	return flow.Output{"main": flow.Item{"status": resp.Status, "body": resp.Body, "rows": len(rows)}}, nil
}

type discordNode struct{}

func (discordNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	text := expr.RenderWithScope(str(in.Config["text"]), scope)
	webhook := credentialField(in, "discord", "webhookUrl")
	if webhook == "" {
		return nil, fmt.Errorf("chat.discord: credential has no webhookUrl")
	}
	return postWebhook(ctx, in, webhook, map[string]any{"content": text}, text)
}

type teamsNode struct{}

func (teamsNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	text := expr.RenderWithScope(str(in.Config["text"]), scope)
	webhook := credentialField(in, "teams", "webhookUrl")
	if webhook == "" {
		return nil, fmt.Errorf("chat.teams: credential has no webhookUrl")
	}
	return postWebhook(ctx, in, webhook, map[string]any{"text": text}, text)
}

func postWebhook(ctx context.Context, in flow.Input, webhook string, body map[string]any, text string) (flow.Output, error) {
	trace(in, "request", map[string]any{"webhook": webhook, "text": text})
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: "POST", URL: webhook, Body: body, ContentType: "application/json",
		IdempotencyKey: in.IdempotencyKey(),
	})
	if err != nil {
		return nil, err
	}
	trace(in, "response", map[string]any{"status": resp.Status})
	return flow.Output{"main": flow.Item{"ok": resp.Status < 300, "status": resp.Status}}, nil
}

type resendNode struct{}

func (resendNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	token := credentialField(in, "resend", "apiKey")
	if token == "" {
		token = credentialField(in, "resend", "token")
	}
	if token == "" {
		return nil, fmt.Errorf("email.resend: credential has no apiKey")
	}
	payload := map[string]any{
		"from":    expr.RenderWithScope(str(in.Config["from"]), scope),
		"to":      []string{expr.RenderWithScope(str(in.Config["to"]), scope)},
		"subject": expr.RenderWithScope(str(in.Config["subject"]), scope),
	}
	if html := expr.RenderWithScope(str(in.Config["html"]), scope); html != "" {
		payload["html"] = html
	}
	if text := expr.RenderWithScope(str(in.Config["text"]), scope); text != "" {
		payload["text"] = text
	}
	trace(in, "request", map[string]any{"from": payload["from"], "to": payload["to"], "subject": payload["subject"]})
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: "POST", URL: "https://api.resend.com/emails",
		Headers:        map[string]string{"Authorization": "Bearer " + token},
		Body:           payload,
		ContentType:    "application/json",
		IdempotencyKey: in.IdempotencyKey(),
	})
	if err != nil {
		return nil, err
	}
	trace(in, "response", map[string]any{"status": resp.Status, "body": resp.Body})
	return flow.Output{"main": flow.Item{"status": resp.Status, "body": resp.Body}}, nil
}

type hubspotNode struct{}

func (hubspotNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	token := credentialField(in, "hubspot", "apiKey")
	if token == "" {
		token = credentialField(in, "hubspot", "token")
	}
	if token == "" {
		return nil, fmt.Errorf("crm.hubspot: credential has no apiKey")
	}
	properties := map[string]string{}
	for _, key := range []string{"email", "firstname", "lastname", "phone"} {
		if value := expr.RenderWithScope(str(in.Config[key]), scope); value != "" {
			properties[key] = value
		}
	}
	payload := map[string]any{"properties": properties}
	trace(in, "request", map[string]any{"email": properties["email"]})
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: "POST", URL: "https://api.hubapi.com/crm/v3/objects/contacts",
		Headers:        map[string]string{"Authorization": "Bearer " + token},
		Body:           payload,
		ContentType:    "application/json",
		IdempotencyKey: in.IdempotencyKey(),
	})
	if err != nil {
		return nil, err
	}
	trace(in, "response", map[string]any{"status": resp.Status, "body": resp.Body})
	return flow.Output{"main": flow.Item{"status": resp.Status, "body": resp.Body}}, nil
}

type jiraNode struct{}

func (jiraNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	base := strings.TrimRight(expr.RenderWithScope(str(in.Config["baseUrl"]), scope), "/")
	email := credentialField(in, "jira", "email")
	token := credentialField(in, "jira", "token")
	if token == "" {
		token = credentialField(in, "jira", "apiKey")
	}
	if base == "" || token == "" {
		return nil, fmt.Errorf("ticket.jira: baseUrl and credential token are required")
	}
	issueType := expr.RenderWithScope(str(in.Config["issueType"]), scope)
	if issueType == "" {
		issueType = "Task"
	}
	payload := map[string]any{"fields": map[string]any{
		"project":     map[string]any{"key": expr.RenderWithScope(str(in.Config["projectKey"]), scope)},
		"summary":     expr.RenderWithScope(str(in.Config["summary"]), scope),
		"description": expr.RenderWithScope(str(in.Config["description"]), scope),
		"issuetype":   map[string]any{"name": issueType},
	}}
	auth := base64.StdEncoding.EncodeToString([]byte(email + ":" + token))
	trace(in, "request", map[string]any{"baseUrl": base, "summary": payload["fields"].(map[string]any)["summary"]})
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: "POST", URL: base + "/rest/api/3/issue",
		Headers:        map[string]string{"Authorization": "Basic " + auth, "Accept": "application/json"},
		Body:           payload,
		ContentType:    "application/json",
		IdempotencyKey: in.IdempotencyKey(),
	})
	if err != nil {
		return nil, err
	}
	trace(in, "response", map[string]any{"status": resp.Status, "body": resp.Body})
	return flow.Output{"main": flow.Item{"status": resp.Status, "body": resp.Body}}, nil
}

type jsonParseNode struct{}

func (jsonParseNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	text := expr.RenderWithScope(str(in.Config["text"]), expr.FromInput(in))
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, fmt.Errorf("data.json.parse: %w", err)
	}
	switch typed := value.(type) {
	case map[string]any:
		return flow.Output{"main": flow.Item(typed)}, nil
	case []any:
		return flow.Output{"main": flow.Item{"items": typed}}, nil
	default:
		return flow.Output{"main": flow.Item{"value": typed}}, nil
	}
}

type csvParseNode struct{}

func (csvParseNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	text := expr.RenderWithScope(str(in.Config["text"]), expr.FromInput(in))
	delimiter := str(in.Config["delimiter"])
	if delimiter == "" {
		delimiter = ","
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = []rune(delimiter)[0]
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("data.csv.parse: %w", err)
	}
	headers := true
	if value, ok := in.Config["headers"].(bool); ok {
		headers = value
	}
	if !headers || len(records) == 0 {
		rows := make([]any, len(records))
		for i, record := range records {
			rows[i] = record
		}
		return flow.Output{"main": flow.Item{"rows": rows}}, nil
	}
	keys := records[0]
	rows := make([]any, 0, len(records)-1)
	for _, record := range records[1:] {
		row := map[string]any{}
		for i, key := range keys {
			if i < len(record) {
				row[key] = record[i]
			}
		}
		rows = append(rows, row)
	}
	return flow.Output{"main": flow.Item{"rows": rows}}, nil
}

type templateNode struct{}

func (templateNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	text := expr.RenderWithScope(str(in.Config["template"]), expr.FromInput(in))
	return flow.Output{"main": flow.Item{"text": text}}, nil
}

type hmacNode struct{}

func (hmacNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	secret := credentialField(in, "hmac", "secret")
	if secret == "" {
		secret = expr.RenderWithScope(str(in.Config["secret"]), scope)
	}
	if secret == "" {
		return nil, fmt.Errorf("crypto.hmac: secret is required")
	}
	value := expr.RenderWithScope(str(in.Config["value"]), scope)
	algorithm := strings.ToLower(str(in.Config["algorithm"]))
	var mac hashWriter
	switch algorithm {
	case "", "sha256":
		mac = hmac.New(sha256.New, []byte(secret))
	case "sha1":
		mac = hmac.New(sha1.New, []byte(secret))
	case "sha512":
		mac = hmac.New(sha512.New, []byte(secret))
	default:
		return nil, fmt.Errorf("crypto.hmac: unsupported algorithm %q", algorithm)
	}
	mac.Write([]byte(value))
	return flow.Output{"main": flow.Item{"signature": hex.EncodeToString(mac.Sum(nil))}}, nil
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

type uuidNode struct{}

func (uuidNode) Execute(_ context.Context, _ flow.Input) (flow.Output, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("random.uuid: %w", err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
	return flow.Output{"main": flow.Item{"uuid": uuid}}, nil
}

type dateFormatNode struct{}

func (dateFormatNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	value := expr.RenderWithScope(str(in.Config["value"]), expr.FromInput(in))
	var parsed time.Time
	var err error
	if value == "" {
		parsed = in.Now
		if parsed.IsZero() {
			parsed = time.Now()
		}
	} else {
		parsed, err = time.Parse(time.RFC3339, value)
		if err != nil {
			parsed, err = time.Parse("2006-01-02 15:04:05", value)
		}
		if err != nil {
			parsed, err = time.Parse("2006-01-02", value)
		}
		if err != nil {
			return nil, fmt.Errorf("date.format: %w", err)
		}
	}
	if zone := strings.TrimSpace(str(in.Config["timezone"])); zone != "" {
		location, loadErr := time.LoadLocation(zone)
		if loadErr != nil {
			return nil, fmt.Errorf("date.format: %w", loadErr)
		}
		parsed = parsed.In(location)
	}
	layout := str(in.Config["layout"])
	if layout == "" {
		layout = time.RFC3339
	}
	return flow.Output{"main": flow.Item{"date": parsed.Format(layout)}}, nil
}

type aiChatNode struct{}

func (aiChatNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	prompt := expr.RenderWithScope(str(in.Config["prompt"]), scope)
	system := expr.RenderWithScope(str(in.Config["system"]), scope)
	messages := make([]any, 0, 2)
	if system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	messages = append(messages, map[string]any{"role": "user", "content": prompt})
	jsonMode, _ := in.Config["jsonMode"].(bool)
	text, body, err := openAIChat(ctx, in, messages, jsonMode)
	if err != nil {
		return nil, err
	}
	item := flow.Item{"text": text, "raw": body}
	if jsonMode {
		var parsed any
		if json.Unmarshal([]byte(text), &parsed) == nil {
			item["json"] = parsed
		}
	}
	return flow.Output{"main": item}, nil
}

type aiClassifyNode struct{}

func (aiClassifyNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	prompt := expr.RenderWithScope(str(in.Config["prompt"]), scope)
	rawLabels, _ := in.Config["labels"].([]any)
	labels := make([]string, 0, len(rawLabels))
	for _, label := range rawLabels {
		labels = append(labels, expr.RenderWithScope(fmt.Sprint(label), scope))
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("ai.classify: labels are required")
	}
	system := "Reply with exactly one of these labels and nothing else: " + strings.Join(labels, ", ")
	messages := []any{
		map[string]any{"role": "system", "content": system},
		map[string]any{"role": "user", "content": prompt},
	}
	text, body, err := openAIChat(ctx, in, messages, false)
	if err != nil {
		return nil, err
	}
	label := strings.TrimSpace(text)
	for _, candidate := range labels {
		if strings.EqualFold(candidate, label) {
			label = candidate
			break
		}
	}
	return flow.Output{"main": flow.Item{"label": label, "text": text, "raw": body}}, nil
}

type aiExtractNode struct{}

func (aiExtractNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	prompt := expr.RenderWithScope(str(in.Config["prompt"]), scope)
	rawFields, _ := in.Config["fields"].([]any)
	fields := make([]string, 0, len(rawFields))
	for _, field := range rawFields {
		fields = append(fields, fmt.Sprint(field))
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("ai.extract: fields are required")
	}
	system := "Return only a JSON object with these keys: " + strings.Join(fields, ", ")
	messages := []any{
		map[string]any{"role": "system", "content": system},
		map[string]any{"role": "user", "content": prompt},
	}
	text, body, err := openAIChat(ctx, in, messages, true)
	if err != nil {
		return nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, fmt.Errorf("ai.extract: model did not return JSON: %w", err)
	}
	return flow.Output{"main": flow.Item{"json": parsed, "text": text, "raw": body}}, nil
}

type aiEmbedNode struct{}

func (aiEmbedNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	token, base, err := openAICredential(in)
	if err != nil {
		return nil, err
	}
	text := expr.RenderWithScope(str(in.Config["text"]), expr.FromInput(in))
	model := expr.RenderWithScope(str(in.Config["model"]), expr.FromInput(in))
	trace(in, "request", map[string]any{"model": model, "chars": len(text)})
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: "POST", URL: base + "/embeddings",
		Headers:        map[string]string{"Authorization": "Bearer " + token},
		Body:           map[string]any{"model": model, "input": text},
		ContentType:    "application/json",
		IdempotencyKey: in.IdempotencyKey(),
	})
	if err != nil {
		return nil, err
	}
	data, _ := resp.Body["data"].([]any)
	if len(data) == 0 {
		return nil, fmt.Errorf("ai.embed: response has no embedding")
	}
	first, _ := data[0].(map[string]any)
	return flow.Output{"main": flow.Item{"embedding": first["embedding"], "raw": resp.Body}}, nil
}

func openAIChat(ctx context.Context, in flow.Input, messages []any, jsonMode bool) (string, flow.Item, error) {
	token, base, err := openAICredential(in)
	if err != nil {
		return "", nil, err
	}
	model := expr.RenderWithScope(str(in.Config["model"]), expr.FromInput(in))
	if model == "" {
		return "", nil, fmt.Errorf("model is required")
	}
	payload := map[string]any{"model": model, "messages": messages}
	if temperature, ok := in.Config["temperature"].(float64); ok {
		payload["temperature"] = temperature
	}
	if jsonMode {
		payload["response_format"] = map[string]any{"type": "json_object"}
	}
	trace(in, "request", map[string]any{"model": model, "messages": len(messages), "jsonMode": jsonMode})
	resp, err := in.Host.HTTP(ctx, flow.HTTPRequest{
		Method: "POST", URL: base + "/chat/completions",
		Headers:        map[string]string{"Authorization": "Bearer " + token},
		Body:           payload,
		ContentType:    "application/json",
		IdempotencyKey: in.IdempotencyKey(),
	})
	if err != nil {
		return "", nil, err
	}
	trace(in, "response", map[string]any{"status": resp.Status})
	return chatText(resp.Body), resp.Body, nil
}

func chatText(body flow.Item) string {
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	text, _ := message["content"].(string)
	return text
}

func openAICredential(in flow.Input) (string, string, error) {
	token := credentialField(in, "openai", "apiKey")
	if token == "" {
		token = credentialField(in, "openai", "token")
	}
	if token == "" {
		return "", "", fmt.Errorf("openai credential has no apiKey")
	}
	base := expr.RenderWithScope(str(in.Config["baseUrl"]), expr.FromInput(in))
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return token, strings.TrimRight(base, "/"), nil
}

func credentialField(in flow.Input, name, field string) string {
	cred, ok := in.CredentialData[name]
	if !ok {
		return ""
	}
	return credentialString(cred.Data[field])
}

func renderRows(value any, scope expr.Scope) [][]string {
	raw, _ := value.([]any)
	rows := make([][]string, 0, len(raw))
	for _, entry := range raw {
		cells, ok := entry.([]any)
		if !ok {
			rows = append(rows, []string{expr.RenderWithScope(fmt.Sprint(entry), scope)})
			continue
		}
		row := make([]string, 0, len(cells))
		for _, cell := range cells {
			if text, ok := cell.(string); ok {
				row = append(row, expr.RenderWithScope(text, scope))
			} else {
				row = append(row, fmt.Sprint(cell))
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func trace(in flow.Input, kind string, data map[string]any) {
	if in.Trace != nil {
		in.Trace(kind, data)
	}
}

func redactHeaders(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if sensitiveHeader(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = value
	}
	return out
}

func sensitiveHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "x-auth-token", "api-key":
		return true
	default:
		return false
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		var f float64
		_, _ = fmt.Sscanf(n, "%f", &f)
		return f
	}
	return 0
}
