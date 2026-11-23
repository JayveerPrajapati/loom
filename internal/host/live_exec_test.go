package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
	"loom/internal/pyenv"
)

func newExecLive() *Live {
	l := NewLive()
	l.EnableSubprocess = true
	return l
}

func TestExecReturnsItemResult(t *testing.T) {
	l := newExecLive()
	res, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: `return {"doubled": item["n"] * 2}`,
		Stdin:  flow.Item{"n": 21},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", res.ExitCode, res.Stderr)
	}
	result, ok := res.Result.(map[string]any)
	if !ok || result["doubled"] != float64(42) {
		t.Fatalf("result = %#v", res.Result)
	}
}

func TestExecUserPrintsSeparatedFromResult(t *testing.T) {
	l := newExecLive()
	res, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: `
print("hello from python")
import sys
print("to stderr", file=sys.stderr)
return {"ok": True}
`,
		Stdin: flow.Item{},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(res.Stdout, "hello from python") {
		t.Fatalf("stdout = %q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "to stderr") {
		t.Fatalf("stderr = %q", res.Stderr)
	}
	if result, _ := res.Result.(map[string]any); result["ok"] != true {
		t.Fatalf("result = %#v", res.Result)
	}
}

func TestExecArrayAndScalarResults(t *testing.T) {
	l := newExecLive()
	res, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: `return [item["a"], item["b"]]`,
		Stdin:  flow.Item{"a": 1, "b": 2},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	arr, ok := res.Result.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("result = %#v", res.Result)
	}

	res, err = l.Exec(context.Background(), flow.ExecRequest{
		Source: `return item["x"] + 1`,
		Stdin:  flow.Item{"x": 41},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Result != float64(42) {
		t.Fatalf("result = %#v", res.Result)
	}
}

func TestExecSyntaxErrorSurfaces(t *testing.T) {
	l := newExecLive()
	_, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: `return {{{`,
		Stdin:  flow.Item{},
	})
	if err == nil {
		t.Fatal("syntax error must fail")
	}
	if !strings.Contains(err.Error(), "exited") && !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecTimeoutKills(t *testing.T) {
	l := newExecLive()
	started := time.Now()
	_, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: `
import time
while True:
    time.sleep(0.1)
`,
		Stdin:   flow.Item{},
		Timeout: 1 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

func TestExecUsesExplicitEnvironment(t *testing.T) {
	t.Setenv("LOOM_TOKEN", "SERVICE-TOKEN-CANARY")
	t.Setenv("LOOM_BOOTSTRAP_ADMIN_PASSWORD", "BOOTSTRAP-PASSWORD-CANARY")
	t.Setenv("LOOM_SECRET", "SECRET-CANARY")
	originalHome := os.Getenv("HOME")
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", originalHome)
		_ = os.Setenv("PATH", originalPath)
	})

	l := newExecLive()
	res, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: `return dict(__import__("os").environ)`,
		Stdin:  flow.Item{},
	})
	if err != nil {
		t.Fatalf("exec: %v (stderr: %s)", err, res.Stderr)
	}
	env, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("environment result = %#v", res.Result)
	}
	for _, name := range []string{
		"LOOM_TOKEN", "LOOM_BOOTSTRAP_ADMIN_PASSWORD", "LOOM_SECRET",
	} {
		if value, present := env[name]; present {
			t.Fatalf("subprocess inherited %s=%q", name, value)
		}
	}
	for _, name := range []string{"HOME", "TMPDIR", "PATH", "LANG", "LC_ALL"} {
		if value, _ := env[name].(string); strings.TrimSpace(value) == "" {
			t.Fatalf("subprocess environment is missing %s: %#v", name, env)
		}
	}
	if home, _ := env["HOME"].(string); home == originalHome || home == "" {
		t.Fatalf("HOME was not isolated: %q", home)
	}
	if path, _ := env["PATH"].(string); path == originalPath {
		t.Fatal("operator PATH was inherited by subprocess")
	}
}

func TestExecDisabledByDefault(t *testing.T) {
	l := NewLive()
	_, err := l.Exec(context.Background(), flow.ExecRequest{Source: "return 1", Stdin: flow.Item{}})
	if err == nil || !strings.Contains(err.Error(), "--enable-subprocess-nodes") {
		t.Fatalf("err = %v, want disabled", err)
	}
}

