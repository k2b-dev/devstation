# Devstation

Small Go CLI for a dedicated, single-user Caddy instance on Linux: routing plus
static artifact publishing. Keep routing, artifacts, release installation, and
host provisioning separate. DNS, VPNs, certificates,
and application process management are external prerequisites.

- Run `go test -race ./...`, `go vet ./...`, and `shellcheck install.sh scripts/*.sh`.
- For routing, artifact, or screenshot changes, run `scripts/integration.sh` with
  Caddy 2.11.4 and Chrome or Chromium available.
- Keep generated Caddy configuration as the single route state; preserve locking,
  atomic writes, validation before reload, and explicit recovery errors.
- Accept only DNS labels and loopback ports. Do not add shell execution, root
  helpers, arbitrary Caddy snippets, or access to unrelated Caddy instances.
- Artifacts copy only explicitly passed paths and render all HTML at publish
  time with html/template (goldmark in safe mode for Markdown). Caddy only
  serves files: no daemons, uploads, or request-time rendering. The artifact
  store has its own lock; only route creation takes the routing lock.
- Screenshots drive a local headless Chromium over `--remote-debugging-pipe`
  with a small built-in DevTools client: no browser library, no shell, no
  listening port. Cookie values come only from files, never from arguments.
- Release assets are Linux amd64/arm64 binaries plus checksums. Installer and
  updater must agree with scripts/build-release.sh on names and validation.
- The agent skill is distributed through `bunx skills add`; no skill CLI command.
- Keep examples generic. Do not add private hostnames, addresses, or credentials.
- Commit, push, and release only when explicitly authorized.
