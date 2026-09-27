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

1. Install [Caddy](https://caddyserver.com/docs/install) (tested with 2.11.4).
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

## Artifacts

`dev publish PATH... --project P --name N` copies the given files and
directories into `$XDG_DATA_HOME/devstation/artifacts` (default
`~/.local/share/devstation/artifacts`) and renders static pages there. Put
that directory on a disk with room for screenshots; a symlink works. After a
successful first publish, Devstation adds the route `artifacts`, which serves
the store's `site/` directory through a fixed `file_server` with
`Cache-Control: no-cache` and `X-Content-Type-Options: nosniff`. From then on
the route decides which store all artifact commands use, whatever
`XDG_DATA_HOME` says, and publishes only write files without reloading Caddy.
If adding the route fails, the artifact is already stored; fix the cause and
publish again. `dev expose` and `dev serve` refuse to replace `artifacts`;
`dev unexpose artifacts` removes the route, and the next publish adds it again
for the store its own `XDG_DATA_HOME` selects.

URLs, below `https://artifacts.dev.example.com`:

| Path | Content |
|---|---|
| `/` | All projects |
| `/P/` | Artifacts of project `P`, newest first |
| `/P/N/` | Latest version, stable across publishes |
| `/P/N/v/K/` | Version `K`; unchanged while the artifact exists |
| `/P/N/compare/J-K/` | Before/after of two consecutive versions |
| `/P/N/#file.png` | One image of the latest version, opened in the viewer |
| `/P/N/artifact.json` | Metadata and file list with SHA-256 per version |

Pages are generated from the files:

- **Gallery:** images and videos (`.png`, `.jpg`, `.gif`, `.webp`, `.avif`,
  `.svg`, `.mp4`, `.webm`) form a grid per directory. The last theme token in a
  file name (`light`, `dark`, `hell`, `dunkel`) and a numeric 3–4 digit width
  directly after it (else directly before it) form the column; the remaining
  tokens form the row. `card-claimed-dark-390-crop.png` becomes row
  `card-claimed-crop`, column `dark · 390`. Files without a numeric width appear
  in a plain grid. A file with the same stem next to an image, such as `x.html`
  next to `x.png`, is linked from that image. Clicking an image opens a viewer
  on the page with its name, row, and column; the arrow keys or a swipe move
  to the neighbors, Escape closes it, and the address carries the image anchor.
- **Plan:** each Markdown file up to 2 MiB becomes a section, `index.md` or
  `README.md` first, up to 8 MiB per version; the rest are listed as files. GitHub-flavored tables,
  task lists, and strikethrough work; raw HTML is dropped. Heading anchors
  follow GitHub's rules, including non-ASCII letters, and links such as
  `other.md#overview` land in that document's section. Relative links and images point into the version, and
  images shown in Markdown are left out of the gallery.
- **Site:** a top-level `index.html`, or a single file that is neither Markdown
  nor media, is served unchanged. The stable URL redirects to the latest
  version's copy, so HTML mockups keep their own scripts and relative assets.
  After a republish, open the stable URL again; reloading the redirected page
  shows the version it points to. Root-absolute paths such as `/app.js` need
  `dev serve` instead.

Inputs are explicit. A single directory publishes its contents; with several
arguments, each is placed under its base name. Keep the same argument shape
across versions so that before/after pages can pair the files. Directory walks
skip hidden files and folders and do not follow symlinks; skipped paths are
reported. A publish fails, listing the files, when a path looks like it holds
secrets or private data: hidden files and files in a hidden folder given as an
argument (such as `playwright/.auth`); `.env`, `.local`, `.dump`, `.sql`,
`.sqlite`, `.sqlite3`, `.db`, `.har`, `.pem`, `.key`, `.p12`, `.pfx`, `.jks`,
`.keystore`, `.kdbx`, `.ppk`, and `.ovpn` files, also compressed; `id_rsa*`,
`id_ecdsa*`, `id_ed25519*`, `*.tfstate*`, `auth.json`, and storage-state
files; zip archives with browser traces; and data files (`.json`, `.txt`,
`.yaml`, `.csv`, no extension, …) whose path mentions a token, cookie, secret,
credential, password, API key, session, or kubeconfig.
Pages, styles, scripts, and media may use those words. Leave such files out;
`--allow-sensitive` overrides the check. A publish is limited to 2000 files and
512 MiB, file names must be valid UTF-8, and metadata does not record source
paths.

Each publish adds an immutable version. `--title` and `--link` (an http(s) URL)
stay for later versions until replaced. An artifact expires 14 days after its
last publish; `--keep` or `dev keep P/N` keeps it until `dev unpublish P/N`.
The project overview shows each artifact's expiry.
Every publish, keep, and unpublish also removes expired artifacts of all
projects, so cleanup does not depend on the agent that published them. Without
any further command, expired pages stay reachable; `dev unpublish --expired` in
a user timer removes them on a schedule if needed. After an artifact is removed,
a new publish of the same name starts again at version 1.

Copying runs without locks, so parallel publishes of different names proceed
side by side. Finalizing takes the store's `lock` file, usually for
milliseconds, and waits up to 30 seconds; Markdown is rendered before that. Publishing the same name in parallel
yields consecutive versions. Pages and metadata are written with temporary
files and renames. A crash can leave an unlisted version directory, reachable
until the artifact is removed, or an artifact directory without metadata that a
later publish removes after an hour. An artifact whose metadata is damaged is
left out of listings with a warning from `dev artifacts` and can be removed
with `dev unpublish P/N`.

All artifacts share one origin, which is same-site with the other routes. Script
in a published mockup can read other artifacts and send same-site requests to
applications behind this Caddy. This matches the single-user trust model above:
publish only what everyone who can reach the listener may see.

`dev list --json` shows the `artifacts` route with kind `artifacts`. `dev artifacts
[PROJECT] --json` lists artifacts with URLs, size, and expiry.

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
its adjacent `dev.update.lock` file, its configuration directory, and the
artifact store (the parent of the `site` path that `dev list` shows for
`artifacts`). Remove only your Devstation DNS/certificate configuration as
appropriate; applications are independent. Removing the binary alone does not stop Caddy or revoke previews.

## Development and Releases

Requires Go 1.26. Build and test:

```sh
go test -race ./...
go vet ./...
go build -o dev ./cmd/dev
scripts/build-release.sh v0.0.0
```

With Caddy on PATH, `scripts/integration.sh` verifies trusted HTTPS, unknown-host
rejection, HTTP upgrades, reload/removal, invalid-certificate rollback, and
artifact publishing using ephemeral listeners. `scripts/test-install.sh` runs offline installer tests on
Linux against the generated release binaries, including corruption rejection.

CI tests changes and builds both Linux architectures. Pushing an explicitly
approved `vX.Y.Z` tag runs the release workflow, repeats tests, and publishes the
two binaries plus `checksums.txt` to GitHub Releases. Releases are not triggered by
ordinary commits. This repository does not build macOS binaries or a website.
