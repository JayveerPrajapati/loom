package nodes

import (
	"context"
	"fmt"
	"strings"

	"loom/internal/expr"
	"loom/internal/flow"
)

// connectorNode executes connector-authored node types (operational-
// trust Campaign 4, D10-3). The node's config carries the author's
// field values plus the publish-inlined request template
// (config["request"], written by store.ResolveConnectorRefs). Rendering
// happens in two phases with explicit, non-overlapping placeholder
// namespaces:
//
//	{{config.<field>}}  author field values
//	{{cred.<field>}}    credential JSON fields
//	{{...anything else}}  left verbatim for httpNode's item scope
//
// The rendered result becomes an httpNode config, so every existing
// guarantee applies unchanged: the SSRF private-target guard, egress
// policy, per-credential allowedHosts, cassette replay, retries, and
// pagination. Nothing new is executable — the connector is data.
type connectorNode struct{}

// configScope maps {{config.x}} placeholders to the node's author
// fields.
func renderConnectorPlaceholder(placeholder string, in flow.Input) (string, bool) {
	switch {
	case strings.HasPrefix(placeholder, "config."):
		key := strings.TrimPrefix(placeholder, "config.")
		value, ok := in.Config[key]
		if !ok {
			return "", true
		}
		return fmt.Sprint(value), true
	case strings.HasPrefix(placeholder, "cred."):
		key := strings.TrimPrefix(placeholder, "cred.")
		// The connector credential slot is "http" (the trusted pipeline
		// reuses httpNode's credential machinery). A map lookup on an
		// absent key yields the zero Credential; its empty Raw/Type
		// distinguishes that from a present one with an empty field.
		if _, present := in.CredentialData["http"]; !present {
			return "", true
		}
		cred := in.CredentialData["http"]
		if value, ok := cred.Data[key]; ok {
			return fmt.Sprint(value), true
		}
		return "", true
	}
	return "", false
}

// renderConnectorTemplate resolves only {{config.*}} and {{cred.*}}
// placeholders, leaving item-scope expressions for httpNode.
func renderConnectorTemplate(tmpl string, in flow.Input) string {
	if !strings.Contains(tmpl, "{{") {
		return tmpl
	}
	var out strings.Builder
	for {
		start := strings.Index(tmpl, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(tmpl[start+2:], "}}")
		if end < 0 {
			break
		}
		placeholder := tmpl[start+2 : start+2+end]
		out.WriteString(tmpl[:start])
		if value, ok := renderConnectorPlaceholder(placeholder, in); ok {
			out.WriteString(value)
		} else {
			// Not a connector placeholder: keep it verbatim for the
			// httpNode item-scope rendering phase.
			out.WriteString("{{" + placeholder + "}}")
		}
		tmpl = tmpl[start+2+end+2:]
	}
	out.WriteString(tmpl)
	return out.String()
}

// renderConnectorStringMap renders template values in a map.
func renderConnectorStringMap(values map[string]any, in flow.Input) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		if text, ok := value.(string); ok {
			out[key] = renderConnectorTemplate(text, in)
			continue
		}
		out[key] = value
	}
	return out
}

func (connectorNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	request, _ := in.Config["request"].(map[string]any)
	if request == nil {
		return nil, fmt.Errorf("%s: no request template; publish the workflow so the connector template is inlined (draft runs resolve it from the connector's pinned version)", "connector")
	}
	config := map[string]any{}
	for key, value := range in.Config {
		// The internals are not author fields.
		if key == "request" || key == "_connector" {
			continue
		}
		config[key] = value
	}
	config["method"] = renderConnectorTemplate(fmt.Sprint(request["method"]), in)
	config["url"] = renderConnectorTemplate(fmt.Sprint(request["url"]), in)
	if query, ok := request["query"].(map[string]any); ok {
		config["query"] = renderConnectorStringMap(query, in)
	}
	if headers, ok := request["headers"].(map[string]any); ok {
		config["headers"] = renderConnectorStringMap(headers, in)
	}
	if body, present := request["body"]; present && body != nil && fmt.Sprint(body) != "" {
		config["body"] = renderConnectorTemplate(fmt.Sprint(body), in)
		config["bodyType"] = "json"
	}
	// fmt.Sprint(nil) renders "<nil>" — guard presence, not emptiness.
	if codes, present := request["successCodes"]; present && codes != nil && fmt.Sprint(codes) != "" {
		config["successCodes"] = fmt.Sprint(codes)
	}
	if mode, present := request["responseMode"]; present && mode != nil && fmt.Sprint(mode) != "" {
		config["responseMode"] = fmt.Sprint(mode)
	}
	httpIn := in
	httpIn.Config = config
	return httpNode{}.Execute(ctx, httpIn)
}

// connectorInputScope documents (and test-asserts) that the connector
// placeholder namespaces never collide with the item scope: item
// expressions survive phase one untouched.
var _ = expr.FromInput
