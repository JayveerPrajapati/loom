//go:build darwin

package host

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// macOS hardens code.python with a Seatbelt profile (sandbox-exec): the
// subprocess may write only inside its per-execution workdir, and may exec
// only the interpreter itself. Reads and network stay allowed — the node
// documents that plainly. sandbox-exec is deprecated by Apple but still the
// only userland Seatbelt entry point; when it is missing or refuses to
// apply (nested sandboxes, future macOS), execution continues with the
// process-level caps and one loud log line — never silently "protected".

var (
	seatbeltOnce sync.Once
	seatbeltOK   bool
)

// seatbeltAvailable probes sandbox-exec once: a trivial profile must apply
// and run. A failed probe means every subprocess runs WITHOUT the OS
// sandbox (time/output caps still apply) and the operator sees one line
// saying so.
func seatbeltAvailable() bool {
	seatbeltOnce.Do(func() {
		path, err := exec.LookPath("sandbox-exec")
		if err != nil {
			log.Printf("subprocess OS sandbox unavailable: sandbox-exec not on PATH; python runs with process-level caps only")
			return
		}
		cmd := exec.Command(path, "-p", "(version 1)(allow default)", "/usr/bin/true")
		if err := cmd.Run(); err != nil {
			log.Printf("subprocess OS sandbox unavailable (sandbox-exec probe failed: %v); python runs with process-level caps only", err)
			return
		}
		seatbeltOK = true
		log.Printf("subprocess OS sandbox active: python executions are confined by a Seatbelt profile (writes only to their workdir)")
	})
	return seatbeltOK
}

// seatbeltProfile renders the SBPL profile for one execution. Filter order
// matters — the last matching rule wins. denyNetwork adds (deny network*):
// the subprocess loses all network access (isolation-runner D9-1), not
// just writes/exec confinement.
func seatbeltProfile(interpreter, workdir string, denyNetwork bool) string {
	// Filters match resolved paths: /var is a symlink to /private/var on
	// macOS, so an unresolved tempdir path would never match its own
	// write-allow rule.
	if resolved, err := filepath.EvalSymlinks(workdir); err == nil {
		workdir = resolved
	}
	var allows strings.Builder
	for _, path := range pythonExecPaths(interpreter) {
		allows.WriteString("(allow process-exec* (literal " + sbq(path) + "))\n")
	}
	profile := "(version 1)\n" +
		"(allow default)\n" +
		"(deny file-write*)\n" +
		"(allow file-write* (subpath " + sbq(workdir) + "))\n" +
		"(deny process-exec*)\n" +
		allows.String()
	if denyNetwork {
		profile += "(deny network*)\n"
	}
	return profile
}

// pythonExecPaths lists the binaries CPython may exec as part of starting
// itself: the resolved interpreter, plus the macOS framework executable
// that framework builds (homebrew, python.org installers) re-exec into at
// startup. Literal filters match resolved paths, so every spawn target
// must be listed by its resolved path — while the invocation itself keeps
// the original (possibly symlinked) path so venvs still work.
func pythonExecPaths(interpreter string) []string {
	paths := []string{interpreter}
	if resolved, err := filepath.EvalSymlinks(interpreter); err == nil {
		paths = append(paths, resolved)
		interpreter = resolved
	}
	dir := filepath.Dir(interpreter)
	for level := 0; level < 6; level++ {
		matches, err := filepath.Glob(filepath.Join(dir,
			"Frameworks", "Python.framework", "Versions", "*",
			"Resources", "Python.app", "Contents", "MacOS", "Python"))
		if err == nil && len(matches) > 0 {
			paths = append(paths, matches...)
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return paths
}

// sbq quotes a path as an SBPL string literal.
func sbq(path string) string {
	path = filepath.Clean(path)
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, "\"", "\\\"")
	return "\"" + path + "\""
}

// sandboxArgv wraps the interpreter invocation with the Seatbelt profile
// when the sandbox is usable on this machine.
func sandboxArgv(interpreter, workdir, shim string, denyNetwork bool) []string {
	argv := []string{interpreter, "-I", "-c", shim}
	if !seatbeltAvailable() {
		return argv
	}
	return append([]string{"sandbox-exec", "-p", seatbeltProfile(interpreter, workdir, denyNetwork)}, argv...)
}

// sandboxSysProcAttr implements the network-deny gate on darwin: there
// is no namespace mechanism, the Seatbelt profile carries the denial,
// and the gate here fails closed when sandbox-exec is unavailable.
func sandboxSysProcAttr(denyNetwork bool) (*syscall.SysProcAttr, error) {
	if denyNetwork && !seatbeltAvailable() {
		return nil, fmt.Errorf("--subprocess-network deny requested but the Seatbelt sandbox is unavailable on this host; refusing to run python with network access")
	}
	return nil, nil
}

// sandboxName identifies the active mechanism for diagnostics.
func sandboxName() string {
	if seatbeltAvailable() {
		return "seatbelt"
	}
	return "none"
}
