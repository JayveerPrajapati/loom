package main

import (
	"fmt"
	"strings"

	"loom/internal/server"
	"loom/internal/store"
)

// startupPostureNotice is the operator-facing startup summary for the two
// deployment postures that change the trust boundary. The anonymous text is
// exact: it is the documented development-only warning.
func startupPostureNotice(allowAnonymous, prod, allowAnonymousUnsafe bool, subprocess server.SubprocessState) string {
	var b strings.Builder
	if allowAnonymous {
		b.WriteString("Anonymous admin mode is enabled.\nUse only for local development.\n")
	}
	if prod && allowAnonymous && allowAnonymousUnsafe {
		b.WriteString("WARNING: production anonymous admin mode is running under LOOM_ALLOW_ANONYMOUS_UNSAFE=1.\n")
	}
	if subprocess.Enabled {
		b.WriteString("\nSubprocess nodes are enabled: editors can execute arbitrary Python on this host.\n")
		b.WriteString("Run Loom in a dedicated container or VM; OS-level confinement is not a security boundary.\n")
		b.WriteString(fmt.Sprintf("Network policy: %s. Environment isolation: dependency environments are built per requirement set and hash-verified; service-token and environment variables are not inherited.\n", subprocess.Network))
		if subprocess.Policy != "" {
			b.WriteString(fmt.Sprintf("Package policy: %s (cap %d MiB per environment).\n", subprocess.Policy, subprocess.CapMB))
		}
	}
	return b.String()
}

// capabilityState is the admin-only capability/auth summary. Warnings are
// consequences, not metrics: each one names a posture the operator chose.
func capabilityState(prod, anonymous, allowAnonymousUnsafe, serviceToken bool, subprocess server.SubprocessState, egress server.EgressState) server.CapabilitiesState {
	warnings := make([]string, 0, 6)
	if anonymous {
		warnings = append(warnings, "Anonymous admin mode is enabled; every unauthenticated request is an admin. Use only for local development.")
	}
	if prod && anonymous && allowAnonymousUnsafe {
		warnings = append(warnings, "Production is running with anonymous admin mode under LOOM_ALLOW_ANONYMOUS_UNSAFE=1.")
	}
	if subprocess.Enabled {
		warnings = append(warnings, "Editors can execute arbitrary Python on this host.")
		warnings = append(warnings, "Run Loom in a dedicated container or VM; OS-level confinement is not a security boundary.")
		if subprocess.Network == "allow" {
			warnings = append(warnings, "Python network access is allowed.")
		}
	}
	if egress.Mode == "allow" {
		warnings = append(warnings, "Egress is unrestricted; http.request, OAuth token URLs, proxies and db.postgres may reach any destination.")
	}
	if serviceToken {
		warnings = append(warnings, "A service token with admin rights is configured.")
	}
	return server.CapabilitiesState{
		Prod:         prod,
		Anonymous:    anonymous,
		ServiceToken: serviceToken,
		Subprocess:   subprocess,
		Egress:       egress,
		Warnings:     warnings,
	}
}

// auditStartupCapabilities records the capability posture at boot. There is
// no mutable capability switch after startup, so a startup row is the change
// record: the event timeline shows when a deployment began running with
// arbitrary Python or anonymous admin access.
func auditStartupCapabilities(st *store.Store, capabilities server.CapabilitiesState) {
	if st == nil {
		return
	}
	if capabilities.Subprocess.Enabled {
		_ = st.AppendAuthEvent(store.AuthEvent{
			Type:   "capability.subprocess",
			Actor:  "system",
			Detail: fmt.Sprintf("enabled network=%s policy=%s", capabilities.Subprocess.Network, capabilities.Subprocess.Policy),
		})
	}
	if capabilities.Anonymous {
		_ = st.AppendAuthEvent(store.AuthEvent{
			Type:   "capability.anonymous",
			Actor:  "system",
			Detail: "enabled",
		})
	}
}
