// Package egress evaluates outbound-network policy for Loom hosts.
//
// Every layer must independently allow a destination (D7-1): the global
// policy from the operator's policy file, the per-workflow allowlist
// from the workflow doc, and the per-credential restriction parsed out
// of the credential JSON. A destination that any declared layer rejects
// is denied with an error naming the rule.
//
// The package is pure: no I/O, no host dependencies, so every rule is
// directly unit-testable and the same evaluator serves the live host,
// the Postgres dialer, publish-time validation, and `loom egress check`.
package egress

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Mode is the global posture. ModeAllow (the zero value / missing file)
// keeps existing deployments unchanged; ModeDeny flips the global layer
// to deny-by-default with the allowlist as the only openings.
type Mode string

const (
	ModeAllow Mode = "allow"
	ModeDeny  Mode = "deny"
)

// Policy is the operator-authored global policy file
// (egress-policy.json, mirroring the subprocess-policy precedent).
type Policy struct {
	Mode      Mode     `json:"mode"`
	Allowlist []string `json:"allowlist,omitempty"`
}

// WorkflowPolicy is the per-workflow `egress` block on the workflow doc
// (D7-2). An absent block means the workflow imposes no restriction of
// its own — the global policy still applies.
type WorkflowPolicy struct {
	Allowlist []string `json:"allowlist,omitempty"`
}

// Check returns whether the global policy allows the destination.
// An empty (zero-value / missing-file) policy allows everything (D7-4);
// a non-empty but unknown mode is a configuration error.
func (p Policy) Check(dest string) error {
	if p.Mode == "" {
		return nil
	}
	if p.Mode != ModeAllow && p.Mode != ModeDeny {
		return fmt.Errorf("egress: invalid policy mode %q (want \"allow\" or \"deny\")", p.Mode)
	}
	if p.Mode == ModeAllow {
		return nil
	}
	if matchAny(p.Allowlist, dest) {
		return nil
	}
	return fmt.Errorf(
		"egress policy: destination %s is not in the global allowlist (mode deny)", dest,
	)
}

// Check returns whether the workflow policy allows the destination.
// A nil or empty policy imposes no restriction.
func (w *WorkflowPolicy) Check(dest string) error {
	if w == nil || len(w.Allowlist) == 0 {
		return nil
	}
	if matchAny(w.Allowlist, dest) {
		return nil
	}
	return fmt.Errorf(
		"egress policy: destination %s is not in this workflow's egress allowlist", dest,
	)
}

// CredentialAllows checks a per-credential `allowedHosts` restriction
// (parsed from the credential JSON by the node layer). An absent or
// empty restriction imposes no limit. The restriction matches hosts,
// not full destinations: entries are "host" or "*.host" (optionally
// with :port).
func CredentialAllows(credentialName string, allowedHosts []string, hostPort string) error {
	if len(allowedHosts) == 0 {
		return nil
	}
	if matchHostAny(allowedHosts, hostPort) {
		return nil
	}
	return fmt.Errorf(
		"egress policy: destination %s is outside credential %q's allowedHosts",
		hostPort, credentialName,
	)
}

// ParseDestination normalizes a URL to the destination form the
// allowlist matches: scheme://host[:port]. Anything unparseable is
// rejected (the caller should never dial it anyway).
func ParseDestination(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("egress: invalid url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("egress: url %q has no scheme or host", rawURL)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("egress: url %q has no host", rawURL)
	}
	// Re-bracket IPv6 literals — Hostname() strips the brackets but the
	// allowlist form keeps them.
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if p := u.Port(); p != "" {
		host = host + ":" + p
	}
	return u.Scheme + "://" + strings.ToLower(host), nil
}

// HostPortFromAddress extracts host[:port] (lowercased) from a
// dial-style "host:port" or "host" address for the Postgres dialer.
func HostPortFromAddress(address string) string {
	host := address
	if h, _, err := splitLastColon(address); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSpace(host))
}

