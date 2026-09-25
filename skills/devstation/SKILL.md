---
name: devstation
description: Expose local development servers through an existing Devstation and Caddy setup, inspect routes, and remove previews. Use when a user asks for a named HTTPS preview on a Devstation host.
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

The command prints the HTTPS URL after Caddy accepts the reload. Names are single
lowercase DNS labels. Reusing a name replaces its upstream; inspect existing
routes before choosing a name. Routes persist until `dev unexpose example`.
Removing a route does not stop the application.

Verify both the loopback application and the returned HTTPS URL. Report DNS,
certificate, upstream, or reachability failures rather than declaring success
from CLI output alone. Do not use insecure TLS options to establish success.
WebSockets are forwarded by Caddy.

Configuration defaults to `~/.config/devstation/config.toml` (or
`$XDG_CONFIG_HOME/devstation/config.toml`). An alternative path precedes the command:
`dev --config /path/config.toml list --json`. Wildcard DNS, TLS renewal, VPN policy,
and Caddy startup belong to the host setup, not individual previews.

Use only `dev expose`, `dev list`, and `dev unexpose` for routine preview work.
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
