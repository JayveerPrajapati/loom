package nodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

// scriptedHost serves a queued sequence of responses and records every
// request, so pagination tests can assert per-page URLs.
type scriptedHost struct {
	responses []flow.HTTPResponse
	errs      []error
	requests  []flow.HTTPRequest
}

func (h *scriptedHost) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.requests = append(h.requests, req)
	i := len(h.requests) - 1
	var err error
	if i < len(h.errs) {
		err = h.errs[i]
	}
	if i < len(h.responses) {
		return h.responses[i], err
	}
	return flow.HTTPResponse{Status: 200, StatusText: "OK", Body: flow.Item{}}, err
}

func (h *scriptedHost) Query(context.Context, string, string) ([]flow.Item, error) { return nil, nil }
func (h *scriptedHost) Slack(context.Context, string, string, string) error        { return nil }
func (h *scriptedHost) Sleep(context.Context, time.Duration) error                 { return nil }
func (h *scriptedHost) Now() time.Time                                             { return time.Unix(0, 0) }

// blobHost adds content-addressed blob storage to the scripted host.
type blobHost struct {
	scriptedHost
	blobs map[string][]byte
}

func newBlobHost(responses ...flow.HTTPResponse) *blobHost {
	return &blobHost{blobs: map[string][]byte{}, scriptedHost: scriptedHost{responses: responses}}
}

func (h *blobHost) SaveBlob(data []byte) (string, int, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	h.blobs[hash] = data
	return hash, len(data), nil
}

func (h *blobHost) LoadBlob(hash string) ([]byte, error) {
	data, ok := h.blobs[hash]
	if !ok {
		return nil, fmt.Errorf("no blob %s", hash)
	}
	return data, nil
}

func page(items ...string) flow.HTTPResponse {
	list := make([]any, 0, len(items))
	for _, id := range items {
		list = append(list, map[string]any{"id": id})
	}
	return flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    flow.Item{"items": list},
	}
}

func TestHTTPPaginationPageMode(t *testing.T) {
	h := &scriptedHost{responses: []flow.HTTPResponse{
		page("a", "b"), page("c"), page(),
	}}
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/things",
			"pagination": map[string]any{
				"mode": "page", "pageParam": "page", "maxPages": 10,
			},
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	item := out["main"]
	if got := item["itemsCount"].(float64); got != 3 {
		t.Fatalf("itemsCount = %v, want 3", got)
	}
	if got := item["pages"].(float64); got != 3 {
		t.Fatalf("pages = %v, want 3", got)
	}
	if len(h.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(h.requests))
	}
	for i, want := range []string{"page=1", "page=2", "page=3"} {
		if !strings.Contains(h.requests[i].URL, want) {
			t.Fatalf("request %d url %q missing %q", i, h.requests[i].URL, want)
		}
	}
}

func TestHTTPPaginationCursorMode(t *testing.T) {
	first := page("a", "b")
	first.Body["nextCursor"] = "p2"
	second := page("c")
	second.Body["nextCursor"] = ""
	h := &scriptedHost{responses: []flow.HTTPResponse{first, second}}
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/things",
			"pagination": map[string]any{
				"mode": "cursor", "cursorPath": "nextCursor", "cursorParam": "after",
			},
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out["main"]["itemsCount"].(float64); got != 3 {
		t.Fatalf("itemsCount = %v, want 3", got)
	}
	if len(h.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(h.requests))
	}
	if !strings.Contains(h.requests[1].URL, "after=p2") {
		t.Fatalf("second request url %q missing after=p2", h.requests[1].URL)
	}
}

func TestHTTPPaginationUntilStopsEarly(t *testing.T) {
	first := page("a")
	second := page("b")
	second.Body["stop"] = true
	h := &scriptedHost{responses: []flow.HTTPResponse{first, second, page("never")}}
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/things",
			"pagination": map[string]any{
				"mode": "page", "until": "$json.stop",
			},
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out["main"]["pages"].(float64); got != 2 {
		t.Fatalf("pages = %v, want 2", got)
	}
	if len(h.requests) != 2 {
		t.Fatalf("requests = %d, want 2 (until must stop before page 3)", len(h.requests))
	}
}

