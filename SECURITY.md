# Security Policy

## Supported versions

Only the latest release line receives security fixes. Run `loom version`
(or check the binary's build info) and upgrade before reporting.

## Reporting a vulnerability

Open a private security advisory in this repository (GitHub: Security tab →
"Report a vulnerability"). Please include:

- the Loom version,
- a reproduction or proof of concept,
- whether the issue requires database access, filesystem access, or only
  network reachability.

Do not open a public issue for security reports.

## Scope

- The Loom server, CLI, embedded web UI, and the node types shipped in
  `internal/nodes` are in scope.
- Workflows and node configurations written by users are trusted input: a
  user who can author a workflow can already make the engine perform
  network requests with stored credentials.
- Out of scope: the host machine's unrelated services, denial of service by
  resource exhaustion on intentionally open deployments, and issues in
  dependencies of the web build toolchain.

Details of the security model — authentication, key handling, signing,
retention, and backup — live in [docs/security.md](docs/security.md).
