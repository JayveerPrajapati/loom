package store

import (
	"encoding/json"
	"regexp"
	"strings"
)

var sensitiveKeyRegex = regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key|private[_-]?key|auth|bearer|credential)`)

// RedactValue recursively walks a map or slice and masks values whose keys indicate sensitive information.
func RedactValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			if isSensitiveKey(k) {
				out[k] = "[redacted]"
			} else {
				out[k] = RedactValue(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = RedactValue(child)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(val))
		for k, child := range val {
			if isSensitiveKey(k) {
				out[k] = "[redacted]"
			} else {
				out[k] = child
			}
		}
		return out
	default:
		return v
	}
}

// RedactJSON parses JSON, redacts any sensitive keys, and returns the modified JSON bytes.
// If data is not valid JSON, it returns the input untouched.
func RedactJSON(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return data
	}
	redacted := RedactValue(root)
	out, err := json.Marshal(redacted)
	if err != nil {
		return data
	}
	return out
}

func isSensitiveKey(key string) bool {
	clean := strings.ToLower(strings.TrimSpace(key))
	switch clean {
	case "authorization", "cookie", "set-cookie", "x-api-key", "proxy-authorization":
		return true
	}
	return sensitiveKeyRegex.MatchString(clean)
}