func TestHTTPPaginationFailedPageFailsNode(t *testing.T) {
	h := &scriptedHost{responses: []flow.HTTPResponse{
		page("a"),
		{Status: 500, StatusText: "Server Error", Body: flow.Item{"error": "boom"}},
	}}
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/things",
			"pagination": map[string]any{
				"mode": "page",
			},
		},
		Host: h,
	})
	if err == nil || !strings.Contains(err.Error(), "page 2 status 500") {
		t.Fatalf("err = %v, want a page-2 failure (no silent truncation)", err)
	}
}

func TestHTTPPaginationMaxPagesCapErrors(t *testing.T) {
	h := &scriptedHost{responses: []flow.HTTPResponse{
		page("a"), page("b"), page("c"), page("d"),
	}}
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/things",
			"pagination": map[string]any{
				"mode": "page", "maxPages": 3,
			},
		},
		Host: h,
	})
	if err == nil || !strings.Contains(err.Error(), "maxPages cap") {
		t.Fatalf("err = %v, want maxPages cap error", err)
	}
}

func TestHTTPPaginationOffsetMode(t *testing.T) {
	h := &scriptedHost{responses: []flow.HTTPResponse{
		page("a", "b"), page(),
	}}
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/things",
			"pagination": map[string]any{
				"mode": "offset", "limit": 2, "offsetParam": "skip", "limitParam": "take",
			},
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out["main"]["itemsCount"].(float64); got != 2 {
		t.Fatalf("itemsCount = %v, want 2", got)
	}
	if !strings.Contains(h.requests[0].URL, "skip=0") || !strings.Contains(h.requests[0].URL, "take=2") {
		t.Fatalf("first url %q", h.requests[0].URL)
	}
	if !strings.Contains(h.requests[1].URL, "skip=2") {
		t.Fatalf("second url %q", h.requests[1].URL)
	}
}

func TestHTTPResponseModeBlob(t *testing.T) {
	h := newBlobHost(flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    flow.Item{"secret": "large-body"},
		Raw:     []byte(`{"secret":"large-body"}`),
	})
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/big",
			"responseMode": "blob",
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	item := out["main"]
	sum := sha256.Sum256([]byte(`{"secret":"large-body"}`))
	want := hex.EncodeToString(sum[:])
	if item["blob"] != want {
		t.Fatalf("blob = %v, want %s", item["blob"], want)
	}
	if _, exists := item["secret"]; exists {
		t.Fatalf("inline body survived the spill: %#v", item)
	}
	if fmt.Sprint(item["statusCode"]) != "200" || item["statusText"] != "OK" {
		t.Fatalf("envelope lost: %v %v", item["statusCode"], item["statusText"])
	}
	if stored, ok := h.blobs[want]; !ok || string(stored) != `{"secret":"large-body"}` {
		t.Fatalf("blob store = %v", stored)
	}
}

func TestHTTPResponseModeAutoThreshold(t *testing.T) {
	small := flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    flow.Item{"secret": "small"},
		Raw:     []byte(`{"secret":"small"}`),
	}
	h := newBlobHost(small)
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/small",
			"responseMode": "auto",
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, exists := out["main"]["secret"]; !exists {
		t.Fatalf("small body should stay inline: %#v", out["main"])
	}

	big := flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    flow.Item{"secret": "x"},
		Raw:     []byte(`{"secret":"` + strings.Repeat("x", 2<<20) + `"}`),
	}
	h2 := newBlobHost(big)
	out, err = httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/big",
			"responseMode": "auto",
		},
		Host: h2,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, exists := out["main"]["blob"]; !exists {
		t.Fatalf("big body should spill: %#v", out["main"])
	}
}

func TestHTTPResponseModeInlineByDefault(t *testing.T) {
	h := newBlobHost(flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    flow.Item{"secret": "x"},
		Raw:     []byte(`{"secret":"x"}`),
	})
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"method": "GET", "url": "https://api.example.com/x"},
		Host:   h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out["main"]["secret"] != "x" {
		t.Fatalf("body missing: %#v", out["main"])
	}
	if len(h.blobs) != 0 {
		t.Fatalf("default mode must not spill, blobs = %d", len(h.blobs))
	}
}

