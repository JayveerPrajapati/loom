package server

import (
	"errors"
	"fmt"
	"net/http"

	"loom/internal/graph"
	"loom/internal/store"
)

// workflowRunError names the asset kind a run could not resolve, so the API
// answers with the same issue codes save and publish use.
type workflowRunError struct {
	Code string
	Err  error
}

func (e *workflowRunError) Error() string { return fmt.Sprintf("%s: %v", e.Code, e.Err) }

func (e *workflowRunError) Unwrap() error { return e.Err }

// workflowForRun returns a fully resolved copy of wf for execution. The
// stored draft keeps only authored values: profile config, bundle refs,
// contracts, mappings, scripts and connector templates stay in their own
// stores until publish inlines them. Manual, webhook, event and
// workflow-cron runs all start from that stored row, so they resolve the
// same chain here. The stored document is left untouched; the run snapshot
// carries the resolved copy, exactly like a published version.
func workflowForRun(st *store.Store, wf *graph.Workflow) (*graph.Workflow, error) {
	runWF, err := cloneWorkflow(wf)
	if err != nil {
		return nil, err
	}
	// Same order as publish (internal/server/workflows.go): profile values
	// first so bundle refs land before their resolvers run, then the asset
	// resolvers, then catalog defaults as the final effective-value layer.
	cache := store.NewLookupCache()
	if _, err := st.ResolveProfileRefsWith(runWF, cache); err != nil {
		return nil, &workflowRunError{Code: "missing_profile", Err: err}
	}
	if _, err := st.ResolveSchemaRefsWith(runWF, cache); err != nil {
		return nil, &workflowRunError{Code: "missing_schema", Err: err}
	}
	if _, err := st.ResolveMappingRefsWith(runWF, cache); err != nil {
		return nil, &workflowRunError{Code: "missing_mapping", Err: err}
	}
	if _, err := st.ResolveScriptRefsWith(runWF, cache); err != nil {
		return nil, &workflowRunError{Code: "missing_script", Err: err}
	}
	if _, err := st.ResolveConnectorRefsWith(runWF, cache); err != nil {
		return nil, &workflowRunError{Code: "missing_connector", Err: err}
	}
	st.ApplyCatalogDefaults(runWF)
	// A published doc is read back from JSON, so the engine only ever sees
	// untyped JSON shapes ([]any, map[string]any). The resolvers write typed
	// slices (e.g. mappingRows as []store.MappingRow) in memory; normalize
	// them here so a draft run validates and executes exactly like a
	// published one.
	normalized, err := cloneWorkflow(runWF)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

// writeRunPrepError answers a failed run preparation with the issue shape
// save and publish use.
func writeRunPrepError(w http.ResponseWriter, err error) {
	var prep *workflowRunError
	if errors.As(err, &prep) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":  "invalid workflow",
			"issues": []graph.Issue{{Code: prep.Code, Message: prep.Err.Error()}},
		})
		return
	}
	writeError(w, http.StatusUnprocessableEntity, err.Error())
}
