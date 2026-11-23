package host

import (
	"fmt"
	"strings"
)

// validateSingleSQLStatement mirrors the nodes-layer statement guard so a
// direct Live.Query call cannot send a second statement to lib/pq's simple
// query protocol.
func validateSingleSQLStatement(query string) error {
	semicolons := scanSQLGuardSemicolons(query)
	if len(semicolons) > 1 {
		return fmt.Errorf("db.postgres: multiple SQL statements are not allowed")
	}
	if len(semicolons) == 1 && !sqlGuardOnlySpaceOrComments(query[semicolons[0]+1:]) {
		return fmt.Errorf("db.postgres: multiple SQL statements are not allowed; a semicolon may only terminate the statement")
	}
	return nil
}

func scanSQLGuardSemicolons(query string) []int {
	var out []int
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
			i = skipSQLGuardBlockComment(query, i)
		case c == '\'':
			eString := sqlGuardUsesBackslashEscapes(query, i)
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
		case c == '$':
			if end, ok := sqlGuardDollarQuoteEnd(query, i); ok {
				i = end
				continue
			}
			i++
		case c == ';':
			out = append(out, i)
			i++
		default:
			i++
		}
	}
	return out
}

func skipSQLGuardBlockComment(query string, i int) int {
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

func sqlGuardDollarQuoteEnd(query string, i int) (int, bool) {
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
	if !sqlGuardWordStart(query[j]) {
		return 0, false
	}
	j++
	for j < len(query) && sqlGuardWordPart(query[j]) {
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

func sqlGuardOnlySpaceOrComments(s string) bool {
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f' || s[i] == '\v':
			i++
		case s[i] == '-' && i+1 < len(s) && s[i+1] == '-':
			if j := strings.IndexByte(s[i+2:], '\n'); j >= 0 {
				i += 2 + j + 1
			} else {
				return true
			}
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipSQLGuardBlockComment(s, i)
		default:
			return false
		}
	}
	return true
}

func sqlGuardWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func sqlGuardWordPart(c byte) bool {
	return sqlGuardWordStart(c) || (c >= '0' && c <= '9')
}

// sqlGuardUsesBackslashEscapes mirrors nodes.sqlUsesBackslashEscapes: only
// E'...' strings consume backslash escapes.
func sqlGuardUsesBackslashEscapes(query string, quote int) bool {
	if quote == 0 {
		return false
	}
	c := query[quote-1]
	if c != 'E' && c != 'e' {
		return false
	}
	return quote == 1 || !sqlGuardWordPart(query[quote-2])
}
