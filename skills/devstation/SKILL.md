---
name: devstation
description: Expose local development servers or static files through an existing Devstation and Caddy setup, publish screenshots, plans, and HTML mockups as versioned pages, inspect routes, and remove previews. Use when a user asks for a named HTTPS preview on a Devstation host, or wants to see screenshots, a plan, a mockup, or a before/after comparison through a link.
---

# Devstation

Before changing routes, identify the current machine and project. Run `dev version`
and `dev list --json`. Execute on the machine running the application and Caddy;
running the command on a laptop does not configure a remote server.

Start the application using the project's normal process manager, bound to
`127.0.0.1:PORT`. Then run:

```sh
dev expose 3000 --name example
```

For a directory or one static file, skip the application process and run:

```sh
dev serve ./dist --name example
```

Review the full directory before serving it. Dotfiles and symlink targets can
be reached by anyone with access to the configured listener. Keep the path in
place: the route survives restarts and Caddy reads files at request time.

The command prints the HTTPS URL after Caddy accepts the reload. Names are single
lowercase DNS labels. Reusing a name replaces its upstream; inspect existing
routes before choosing a name. Routes persist until `dev unexpose example`.
Removing a route does not stop a proxied application or delete served files.

Verify both the loopback application and the returned HTTPS URL. Report DNS,
certificate, upstream, or reachability failures rather than declaring success
from CLI output alone. Do not use insecure TLS options to establish success.
WebSockets are forwarded by Caddy.

## Share screenshots, plans, and mockups

Use `dev publish` instead of hand-written gallery pages or `dev serve` for
anything a person should look at: screenshots, Markdown plans, HTML mockups.
It copies the files, so later changes to the source do not leak into the page.

```sh
dev artifacts cloud --json                 # check existing names first
dev publish ./shots --project cloud --name login-states \
  --title "Login states" --link https://github.com/org/repo/pull/12 --json
```

Hand on `url` from the JSON (and `compare_url` from the second version on).
Verify it with `curl -fsSI <url>` before reporting success. If this host
cannot resolve its own preview names, add
`--resolve artifacts.<domain>:<port>:<listen address from config.toml>`; that checks
Caddy here, not the user's DNS.

- Name images `<motif>-<state>-<theme>-<width>.png` with a numeric width, for
  example `login-empty-dark-1440.png`. Theme and width become gallery columns;
  the rest becomes the row. To point at one image, append `#<file path>` to
  the URL; it opens in the page's image viewer.
- For before/after, publish the before set, then the after set under the same
  name with the same file names and the same argument shape, and hand on
  `compare_url`. Every publish adds a version.
- Pass only the files that belong in the artifact, never a repository or app
  directory. If publish refuses files as possibly sensitive, leave them out.
  Use `--allow-sensitive` only when the user confirms they hold no secrets.
- Artifacts expire 14 days after their last publish. Add `--keep` only when the
  user asks or the artifact documents a decision. Do not unpublish or keep
  other agents' artifacts unless asked.
- A folder with `index.html` or a single `.html` file is served unchanged, so
  mockups keep their scripts. Use relative asset paths.

## Configuration

Configuration defaults to `~/.config/devstation/config.toml` (or
`$XDG_CONFIG_HOME/devstation/config.toml`). An alternative path precedes the command:
`dev --config /path/config.toml list --json`. Wildcard DNS, TLS renewal, VPN policy,
and Caddy startup belong to the host setup, not individual previews.

Use only `dev expose`, `dev serve`, `dev list`, `dev unexpose`, and the artifact
commands (`dev publish`, `dev artifacts`, `dev keep`, `dev unpublish`) for
routine preview work.
Do not edit generated `state/caddy.json`, operate unrelated Caddy instances, or
run commands with sudo. `--no-reload` is for initial provisioning only: it writes
validated configuration but does not make a URL live. A failed update that reports
uncertain live state requires checking Caddy before further changes.

Devstation does not provide authentication. Exposing an application makes it
available to anyone who can reach the configured listener, subject to the
application's own authentication. Respect the user's existing access policy.

Binary updates are explicit maintenance: `dev update` or `dev update vX.Y.Z`.
They do not update this skill. Install/update the skill with
`bunx skills add k2b-dev/devstation --skill devstation`.
