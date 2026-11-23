package main

import (
	"strings"
	"testing"

	"loom/internal/server"
	"loom/internal/store"
)

func TestStartupPostureNotice(t *testing.T) {
	anonymous := startupPostureNotice(true, false, false, server.SubprocessState{})
	if !strings.Contains(anonymous, "Anonymous admin mode is enabled.\nUse only for local development.\n") {
		t.Fatalf("anonymous notice = %q", anonymous)
	}

	subprocess := startupPostureNotice(false, false, false, server.SubprocessState{
		Enabled: true, Policy: "policy.json", CapMB: 1024, Network: "deny",
	})
	for _, want := range []string{
		"editors can execute arbitrary Python on this host",
		"dedicated container or VM",
		"Network policy: deny",
		"Environment isolation",
		"Package policy: policy.json",
		"cap 1024 MiB",
	} {
		if !strings.Contains(subprocess, want) {
			t.Fatalf("subprocess notice missing %q: %s", want, subprocess)
		}
	}

	if empty := startupPostureNotice(false, false, false, server.SubprocessState{}); empty != "" {
		t.Fatalf("disabled posture printed %q", empty)
	}

	override := startupPostureNotice(true, true, true, server.SubprocessState{})
	if !strings.Contains(override, "LOOM_ALLOW_ANONYMOUS_UNSAFE=1") {
		t.Fatalf("production override notice = %q", override)
	}
}

func TestCapabilityStateWarnings(t *testing.T) {
	state := capabilityState(true, true, true, true,
		server.SubprocessState{Enabled: true, Network: "allow"},
		server.EgressState{Mode: "allow"})
	if !state.Prod || !state.Anonymous || !state.ServiceToken || !state.Subprocess.Enabled {
		t.Fatalf("state = %#v", state)
	}
	joined := strings.Join(state.Warnings, "\n")
	for _, want := range []string{
		"Anonymous admin mode is enabled",
		"LOOM_ALLOW_ANONYMOUS_UNSAFE=1",
		"arbitrary Python",
		"container or VM",
		"Python network access is allowed",
		"Egress is unrestricted",
		"service token",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings missing %q: %v", want, state.Warnings)
		}
	}
	if state.Warnings == nil {
		t.Fatal("warnings must be an empty slice, not nil")
	}
}

func TestAuditStartupCapabilities(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	auditStartupCapabilities(st, server.CapabilitiesState{
		Subprocess: server.SubprocessState{Enabled: true, Network: "deny", Policy: "policy.json"},
		Anonymous:  true,
	})
	events, err := st.ListAuthEvents(10)
	if err != nil {
		t.Fatalf("list auth events: %v", err)
	}
	found := map[string]bool{}
	for _, event := range events {
		if event.Actor != "system" {
			t.Fatalf("capability event actor = %q, want system", event.Actor)
		}
		found[event.Type] = true
	}
	for _, want := range []string{"capability.subprocess", "capability.anonymous"} {
		if !found[want] {
			t.Fatalf("missing %q in %#v", want, events)
		}
	}
}
