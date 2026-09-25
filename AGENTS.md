# Devstation

Small Go CLI for a dedicated, single-user Caddy instance on Linux. Keep routing,
release installation, and host provisioning separate. DNS, VPNs, certificates,
and application process management are external prerequisites.

- Run `go test -race ./...`, `go vet ./...`, and `shellcheck install.sh scripts/*.sh`.
- For routing changes, run `scripts/integration.sh` with Caddy 2.11.2 on PATH.
- Keep generated Caddy configuration as the single route state; preserve locking,
  atomic writes, validation before reload, and explicit recovery errors.
- Accept only DNS labels and loopback ports. Do not add shell execution, root
  helpers, arbitrary Caddy snippets, or access to unrelated Caddy instances.
- Release assets are Linux amd64/arm64 binaries plus checksums. Installer and
  updater must agree with scripts/build-release.sh on names and validation.
- The agent skill is distributed through `bunx skills add`; no skill CLI command.
- Keep examples generic. Do not add private hostnames, addresses, or credentials.
- Commit, push, and release only when explicitly authorized.
