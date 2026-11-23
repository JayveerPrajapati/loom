package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"loom/internal/expr"
	"loom/internal/flow"
)

type fakeHost struct {
	req  flow.HTTPRequest
	resp flow.HTTPResponse
	err  error

	rows     []flow.Item
	queryErr error

	slackErr     error
	slackChannel string
	slackText    string

	sleeps []time.Duration
}

func (h *fakeHost) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.req = req
	return h.resp, h.err
}

func (h *fakeHost) Query(context.Context, string, string) ([]flow.Item, error) {
	return h.rows, h.queryErr
}

func (h *fakeHost) Slack(_ context.Context, _, channel, text string) error {
	h.slackChannel, h.slackText = channel, text
	return h.slackErr
}

func (h *fakeHost) Sleep(_ context.Context, d time.Duration) error {
	h.sleeps = append(h.sleeps, d)
	return nil
}

func (h *fakeHost) Now() time.Time { return time.Unix(0, 0) }

func TestPortAndFieldTypes(t *testing.T) {
	for _, p := range []PortType{TypeAny, TypeJSON, TypeText, TypeEvent, TypeOrder, TypeMessage, TypeRows} {
		if !p.Valid() {
			t.Errorf("PortType(%q) should be valid", p)
		}
	}
	if PortType("bogus").Valid() {
		t.Error("bogus PortType accepted")
	}
	for _, f := range []FieldType{FieldString, FieldNumber, FieldBoolean, FieldArray, FieldObject, FieldAny} {
		if !f.Valid() {
			t.Errorf("FieldType(%q) should be valid", f)
		}
	}
	if FieldType("bogus").Valid() {
		t.Error("bogus FieldType accepted")
	}
}

func TestCatalogLookupAndOutputType(t *testing.T) {
	def, ok := Get("data.set")
	if !ok || def.Type != "data.set" {
		t.Fatalf("Get(data.set) = %#v, %v", def, ok)
	}
	if _, ok := Get("does.not.exist"); ok {
		t.Fatal("unknown node type resolved")
	}
	all := All()
	if len(all) == 0 {
		t.Fatal("catalog is empty")
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Type > all[i].Type {
			t.Fatalf("All() is not sorted at %d", i)
		}
	}

	d := Definition{OutputType: TypeJSON, OutputTypeKey: "outputType"}
	if got := d.OutputTypeFor(map[string]any{"outputType": "text"}); got != TypeText {
		t.Fatalf("override = %q, want text", got)
	}
	if got := d.OutputTypeFor(map[string]any{"outputType": "bogus"}); got != TypeJSON {
		t.Fatalf("invalid override = %q, want json", got)
	}
	if got := d.OutputTypeFor(nil); got != TypeJSON {
		t.Fatalf("missing override = %q, want json", got)
	}
	if got := (Definition{OutputType: TypeText}).OutputTypeFor(nil); got != TypeText {
		t.Fatalf("static output = %q, want text", got)
	}
}

func TestStatusMatches(t *testing.T) {
	cases := []struct {
		status int
		spec   string
		want   bool
	}{
		{200, "", true},
		{299, "", true},
		{300, "", false},
		{200, "200", true},
		{201, "200", false},
		{204, "2xx", true},
		{404, "4xx", true},
		{200, "200-299", true},
		{500, "200-299", false},
		{404, "404,500", true},
		{502, "404, 500-599", true},
		{200, "bogus", false},
	}
	for _, tc := range cases {
		if got := statusMatches(tc.status, tc.spec); got != tc.want {
			t.Errorf("statusMatches(%d, %q) = %v, want %v", tc.status, tc.spec, got, tc.want)
		}
	}
}

