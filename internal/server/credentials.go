package server

import (
	"encoding/json"
	"fmt"
	"loom/internal/flow"
	"loom/internal/store"
	"net/http"
	"strings"
)

func (s *api) listCredentials(w http.ResponseWriter, r *http.Request) {
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	counts, err := s.deps.Store.CredentialUsageCounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if params.Paged {
		page, err := s.deps.Store.ListCredentialsPage(params.Query, params.Cursor, params.Limit)
		if err != nil {
			writeListPageError(w, err)
			return
		}
		for i := range page.Items {
			page.Items[i].UsageCount = counts[page.Items[i].ID]
		}
		writeListPage(w, params, "credentials", page)
		return
	}
	list, err := s.deps.Store.ListCredentials()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range list {
		list[i].UsageCount = counts[list[i].ID]
	}
	if list == nil {
		list = []store.CredentialInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": list, "count": len(list)})
}

func (s *api) createCredential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string          `json:"id"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}
	if len(req.Data) == 0 {
		req.Data = json.RawMessage(`{}`)
	}
	if !json.Valid(req.Data) {
		writeError(w, http.StatusBadRequest, "data must be valid JSON")
		return
	}
	if req.ID == "" {
		req.ID = flow.NewID("cred")
	}
	existing, err := s.deps.Store.CredentialExists(req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.deps.Store.SaveCredential(req.ID, req.Type, req.Data); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	event := "credential.created"
	if existing {
		event = "credential.updated"
	}
	s.auditCredential(r, event, req.ID+" type="+req.Type)
	info, err := s.deps.Store.GetCredentialInfo(req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *api) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, err := s.deps.Store.GetCredentialInfo(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	usage, err := s.deps.Store.CredentialUsage(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if summary := credentialUsageSummary(usage); summary != "" {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("credential %q is referenced by %s; remove those references before deleting", id, summary))
		return
	}
	if err := s.deps.Store.DeleteCredential(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.auditCredential(r, "credential.deleted", id+" type="+info.Type)
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) credentialUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.GetCredentialInfo(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	usage, err := s.deps.Store.CredentialUsage(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

func (s *api) rotateCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Data json.RawMessage `json:"data"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Data) == 0 || !json.Valid(req.Data) {
		writeError(w, http.StatusBadRequest, "data must be valid JSON")
		return
	}
	var object map[string]any
	if err := json.Unmarshal(req.Data, &object); err != nil || object == nil {
		writeError(w, http.StatusBadRequest, "data must be a JSON object")
		return
	}
	info, err := s.deps.Store.GetCredentialInfo(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := s.deps.Store.SaveCredential(id, info.Type, req.Data); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	usage, err := s.deps.Store.CredentialUsage(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	references := len(usage.Profiles) + len(usage.Workflows)
	info.UsageCount = references
	s.auditCredential(r, "credential.rotated",
		fmt.Sprintf("%s type=%s references=%d", id, info.Type, references))
	writeJSON(w, http.StatusOK, map[string]any{"credential": info, "references": references})
}

func (s *api) auditCredential(r *http.Request, eventType, detail string) {
	id, label := actorIdentity(r)
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type:   eventType,
		UserID: id,
		Actor:  label,
		IP:     clientIP(r, s.deps.TrustedProxyCIDRs),
		Detail: detail,
	})
}

func credentialUsageSummary(usage store.CredentialUsage) string {
	names := make([]string, 0, len(usage.Profiles)+len(usage.Workflows))
	for _, profile := range usage.Profiles {
		names = append(names, fmt.Sprintf("profile %q", profile.Name))
	}
	for _, workflow := range usage.Workflows {
		if workflow.Current {
			names = append(names, fmt.Sprintf("workflow %q (current)", workflow.Name))
			continue
		}
		names = append(names, fmt.Sprintf("workflow %q v%d", workflow.Name, workflow.Version))
	}
	if len(names) > 3 {
		rest := len(names) - 3
		names = append(names[:3], fmt.Sprintf("and %d more", rest))
	}
	return strings.Join(names, ", ")
}
