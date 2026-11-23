package store

import (
	"encoding/json"
	"strings"
)

// sensitiveExact names are redacted verbatim. normalizeKey removes separators
// so api_key, apiKey and api-key all collapse to the same token.
var sensitiveExact = map[string]bool{
	"authorization": true, "cookie": true, "set-cookie": true, "x-api-key": true,
	"proxy-authorization": true, "password": true, "passwd": true, "secret": true,
	"token": true, "api_key": true, "apikey": true, "api-key": true,
	"private_key": true, "privatekey": true, "private-key": true, "bearer": true,
	"credential": true, "credentials": true, "auth": true, "authentication": true,
	"access_token": true, "refresh_token": true, "client_secret": true,
}

// sensitiveSuffixes catch compound names such as accessToken or clientSecret
// without redacting counters like tokenCount.
var sensitiveSuffixes = []string{
	"password", "passwd", "secret", "token", "apikey", "privatekey", "credential", "credentials",
}

// nonSensitiveNames are word-shaped names that a naive substring match would
// over-redact. They are only exempt from the substring rules, never from an
// exact or suffix match.
var nonSensitiveNames = map[string]bool{
	"author": true, "authorname": true, "authoremail": true, "authorid": true,
	"tokencount": true, "tokenizer": true, "secretary": true,
	"authenticationstatus": true, "authmode": true, "authmethod": true,
}

func normalizeKey(key string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', '.', ' ':
			return -1
		}
		return r
	}, strings.ToLower(strings.TrimSpace(key)))
}

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
	norm := normalizeKey(clean)
	if sensitiveExact[clean] || sensitiveExact[norm] {
		return true
	}
	for _, suffix := range sensitiveSuffixes {
		if strings.HasSuffix(norm, suffix) {
			return true
		}
	}
	if nonSensitiveNames[norm] {
		return false
	}
	for _, word := range []string{"password", "passwd", "authorization", "credential", "privatekey"} {
		if strings.Contains(norm, word) {
			return true
		}
	}
	if strings.Contains(norm, "secret") && !strings.Contains(norm, "secretary") {
		return true
	}
	return strings.Contains(norm, "apikey")
}
