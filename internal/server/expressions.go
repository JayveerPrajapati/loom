package server

import (
	"net/http"
	"strings"

	"loom/internal/expr"
	"loom/internal/flow"
)

// Expression tester (Configuration Hub v2, plan D14): evaluates a template
// against a caller-supplied sample item with the production renderer —
// what you test is exactly what nodes render at run time. No host calls
// are exposed (the code-step $http surface stays gated), and the sandbox's
// own script-length and timeout limits apply unchanged.

func (s *api) evalExpression(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expression string         `json:"expression"`
		Sample     map[string]any `json:"sample"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Expression = strings.TrimSpace(req.Expression)
	if req.Expression == "" {
		writeError(w, http.StatusBadRequest, "expression is required")
		return
	}
	if len(req.Expression) > 64*1024 {
		writeError(w, http.StatusBadRequest, "expression is too long (64 KiB cap)")
		return
	}
	if req.Sample == nil {
		req.Sample = map[string]any{}
	}
	scope := expr.Scope{Item: flow.Item(req.Sample)}
	rendered, err := expr.RenderStrictWithScope(req.Expression, scope)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "value": rendered})
}
