package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/schemas"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *api) webhook(w http.ResponseWriter, r *http.Request) {
	if !s.acquireWebhook(w, r) {
		return
	}
	if s.webhookSem != nil {
		defer func() { <-s.webhookSem }()
	}
	path := strings.Trim(r.PathValue("path"), "/")
	wf, nodeID, err := s.workflowByWebhook(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Resolve before the trigger's own checks: method, secret and contract
	// fields may come from the node's profile on a never-published draft.
	wf, err = workflowForRun(s.deps.Store, wf)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	node := findNode(wf, nodeID)
	if node != nil {
		if method, _ := node.Config["method"].(string); method != "" && method != r.Method {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		if secret, _ := node.Config["secret"].(string); strings.TrimSpace(secret) != "" {
			secret = strings.TrimSpace(secret)
			headerName, _ := node.Config["secretHeader"].(string)
			if strings.TrimSpace(headerName) == "" {
				headerName = "X-Webhook-Secret"
			}
			gotSecret := r.Header.Get(headerName)
			if gotSecret == "" {
				gotSecret = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			}
			if gotSecret == "" {
				gotSecret = r.URL.Query().Get("secret")
				if gotSecret != "" {
					if s.deps.RejectWebhookQuerySecrets {
						writeError(w, http.StatusUnauthorized, "query string webhook secrets are rejected on this server; use header or bearer authentication")
						return
					}
					log.Printf("WARNING: webhook %q authenticated with deprecated query string secret; use header or bearer authentication", path)
				}
			}
			if subtle.ConstantTimeCompare([]byte(gotSecret), []byte(secret)) != 1 {
				writeError(w, http.StatusUnauthorized, "invalid webhook secret")
				return
			}
		}
	}

	body := flow.Item{}
	data, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(data) > 1<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
		return
	}

	if node != nil {
		signatureHeader := r.Header.Get(webhookHeaderName(node, "hmacHeader", defaultWebhookHMACHeader))
		timestampHeader := r.Header.Get(webhookHeaderName(node, "timestampHeader", defaultWebhookTimestampHeader))
		if !verifyWebhookHMAC(node, data, signatureHeader, timestampHeader, time.Now()) {
			writeError(w, http.StatusUnauthorized, "invalid webhook signature")
			return
		}
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		var decoded any
		if err := json.Unmarshal(data, &decoded); err == nil {
			if m, ok := decoded.(map[string]any); ok {
				body = flow.Item(m)
			} else {
				body = flow.Item{"body": decoded}
			}
		} else if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			if form, err := url.ParseQuery(string(data)); err == nil && len(form) > 0 {
				for k, v := range form {
					if len(v) == 1 {
						body[k] = v[0]
					} else {
						body[k] = v
					}
				}
			} else {
				body = flow.Item{"body": string(data)}
			}
		} else {
			body = flow.Item{"body": string(data)}
		}
	}

	if len(r.URL.Query()) > 0 {
		queryParams := map[string]any{}
		for k, v := range r.URL.Query() {
			if k == "async" || k == "secret" {
				continue
			}
			if len(v) == 1 {
				queryParams[k] = v[0]
			} else {
				queryParams[k] = v
			}
		}
		if len(queryParams) > 0 {
			if len(body) == 0 {
				for k, v := range queryParams {
					body[k] = v
				}
			}
			body["query"] = queryParams
		}
	}

	reqHeaders := map[string]any{}
	for k, v := range r.Header {
		if len(v) == 1 {
			reqHeaders[k] = v[0]
		} else {
			reqHeaders[k] = v
		}
	}
	body["headers"] = reqHeaders

	// The authentication decision is already made; the persisted run input
	// must not retain the credentials or the signature that authorised it.
	sanitizeWebhookInput(body, node)

	// Input contract (v2 plan D11): the payload is validated against the
	// node's resolved contract, falling back to the workflow's. The result
	// rides the trigger item; a per-trigger strict option rejects
	// non-conforming requests instead.
	if contract := inputContractFor(node, wf); len(contract) > 0 {
		violations := schemas.Check(body, contract)
		body["schemaValid"] = len(violations) == 0
		if len(violations) > 0 {
			rows := make([]map[string]any, 0, len(violations))
			for _, v := range violations {
				rows = append(rows, map[string]any{"path": v.Path, "message": v.Message})
			}
			body["schemaViolations"] = rows
			if strict, _ := node.Config["strictSchema"].(bool); strict {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":      "request body does not match the webhook input contract",
					"violations": rows,
				})
				return
			}
		}
	}

	opts := engine.Options{Trigger: "webhook", Input: body, Actor: "system:webhook"}
	if r.URL.Query().Get("async") == "1" {
		runID, err := s.deps.Engine.StartAsync(context.WithoutCancel(r.Context()), wf, opts)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"runId": runID, "status": "queued"})
		return
	}
	res, err := s.deps.Engine.Start(context.WithoutCancel(r.Context()), wf, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Public callers only learn that the run was accepted. Node inputs,
	// outputs, events and approval tokens stay behind the authenticated API.
	writeJSON(w, http.StatusOK, map[string]any{"runId": res.RunID, "status": res.Status})
}