func TestHTTPMultipartBody(t *testing.T) {
	h := newBlobHost()
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "POST", "url": "https://api.example.com/upload",
			"bodyType": "multipart",
			"parts": []any{
				map[string]any{"name": "note", "value": "hello"},
				map[string]any{"name": "data", "type": "json", "value": map[string]any{"a": 1}},
				map[string]any{"name": "file", "type": "text", "value": "body", "filename": "a.txt", "contentType": "text/plain"},
			},
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	_ = out
	req := h.requests[0]
	if !strings.HasPrefix(req.ContentType, "multipart/form-data; boundary=loom-") {
		t.Fatalf("contentType = %q", req.ContentType)
	}
	boundary := strings.TrimPrefix(req.ContentType, "multipart/form-data; boundary=")
	reader := multipart.NewReader(strings.NewReader(string(req.BodyRaw)), boundary)
	part, err := reader.NextPart()
	if err != nil {
		t.Fatalf("part 1: %v", err)
	}
	if part.FormName() != "note" {
		t.Fatalf("part 1 name = %q", part.FormName())
	}
	data, _ := io.ReadAll(part)
	if string(data) != "hello" {
		t.Fatalf("part 1 = %q", data)
	}
	part, err = reader.NextPart()
	if err != nil {
		t.Fatalf("part 2: %v", err)
	}
	if part.FormName() != "data" {
		t.Fatalf("part 2 name = %q", part.FormName())
	}
	data, _ = io.ReadAll(part)
	if string(data) != `{"a":1}` {
		t.Fatalf("part 2 = %q", data)
	}
	part, err = reader.NextPart()
	if err != nil {
		t.Fatalf("part 3: %v", err)
	}
	if part.FileName() != "a.txt" || part.Header.Get("Content-Type") != "text/plain" {
		t.Fatalf("part 3 = %q %q", part.FileName(), part.Header.Get("Content-Type"))
	}
	data, _ = io.ReadAll(part)
	if string(data) != "body" {
		t.Fatalf("part 3 = %q", data)
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}

	// Determinism: the same inputs must rebuild a byte-identical body, or
	// a replayed node would report request drift against its own tape.
	h2 := newBlobHost()
	_, err = httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "POST", "url": "https://api.example.com/upload",
			"bodyType": "multipart",
			"parts": []any{
				map[string]any{"name": "note", "value": "hello"},
				map[string]any{"name": "data", "type": "json", "value": map[string]any{"a": 1}},
				map[string]any{"name": "file", "type": "text", "value": "body", "filename": "a.txt", "contentType": "text/plain"},
			},
		},
		Host: h2,
	})
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if string(h.requests[0].BodyRaw) != string(h2.requests[0].BodyRaw) {
		t.Fatalf("multipart body is not deterministic")
	}
}

func TestHTTPMultipartBlobPart(t *testing.T) {
	h := newBlobHost()
	sum := sha256.Sum256([]byte("file-bytes"))
	hash := hex.EncodeToString(sum[:])
	h.blobs[hash] = []byte("file-bytes")
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "POST", "url": "https://api.example.com/upload",
			"bodyType": "multipart",
			"parts": []any{
				map[string]any{"name": "file", "type": "blob", "value": hash, "filename": "bin.dat"},
			},
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(string(h.requests[0].BodyRaw), "file-bytes") {
		t.Fatalf("blob part content missing from body")
	}
}

func TestHTTPHeadAndOptionsPassThrough(t *testing.T) {
	h := &scriptedHost{}
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"method": "HEAD", "url": "https://api.example.com/x"},
		Host:   h,
	})
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if h.requests[0].Method != "HEAD" {
		t.Fatalf("method = %q", h.requests[0].Method)
	}
	_, err = httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"method": "OPTIONS", "url": "https://api.example.com/x"},
		Host:   h,
	})
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if h.requests[1].Method != "OPTIONS" {
		t.Fatalf("method = %q", h.requests[1].Method)
	}
}
