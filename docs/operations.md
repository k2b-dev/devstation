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
| `/P/N/comments.jsonl` | Comments, see [Comments](#comments) |

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
argument (such as `playwright/.auth`); `.env`, `.cookie`, `.local`, `.dump`, `.sql`,
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
[PROJECT] --json` lists artifacts with URLs, size, expiry, and open comments.

## Comments

In the image viewer, click the image to pin a spot, type a comment, and send it
with the button or Ctrl/⌘+Enter. Comments without a pin are fine too. Each
comment gets a number per image and version, shown in a circle on the image.
Thumbnails show how many comments are open, and the viewer lists all comments
of an image with their version; "done" marks one as resolved. Pages switch
between light, dark, and the system setting with the button at the top right;
the choice is stored in the browser and applied before the page is drawn.

Agents read and resolve comments from the command line:

```sh
dev comments app/login-states            # open comments with image, version, pin, link
dev comments app/login-states --all --json
dev comments app/login-states --images /tmp/pins   # copies with numbered pins drawn in
dev comments resolve app/login-states 3fa2c1d9e0 7b41c0e2aa
```

`--images` writes PNG copies of images that have pinned comments (PNG, JPEG,
and GIF sources up to 50 megapixels) to `DIR/vVERSION/PATH`, with `.png`
appended for other formats. Open comments are red, resolved ones gray.

Pages post comments to `/_devstation/comments/PROJECT/NAME` on the artifacts
host. The `artifacts` route sends `/_devstation/` to `dev daemon`, the one
background process of Devstation, which listens on the Unix socket
`daemon.sock` next to Caddy's admin socket; everything else stays static. A
publish updates an older `artifacts` route to this shape. Run the daemon as the
same user as Caddy, for example with the
[user unit](../examples/devstation-daemon.service):

```sh
cp examples/devstation-daemon.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now devstation-daemon
```

Without the daemon, pages still show existing comments and report that new
ones cannot be sent. The daemon accepts only JSON requests whose `Origin`
matches the host, so other sites cannot post through a visitor's browser.
There is no login: everyone who can reach the listener can comment, as they
can read. Comments are plain text of up to 4000 characters without control
characters (line breaks and tabs are fine) and are shown as text, never as
HTML. They are stored as `comments.jsonl` in the artifact's directory,
readable at `/PROJECT/NAME/comments.jsonl`, and removed with the artifact. One
artifact takes new comments until it holds 1 MiB of them; marking comments
done keeps working beyond that.

## Screenshots

`dev shot [LABEL=]URL... --out DIR` opens each URL in a local headless Chrome
or Chromium and writes one PNG per theme and width, `DIR/LABEL-THEME-WIDTH.png`,
which is the naming the artifact gallery expects. Without `LABEL=`, the label
comes from the URL path; two URLs with the same label are refused. Take one
state per label, for example `board-empty=URL` and `board-full=URL`. All
options apply to every URL of one command.

- **Browser:** `--browser PATH`, `$DEVSTATION_BROWSER`, a Chrome or Chromium on
  `PATH`, or the highest revision Playwright downloaded to
  `~/.cache/ms-playwright` (or `$PLAYWRIGHT_BROWSERS_PATH`), preferring its
  headless shell. Devstation downloads nothing. The browser talks to
  Devstation over a pipe and opens no port. It gets a temporary profile that
  is removed at the end, also after Ctrl-C or `--timeout`. Like Playwright by
  default, it runs without the Chromium sandbox, because many hosts do not
  allow the user namespaces it needs; a compromised page could then act with
  your user's rights, so capture only pages you trust.
- **Isolation:** every screenshot starts in a fresh browser context, so
  storage, cookies, and cache never carry over between themes, widths, or URLs.
- **Themes and sizes:** `--themes light,dark` (also `hell` and `dunkel`) sets
  `prefers-color-scheme`. `--theme-cookie NAME` also sets the cookie `NAME` to
  `light` or `dark` for applications that read the theme from a cookie; when
  light and dark come out identical, a warning suggests it. `--widths
  1440,390` sets the viewport width; the height is 900 px, or 844 px below
  600 px (`--height`). Widths below 600 px emulate a phone with touch input;
  wider ones a desktop with a mouse, so hover styles apply. `--scale 2` gives
  sharper images; file names keep the CSS width. `--full-page` captures the
  whole document; layouts that scroll inside a fixed-height container still
  show one viewport, so raise `--height` for those.
- **Sign-in:** `--cookie NAME=@FILE` sets an HTTP-only cookie for each URL of
  the command from a file that holds only the value, so values never appear in
  arguments. Keep such files private (mode 600, for example in
  `$XDG_RUNTIME_DIR`), outside published folders, and do not mix URLs of
  different applications in one command. `dev publish` refuses `*.cookie`
  files. How to obtain a session depends on the application.
- **Interaction:** after the page loads, `--eval JS` runs (top-level `await`
  works), then each `--click SELECTOR` in order, then `--hover SELECTOR`, then
  `--wait-for SELECTOR`. Each selector must match a visible element within
  10 seconds; clicks and hovers scroll it into view if needed, `--wait-for`
  does not scroll. Alert, confirm, and prompt dialogs are dismissed with a
  warning. Animations and transitions are turned off, and the capture waits for
  web fonts.
- **Loading:** the capture follows redirects, including ones by script, waits
  for the load event (at most 30 seconds), and then until no request has been
  in flight for half a second, at most five seconds; event streams do not
  count. Requests still running then are named in a warning. Content that
  arrives later, for example over a WebSocket, needs `--wait-for`.
- **Host names:** names below the configured domain resolve to the configured
  listener address, so this host's previews work even when the host cannot
  resolve its own names.
- **Failures:** an HTTP status of 400 or more, a failed navigation, a missing
  selector, a crashed page, or `--timeout` (90 seconds for the whole command
  by default) stop the command with an error; files written before remain and
  are listed. Showing another address than requested, such as a sign-in page,
  and uncaught page errors are warnings.
- `--json` lists the files with URL, final URL, status, theme, and width, the
  warnings, and an `error` field when the command failed.

## Updates and removal

```sh
dev update            # latest stable GitHub release
dev update v0.1.0     # pin or roll back
```

Updates verify SHA-256 and atomically replace the executable at its resolved
installation path. The directory must be writable by your user. Concurrent updates
are rejected. Configuration, routes, Caddy, and the agent skill are unchanged.
A running daemon keeps the old version until you restart it
(`systemctl --user restart devstation-daemon`).
Development builds can also use the updater after the first release exists.
Do not run the installer and updater concurrently.

To remove Devstation, stop/disable its Caddy and daemon services and remove the `dev` binary,
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
