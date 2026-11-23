package schemas

import (
	"strings"
	"testing"
)

func strp() map[string]any {
	return map[string]any{"name": "ada", "age": float64(36), "admin": true}
}

func TestValidateAcceptsSoundContract(t *testing.T) {
	fields := []Field{
		{Name: "name", Type: TypeString, Required: true},
		{Name: "age", Type: TypeNumber},
		{Name: "admin", Type: TypeBoolean},
		{Name: "address", Type: TypeObject, Fields: []Field{
			{Name: "city", Type: TypeString, Required: true},
		}},
		{Name: "tags", Type: TypeArray, Items: &Field{Name: "tag", Type: TypeString}},
	}
	if err := Validate(fields); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := EnforceDepth(fields); err != nil {
		t.Fatalf("depth: %v", err)
	}
}

func TestValidateRejectsBrokenContracts(t *testing.T) {
	cases := []struct {
		name   string
		fields []Field
		want   string
	}{
		{"empty name", []Field{{Name: " ", Type: TypeString}}, "no name"},
		{"bad name", []Field{{Name: "bad-name", Type: TypeString}}, "may only use"},
		{"unknown type", []Field{{Name: "x", Type: "datetime"}}, "unknown type"},
		{"duplicate", []Field{{Name: "x", Type: TypeString}, {Name: "x", Type: TypeNumber}}, "duplicate"},
		{"empty object", []Field{{Name: "obj", Type: TypeObject}}, "declares no nested fields"},
		{"object with items", []Field{{Name: "obj", Type: TypeObject, Fields: []Field{{Name: "x", Type: TypeString}}, Items: &Field{Name: "i", Type: TypeString}}}, "cannot declare array items"},
		{"array with fields", []Field{{Name: "arr", Type: TypeArray, Fields: []Field{{Name: "x", Type: TypeString}}}}, "use items"},
		{"scalar with fields", []Field{{Name: "s", Type: TypeString, Fields: []Field{{Name: "x", Type: TypeString}}}}, "cannot declare nested fields"},
		{"nested bad name", []Field{{Name: "obj", Type: TypeObject, Fields: []Field{{Name: "bad!", Type: TypeString}}}}, "may only use"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.fields)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestCheckFindsViolations(t *testing.T) {
	fields := []Field{
		{Name: "name", Type: TypeString, Required: true},
		{Name: "age", Type: TypeNumber},
		{Name: "admin", Type: TypeBoolean},
		{Name: "address", Type: TypeObject, Fields: []Field{
			{Name: "city", Type: TypeString, Required: true},
		}},
		{Name: "tags", Type: TypeArray, Items: &Field{Name: "tag", Type: TypeString}},
	}

	// A conforming payload reports nothing.
	ok := map[string]any{
		"name": "ada", "age": float64(36), "admin": true,
		"address": map[string]any{"city": "london", "zip": "unused"},
		"tags":    []any{"a", "b"},
	}
	if v := Check(ok, fields); len(v) != 0 {
		t.Fatalf("conforming payload got violations: %v", v)
	}

	// Every breach is named with its path.
	bad := map[string]any{
		"age":     "old",
		"admin":   1,
		"address": map[string]any{"city": 7},
		"tags":    []any{"a", 2},
	}
	got := map[string]string{}
	for _, v := range Check(bad, fields) {
		got[v.Path] = v.Message
	}
	if msg, ok := got["name"]; !ok || msg != "required field is missing" {
		t.Fatalf("name: %v", got)
	}
	if msg, ok := got["age"]; !ok || msg != "expected a number" {
		t.Fatalf("age: %v", got)
	}
	if msg, ok := got["admin"]; !ok || msg != "expected a boolean" {
		t.Fatalf("admin: %v", got)
	}
	if msg, ok := got["address.city"]; !ok || msg != "expected a string" {
		t.Fatalf("address.city: %v", got)
	}
	if msg, ok := got["tags[1]"]; !ok || msg != "expected a string" {
		t.Fatalf("tags[1]: %v", got)
	}

	// null counts as absent: only required fields report.
	nulls := map[string]any{"name": nil}
	if v := Check(nulls, fields); len(v) != 1 || v[0].Path != "name" {
		t.Fatalf("null handling: %v", v)
	}

	// Extra fields are allowed — contracts never forbid.
	if v := Check(map[string]any{"name": "x", "extra": true}, fields); len(v) != 0 {
		t.Fatalf("extra field reported: %v", v)
	}
}

func TestCheckNonObjectRoot(t *testing.T) {
	v := Check([]any{"a"}, []Field{{Name: "x", Type: TypeString}})
	if len(v) != 1 || v[0].Message != "expected an object" {
		t.Fatalf("non-object root: %v", v)
	}
}

func TestDepthCap(t *testing.T) {
	deep := Field{Name: "l1", Type: TypeObject, Fields: []Field{
		{Name: "l2", Type: TypeObject, Fields: []Field{
			{Name: "l3", Type: TypeObject, Fields: []Field{
				{Name: "l4", Type: TypeObject, Fields: []Field{
					{Name: "l5", Type: TypeObject, Fields: []Field{
						{Name: "l6", Type: TypeObject, Fields: []Field{
							{Name: "l7", Type: TypeString},
						}},
					}},
				}},
			}},
		}},
	}}
	if err := Validate([]Field{deep}); err != nil {
		t.Fatalf("validate walks deep contracts: %v", err)
	}
	if err := EnforceDepth([]Field{deep}); err == nil {
		t.Fatalf("depth cap did not trip at 7 levels")
	}
}

// Regression: named map types (flow.Item) must pass the object check —
// a plain map[string]any assertion failed them and every annotated
// webhook run reported a bogus root violation.
type namedItem map[string]any
type namedStrings []string

func TestCheckAcceptsNamedMapTypes(t *testing.T) {
	fields := []Field{
		{Name: "email", Type: TypeString, Required: true},
		{Name: "tags", Type: TypeArray, Items: &Field{Name: "tag", Type: TypeString}},
	}
	violations := Check(namedItem{"email": 7, "tags": namedStrings{"x", "y"}}, fields)
	if len(violations) != 1 {
		t.Fatalf("violations = %+v", violations)
	}
	if violations[0].Path != "email" || violations[0].Message != "expected a string" {
		t.Fatalf("first violation = %+v", violations[0])
	}
	if ok := Check(namedItem{"email": "a@b.c", "tags": namedStrings{"x"}}, fields); len(ok) != 0 {
		t.Fatalf("valid named payload flagged: %+v", ok)
	}
}
