package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"loom/internal/engine"
	"loom/internal/store"
	"net/http"
	"strings"
	"unicode/utf8"
)

func (s *api) approvalCallback(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	run, err := s.deps.Store.GetRunByResumeToken(token)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	wantsJSON := strings.Contains(r.Header.Get("Content-Type"), "application/json") ||
		strings.Contains(r.Header.Get("Accept"), "application/json")
	if r.Method == http.MethodGet {
		// Opening the link only renders the decision page. A GET must never
		// approve a run: link prefetchers and scanners issue GETs.
		approvalPage(w, http.StatusOK, token, run, "", "", "")
		return
	}
	decision := ""
	note := ""
	viaSlack := false
	if wantsJSON {
		var req struct {
			Decision string `json:"decision"`
			Note     string `json:"note"`
		}
		if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		decision = req.Decision
		note = strings.TrimSpace(req.Note)
	} else {
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		decision = r.FormValue("decision")
		if decision == "" {
			decision = slackDecision(r.FormValue("payload"))
			viaSlack = decision != ""
			// Slack renders decision buttons only, so a click carries no
			// free-text reason. Record where the rejection came from.
			if decision == "rejected" {
				note = "Rejected via Slack"
			}
		} else {
			note = strings.TrimSpace(r.FormValue("note"))
		}
	}
	if decision == "" {
		approvalPage(w, http.StatusOK, token, run, "", note, "")
		return
	}
	if decision != "approved" && decision != "rejected" {
		writeError(w, http.StatusBadRequest, "decision must be approved or rejected")
		return
	}
	if utf8.RuneCountInString(note) > 500 {
		approvalPage(w, http.StatusUnprocessableEntity, token, run, "", note,
			"A rejection reason must be 500 characters or fewer.")
		return
	}
	if decision == "rejected" && utf8.RuneCountInString(note) < 3 {
		approvalPage(w, http.StatusBadRequest, token, run, "", note,
			"A rejection reason is required (at least 3 characters).")
		return
	}
	source := "link"
	actor := "system:approval-link"
	if viaSlack {
		source = "slack"
		actor = "system:slack"
	}
	res, err := s.deps.Engine.ResumeDecision(r.Context(), run.ID, decision,
		engine.DecisionMeta{ActorID: actor, ActorLabel: actor, Source: source}, note)
	if err != nil {
		approvalPage(w, http.StatusConflict, token, run, "", note, err.Error())
		return
	}
	if wantsJSON {
		writeJSON(w, http.StatusOK, map[string]any{"runId": res.RunID, "status": res.Status})
		return
	}
	approvalPage(w, http.StatusOK, token, run, decision, note, "")
}

func slackDecision(payload string) string {
	if payload == "" {
		return ""
	}
	var decoded struct {
		Actions []struct {
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil || len(decoded.Actions) == 0 {
		return ""
	}
	if value := strings.TrimSpace(decoded.Actions[0].Value); value != "" {
		return value
	}
	return strings.TrimSpace(decoded.Actions[0].ActionID)
}

// approvalPage renders the public approval surface. It is server-rendered so a
// link works without the app bundle, but it uses the same visual vocabulary:
// one card, one primary action, tokens rather than arbitrary colours.
func approvalPage(w http.ResponseWriter, status int, token string, run *store.Run, decision, note, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'")
	w.WriteHeader(status)

	workflow := html.EscapeString(run.WorkflowID)
	message := "This run is waiting for a decision."
	if len(run.ResumeData) > 0 {
		var data map[string]any
		if json.Unmarshal(run.ResumeData, &data) == nil {
			if text, ok := data["message"].(string); ok && strings.TrimSpace(text) != "" {
				message = text
			}
		}
	}

	expiry := ""
	if run.ResumeExpiresAt != nil {
		expiry = fmt.Sprintf(`<p class="meta">This link expires %s UTC.</p>`,
			run.ResumeExpiresAt.UTC().Format("2 Jan 2006 15:04"))
	}

	style := `<style>
:root{--bg:#f7f7f8;--panel:#fff;--ink:#111114;--muted:#5c5c63;--line:#d9d9de;--accent:#0a6fc0;--accent-ink:#fff;--danger:#b42318;--radius:12px}
@media (prefers-color-scheme:dark){:root{--bg:#0b0f14;--panel:#151b23;--ink:#f2f5f8;--muted:#a3b0bf;--line:#2a3542;--accent:#0a6fc0;--danger:#f97066}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--ink);font:15px/1.55 system-ui,-apple-system,"Segoe UI",sans-serif;padding:24px}
main{width:100%;max-width:36rem;background:var(--panel);border:1px solid var(--line);border-radius:var(--radius);padding:28px}
h1{font-size:19px;line-height:1.3;margin:0 0 4px}
p{margin:12px 0}
.meta{color:var(--muted);font-size:12px;margin:0 0 16px}
code{font:12px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;background:var(--bg);border:1px solid var(--line);border-radius:4px;padding:1px 5px}
label{display:block;font-size:12px;font-weight:600;margin:20px 0 6px}
textarea{width:100%;min-height:84px;resize:vertical;font:inherit;color:inherit;background:var(--bg);border:1px solid var(--line);border-radius:8px;padding:10px 12px}
textarea:focus-visible,button:focus-visible,a:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.actions{display:flex;gap:10px;margin-top:16px;flex-wrap:wrap}
button{font:inherit;font-weight:600;border-radius:8px;border:1px solid var(--line);background:var(--panel);color:var(--ink);padding:10px 16px;min-height:44px;cursor:pointer}
button.primary{background:var(--accent);border-color:var(--accent);color:var(--accent-ink)}
button.danger{color:var(--danger);border-color:var(--line)}
[role=alert]{border:1px solid var(--danger);color:var(--danger);border-radius:8px;padding:10px 12px;font-size:13px}
a{color:var(--accent)}
</style>`

	if decision == "approved" || decision == "rejected" {
		reason := ""
		if strings.TrimSpace(note) != "" {
			reason = fmt.Sprintf("<p><strong>Reason:</strong> %s</p>", html.EscapeString(note))
		}
		fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Loom approval</title>%s
<body><main><h1>Run %s</h1><p class="meta">Workflow %s</p><p>Run <code>%s</code> was %s.</p>%s
<p><a href="/">Open Loom</a></p></main></body></html>`,
			style, decision, workflow, html.EscapeString(run.ID), decision, reason)
		return
	}

	action := "/hook/approval/" + token
	errorBlock := ""
	if errMsg != "" {
		errorBlock = fmt.Sprintf(`<p role="alert">%s</p>`, html.EscapeString(errMsg))
	}
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Loom approval</title>%s
<body><main>
<h1>Approve this run?</h1>
<p class="meta">Workflow %s · Run <code>%s</code></p>
<p>%s</p>%s%s
<form method="post" action="%s">
<label for="note">Decision note</label>
<textarea id="note" name="note" rows="3" maxlength="500" placeholder="Required when rejecting (at least 3 characters)">%s</textarea>
<div class="actions">
<button class="primary" name="decision" value="approved">Approve</button>
<button class="danger" name="decision" value="rejected">Reject</button>
</div>
</form>
</main></body></html>`, style, workflow, html.EscapeString(run.ID), html.EscapeString(message),
		errorBlock, expiry, html.EscapeString(action), html.EscapeString(note))
}
