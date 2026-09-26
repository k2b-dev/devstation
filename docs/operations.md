# Host Setup and Maintenance

[Back to README](../README.md)

## Installation Options

For a reproducible installation, use a release tag for both script and binary:

```sh
curl --proto '=https' -fsSL https://raw.githubusercontent.com/k2b-dev/devstation/v0.1.0/install.sh \
  | VERSION=v0.1.0 sh
```

Set `INSTALL_DIR` to change the destination. Add that directory to `PATH`.
You can also download and inspect `install.sh` before executing it. The installer
verifies the binary against the release's SHA-256 manifest before replacing an
existing installation. Checksums detect corruption; they are not an independent
signature or protection against a compromised repository.

## Host Setup

Provision these once, outside Devstation:

1. Install [Caddy](https://caddyserver.com/docs/install) (tested with 2.11.2).
2. Point `*.dev.example.com` to this host in the DNS resolver used by your clients.
3. Provide and renew a certificate covering `*.dev.example.com`. For a private
   host, DNS-01 can obtain a publicly trusted certificate without public web access.
   A private CA also works if every client trusts it.
4. Choose the private listener address and restrict access with your VPN/firewall.
   DNS names alone do not restrict access. Devstation adds no authentication.

Create `~/.config/devstation/config.toml`:

```toml
domain = "dev.example.com"
listen = "127.0.0.1:8443"
certificate = "/absolute/path/to/fullchain.pem"
key = "/absolute/path/to/privkey.pem"
```

Replace the loopback address with your private interface address for remote access.
Use port `443` for URLs without a port suffix. The default `8443` needs no elevated
privileges. The certificate must cover one-label children of the configured domain.
The apex domain itself has no application route.

The configuration location respects `XDG_CONFIG_HOME`. Override it with
`dev --config /path/config.toml COMMAND`. Relative certificate paths and unknown
configuration keys are rejected. Keep the directory private:

```sh
chmod 700 ~/.config/devstation
chmod 600 ~/.config/devstation/config.toml
```

Run Caddy and Devstation as the **same Unix user**. That user needs read access to
the certificate/key and write access to the configuration directory. This is a
single-user development tool: agents running as that user are trusted to manage
that user's previews. It is not an isolation boundary between mutually untrusted
agents. No root daemon, sudo rule, or general privileged command runner is needed.

### Start Caddy

Bootstrap a route without reloading a service that does not exist yet:

```sh
dev expose 3000 --name example --no-reload
caddy run --config ~/.config/devstation/state/caddy.json
```

`--no-reload` still validates the complete Caddy configuration. It saves the configuration
but does **not** claim the service is live. Use it only during initial provisioning
or deliberate offline maintenance. Start your application separately, listening on
`127.0.0.1:3000`.

For persistence, use [the user systemd service](../examples/devstation-caddy.service).
Adjust the Caddy binary and configuration paths if needed:

```sh
mkdir -p ~/.config/systemd/user
cp examples/devstation-caddy.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now devstation-caddy
```

An administrator can enable lingering with `loginctl enable-linger USER` if this
service must run without a login session. For port 443, an administrator should
create a system service running as the same unprivileged user with only
`AmbientCapabilities=CAP_NET_BIND_SERVICE` and
`CapabilityBoundingSet=CAP_NET_BIND_SERVICE`. Do not run Caddy or `dev` as root.
A VPN-bound listener must start after that address is available; service restart
handles temporary startup failures.

Devstation exclusively owns this Caddy instance and its generated configuration.
Do not point it at an existing shared Caddy instance. The admin API uses a Unix
socket inside the private state directory, not an exposed TCP port.
Automatic certificate issuance and HTTP redirects are disabled; only the explicit
HTTPS listener is opened. Requests for unknown hosts return 404.

After certificate renewal, force a reload so Caddy reads the updated files:

```sh
caddy reload --config ~/.config/devstation/state/caddy.json \
  --address "unix/$HOME/.config/devstation/state/admin.sock" --force
```

Run this in the renewal hook as the same service user, serialized with route
changes. Devstation's lock file is `state/lock` (Linux `flock`); e.g. wrap the
renewal reload in `flock ~/.config/devstation/state/lock ...`.

## Route Behavior and Recovery

Names are lowercase DNS labels of 1–63 characters. Reusing a name replaces its
route. `dev expose` upstreams are always loopback HTTP; Caddy handles TLS and
WebSocket upgrades. Docker applications should publish their port on loopback,
for example `127.0.0.1:8317:8317`.

`dev serve PATH --name NAME` serves an existing regular file or directory with
Caddy, without a separate process. The path is resolved to an absolute path and
saved in the route. Caddy reads the files at request time, so content changes do
not require another `dev serve`; the path must remain available after a restart.
A directory serves `index.html` at `/` and does not enable directory listings.
A single file is served for requests to its host without exposing sibling files.
Only serve directories whose full contents, including dotfiles and symlink
targets, are safe for everyone who can reach the listener. `dev list` shows the
saved path or loopback port. `dev unexpose` removes either kind of route.

There is no arbitrary upstream URL, Caddy snippet, shell command, or automatic
route expiration.

Routes persist in `state/caddy.json` next to the TOML configuration. Do not edit this
file directly. A nonblocking file lock prevents concurrent writers. Changes are
validated, written atomically, and reloaded gracefully. A failed reload restores
the previous file and attempts to reconcile Caddy. If reconciliation fails, the
command reports uncertain live state; inspect Caddy before continuing.

A process or machine crash between the file replacement and reload can leave the
running process on the previous configuration. Restarting Caddy loads the saved
configuration. `dev list` reports saved configuration, not service health. Verify
the returned URL from the intended client before sharing it.

Changing the domain/listener in TOML requires a subsequent `expose` or `unexpose`
to regenerate Caddy's configuration; changing DNS and certificates is separate.
Keep the configuration path stable because it also determines the state and admin socket.

## Updates and removal

```sh
dev update            # latest stable GitHub release
dev update v0.1.0     # pin or roll back
```

Updates verify SHA-256 and atomically replace the executable at its resolved
installation path. The directory must be writable by your user. Concurrent updates
are rejected. Configuration, routes, Caddy, and the agent skill are unchanged.
Development builds can also use the updater after the first release exists.
Do not run the installer and updater concurrently.

To remove Devstation, stop/disable its Caddy service and remove the `dev` binary,
its adjacent `dev.update.lock` file, and its configuration directory. Remove only
your Devstation DNS/certificate configuration as appropriate; applications are
independent. Removing the binary alone does not stop Caddy or revoke previews.

## Development and Releases

Requires Go 1.26. Build and test:

```sh
go test -race ./...
go vet ./...
go build -o dev ./cmd/dev
scripts/build-release.sh v0.0.0
```

With Caddy on PATH, `scripts/integration.sh` verifies trusted HTTPS, unknown-host
rejection, HTTP upgrades, reload/removal, and invalid-certificate rollback using
ephemeral listeners. `scripts/test-install.sh` runs offline installer tests on
Linux against the generated release binaries, including corruption rejection.

CI tests changes and builds both Linux architectures. Pushing an explicitly
approved `vX.Y.Z` tag runs the release workflow, repeats tests, and publishes the
two binaries plus `checksums.txt` to GitHub Releases. Releases are not triggered by
ordinary commits. This repository does not build macOS binaries or a website.
