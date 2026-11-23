package expr_test

import (
	"context"
	"fmt"
	"testing"

	"loom/internal/expr"
	"loom/internal/flow"
)

func TestSandboxCapsStringAllocation(t *testing.T) {
	cases := []struct {
		name       string
		expression string
		wantErr    bool
	}{
		{"repeat far above cap", "'A'.repeat(134217728)", true},
		{"repeat just above cap", "'A'.repeat(1048577)", true},
		{"repeat below cap", "'A'.repeat(1024).length", false},
		{"repeat at cap", "'A'.repeat(1048576).length", false},
		{"repeat long unit above cap", "'abcd'.repeat(300000)", true},
		{"padStart above cap", "'x'.padStart(1048577, 'a')", true},
		{"padEnd above cap", "'x'.padEnd(2000000, 'a')", true},
		{"padStart below cap works", "'x'.padStart(5, 'a') === 'axxxx'", false},
		{"padEnd below cap works", "'x'.padEnd(3, 'a') === 'xaa'", false},
		{"ordinary array literal", "[1,2,3].map(function(x){return x*2}).join(',') === '2,4,6'", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := expr.EvalValue(context.Background(), tc.expression, flow.Item{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("EvalValue(%q) error = %v, want error %v", tc.expression, err, tc.wantErr)
			}
		})
	}
}

func TestArrayConstructorCannotBypassCap(t *testing.T) {
	for _, expression := range []string{
		"[].constructor(1e9)",
		"new ([].constructor)(1e9)",
		"Array(1e9)",
		"new Array(1e9)",
	} {
		if _, err := expr.EvalValue(context.Background(), expression, flow.Item{}); err == nil {
			t.Errorf("EvalValue(%q) succeeded; array length cap was bypassed", expression)
		}
	}

	v, err := expr.EvalValue(context.Background(), "[].constructor === Array && [].constructor(3).length", flow.Item{})
	if err != nil {
		t.Fatalf("safe array constructor should still work: %v", err)
	}
	if got := fmt.Sprint(v); got != "3" {
		t.Fatalf("constructor identity/length = %q, want 3", got)
	}
}

// Array.from is a native allocator: {length: 1e9} makes goja reserve the whole
// result in one call, before the script interrupt can fire. The same 100000
// element cap as the constructor must apply.
func TestArrayFromCannotBypassCap(t *testing.T) {
	for _, expression := range []string{
		"Array.from({length:1e9})",
		"Array.from({length:100001})",
		"[].constructor.from({length:1e9})",
	} {
		if _, err := expr.EvalValue(context.Background(), expression, flow.Item{}); err == nil {
			t.Errorf("EvalValue(%q) succeeded; Array.from cap was bypassed", expression)
		}
	}

	cases := map[string]string{
		"Array.from([1,2,3]).join(',')":                             "1,2,3",
		"Array.from('abc').join(',')":                               "a,b,c",
		"Array.from({length:100000}).length":                        "100000",
		"Array.from({length:3}, function(_,i){return i}).join(',')": "0,1,2",
		"Array.of(1,2,3).join(',')":                                 "1,2,3",
	}
	for expression, want := range cases {
		v, err := expr.EvalValue(context.Background(), expression, flow.Item{})
		if err != nil {
			t.Errorf("EvalValue(%q): %v", expression, err)
			continue
		}
		if got := fmt.Sprint(v); got != want {
			t.Errorf("EvalValue(%q) = %q, want %q", expression, got, want)
		}
	}
}
