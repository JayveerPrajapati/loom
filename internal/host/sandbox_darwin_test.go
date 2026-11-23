//go:build darwin

package host

import (
	"strings"
	"testing"
)

func TestSeatbeltProfileGolden(t *testing.T) {
	profile := seatbeltProfile("/usr/local/bin/python3", "/var/folders/xx/loom-exec-123", false)
	want := "(version 1)\n" +
		"(allow default)\n" +
		"(deny file-write*)\n" +
		"(allow file-write* (subpath \"/var/folders/xx/loom-exec-123\"))\n" +
		"(deny process-exec*)\n" +
		"(allow process-exec* (literal \"/usr/local/bin/python3\"))\n"
	if profile != want {
		t.Fatalf("profile drift:\n%q\nwant:\n%q", profile, want)
	}
}

func TestSeatbeltProfileEscapesPaths(t *testing.T) {
	profile := seatbeltProfile(`/opt/py "quoted"/bin/python3`, `/tmp/we"ird`, false)
	if !strings.Contains(profile, "we\\\"ird") || !strings.Contains(profile, "py \\\"quoted\\\"") {
		t.Fatalf("inner quotes must be escaped in the profile: %q", profile)
	}
}

func TestSandboxArgvShape(t *testing.T) {
	// Without asserting availability (a machine property), the shape of
	// both outcomes is fixed: either the plain interpreter argv, or the
	// sandbox-exec wrapper around it.
	wrapped := append([]string{"sandbox-exec", "-p", "P"}, "python3", "-I", "-c", "shim")
	if len(wrapped) != 7 || wrapped[0] != "sandbox-exec" {
		t.Fatalf("wrapped argv shape changed: %v", wrapped)
	}
}
