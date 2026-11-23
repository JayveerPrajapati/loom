package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"loom/internal/store"
)

// listPageParams is the opt-in paging contract shared by the asset list
// handlers. A request without limit, cursor or q keeps the legacy response
// exactly as it was; paging never applies a hidden default to boot loaders.
type listPageParams struct {
	Limit  int
	Cursor string
	Query  string
	Paged  bool
}

func listPageParamsFrom(r *http.Request) (listPageParams, error) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	cursor := strings.TrimSpace(q.Get("cursor"))
	params := listPageParams{
		Limit:  store.DefaultPageLimit,
		Cursor: cursor,
		Query:  query,
		Paged:  q.Has("limit") || cursor != "" || query != "",
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return listPageParams{}, fmt.Errorf("limit must be a positive integer")
		}
		if n > store.MaxPageLimit {
			n = store.MaxPageLimit
		}
		params.Limit = n
	}
	return params, nil
}

// writeListPage adds the paging overlay to the existing resource key. The
// array key stays profiles/mappings/... so every current client keeps
// working; only callers that opted into paging see the extra fields.
func writeListPage[T any](w http.ResponseWriter, params listPageParams, key string, page store.Page[T]) {
	items := page.Items
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		key:          items,
		"count":      page.Total,
		"limit":      params.Limit,
		"nextCursor": page.NextCursor,
		"hasMore":    page.NextCursor != "",
	})
}

func writeListPageError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrInvalidCursor) {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
