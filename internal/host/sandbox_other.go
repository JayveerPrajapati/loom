//go:build !darwin && !linux

package host

import "syscall"

// Platforms without a specific OS sandbox mechanism (the darwin and
// linux builds have their own) run code.python with process-level caps
// only: timeout with process-group kill, output caps, and the
// RLIMIT_AS/RLIMIT_CPU caps the shim applies in the child. Because no
// mechanism exists here, --subprocess-network deny fails closed in Exec
// rather than silently allowing network access.

// sandboxSysProcAttr returns the isolation attrs; none exist here.
func sandboxSysProcAttr(denyNetwork bool) (*syscall.SysProcAttr, error) {
	return nil, nil
}

// sandboxArgv returns the interpreter invocation un-wrapped on this
// platform.
func sandboxArgv(interpreter, workdir, shim string, denyNetwork bool) []string {
	return []string{interpreter, "-I", "-c", shim}
}

// sandboxName identifies the active mechanism for diagnostics.
func sandboxName() string {
	return "none"
}
