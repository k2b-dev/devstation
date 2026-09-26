# Devstation

Stable HTTPS URLs for local development servers. A small Linux CLI backed by Caddy.

```sh
dev expose 3000 --name app
# https://app.dev.example.com:8443

dev serve ./dist --name docs
# https://docs.dev.example.com:8443

dev list
dev unexpose app
dev update
```

## Install

Linux amd64 and arm64. Installs to `~/.local/bin`; add it to your `PATH`.
Requires `curl` and `sha256sum`.

```sh
curl --proto '=https' -fsSL https://raw.githubusercontent.com/k2b-dev/devstation/main/install.sh | sh
```

## Set Up Once

Install [Caddy](https://caddyserver.com/docs/install), point `*.dev.example.com` to
this host, and provide a matching TLS certificate. Create
`~/.config/devstation/config.toml`:

```toml
domain = "dev.example.com"
listen = "127.0.0.1:8443"
certificate = "/absolute/path/to/fullchain.pem"
key = "/absolute/path/to/privkey.pem"
```

Run your application on `127.0.0.1:3000`, then bootstrap a dedicated Caddy instance:

```sh
dev expose 3000 --name app --no-reload
caddy run --config ~/.config/devstation/state/caddy.json
```

Run both tools as the same unprivileged user. For remote access, bind to your
private interface and restrict access through your VPN/firewall. Devstation
manages routes; you manage application startup, DNS, certificate renewal, and access.

[Service setup, port 443, TLS renewal, and troubleshooting →](docs/operations.md)

## Everyday Use

`dev expose PORT --name NAME` routes to a local HTTP server. `dev serve PATH
--name NAME` serves a directory or one file directly with Caddy; a directory
uses its `index.html` at `/`. Both commands create or replace a persistent
route. `dev unexpose NAME` removes it. Removing a proxy route does not stop its
application. Caddy handles HTTPS and WebSockets for proxy routes.

Use `dev list --json` for scripts, `dev version` to check the installed version,
and `dev update [vX.Y.Z]` to update or pin it. Downloads are checksum-verified;
updates preserve your configuration and routes.

## Agent Skill

```sh
bunx skills add k2b-dev/devstation --skill devstation
```

[Development and releases](docs/operations.md#development-and-releases) · [MIT license](LICENSE)
