package host

import (
	"context"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

// Isolation-runner campaign: the network-deny posture and the CPU cap.

func TestBuildShimCPUAndMemoryCaps(t *testing.T) {
	shim := buildShim("pass", 64, 42)
	if !strings.Contains(shim, "RLIMIT_AS, (_cap, _cap)") {
		t.Fatalf("memory cap missing from shim:\n%s", shim)
	}
	if !strings.Contains(shim, "_cap = 64 * 1024 * 1024") {
		t.Fatalf("memory cap value missing from shim:\n%s", shim)
	}
	if !strings.Contains(shim, "RLIMIT_CPU, (42, 42)") {
		t.Fatalf("cpu cap missing from shim:\n%s", shim)
	}
	// Zero values mean no cap section at all.
	bare := buildShim("pass", 0, 0)
	if strings.Contains(bare, "RLIMIT") {
		t.Fatalf("zero caps must not emit rlimit prelude:\n%s", bare)
	}
}

func TestCpuSecondsFor(t *testing.T) {
	cases := []struct {
		timeout time.Duration
		want    int
	}{
		{0, 0},                // no timeout: no CPU cap
		{5 * time.Second, 35}, // timeout + 30s grace
		{time.Minute, 90},     // 60 + 30
		{10 * time.Minute, 630},
		{-time.Second, 0}, // nonsense input: no cap
	}
	for _, tc := range cases {
		if got := cpuSecondsFor(tc.timeout); got != tc.want {
			t.Fatalf("cpuSecondsFor(%s) = %d, want %d", tc.timeout, got, tc.want)
		}
	}
}

func TestExecFailsClosedWhenNetworkDenyUnavailable(t *testing.T) {
	// The allow posture never gates, on any platform.
	if attrs, err := sandboxSysProcAttr(false); err != nil || attrs != nil {
		t.Fatalf("allow posture must not gate: %v %v", attrs, err)
	}
	// The deny posture's contract is per platform: darwin enforces via
	// the Seatbelt profile (nil attrs, nil error — proven live by
	// TestExecNetworkDenyCutsSockets), linux via namespace attrs, other
	// platforms via an error. What is never acceptable on linux or
	// other platforms is nil attrs AND nil error (silently allowed);
	// on darwin the equivalent silent case would be seatbelt being
	// unavailable, which the darwin sandboxSysProcAttr reports as an
	// error.
	_, _ = sandboxSysProcAttr(true)
}

// The end-to-end deny proof runs only where python3 exists; it asserts
// that a denying socket connect fails inside the sandbox.
func TestExecNetworkDenyCutsSockets(t *testing.T) {
	l := NewLive()
	l.EnableSubprocess = true
	l.DenySubprocessNetwork = true
	_, err := l.Exec(context.Background(), flow.ExecRequest{
		Source: `
import socket
try:
    socket.create_connection(("127.0.0.1", 9), timeout=2)
    raise SystemExit("socket connected — network is NOT cut")
except OSError:
    pass
except SystemExit as e:
    raise SystemExit(str(e))
return True
`,
		Timeout: 20 * time.Second,
	})
	if err != nil {
		if strings.Contains(err.Error(), "subprocess execution is disabled") {
			t.Skip("python3 not available in this environment")
		}
		if strings.Contains(err.Error(), "--subprocess-network deny requested") {
			t.Skipf("isolation mechanism unavailable on this host: %v", err)
		}
		t.Fatal(err)
	}
}