func TestCredentialStringAndNum(t *testing.T) {
	if credentialString(nil) != "" || credentialString("  x  ") != "x" || credentialString(42) != "42" {
		t.Fatal("credentialString conversion failed")
	}
	for _, tc := range []struct {
		in   any
		want float64
	}{
		{float64(1.5), 1.5}, {float32(2), 2}, {int(3), 3}, {int64(4), 4}, {"5.5", 5.5}, {"x", 0}, {nil, 0},
	} {
		if got := num(tc.in); got != tc.want {
			t.Errorf("num(%#v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if str("s") != "s" || str(1) != "" {
		t.Fatal("str conversion failed")
	}
}

func TestHTTPBodyModes(t *testing.T) {
	scope := expr.Scope{}
	if body, raw, ct, err := httpBody(flow.Input{Config: map[string]any{"bodyType": "none"}}, scope); err != nil || body != nil || raw != nil || ct != "" {
		t.Fatalf("none = %v %q %q %v", body, raw, ct, err)
	}
	_, raw, ct, err := httpBody(flow.Input{Config: map[string]any{"bodyType": "raw", "rawBody": "hi {{ $json.name }}"}}, expr.Scope{Item: flow.Item{"name": "loom"}})
	if err != nil || string(raw) != "hi loom" || !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("raw = %q %q %v", raw, ct, err)
	}
	_, raw, ct, err = httpBody(flow.Input{Config: map[string]any{"bodyType": "form", "body": map[string]any{"a": "1"}}}, scope)
	if err != nil || string(raw) != "a=1" || ct != "application/x-www-form-urlencoded" {
		t.Fatalf("form = %q %q %v", raw, ct, err)
	}
	body, _, ct, err := httpBody(flow.Input{Config: map[string]any{"bodyType": "json", "body": map[string]any{"a": "b"}}}, scope)
	if err != nil || ct != "application/json" {
		t.Fatalf("json = %v %q %v", body, ct, err)
	}
	body, _, ct, err = httpBody(flow.Input{Config: map[string]any{"body": map[string]any{"x": "y"}, "contentType": "text/csv"}}, scope)
	if err != nil || ct != "text/csv" {
		t.Fatalf("default = %v %q %v", body, ct, err)
	}
	if body, raw, ct, err := httpBody(flow.Input{Config: map[string]any{}}, scope); err != nil || body != nil || raw != nil || ct != "" {
		t.Fatalf("empty = %v %q %q %v", body, raw, ct, err)
	}
}

func TestHTTPAuthHeaders(t *testing.T) {
	if headers, err := httpAuthHeaders("none", flow.Credential{}); err != nil || len(headers) != 0 {
		t.Fatalf("none = %v %v", headers, err)
	}
	if headers, err := httpAuthHeaders("", flow.Credential{}); err != nil || len(headers) != 0 {
		t.Fatalf("empty credential = %v %v", headers, err)
	}
	if _, err := httpAuthHeaders("bearer", flow.Credential{}); err == nil {
		t.Fatal("bearer without credential accepted")
	}
	if _, err := httpAuthHeaders("basic", flow.Credential{Type: "http"}); err == nil {
		t.Fatal("basic without username accepted")
	}
	if _, err := httpAuthHeaders("apiKey", flow.Credential{Type: "http"}); err == nil {
		t.Fatal("apiKey without key accepted")
	}

	headers, err := httpAuthHeaders("bearer", flow.Credential{
		Type: "http", Data: map[string]any{"token": "abc", "headers": map[string]any{"X-Tenant": "t"}},
	})
	if err != nil || headers["Authorization"] != "Bearer abc" || headers["X-Tenant"] != "t" {
		t.Fatalf("bearer = %#v %v", headers, err)
	}
	headers, err = httpAuthHeaders("apiKey", flow.Credential{
		Type: "http", Data: map[string]any{"apiKey": "k", "apiKeyHeader": "X-Key"},
	})
	if err != nil || headers["X-Key"] != "k" {
		t.Fatalf("apiKey = %#v %v", headers, err)
	}
	headers, err = httpAuthHeaders("basic", flow.Credential{
		Type: "http", Data: map[string]any{"username": "u", "password": "p"},
	})
	if err != nil || !strings.HasPrefix(headers["Authorization"], "Basic ") {
		t.Fatalf("basic = %#v %v", headers, err)
	}
	headers, err = httpAuthHeaders("credential", flow.Credential{Type: "http", Raw: "raw-token"})
	if err != nil || headers["Authorization"] != "Bearer raw-token" {
		t.Fatalf("raw = %#v %v", headers, err)
	}
}

func TestRedactHeadersAndSensitiveHeader(t *testing.T) {
	in := map[string]string{
		"Authorization": "a", "X-API-Key": "b", "Cookie": "c", "X-Trace": "d", "X-Auth-Token": "e",
	}
	out := redactHeaders(in)
	for _, key := range []string{"Authorization", "X-API-Key", "Cookie", "X-Auth-Token"} {
		if out[key] != "[redacted]" {
			t.Errorf("%s = %q, want [redacted]", key, out[key])
		}
	}
	if out["X-Trace"] != "d" {
		t.Errorf("X-Trace = %q, want d", out["X-Trace"])
	}
	if sensitiveHeader("not-sensitive") {
		t.Error("ordinary header flagged sensitive")
	}
}

func TestRenderHelpersAndPreview(t *testing.T) {
	scope := expr.Scope{Item: flow.Item{"name": "loom"}}
	m := renderMap(map[string]any{"a": "{{ $json.name }}"}, scope)
	if m["a"] != "loom" {
		t.Fatalf("renderMap = %#v", m)
	}
	if got := renderMap("not-a-map", scope); len(got) != 0 {
		t.Fatalf("renderMap(non-map) = %#v", got)
	}
	sm := renderStringMap(map[string]any{"n": 3}, scope)
	if sm["n"] != "3" {
		t.Fatalf("renderStringMap = %#v", sm)
	}
	rows := renderRows([]any{[]any{"a", 2}, "plain"}, scope)
	if len(rows) != 2 || rows[0][0] != "a" || rows[0][1] != "2" || rows[1][0] != "plain" {
		t.Fatalf("renderRows = %#v", rows)
	}
	if got := previewValue(map[string]any{"ok": true}); got != `{"ok":true}` {
		t.Fatalf("previewValue = %q", got)
	}
	long := strings.Repeat("x", 2100)
	if got := previewValue(long); !strings.HasSuffix(got, "…") {
		t.Fatalf("long preview not truncated: %d chars", len(got))
	}
}

func TestTraceCallback(t *testing.T) {
	var kinds []string
	trace(flow.Input{Trace: func(kind string, _ map[string]any) { kinds = append(kinds, kind) }}, "request", nil)
	trace(flow.Input{}, "response", nil)
	if len(kinds) != 1 || kinds[0] != "request" {
		t.Fatalf("trace calls = %#v", kinds)
	}
}

func TestHTTPNodeSuccessAndTracing(t *testing.T) {
	h := &fakeHost{resp: flow.HTTPResponse{
		Status: 201, StatusText: "Created",
		Headers: map[string]string{"Authorization": "secret", "X-Request": "r"},
		Body:    flow.Item{"id": "x"},
	}}
	var traceKinds []string
	var requestTrace map[string]any
	var responseTrace map[string]any
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "post", "url": "https://api.example.com/orders/{id}?q=1",
			"pathParams": map[string]any{"id": "A 1"},
			"headers":    map[string]any{"X-Custom": "v", "Authorization": "keep"},
			"auth":       "credential",
			"bodyType":   "json",
			"body":       map[string]any{"name": "n"},
		},
		Item:           flow.Item{"seed": "s"},
		CredentialData: map[string]flow.Credential{"http": {Type: "http", Data: map[string]any{"token": "tok"}}},
		Host:           h,
		RunID:          "run",
		NodeID:         "node",
		Trace: func(kind string, data map[string]any) {
			traceKinds = append(traceKinds, kind)
			if kind == "request" {
				requestTrace = data
			} else {
				responseTrace = data
			}
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if h.req.Method != "POST" || h.req.URL != "https://api.example.com/orders/A%201?q=1" {
		t.Fatalf("request = %s %s", h.req.Method, h.req.URL)
	}
	if h.req.Headers["Authorization"] != "keep" {
		t.Fatalf("explicit Authorization overridden: %#v", h.req.Headers)
	}
	if h.req.ContentType != "application/json" {
		t.Fatalf("content type = %q", h.req.ContentType)
	}
	item := out["main"]
	if item["status"] != float64(201) || item["ok"] != true || item["id"] != "x" {
		t.Fatalf("output = %#v", item)
	}
	if len(traceKinds) != 2 || requestTrace == nil || responseTrace == nil {
		t.Fatalf("traces = %#v", traceKinds)
	}
	traceHeaders := requestTrace["headers"].(map[string]string)
	if traceHeaders["Authorization"] != "[redacted]" {
		t.Fatalf("trace leaked Authorization: %#v", traceHeaders)
	}
	respHeaders := responseTrace["headers"].(map[string]string)
	if respHeaders["Authorization"] != "[redacted]" || respHeaders["X-Request"] != "r" {
		t.Fatalf("response trace = %#v", respHeaders)
	}
}

func TestHTTPNodeEnvelopeKeepsBodyKeys(t *testing.T) {
	h := &fakeHost{resp: flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"X-Test": "v"},
		Body: flow.Item{
			"ok": "BODY-OK", "status": "BODY-STATUS", "headers": "BODY-HEADERS",
		},
	}}
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"url": "https://api.example.com/x"},
		Host:   h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	item := out["main"]
	if item["ok"] != "BODY-OK" || item["status"] != "BODY-STATUS" || item["headers"] != "BODY-HEADERS" {
		t.Fatalf("body keys were clobbered: %#v", item)
	}
	if item["statusCode"] != 200 {
		t.Fatalf("statusCode = %#v, want int 200", item["statusCode"])
	}
	if item["statusText"] != "OK" {
		t.Fatalf("statusText = %#v", item["statusText"])
	}
	for _, key := range []string{"url", "method", "durationMs"} {
		if _, ok := item[key]; !ok {
			t.Fatalf("envelope field %q missing: %#v", key, item)
		}
	}

	// Non-object bodies keep the host's existing {"items": [...]} shape and
	// still receive the collision-free status fields.
	h = &fakeHost{resp: flow.HTTPResponse{Status: 201, StatusText: "Created", Body: flow.Item{"items": []any{float64(1)}}}}
	out, err = httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"url": "https://api.example.com/x"},
		Host:   h,
	})
	if err != nil {
		t.Fatalf("array body: %v", err)
	}
	item = out["main"]
	if items, ok := item["items"].([]any); !ok || len(items) != 1 {
		t.Fatalf("items body was lost: %#v", item)
	}
	if item["statusCode"] != 201 {
		t.Fatalf("statusCode = %#v, want int 201", item["statusCode"])
	}
}

