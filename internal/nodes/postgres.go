package nodes

import (
	"context"
	"fmt"
	"loom/internal/expr"
	"loom/internal/flow"
	"strings"
)

type postgresNode struct{}

func (postgresNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	query := expr.RenderWithScope(str(in.Config["query"]), expr.FromInput(in))
	operation := strings.ToLower(strings.TrimSpace(str(in.Config["operation"])))
	if err := validatePostgresQuery(query, operation); err != nil {
		return nil, err
	}
	trace(in, "request", map[string]any{
		"operation": operation, "query": query,
	})
	rows, err := in.Host.Query(ctx, in.Credentials["postgres"], query)
	if err != nil {
		return nil, err
	}
	trace(in, "response", map[string]any{"rows": len(rows)})
	arr := make([]any, len(rows))
	for i := range rows {
		arr[i] = map[string]any(rows[i])
	}
	return flow.Output{"main": flow.Item{"rows": arr}}, nil
}

// firstSQLKeyword returns the first statement keyword, skipping leading
// whitespace, SQL line/block comments and an opening parenthesis. Without
// this, a comment-prefixed query would be rejected by operation validation.
func firstSQLKeyword(query string) string {
	rest := query
	for {
		rest = strings.TrimSpace(rest)
		switch {
		case strings.HasPrefix(rest, "--"):
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				rest = rest[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(rest, "/*"):
			rest = rest[skipSQLBlockComment(rest, 0):]
			continue
		case strings.HasPrefix(rest, "("):
			rest = rest[1:]
			continue
		}
		break
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

// readOnlyForbiddenSQL lists statements that mutate data, schema or
// permissions. A declared "query" operation must not contain any of them,
// even inside a data-modifying CTE.
var readOnlyForbiddenSQL = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "DROP": true,
	"ALTER": true, "TRUNCATE": true, "GRANT": true, "REVOKE": true,
	"COPY": true, "CREATE": true, "VACUUM": true, "REINDEX": true,
	"CALL": true, "DO": true, "MERGE": true, "SET": true,
	"LOCK": true, "COMMENT": true, "REFRESH": true, "ANALYZE": true,
}

// validatePostgresQuery enforces a single statement for every db.postgres
// node and the declared-operation allow-list. lib/pq sends a bare query
// through the simple query protocol, which executes every semicolon-separated
// statement, so a node must never let a second statement through.
//
// An absent or unrecognised operation fails closed as "query": the allow-list
// and read-only checks must not be skippable by omitting the field.
func validatePostgresQuery(query, operation string) error {
	scan := scanSQLQuery(query)
	if len(scan.semicolons) > 1 {
		return fmt.Errorf("db.postgres: multiple SQL statements are not allowed")
	}
	if len(scan.semicolons) == 1 && !onlySQLSpaceOrComments(query[scan.semicolons[0]+1:]) {
		return fmt.Errorf("db.postgres: multiple SQL statements are not allowed; a semicolon may only terminate the statement")
	}

	op := strings.ToLower(strings.TrimSpace(operation))
	switch op {
	case "insert", "update":
		// keep the declared operation
	default:
		op = "query"
	}

	keyword := firstSQLKeyword(query)
	var allowed []string
	switch op {
	case "query":
		allowed = []string{"SELECT", "WITH"}
	case "insert":
		allowed = []string{"INSERT", "WITH"}
	case "update":
		allowed = []string{"UPDATE", "WITH"}
	}
	ok := false
	for _, candidate := range allowed {
		if keyword == candidate {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("db.postgres: operation %q does not match statement %q (expected %s)",
			op, keyword, strings.Join(allowed, " or "))
	}

	if op == "query" {
		for _, kw := range scan.starts {
			if readOnlyForbiddenSQL[kw] {
				return fmt.Errorf("db.postgres: operation %q must be read-only; %s is not allowed", op, kw)
			}
		}
		// SELECT ... INTO creates a table, so it is a write even though the
		// leading keyword is SELECT. The allow-list alone cannot catch it.
		for _, token := range scan.tokens {
			if token == "INTO" {
				return fmt.Errorf("db.postgres: operation %q must be read-only; SELECT INTO is not allowed", op)
			}
		}
	}
	return nil
}

// sqlScan records the semicolons and statement-start keywords found outside
// strings and comments, plus every bare word for token-level checks.
type sqlScan struct {
	semicolons []int
	starts     []string
	tokens     []string
}

// scanSQLQuery walks the query with the same quoting/commenting rules
// PostgreSQL uses: single-quoted strings (doubled quotes and backslash
// escapes), double-quoted identifiers, $tag$...$tag$ dollar quoting, --
// line comments and nested /* */ comments.
func scanSQLQuery(query string) sqlScan {
	var out sqlScan
	atStart := true
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			if j := strings.IndexByte(query[i+2:], '\n'); j >= 0 {
				i += 2 + j + 1
			} else {
				i = len(query)
			}
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			i = skipSQLBlockComment(query, i)
		case c == '\'':
			eString := sqlUsesBackslashEscapes(query, i)
			i++
			for i < len(query) {
				if eString && query[i] == '\\' && i+1 < len(query) {
					i += 2
					continue
				}
				if query[i] == '\'' {
					if i+1 < len(query) && query[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			atStart = false
		case c == '"':
			i++
			for i < len(query) {
				if query[i] == '"' {
					if i+1 < len(query) && query[i+1] == '"' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			atStart = false
		case c == '$':
			if end, ok := sqlDollarQuoteEnd(query, i); ok {
				i = end
				atStart = false
				continue
			}
			atStart = false
			i++
		case c == ';':
			out.semicolons = append(out.semicolons, i)
			atStart = true
			i++
		case c == '(':
			// A parenthesised body (e.g. a CTE) starts a new statement-like
			// keyword position so data-modifying CTEs are visible.
			atStart = true
			i++
		case isSQLWordStart(c):
			j := i + 1
			for j < len(query) && isSQLWordPart(query[j]) {
				j++
			}
			word := strings.ToUpper(query[i:j])
			out.tokens = append(out.tokens, word)
			if atStart {
				out.starts = append(out.starts, word)
				atStart = false
			}
			i = j
		case isSQLSpace(c):
			i++
		default:
			atStart = false
			i++
		}
	}
	return out
}

func skipSQLBlockComment(query string, i int) int {
	depth := 1
	i += 2
	for i < len(query) && depth > 0 {
		switch {
		case query[i] == '/' && i+1 < len(query) && query[i+1] == '*':
			depth++
			i += 2
		case query[i] == '*' && i+1 < len(query) && query[i+1] == '/':
			depth--
			i += 2
		default:
			i++
		}
	}
	return i
}

// sqlDollarQuoteEnd returns the index just past a $tag$...$tag$ string. An
// unterminated opener consumes the rest, matching PostgreSQL's error case.
func sqlDollarQuoteEnd(query string, i int) (int, bool) {
	j := i + 1
	if j >= len(query) {
		return 0, false
	}
	if query[j] == '$' {
		tag := "$$"
		if k := strings.Index(query[j+1:], tag); k >= 0 {
			return j + 1 + k + len(tag), true
		}
		return len(query), true
	}
	if !isSQLWordStart(query[j]) {
		return 0, false
	}
	j++
	for j < len(query) && isSQLWordPart(query[j]) {
		j++
	}
	if j >= len(query) || query[j] != '$' {
		return 0, false
	}
	tag := query[i : j+1]
	if k := strings.Index(query[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag), true
	}
	return len(query), true
}

func onlySQLSpaceOrComments(s string) bool {
	for i := 0; i < len(s); {
		switch {
		case isSQLSpace(s[i]):
			i++
		case s[i] == '-' && i+1 < len(s) && s[i+1] == '-':
			if j := strings.IndexByte(s[i+2:], '\n'); j >= 0 {
				i += 2 + j + 1
			} else {
				return true
			}
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipSQLBlockComment(s, i)
		default:
			return false
		}
	}
	return true
}

func isSQLWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isSQLWordPart(c byte) bool {
	return isSQLWordStart(c) || (c >= '0' && c <= '9')
}

func isSQLSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

// sqlUsesBackslashEscapes reports whether the single quote at index quote
// opens a PostgreSQL E'...' string. Ordinary strings treat backslash as a
// literal under standard_conforming_strings, so treating it as an escape
// there could hide a closing quote and let a second statement through.
func sqlUsesBackslashEscapes(query string, quote int) bool {
	if quote == 0 {
		return false
	}
	c := query[quote-1]
	if c != 'E' && c != 'e' {
		return false
	}
	return quote == 1 || !isSQLWordPart(query[quote-2])
}
