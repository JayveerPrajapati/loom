package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidCursor marks a malformed or unsupported list cursor. Handlers
// map it to 400; the store never guesses a starting position.
var ErrInvalidCursor = errors.New("invalid cursor")

const (
	// DefaultPageLimit is the page size when a caller opts into paging
	// without naming a limit. MaxPageLimit caps every paged list.
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// Page is one keyset page plus the filtered total. Items is always non-nil
// for a successful call so JSON encodes [] rather than null.
type Page[T any] struct {
	Items      []T
	Total      int
	NextCursor string
}

// VisibilityScope mirrors the server's asset visibility rule at SQL level:
// anything that is not explicitly private reads as shared, private reads
// only for admins and its creator. Creator-less assets are migrated to
// shared, so the empty-user branch is only a safety net.
type VisibilityScope struct {
	Admin  bool
	UserID string
}

func (v VisibilityScope) clause(alias string) (string, []any) {
	if v.Admin {
		return "1=1", nil
	}
	if v.UserID == "" {
		return alias + ".visibility <> 'private'", nil
	}
	return "(" + alias + ".visibility <> 'private' OR " + alias + ".creator_id = ?)", []any{v.UserID}
}

type listCursor struct {
	V  int    `json:"v"`
	K  string `json:"k"`
	ID string `json:"id"`
}

func normalizePageLimit(limit int) int {
	if limit <= 0 {
		return DefaultPageLimit
	}
	if limit > MaxPageLimit {
		return MaxPageLimit
	}
	return limit
}

// encodeListCursor stores the sort key and id of the last returned row.
// The key is already normalized by the caller (lower-cased names).
func encodeListCursor(key, id string) string {
	raw, _ := json.Marshal(listCursor{V: 1, K: key, ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeListCursor(cursor string) (string, string, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return "", "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	var c listCursor
	if err := json.Unmarshal(raw, &c); err != nil || c.V != 1 || c.ID == "" {
		return "", "", ErrInvalidCursor
	}
	return c.K, c.ID, nil
}

// likePattern makes a user query a literal LIKE pattern. The escape
// character is a backslash; callers add ESCAPE '\' to each expression.
func likePattern(query string) string {
	query = strings.TrimSpace(strings.ToLower(query))
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(query) + "%"
}

func appendSearch(where *[]string, args *[]any, pattern string, exprs ...string) {
	if pattern == "" {
		return
	}
	parts := make([]string, 0, len(exprs))
	for _, expr := range exprs {
		parts = append(parts, "lower("+expr+") LIKE ? ESCAPE '\\'")
	}
	*where = append(*where, "("+strings.Join(parts, " OR ")+")")
	for range exprs {
		*args = append(*args, pattern)
	}
}

// ascNameCursorClause compares a lower-cased name expression with the
// cursor key through SQLite's own lower() so the boundary matches the
// ORDER BY expression even for non-ASCII names.
func ascNameCursorClause(cursor, keyExpr, idExpr string) (string, []any, error) {
	if strings.TrimSpace(cursor) == "" {
		return "", nil, nil
	}
	key, id, err := decodeListCursor(cursor)
	if err != nil {
		return "", nil, err
	}
	return "(" + keyExpr + " > lower(?) OR (" + keyExpr + " = lower(?) AND " + idExpr + " > ?))",
		[]any{key, key, id}, nil
}

func descCursorClause(cursor, keyExpr, idExpr string) (string, []any, error) {
	if strings.TrimSpace(cursor) == "" {
		return "", nil, nil
	}
	key, id, err := decodeListCursor(cursor)
	if err != nil {
		return "", nil, err
	}
	return "(" + keyExpr + " < ? OR (" + keyExpr + " = ? AND " + idExpr + " < ?))",
		[]any{key, key, id}, nil
}