func TestHTTPNodeFailOnError(t *testing.T) {
	h := &fakeHost{resp: flow.HTTPResponse{Status: 500, Body: flow.Item{"error": "boom"}}}
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"url": "https://api.example.com/x", "failOnError": true, "successCodes": "2xx"},
		Host:   h,
	})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("failOnError err = %v", err)
	}

	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"url": "https://api.example.com/x", "successCodes": "2xx"},
		Host:   h,
	})
	if err != nil {
		t.Fatalf("non-failing 500: %v", err)
	}
	if out["main"]["ok"] != false {
		t.Fatalf("ok = %v, want false", out["main"]["ok"])
	}
}

func TestHTTPNodeHostError(t *testing.T) {
	h := &fakeHost{err: errors.New("dial failed")}
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"url": "https://api.example.com/x"},
		Host:   h,
	})
	if err == nil || !strings.Contains(err.Error(), "dial failed") {
		t.Fatalf("host error = %v", err)
	}
}

func TestTriggerCronSetsNow(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	out, err := triggerNode{typ: "trigger.cron"}.Execute(context.Background(), flow.Input{
		Item: flow.Item{"seed": 1}, Now: now,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out["main"]["now"] != "2026-10-31T12:00:00Z" || out["main"]["seed"] != float64(1) && out["main"]["seed"] != 1 {
		t.Fatalf("output = %#v", out["main"])
	}
}
