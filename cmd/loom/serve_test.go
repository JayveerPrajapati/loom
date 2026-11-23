package main

import (
	"strings"
	"testing"
)

func TestSubprocessTokenGate(t *testing.T) {
	tests := []struct {
		name             string
		enableSubprocess bool
		token            string
		ack              string
		wantErrSubstring string
	}{
		{name: "subprocess with service token refuses startup", enableSubprocess: true, token: "service-token", ack: "",
			wantErrSubstring: "editor/admin boundary"},
		{name: "whitespace-only token is no token", enableSubprocess: true, token: "   ", ack: ""},
		{name: "no subprocess opt-in is unrestricted", enableSubprocess: false, token: "service-token", ack: ""},
		{name: "explicit acknowledgement allows the combination", enableSubprocess: true, token: "service-token", ack: "1"},
		{name: "blank acknowledgement is not an opt-in", enableSubprocess: true, token: "service-token", ack: "   ",
			wantErrSubstring: "editor/admin boundary"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSubprocessTokenGate(tc.enableSubprocess, tc.token, tc.ack)
			if tc.wantErrSubstring == "" {
				if err != nil {
					t.Fatalf("validateSubprocessTokenGate(%v, %q, %q) = %v, want nil", tc.enableSubprocess, tc.token, tc.ack, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateSubprocessTokenGate(%v, %q, %q) = nil, want failure", tc.enableSubprocess, tc.token, tc.ack)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstring) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), tc.wantErrSubstring)
			}
			if !strings.Contains(err.Error(), "LOOM_SUBPROCESS_UNSAFE_WITH_TOKEN") {
				t.Fatalf("error must name the acknowledgement env: %q", err.Error())
			}
		})
	}
}

// TestServeSubprocessTokenGateStartup exercises the gate through runServe so
// the wiring (flag + env) is covered, not just the helper.
func TestServeSubprocessTokenGateStartup(t *testing.T) {
	err := runServe([]string{"-data", t.TempDir(), "--enable-subprocess-nodes", "--token", "service-token"})
	if err == nil || !strings.Contains(err.Error(), "LOOM_SUBPROCESS_UNSAFE_WITH_TOKEN") {
		t.Fatalf("runServe error = %v, want the subprocess/token gate", err)
	}
}

func TestServeMaxTokenLifetimeValidation(t *testing.T) {
	t.Run("negative flag", func(t *testing.T) {
		err := runServe([]string{"-data", t.TempDir(), "--max-token-lifetime", "-1h"})
		if err == nil || !strings.Contains(err.Error(), "max-token-lifetime") {
			t.Fatalf("runServe error = %v, want the max-token-lifetime guard", err)
		}
	})
	t.Run("invalid env", func(t *testing.T) {
		t.Setenv("LOOM_MAX_TOKEN_LIFETIME", "forever")
		err := runServe([]string{"-data", t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "LOOM_MAX_TOKEN_LIFETIME") {
			t.Fatalf("runServe error = %v, want the env parse failure", err)
		}
	})
}

func TestServeWaitSweepIntervalValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "zero", args: []string{"--wait-sweep-interval", "0s"}, want: "wait-sweep-interval"},
		{name: "negative", args: []string{"--wait-sweep-interval", "-1s"}, want: "wait-sweep-interval"},
		{name: "below minimum", args: []string{"--wait-sweep-interval", "10ms"}, want: "wait-sweep-interval"},
		{name: "unparsable flag value", args: []string{"--wait-sweep-interval", "soon"}, want: "wait-sweep-interval"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runServe(append([]string{"-data", t.TempDir()}, tc.args...))
			if err == nil {
				t.Fatalf("runServe accepted %v, want startup failure", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runServe error = %q, want it to mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestServeDBReadersValidation(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		env    string
		want   string
		wantOK bool
	}{
		{name: "flag below one", args: []string{"--db-readers", "0"}, want: "db-readers"},
		{name: "negative flag", args: []string{"--db-readers", "-3"}, want: "db-readers"},
		{name: "env invalid", args: nil, env: "soon", want: "LOOM_DB_READERS"},
		{name: "env zero", args: nil, env: "0", want: "LOOM_DB_READERS"},
		{name: "flag ok", args: []string{"--db-readers", "8", "--addr", "127.0.0.1:1"}, wantOK: true},
		{name: "env ok", args: []string{"--addr", "127.0.0.1:1"}, env: "2", wantOK: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("LOOM_DB_READERS", tc.env)
			}
			err := runServe(append([]string{"--allow-anonymous"}, tc.args...))
			if tc.wantOK {
				if err == nil {
					t.Fatal("runServe served instead of failing on the privileged port")
				}
				if !strings.Contains(err.Error(), "permission denied") && !strings.Contains(err.Error(), "listen") {
					t.Fatalf("runServe should fail only on listen, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runServe error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestServeWaitSweepIntervalEnvValidation(t *testing.T) {
	t.Setenv("LOOM_WAIT_SWEEP_INTERVAL", "bogus")
	if err := runServe([]string{"-data", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "LOOM_WAIT_SWEEP_INTERVAL") {
		t.Fatalf("runServe error = %v, want invalid LOOM_WAIT_SWEEP_INTERVAL to fail startup", err)
	}

	t.Setenv("LOOM_WAIT_SWEEP_INTERVAL", "5ms")
	if err := runServe([]string{"-data", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "wait-sweep-interval") {
		t.Fatalf("runServe error = %v, want env-derived sub-minimum interval to fail startup", err)
	}
}

// The bootstrap-credential hygiene warning (P3-5): fired only when accounts
// already exist and the bootstrap password is still exported — the bootstrap
// path itself (zero users) stays silent.
func TestBootstrapCredWarning(t *testing.T) {
	if w := bootstrapCredWarning(0, true); w != "" {
		t.Fatalf("zero users + env set: warning %q, want empty (first boot creates the admin)", w)
	}
	if w := bootstrapCredWarning(3, false); w != "" {
		t.Fatalf("users exist + env unset: warning %q, want empty", w)
	}
	w := bootstrapCredWarning(3, true)
	if w == "" || !strings.Contains(w, "LOOM_BOOTSTRAP_ADMIN_PASSWORD") || !strings.Contains(w, "3 users") {
		t.Fatalf("users exist + env set: warning %q, want a mention of the variable and the user count", w)
	}
}

func TestParseTrustedProxyCIDRs(t *testing.T) {
	networks, err := parseTrustedProxyCIDRs("10.0.0.0/8, 2001:db8::/32")
	if err != nil {
		t.Fatalf("parse valid CIDRs: %v", err)
	}
	if len(networks) != 2 {
		t.Fatalf("network count = %d, want 2", len(networks))
	}

	for _, value := range []string{"", "   "} {
		if got, err := parseTrustedProxyCIDRs(value); err != nil || got != nil {
			t.Fatalf("parse %q = %#v, %v; want nil, nil", value, got, err)
		}
	}
	for _, value := range []string{"10.0.0.1", "10.0.0.0/8,,192.0.2.0/24"} {
		if _, err := parseTrustedProxyCIDRs(value); err == nil {
			t.Fatalf("parseTrustedProxyCIDRs(%q) succeeded, want error", value)
		}
	}
}
