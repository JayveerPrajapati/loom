// Package schemas is Loom's data-contract model (Configuration Hub v2,
// plan D10): a contract is a typed field list mirroring the node catalog's
// field shape. Contracts describe the shape of data entering or leaving a
// surface (webhook input, workflow in/out via subflow, node outputs); they
// validate known fields and never forbid extra ones — payload authors can
// carry more than the contract lists.
package schemas

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Type is the set of field types a contract can express.
type Type string

const (
	TypeString  Type = "string"
	TypeNumber  Type = "number"
	TypeBoolean Type = "boolean"
	TypeObject  Type = "object"
	TypeArray   Type = "array"
)

// Valid reports whether t is one of the expressible types.
func (t Type) Valid() bool {
	switch t {
	case TypeString, TypeNumber, TypeBoolean, TypeObject, TypeArray:
		return true
	}
	return false
}

const (
	// maxDepth caps nested object/array contracts so validation and the
	// editor stay bounded.
	maxDepth = 6
	// maxFields caps the number of fields per level for the same reason.
	maxFields = 64
)

var nameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Field is one entry in a data contract. Objects carry nested Fields;
// arrays may carry an element contract in Items.
type Field struct {
	Name        string `json:"name"`
	Type        Type   `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	// Fields is the nested contract; object only.
	Fields []Field `json:"fields,omitempty"`
	// Items is the element contract; array only.
	Items *Field `json:"items,omitempty"`
}

// Violation is one contract breach found by Check.
type Violation struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (v Violation) Error() string {
	if v.Path == "" {
		return v.Message
	}
	return v.Path + ": " + v.Message
}

// Validate checks the structural integrity of a contract: field names,
// types, per-type child rules, duplicate names, and depth/size caps.
// The returned error names the offending field path.
func Validate(fields []Field) error {
	return validateLevel(fields, "")
}

func validateLevel(fields []Field, prefix string) error {
	if len(fields) > maxFields {
		return fmt.Errorf("%s: too many fields (max %d)", pathOf(prefix, ""), maxFields)
	}
	seen := map[string]bool{}
	for i := range fields {
		f := &fields[i]
		name := strings.TrimSpace(f.Name)
		if name == "" {
			return fmt.Errorf("%s: field %d has no name", orRoot(prefix), i+1)
		}
		if !nameRe.MatchString(name) {
			return fmt.Errorf("%s: field name %q may only use letters, digits and underscores",
				pathOf(prefix, name), name)
		}
		if seen[name] {
			return fmt.Errorf("%s: duplicate field name %q", pathOf(prefix, name), name)
		}
		seen[name] = true
		f.Name = name
		if !f.Type.Valid() {
			return fmt.Errorf("%s: field %q has unknown type %q", pathOf(prefix, name), name, f.Type)
		}
		childPrefix := pathOf(prefix, name)
		switch f.Type {
		case TypeObject:
			if len(f.Fields) == 0 {
				return fmt.Errorf("%s: object field %q declares no nested fields", pathOf(prefix, name), name)
			}
			if f.Items != nil {
				return fmt.Errorf("%s: object field %q cannot declare array items", pathOf(prefix, name), name)
			}
			if err := validateLevel(f.Fields, childPrefix); err != nil {
				return err
			}
		case TypeArray:
			if len(f.Fields) > 0 {
				return fmt.Errorf("%s: array field %q cannot declare nested fields directly (use items)", pathOf(prefix, name), name)
			}
			if f.Items != nil {
				if !f.Items.Type.Valid() {
					return fmt.Errorf("%s: array items type %q is unknown", childPrefix, f.Items.Type)
				}
				if err := validateLevel([]Field{*f.Items}, childPrefix); err != nil {
					return err
				}
			}
		default:
			if len(f.Fields) > 0 {
				return fmt.Errorf("%s: scalar field %q cannot declare nested fields", pathOf(prefix, name), name)
			}
			if f.Items != nil {
				return fmt.Errorf("%s: scalar field %q cannot declare array items", pathOf(prefix, name), name)
			}
		}
	}
	return nil
}

// depth reports the deepest nesting level of a field list.
func depth(fields []Field) int {
	d := 1
	for i := range fields {
		sub := 0
		if len(fields[i].Fields) > 0 {
			sub = depth(fields[i].Fields)
		}
		if fields[i].Items != nil {
			sub = depth([]Field{*fields[i].Items})
		}
		if sub+1 > d {
			d = sub + 1
		}
	}
	return d
}

// EnforceDepth is a hard stop for absurdly nested contracts; Validate
// recurses structurally, so the cap keeps that bounded.
func EnforceDepth(fields []Field) error {
	if d := depth(fields); d > maxDepth {
		return fmt.Errorf("contract nesting depth %d exceeds the maximum of %d", d, maxDepth)
	}
	return nil
}

func pathOf(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func orRoot(prefix string) string {
	if prefix == "" {
		return "contract root"
	}
	return prefix
}

// Check validates a payload against a contract. Unknown fields are
// allowed: contracts describe what must be present and correctly typed,
// never what is forbidden. A nil value (JSON null) counts as absent, so
// only required fields report a violation for it.
func Check(value any, fields []Field) []Violation {
	return checkObjectFields(value, fields, "", 0)
}

func checkObjectFields(value any, fields []Field, prefix string, level int) []Violation {
	if level > maxDepth {
		return []Violation{{Path: prefix, Message: "contract nesting exceeds the maximum depth"}}
	}
	obj, ok := asObject(value)
	if !ok {
		return []Violation{{Path: prefix, Message: "expected an object"}}
	}
	var out []Violation
	for _, f := range fields {
		raw, present := obj[f.Name]
		if !present || raw == nil {
			if f.Required {
				out = append(out, Violation{Path: pathOf(prefix, f.Name), Message: "required field is missing"})
			}
			continue
		}
		out = append(out, checkField(raw, f, pathOf(prefix, f.Name), level)...)
	}
	return out
}

func checkField(raw any, f Field, path string, level int) []Violation {
	if level > maxDepth {
		return []Violation{{Path: path, Message: "contract nesting exceeds the maximum depth"}}
	}
	switch f.Type {
	case TypeString:
		if _, ok := raw.(string); !ok {
			return []Violation{{Path: path, Message: "expected a string"}}
		}
	case TypeNumber:
		if _, ok := toNumber(raw); !ok {
			return []Violation{{Path: path, Message: "expected a number"}}
		}
	case TypeBoolean:
		if _, ok := raw.(bool); !ok {
			return []Violation{{Path: path, Message: "expected a boolean"}}
		}
	case TypeObject:
		if len(f.Fields) == 0 {
			return nil
		}
		return checkObjectFields(raw, f.Fields, path, level+1)
	case TypeArray:
		arr, ok := asArray(raw)
		if !ok {
			return []Violation{{Path: path, Message: "expected an array"}}
		}
		if f.Items == nil {
			return nil
		}
		var out []Violation
		for i, el := range arr {
			p := fmt.Sprintf("%s[%d]", path, i)
			out = append(out, checkField(el, *f.Items, p, level+1)...)
		}
		return out
	}
	return nil
}

// asObject accepts any map with string keys — including named types like
// flow.Item, which fail a plain map[string]any type assertion.
func asObject(value any) (map[string]any, bool) {
	if m, ok := value.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface()
		}
		return out, true
	}
	return nil, false
}

// asArray accepts any slice, including named element types like
// []map[string]any.
func asArray(value any) ([]any, bool) {
	if a, ok := value.([]any); ok {
		return a, true
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice {
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = rv.Index(i).Interface()
		}
		return out, true
	}
	return nil, false
}

// toNumber accepts every JSON number shape Go's decoder produces.
func toNumber(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

// SortedFields returns fields ordered by name for stable diffs and
// display; contracts are unordered sets semantically.
func SortedFields(fields []Field) []Field {
	out := make([]Field, len(fields))
	copy(out, fields)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ErrEmptyName is a convenience sentinel for API layers that want to map
// validation failures to a status code.
var ErrEmptyName = errors.New("contract has no fields")