// TestExecVenvUsesExplicitEnvironment proves an environment-keyed execution
// (a publish-time venv) still runs under the explicit allowlisted
// environment: the venv interpreter is used (sys.prefix inside the env
// directory) and no inherited service variables leak into the child.
func TestExecVenvUsesExplicitEnvironment(t *testing.T) {
	t.Setenv("LOOM_TOKEN", "SERVICE-TOKEN-CANARY")
	t.Setenv("LOOM_BOOTSTRAP_ADMIN_PASSWORD", "BOOTSTRAP-PASSWORD-CANARY")

	envRoot := t.TempDir()
	key := pyenv.Key([]string{"iso8601==2.1.0"})
	venvDir := filepath.Join(envRoot, key)
	if out, err := exec.Command("python3", "-m", "venv", venvDir).CombinedOutput(); err != nil {
		t.Skipf("python3 -m venv unavailable: %v: %s", err, out)
	}

	l := newExecLive()
	l.EnvsRoot = envRoot
	res, err := l.Exec(context.Background(), flow.ExecRequest{
		EnvKey: key,
		Source: `import sys
return {"prefix": sys.prefix, "env": dict(__import__("os").environ)}`,
		Stdin: flow.Item{},
	})
	if err != nil {
		t.Fatalf("exec: %v (stderr: %s)", err, res.Stderr)
	}
	result, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v", res.Result)
	}
	// macOS resolves /var to /private/var in sys.prefix; compare the
	// symlink-evaluated directory so the assertion is about which
	// interpreter ran, not path spelling.
	resolvedVenv, err := filepath.EvalSymlinks(venvDir)
	if err != nil {
		t.Fatalf("resolve venv dir: %v", err)
	}
	if prefix, _ := result["prefix"].(string); !strings.HasPrefix(prefix, resolvedVenv) {
		t.Fatalf("sys.prefix = %q, want the venv at %q", prefix, resolvedVenv)
	}
	env, _ := result["env"].(map[string]any)
	for _, name := range []string{"LOOM_TOKEN", "LOOM_BOOTSTRAP_ADMIN_PASSWORD"} {
		if value, present := env[name]; present {
			t.Fatalf("venv subprocess inherited %s=%q", name, value)
		}
	}
	if home, _ := env["HOME"].(string); home == "" || home == os.Getenv("HOME") {
		t.Fatalf("HOME was not isolated in venv execution: %q", home)
	}
	if path, _ := env["PATH"].(string); !strings.Contains(path, venvDir) {
		t.Fatalf("venv bin missing from controlled PATH: %q", path)
	}
}

func TestExecMissingEnvironment(t *testing.T) {
	l := newExecLive()
	l.EnvsRoot = t.TempDir()
	_, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: "return 1", Stdin: flow.Item{},
		EnvKey: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	})
	if err == nil || !strings.Contains(err.Error(), "not built") {
		t.Fatalf("err = %v, want environment-not-built", err)
	}
}

func TestExecMemoryCapAppliedOrLoudlyRefused(t *testing.T) {
	l := newExecLive()
	res, err := l.Exec(context.Background(), flow.ExecRequest{
		Source:      `return {"ok": True}`,
		Stdin:       flow.Item{},
		MaxMemoryMB: 16,
	})
	if err != nil {
		// Enforced kernels kill the allocation inside the shim → the
		// failure must be the memory error, not something else.
		if !strings.Contains(err.Error(), "MemoryError") && !strings.Contains(res.Stderr, "MemoryError") {
			t.Fatalf("exec with memory cap failed unexpectedly: %v (stderr: %s)", err, res.Stderr)
		}
		return
	}
	// Kernels that refuse RLIMIT_AS (macOS reserves a huge fixed address
	// space) must SAY so — a silent non-enforcement is the bug this test
	// exists to catch.
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "[loom] memory cap not enforced") {
		t.Fatalf("memory cap was neither enforced nor reported as unenforceable; stderr: %q", res.Stderr)
	}
}

func TestExecWorkdirIsWritableAndRemoved(t *testing.T) {
	l := newExecLive()
	res, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: `f = open("scratch.txt", "w")
f.write("hello")
f.close()
return {"wrote": True}`,
		Stdin: flow.Item{},
	})
	if err != nil {
		t.Fatalf("exec: %v (stderr: %s)", err, res.Stderr)
	}
	if result, ok := res.Result.(map[string]any); !ok || result["wrote"] != true {
		t.Fatalf("result = %#v", res.Result)
	}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "loom-exec-*"))
	for _, m := range matches {
		t.Errorf("workdir leaked: %s", m)
	}
}

func TestClampMemoryMB(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{0, 0}, {-5, 0}, {1, 16}, {16, 16}, {512, 512}, {8192, 8192}, {99999, 8192},
	} {
		if got := clampMemoryMB(tc.in); got != tc.want {
			t.Errorf("clampMemoryMB(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
