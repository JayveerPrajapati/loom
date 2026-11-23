//go:build linux

package host

import (
	"fmt"
	"log"
	"os"
	"sync"
	"syscall"
)

// Linux isolates code.python's network with namespaces applied by Go's
// exec package in the fork/exec window (SysProcAttr.Unshareflags — the
// same window the old sandbox_other.go comment claimed was missing):
// an unprivileged user namespace with the current uid mapped to root
// inside, plus an empty network namespace. No network interfaces exist
// inside, so the subprocess cannot reach anything — the OS answer to
// "python can open any socket". Filesystem confinement (pivot_root to
// a minimal rootfs) stays out of scope and documented.

var (
	namespaceOnce sync.Once
	namespaceOK   bool
)

// namespaceSandboxAvailable probes once whether the kernel allows an
// unprivileged user+network namespace: exec /bin/true with the same
// unshare flags the real executions use. A failed probe means --subprocess-network
// deny cannot be honored, and Exec fails closed (D9-2) — the operator
// sees one line explaining why.
func namespaceSandboxAvailable() bool {
	namespaceOnce.Do(func() {
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer devNull.Close()
		attrs, err := namespaceSysProcAttr()
		if err != nil {
			log.Printf("subprocess network isolation unavailable (uid/gid mapping: %v)", err)
			return
		}
		pid, err := syscall.ForkExec("/bin/true", []string{"/bin/true"}, &syscall.ProcAttr{
			Files: []uintptr{0, devNull.Fd(), devNull.Fd()},
			Sys:   attrs,
		})
		if err != nil || pid < 0 {
			log.Printf("subprocess network isolation unavailable (unprivileged user namespaces refused: %v); --subprocess-network deny will fail closed", err)
			return
		}
		var status syscall.WaitStatus
		if _, err := syscall.Wait4(pid, &status, 0, nil); err != nil || !status.Exited() || status.ExitStatus() != 0 {
			log.Printf("subprocess network isolation unavailable (namespace probe exited abnormally); --subprocess-network deny will fail closed")
			return
		}
		namespaceOK = true
		log.Printf("subprocess network isolation active: python executions run in an empty network namespace (no outbound or inbound network)")
	})
	return namespaceOK
}

// namespaceSysProcAttr builds the child process attributes for network
// isolation: a fresh user namespace with the current uid/gid mapped to
// root inside (so the process keeps its file access), joined with an
// empty network namespace.
func namespaceSysProcAttr() (*syscall.SysProcAttr, error) {
	uid := os.Getuid()
	gid := os.Getgid()
	return &syscall.SysProcAttr{
		Unshareflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		UidMappings: []syscall.SysProcIDMap{{
			ContainerID: 0, HostID: uid, Size: 1,
		}},
		GidMappings: []syscall.SysProcIDMap{{
			ContainerID: 0, HostID: gid, Size: 1,
		}},
	}, nil
}

// sandboxSysProcAttr returns the isolation attrs when network denial is
// requested and the mechanism is available; it fails closed otherwise.
func sandboxSysProcAttr(denyNetwork bool) (*syscall.SysProcAttr, error) {
	if !denyNetwork {
		return nil, nil
	}
	if !namespaceSandboxAvailable() {
		return nil, fmt.Errorf("--subprocess-network deny requested but this kernel refuses unprivileged user namespaces; refusing to run python with network access")
	}
	attrs, err := namespaceSysProcAttr()
	if err != nil {
		return nil, err
	}
	return attrs, nil
}

// sandboxArgv returns the interpreter invocation un-wrapped on this
// platform (isolation rides in SysProcAttr, not a wrapper binary).
func sandboxArgv(interpreter, workdir, shim string, denyNetwork bool) []string {
	return []string{interpreter, "-I", "-c", shim}
}

// sandboxName identifies the active mechanism for diagnostics.
func sandboxName() string {
	if namespaceSandboxAvailable() {
		return "namespace"
	}
	return "none"
}
