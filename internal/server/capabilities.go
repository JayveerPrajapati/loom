package server

import "net/http"

func (s *api) capabilities(w http.ResponseWriter, _ *http.Request) {
	state := s.deps.Capabilities
	if state.Warnings == nil {
		state.Warnings = []string{}
	}
	writeJSON(w, http.StatusOK, state)
}
