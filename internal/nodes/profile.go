package nodes

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// ValidateProfileConfig checks a Configuration Hub profile body against the
// catalog allowlist (plan §18 D1): every key must be a field the node type
// marks Profileable, and every value must satisfy that field's contract.
// Secret and non-profileable keys are rejected structurally, so a profile
// can never carry node configuration it should not.
func ValidateProfileConfig(def Definition, config map[string]any) error {
	if config == nil {
		return nil
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		spec, ok := def.Config[key]
		if !ok {
			return fmt.Errorf("%s has no config field %q", def.Type, key)
		}
		if !spec.Profileable {
			return fmt.Errorf("%s config %q is not profileable", def.Type, key)
		}
		raw := config[key]
		if !profileFieldMatches(raw, spec.Type) {
			return fmt.Errorf("profile config %q must be a %s", key, spec.Type)
		}
		if len(spec.Enum) > 0 {
			if s, _ := raw.(string); !slices.Contains(spec.Enum, s) {
				return fmt.Errorf("profile config %q must be one of %s, got %q", key, strings.Join(spec.Enum, ", "), s)
			}
		}
		if spec.Pattern != "" {
			if s, ok := raw.(string); ok {
				if re, err := regexp.Compile(spec.Pattern); err == nil && !re.MatchString(s) {
					return fmt.Errorf("profile config %q must match %s, got %q", key, spec.Pattern, s)
				}
			}
		}
		if numVal, ok := raw.(float64); ok {
			if spec.Min != nil && numVal < *spec.Min {
				return fmt.Errorf("profile config %q must be at least %v, got %v", key, *spec.Min, numVal)
			}
			if spec.Max != nil && numVal > *spec.Max {
				return fmt.Errorf("profile config %q must be at most %v, got %v", key, *spec.Max, numVal)
			}
		}
	}
	return nil
}

func profileFieldMatches(v any, t FieldType) bool {
	switch t {
	case FieldString:
		_, ok := v.(string)
		return ok
	case FieldNumber:
		_, ok := v.(float64)
		return ok
	case FieldBoolean:
		_, ok := v.(bool)
		return ok
	case FieldArray:
		_, ok := v.([]any)
		return ok
	case FieldObject:
		_, ok := v.(map[string]any)
		return ok
	case FieldAny:
		return true
	default:
		return false
	}
}

// ProfileableCredentialSlots returns the credential slot names a profile
// for this node type may carry references for.
func ProfileableCredentialSlots(def Definition) []string {
	slots := append([]string{}, def.Credentials...)
	slots = append(slots, def.OptionalCredentials...)
	sort.Strings(slots)
	return slots
}
