package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"loom/examples"
	"loom/internal/auth"
	"loom/internal/graph"
	"loom/internal/nodes"
	"loom/internal/store"
	"net/http"
	"strings"
	"sync"
	"time"
)

type api struct {
	deps       Deps
	webhookSem chan struct{}
	limiter    loginLimiter
	// webhookPathMu serializes validateWebhookPaths + save so two concurrent
	// writes cannot both observe the same /hook path as free.
	webhookPathMu sync.Mutex
	// eventTriggers is the in-memory index event name -> workflow IDs,
	// rebuilt at startup and after every workflow mutation so onEvent never
	// scans unrelated workflows.
	eventTriggersMu sync.RWMutex
	eventTriggers   map[string]map[string]bool
	// publishProgresses is the in-memory live progress view for the
	// synchronous publish POST (audit 4E). Value, zero-value safe, so
	// &api{deps: ...} constructors need no init.
	publishProgresses publishProgressRegistry
}

// redactedSecret is the display mask for stored webhook credentials.
const redactedSecret = "[redacted]"

// redactWorkflowSecrets returns a deep copy of wf with webhook trigger secrets
// masked. The caller's workflow document is never mutated. A non-empty secret
// becomes the mask and the node gains secretSet so the editor can still show
// that a credential is configured without revealing it.
func redactWorkflowSecrets(wf *graph.Workflow) *graph.Workflow {
	if wf == nil {
		return nil
	}
	data, err := json.Marshal(wf)
	if err != nil {
		return wf
	}
	var out graph.Workflow
	if err := json.Unmarshal(data, &out); err != nil {
		return wf
	}
	for i := range out.Nodes {
		node := &out.Nodes[i]
		if node.Type != "trigger.webhook" || node.Config == nil {
			continue
		}
		secretSet := false
		for _, key := range []string{"secret", "hmacSecret"} {
			raw, ok := node.Config[key]
			if !ok {
				continue
			}
			value, _ := raw.(string)
			if strings.TrimSpace(value) == "" {
				continue
			}
			node.Config[key] = redactedSecret
			secretSet = true
		}
		if secretSet {
			node.Config["secretSet"] = true
		}
	}
	return &out
}

// workflowForResponse masks webhook secrets for principals below editor.
// Editors and admins keep the plaintext document so the editor still works.
func workflowForResponse(r *http.Request, wf *graph.Workflow) *graph.Workflow {
	if principal, ok := principalFrom(r.Context()); ok && principal.Role.Allows(auth.RoleEditor) {
		return wf
	}
	return redactWorkflowSecrets(wf)
}

func (s *api) acquireWebhook(w http.ResponseWriter, r *http.Request) bool {
	if s.webhookSem == nil {
		return true
	}
	select {
	case s.webhookSem <- struct{}{}:
		return true
	case <-time.After(5 * time.Second):
		writeError(w, http.StatusTooManyRequests, "webhook ingestion queue full, retry later")
		return false
	case <-r.Context().Done():
		return false
	}
}

func (s *api) reload() {
	// Workflow mutations are the one moment the event-trigger index can
	// change, so every mutation path rebuilds it before anything else.
	s.rebuildEventTriggers()
	if s.deps.Reload == nil {
		return
	}
	if err := s.deps.Reload(); err != nil {
		log.Printf("scheduler reload: %v", err)
	}
}

func (s *api) nodeTypes(w http.ResponseWriter, r *http.Request) {
	list := nodes.All()
	// Connector-authored types (Campaign 4) join the listing from the
	// connector store; the category tells the editor they are data-defined.
	connectors, err := s.deps.Store.ListConnectors()
	if err == nil {
		v := viewerFrom(r)
		for _, c := range connectors {
			if !v.canSee(c.Visibility, c.CreatorID) {
				continue
			}
			list = append(list, store.ConnectorNodeTypes(&c)...)
		}
	}
	for index := range list {
		if list[index].Inputs == nil {
			list[index].Inputs = []string{}
		}
		if list[index].Outputs == nil {
			list[index].Outputs = []string{}
		}
		if list[index].Credentials == nil {
			list[index].Credentials = []string{}
		}
		if list[index].OptionalCredentials == nil {
			list[index].OptionalCredentials = []string{}
		}
		if list[index].MappingKinds == nil {
			list[index].MappingKinds = []string{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodeTypes": list})
}

func (s *api) runDataContext(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.deps.Engine.DataContext(r.PathValue("id"), r.URL.Query().Get("nodeId"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ctx)
}

func (s *api) previewExpression(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("id")
	if _, err := s.deps.Store.GetWorkflow(workflowID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req struct {
		RunID      string `json:"runId"`
		NodeID     string `json:"nodeId"`
		Expression string `json:"expression"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Expression) == "" {
		writeError(w, http.StatusBadRequest, "expression is required")
		return
	}
	if req.RunID == "" {
		runs, err := s.deps.Store.ListRuns(workflowID, 1)
		if err != nil || len(runs) == 0 {
			writeError(w, http.StatusBadRequest, "no run available for preview")
			return
		}
		req.RunID = runs[0].ID
	}
	value, err := s.deps.Engine.Preview(req.RunID, req.NodeID, req.Expression)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runId": req.RunID, "nodeId": req.NodeID, "expression": req.Expression, "value": value,
	})
}

func (s *api) listExamples(w http.ResponseWriter, _ *http.Request) {
	list, err := examples.All()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"examples": list})
}

func (s *api) stats(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.deps.Store.RunStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func readJSON(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, (5<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 5<<20 {
		return errors.New("request body exceeds limit of 5 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func terminal(status string) bool {
	return status == "done" || status == "failed" || status == "cancelled"
}

// validRunStatus reports whether a status filter is one the store knows.
func validRunStatus(status string) bool {
	switch status {
	case "queued", "running", "waiting", "done", "failed", "cancelled":
		return true
	}
	return false
}

func findNode(wf *graph.Workflow, id string) *graph.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}
