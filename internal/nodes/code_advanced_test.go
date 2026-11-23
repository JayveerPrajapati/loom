package nodes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

func codeConfig(source string) map[string]any {
	return map[string]any{"language": "javascript", "source": source}
}

func TestCodeRunHTTPRoutesThroughHost(t *testing.T) {
	h := &fakeHost{resp: flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    flow.Item{"token": "abc"},
	}}
	out, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: codeConfig(`
			const resp = $http({method: "POST", url: "https://api.example.com/login",
			                     headers: {"X-A": "1"}, json: {user: "loom"}});
			return {status: resp.statusCode, token: resp.body.token};
		`),
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out["main"]["status"] != float64(200) || out["main"]["token"] != "abc" {
		t.Fatalf("out = %#v", out["main"])
	}
	if h.req.Method != "POST" || h.req.URL != "https://api.example.com/login" {
		t.Fatalf("req = %s %s", h.req.Method, h.req.URL)
	}
	if h.req.Headers["X-A"] != "1" || h.req.Body == nil {
		t.Fatalf("headers = %v body = %v", h.req.Headers, h.req.Body)
	}
}

func TestCodeRunHTTPWithoutHostFailsCleanly(t *testing.T) {
	_, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: codeConfig(`return $http({url: "https://api.example.com"});`),
	})
	if err == nil || !strings.Contains(err.Error(), "$http is not defined") {
		t.Fatalf("err = %v, want $http unavailable without a host", err)
	}
}

func TestCodeRunConsoleCapture(t *testing.T) {
	var traced map[string]any
	out, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: codeConfig(`
			console.log("hello", 123);
			console.warn("careful");
			return {ok: true};
		`),
		Trace: func(kind string, data map[string]any) {
			if kind == "console" {
				traced = data
			}
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out["main"]["ok"] != true {
		t.Fatalf("out = %#v", out["main"])
	}
	if traced == nil {
		t.Fatal("no console trace recorded")
	}
	logs, _ := traced["logs"].([]any)
	if len(logs) != 2 {
		t.Fatalf("logs = %#v", traced["logs"])
	}
	first, _ := logs[0].(map[string]any)
	if first["level"] != "log" || first["message"] != "hello 123" {
		t.Fatalf("first = %#v", first)
	}
	second, _ := logs[1].(map[string]any)
	if second["level"] != "warn" || second["message"] != "careful" {
		t.Fatalf("second = %#v", second)
	}
}

func TestCodeRunNoConsoleTraceWithoutLogs(t *testing.T) {
	called := false
	_, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: codeConfig(`return {ok: true};`),
		Trace: func(kind string, data map[string]any) {
			if kind == "console" {
				called = true
			}
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if called {
		t.Fatal("empty console trace should not be recorded")
	}
}

func TestCodeRunBatteries(t *testing.T) {
	out, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: codeConfig(`
			return {
				sha: $crypto.sha256("loom"),
				hmac: $crypto.hmacSha256("loom", "key"),
				b64: $base64.encode("loom"),
				decoded: $base64.decode($base64.encode("loom")),
				btoa: btoa("loom"),
			};
		`),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	wantSha := sha256.Sum256([]byte("loom"))
	mac := hmac.New(sha256.New, []byte("key"))
	mac.Write([]byte("loom"))
	if out["main"]["sha"] != hex.EncodeToString(wantSha[:]) {
		t.Fatalf("sha = %v", out["main"]["sha"])
	}
	if out["main"]["hmac"] != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("hmac = %v", out["main"]["hmac"])
	}
	if out["main"]["b64"] != base64.StdEncoding.EncodeToString([]byte("loom")) || out["main"]["btoa"] != "bG9vbQ==" {
		t.Fatalf("base64 = %v / %v", out["main"]["b64"], out["main"]["btoa"])
	}
	if out["main"]["decoded"] != "loom" {
		t.Fatalf("decoded = %v", out["main"]["decoded"])
	}
}

func TestCodeRunMultiItemOutput(t *testing.T) {
	out, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: codeConfig(`return [1, 2, 3];`),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	items, ok := out["main"]["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("out = %#v, want items[3]", out["main"])
	}
}

func TestCodeRunTimeoutCap(t *testing.T) {
	// A script that spins forever must die at the timeout, and the node
	// clamp accepts up to 300s (this test uses 1s).
	started := time.Now()
	_, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"language":       "javascript",
			"source":         `while (true) {}`,
			"timeoutSeconds": 1,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("timeout took %v", time.Since(started))
	}
}
