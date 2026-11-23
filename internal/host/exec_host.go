package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"encoding/json"
	"os/exec"
	"path/filepath"

	"loom/internal/flow"
)

// subprocessEnv is the explicit allowlist handed to code.python. A nil
// cmd.Env would inherit every server variable, including LOOM_TOKEN and
// bootstrap credentials, so the child receives only the values needed to run
// Python and use the platform trust store.
func subprocessEnv(interpreter, workdir string) []string {
	// Keep the interpreter discoverable for user-spawned subprocesses without
	// forwarding an operator-controlled PATH. The standard directories cover
	// the common system tools; Python's -I mode already ignores PYTHON* config.
	pathEntries := []string{filepath.Dir(interpreter), "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	env := []string{
		"HOME=" + workdir,
		"TMPDIR=" + workdir,
		"PATH=" + strings.Join(pathEntries, string(os.PathListSeparator)),
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
	// Container and macOS setups commonly configure the TLS trust store through
	// these non-secret variables. Without them, Python may reject otherwise
	// valid certificates on hosts whose compiled default is incomplete.
	for _, name := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// resultMarker splits user prints from the JSON result on stdout.
const resultMarker = "\n===LOOM_RESULT===\n"

// maxExecOutput caps captured stdout/stderr per subprocess run, matching
// the JS sandbox's string cap.
const maxExecOutput = 1 << 20

// buildShim wraps user source as the body of a function receiving the
// incoming item, then writes a marker line and the JSON return value to
// stdout. The marker splits user prints from the structured result.
// With memoryMB > 0 the shim first applies an RLIMIT_AS cap in the child
// (soft=hard, so the script cannot raise it). With timeout > 0 an
// RLIMIT_CPU cap (timeout + grace) applies the same way — the
// process-group kill stays the outer backstop, the CPU cap stops a
// spinning child from outliving its deadline in wall-clock terms.
// Kernels that refuse to lower the limits — macOS reserves a huge fixed
// address space for the interpreter — print a note and continue: the
// remaining caps still apply, and the refusal is visible, never silent.
func buildShim(source string, memoryMB int, cpuSeconds int) string {
	if source == "" {
		source = "pass"
	}
	source = strings.TrimLeft(source, "\n")
	body := "    " + strings.ReplaceAll(source, "\n", "\n    ")
	prelude := "import json, sys\n"
	if memoryMB > 0 {
		prelude += fmt.Sprintf(
			"import resource as _r\n"+
				"try:\n"+
				"    _cap = %d * 1024 * 1024\n"+
				"    _r.setrlimit(_r.RLIMIT_AS, (_cap, _cap))\n"+
				"except Exception:\n"+
				"    sys.stderr.write(\"[loom] memory cap not enforced (kernel refused setrlimit); time and output caps still apply\\n\")\n",
			memoryMB)
	}
	if cpuSeconds > 0 {
		prelude += fmt.Sprintf(
			"try:\n"+
				"    _r.setrlimit(_r.RLIMIT_CPU, (%d, %d))\n"+
				"except Exception:\n"+
				"    sys.stderr.write(\"[loom] cpu cap not enforced (kernel refused setrlimit)\\n\")\n",
			cpuSeconds, cpuSeconds)
	}
	prelude += "\n"
	return prelude +
		"def _loom_main(item):\n" + body + "\n\n" +
		"_result = _loom_main(json.loads(sys.stdin.read())[\"item\"])\n" +
		"sys.stdout.write(\"\\n===LOOM_RESULT===\\n\")\n" +
		"sys.stdout.write(json.dumps(_result, default=str))\n" +
		"sys.stdout.flush()\n"
}

// clampMemoryMB bounds the requested subprocess address-space cap:
// 0 stays 0 (no cap requested), otherwise 16 MiB .. 8 GiB.
func clampMemoryMB(mb int) int {
	switch {
	case mb <= 0:
		return 0
	case mb < 16:
		return 16
	case mb > 8192:
		return 8192
	}
	return mb
}

// cpuSecondsFor derives the child's RLIMIT_CPU from the requested
// timeout: the deadline plus a 30-second grace so the process-group
// kill (not SIGKILL from RLIMIT_CPU) remains the visible failure mode,
// with a 10-second floor.
func cpuSecondsFor(timeout time.Duration) int {
	if timeout <= 0 {
		return 0
	}
	seconds := int(timeout/time.Second) + 30
	if seconds < 10 {
		seconds = 10
	}
	return seconds
}

// Exec runs Python source in an isolated subprocess. The protocol: the
// item arrives on stdin as JSON; the return value is written after the
// marker on stdout; non-zero exits surface as errors with stderr.
func (l *Live) Exec(ctx context.Context, req flow.ExecRequest) (flow.ExecResult, error) {
	if !l.EnableSubprocess {
		return flow.ExecResult{}, fmt.Errorf("subprocess execution is disabled; start with --enable-subprocess-nodes")
	}
	// The network-deny posture fails closed when the platform mechanism
	// is unavailable (D9-2): the operator asked for isolation, so
	// running python with network access instead would be the silent
	// failure this codebase never tolerates.
	isolation, err := sandboxSysProcAttr(l.DenySubprocessNetwork)
	if err != nil {
		return flow.ExecResult{}, err
	}
	// The interpreter is invoked by its original path — a venv python is a
	// symlink, and exec'ing the resolved target would lose pyvenv.cfg (and
	// with it the environment's site-packages). The Seatbelt profile is
	// what must list RESOLVED paths: literal filters match the resolution
	// of the path being exec'd, so sandboxArgv resolves for the allow-list
	// while the invocation keeps the symlink.
	interpreter := "python3"
	if resolved, err := exec.LookPath(interpreter); err == nil {
		interpreter = resolved
	}
	if req.EnvKey != "" {
		if l.EnvsRoot == "" {
			return flow.ExecResult{}, fmt.Errorf("subprocess environment root is not configured")
		}
		envPython := filepath.Join(l.EnvsRoot, req.EnvKey, "bin", "python")
		// sandbox-exec (and execvp in general) needs an absolute
		// interpreter path — a venv path built from a relative -data
		// would fail to exec inside the sandbox wrapper.
		if abs, err := filepath.Abs(envPython); err == nil {
			envPython = abs
		}
		if _, err := os.Stat(envPython); err != nil {
			return flow.ExecResult{}, fmt.Errorf("python environment %s is not built; publish the workflow to build it (runs never install dependencies)", req.EnvKey[:12])
		}
		interpreter = envPython
	}

	shim := buildShim(req.Source, clampMemoryMB(req.MaxMemoryMB), cpuSecondsFor(req.Timeout))
	stdin, err := json.Marshal(map[string]any{"item": req.Stdin})
	if err != nil {
		return flow.ExecResult{}, fmt.Errorf("encode stdin: %w", err)
	}

	runCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	// A private, writable workdir per execution: the only place the OS
	// sandbox profile allows writes, and it is removed afterwards.
	workdir, err := os.MkdirTemp("", "loom-exec-")
	if err != nil {
		return flow.ExecResult{}, fmt.Errorf("workdir: %w", err)
	}
	defer os.RemoveAll(workdir)

	argv := sandboxArgv(interpreter, workdir, shim, l.DenySubprocessNetwork)
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr cappedBuffer
	stdout.max, stderr.max = maxExecOutput, maxExecOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = workdir
	cmd.Env = subprocessEnv(interpreter, workdir)
	// Own a process group so a timeout kills user-spawned children too;
	// on Linux the network-namespace isolation rides the same attrs.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if isolation != nil {
		isolation.Setpgid = true
		cmd.SysProcAttr = isolation
	}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	runErr := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return flow.ExecResult{}, fmt.Errorf("subprocess exceeded its timeout (%s)", req.Timeout)
	}
	res := flow.ExecResult{
		ExitCode: cmd.ProcessState.ExitCode(),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	if runErr != nil {
		if res.ExitCode < 0 {
			return res, fmt.Errorf("subprocess killed by signal %d: %s", -res.ExitCode, previewText(res.Stderr))
		}
		return res, fmt.Errorf("subprocess exited %d: %s", res.ExitCode, previewText(res.Stderr))
	}
	userOutput, result, ok := splitResult(res.Stdout)
	if !ok {
		return res, fmt.Errorf("subprocess produced no result marker (the source must be a function body that returns a value): %s", previewText(res.Stdout))
	}
	res.Stdout = userOutput
	if len(result) > 0 && string(result) != "null" {
		if err := json.Unmarshal(result, &res.Result); err != nil {
			return res, fmt.Errorf("subprocess result is not valid JSON: %w", err)
		}
	}
	return res, nil
}

// splitResult separates user prints from the JSON result after the marker.
func splitResult(stdout string) (userOutput string, result []byte, ok bool) {
	index := strings.Index(stdout, resultMarker)
	if index < 0 {
		return stdout, nil, false
	}
	return stdout[:index], []byte(strings.TrimSpace(stdout[index+len(resultMarker):])), true
}

// previewText renders a bounded error excerpt.
func previewText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 200 {
		return text[len(text)-200:]
	}
	return text
}

// cappedBuffer caps captured subprocess output.
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.max == 0 {
		c.max = maxExecOutput
	}
	room := c.max - c.buf.Len()
	if room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
			c.truncated = true
			return len(p), nil
		}
		return c.buf.Write(p)
	}
	c.truncated = true
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	if c.truncated {
		return c.buf.String() + "\n[output truncated]"
	}
	return c.buf.String()
}