// inputContractFor returns the resolved input contract for a webhook
// trigger: the node's own contract wins, the workflow's is the fallback.
func inputContractFor(node *graph.Node, wf *graph.Workflow) []schemas.Field {
	if node != nil && len(node.InputSchemaFields) > 0 {
		return node.InputSchemaFields
	}
	return wf.InputSchemaFields
}

// validateWebhookPaths rejects duplicate /hook paths across the workspace.
// Without this, the first workflow returned by the store silently shadows the
// other and requests land on the wrong graph.
func (s *api) validateWebhookPaths(wf *graph.Workflow) error {
	if len(graph.WebhookPaths(wf)) == 0 {
		return nil
	}
	summaries, err := s.deps.Store.ListWorkflows()
	if err != nil {
		return err
	}
	others := make([]*graph.Workflow, 0, len(summaries))
	for _, summary := range summaries {
		if summary.ID == wf.ID {
			continue
		}
		other, err := s.deps.Store.GetWorkflow(summary.ID)
		if err != nil {
			continue
		}
		others = append(others, other)
	}
	return graph.ValidateWebhookPaths(wf, others)
}

const (
	defaultWebhookSecretHeader = "X-Webhook-Secret"
	defaultWebhookHMACHeader   = "X-Hub-Signature-256"
)

func webhookHeaderName(node *graph.Node, key, fallback string) string {
	if node != nil {
		if value, _ := node.Config[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fallback
}

// sanitizeWebhookInput removes the credentials that authenticated a webhook
// call from the input persisted as runs.input. The run already passed the
// secret/HMAC check, so only the stored copy is affected; other headers and
// query parameters are preserved verbatim.
func sanitizeWebhookInput(body flow.Item, node *graph.Node) {
	if body == nil {
		return
	}
	drop := map[string]bool{
		http.CanonicalHeaderKey(webhookHeaderName(node, "secretHeader", defaultWebhookSecretHeader)):       true,
		http.CanonicalHeaderKey(webhookHeaderName(node, "hmacHeader", defaultWebhookHMACHeader)):           true,
		http.CanonicalHeaderKey(webhookHeaderName(node, "timestampHeader", defaultWebhookTimestampHeader)): true,
		"Authorization": true,
	}
	if headers, ok := body["headers"].(map[string]any); ok {
		for name := range headers {
			if drop[http.CanonicalHeaderKey(name)] {
				delete(headers, name)
			}
		}
	}
	if query, ok := body["query"].(map[string]any); ok {
		delete(query, "secret")
	}
}

func (s *api) workflowByWebhook(path string) (*graph.Workflow, string, error) {
	list, err := s.deps.Store.ListWorkflows()
	if err != nil {
		return nil, "", err
	}
	for _, summary := range list {
		wf, err := s.deps.Store.GetWorkflow(summary.ID)
		if err != nil {
			continue
		}
		for _, node := range wf.Nodes {
			if node.Type != "trigger.webhook" {
				continue
			}
			configured, _ := node.Config["path"].(string)
			if strings.Trim(configured, "/") == path {
				return wf, node.ID, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no workflow listens on /hook/%s", path)
}

const defaultWebhookTimestampHeader = "X-Webhook-Timestamp"
const defaultWebhookHMACWindow = 5 * time.Minute

// verifyWebhookHMAC accepts the original body-only signature for
// compatibility. When hmacTimestamps is true, only the timestamped signature is
// accepted; its payload is "<RFC3339 timestamp>.<body>" and timestamps outside
// the fixed window are rejected.
func verifyWebhookHMAC(node *graph.Node, body []byte, signatureHeader, timestampHeader string, now time.Time) bool {
	hmacSecret, _ := node.Config["hmacSecret"].(string)
	hmacSecret = strings.TrimSpace(hmacSecret)
	if hmacSecret == "" {
		return true
	}

	gotSig := strings.TrimPrefix(strings.TrimSpace(signatureHeader), "sha256=")
	enabled, _ := node.Config["hmacTimestamps"].(bool)
	if !enabled {
		legacy := hmac.New(sha256.New, []byte(hmacSecret))
		legacy.Write(body)
		return subtle.ConstantTimeCompare([]byte(gotSig), []byte(hex.EncodeToString(legacy.Sum(nil)))) == 1
	}

	timestamp := strings.TrimSpace(timestampHeader)
	parsed, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return false
	}
	delta := now.Sub(parsed)
	if delta < 0 {
		delta = -delta
	}
	if delta > defaultWebhookHMACWindow {
		return false
	}

	mac := hmac.New(sha256.New, []byte(hmacSecret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(gotSig), []byte(expected)) == 1
}