// ValidateEntry reports whether an allowlist entry is well-formed
// (scheme://host[:port], host may lead with "*."). Used by publish
// validation and `loom egress check` so bad policy fails fast with the
// offending entry named.
func ValidateEntry(entry string) error {
	u, err := url.Parse(entry)
	if err != nil {
		return fmt.Errorf("invalid entry %q: %v", entry, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "postgres" {
		return fmt.Errorf("invalid entry %q: scheme must be http, https or postgres", entry)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("invalid entry %q: missing host", entry)
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("invalid entry %q: paths are not allowed (destination-level only)", entry)
	}
	if strings.ContainsAny(host, " /@?") {
		return fmt.Errorf("invalid entry %q: bad host", entry)
	}
	if bad := strings.TrimPrefix(host, "*."); strings.Contains(bad, "*") {
		return fmt.Errorf("invalid entry %q: wildcard only allowed as a leading \"*.\"", entry)
	}
	return nil
}

// LoadPolicyFile loads the operator's policy. An empty path means
// "use the default <data>/egress-policy.json"; a missing default file
// yields the zero-value allow-all policy, while an explicitly named
// missing file is a hard error. Every allowlist entry is validated so
// serve fails fast on a bad policy instead of at first use.
func LoadPolicyFile(path, dataDir string) (*Policy, error) {
	explicit := strings.TrimSpace(path) != ""
	file := strings.TrimSpace(path)
	if file == "" {
		file = filepath.Join(dataDir, "egress-policy.json")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return &Policy{}, nil
		}
		return nil, fmt.Errorf("egress policy: %w", err)
	}
	var p Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("egress policy %s: %w", file, err)
	}
	if p.Mode == "" {
		return nil, fmt.Errorf("egress policy %s: missing \"mode\" (use \"allow\" or \"deny\")", file)
	}
	if p.Mode != ModeAllow && p.Mode != ModeDeny {
		return nil, fmt.Errorf("egress policy %s: invalid mode %q (want \"allow\" or \"deny\")", file, p.Mode)
	}
	for _, entry := range p.Allowlist {
		if err := ValidateEntry(entry); err != nil {
			return nil, fmt.Errorf("egress policy %s: %v", file, err)
		}
	}
	return &p, nil
}

// matchAny reports whether dest matches any allowlist entry.
func matchAny(entries []string, dest string) bool {
	for _, entry := range entries {
		if matchEntry(entry, dest) {
			return true
		}
	}
	return false
}

// matchEntry matches a scheme://host[:port] entry against a normalized
// destination (D7-3). An entry without a port matches any port; a
// "*.host" entry matches host and any subdomain at any depth.
func matchEntry(entry, dest string) bool {
	e, err := url.Parse(entry)
	if err != nil {
		return false
	}
	d, err := url.Parse(dest)
	if err != nil {
		return false
	}
	if e.Scheme != d.Scheme {
		return false
	}
	ehost, eport := splitHostPort(e.Host)
	dhost, dport := splitHostPort(d.Host)
	if eport != "" && eport != dport {
		return false
	}
	return matchHost(ehost, dhost)
}

// matchHostAny matches host-only entries against a host[:port].
func matchHostAny(entries []string, hostPort string) bool {
	h, p := splitHostPort(hostPort)
	for _, entry := range entries {
		ehost, eport := splitHostPort(strings.TrimSpace(entry))
		if eport != "" && eport != p {
			continue
		}
		if matchHost(ehost, h) {
			return true
		}
	}
	return false
}

func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	host = strings.ToLower(strings.TrimSpace(host))
	if pattern == "" || host == "" {
		return false
	}
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix)
	}
	return false
}

// splitHostPort splits "host:port" tolerantly (IPv6 brackets, missing
// port).
func splitHostPort(hostPort string) (host, port string) {
	if h, p, err := splitLastColon(hostPort); err == nil && p != "" && !strings.Contains(p, "]") {
		return strings.ToLower(h), p
	}
	return strings.ToLower(hostPort), ""
}

// splitLastColon splits at the last colon, treating bracketed IPv6
// hosts as unsplittable.
func splitLastColon(s string) (string, string, error) {
	if strings.HasPrefix(s, "[") {
		if end := strings.LastIndex(s, "]"); end >= 0 {
			rest := s[end+1:]
			if strings.HasPrefix(rest, ":") {
				return s[:end+1], rest[1:], nil
			}
			return s, "", nil
		}
		return "", "", fmt.Errorf("unbalanced brackets in %q", s)
	}
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, "", nil
	}
	return s[:i], s[i+1:], nil
}
