package server

import (
	"net/http"
)

// listStorageOps answers GET /api/ops/storage (storage-operations
// campaign, D8-1): the read-only operational view of the append-only
// store — growth, dedup win, largest consumers, reclaimable-bytes
// forecasts at retention horizons. Editor role.
func (s *api) listStorageOps(w http.ResponseWriter, r *http.Request) {
	stats, err := s.deps.Store.StorageStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stats": stats,
		"hint":  "Reclaimable forecasts use the prune rules: terminal runs older than the horizon, plus blobs nothing surviving references.",
		"retention": map[string]any{
			"command": "loom serve --retention-days N",
			"note":    "Set a retention window on serve to prune automatically, or run `loom doctor` / `loom backup` for manual maintenance.",
		},
	})
}
