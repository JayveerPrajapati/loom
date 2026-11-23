package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"loom/internal/expr"
	"loom/internal/flow"
	"strings"
	"time"
)

// Registry returns one stateless implementation per catalog type.
func Registry() map[string]flow.Node {
	return map[string]flow.Node{
		"trigger.manual":  triggerNode{typ: "trigger.manual"},
		"trigger.webhook": triggerNode{typ: "trigger.webhook"},
		"trigger.cron":    triggerNode{typ: "trigger.cron"},
		"trigger.subflow": triggerNode{typ: "trigger.subflow"},
		"trigger.event":   triggerNode{typ: "trigger.event"},
		"http.request":    httpNode{},
		"code.run":        codeNode{},
		"code.python":     pythonNode{},
		"data.set":        setNode{},
		"data.map":        mapNode{},
		"flow.if":         ifNode{},
		"flow.switch":     switchNode{},
		"flow.merge":      mergeNode{},
		"flow.loop":       loopNode{},
		"flow.wait":       waitNode{},
		"flow.approval":   approvalNode{},
		"flow.subflow":    subflowNode{},
		"flow.emit":       emitNode{},
		"db.postgres":     postgresNode{},
		"data.json.parse": jsonParseNode{},
		"text.template":   templateNode{},
		// Connector-authored node types (Campaign 4): the engine
		// dispatches every connector.<id>.<op> type here.
		"connector.": connectorNode{},
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

// setIfAbsent adds an envelope field without overwriting a same-named field
// from the response body.
func setIfAbsent(item flow.Item, key string, value any) {
	if _, ok := item[key]; !ok {
		item[key] = value
	}
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

func credentialString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// credentialStringList extracts a []string field from credential data,
// tolerating nil, a JSON array, or a comma-separated string.
func credentialStringList(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := credentialString(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return typed
	default:
		if s := credentialString(value); s != "" {
			return strings.Split(s, ",")
		}
		return nil
	}
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
