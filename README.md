# Dad Joke

Windows Home parental control & native policy enforcement engine (test-first).

[![CI](https://github.com/barzoj/dad-joke/actions/workflows/ci.yml/badge.svg)](https://github.com/barzoj/dad-joke/actions/workflows/ci.yml)
[![Security](https://github.com/barzoj/dad-joke/actions/workflows/security.yml/badge.svg)](https://github.com/barzoj/dad-joke/actions/workflows/security.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/barzoj/dad-joke/badges/coverage.json)](https://github.com/barzoj/dad-joke/actions/workflows/ci.yml)

Security policy: [`SECURITY.md`](SECURITY.md). Threat model: [`docs/SECURITY_MODEL.md`](docs/SECURITY_MODEL.md). Architecture: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

The coverage badge is a free [shields.io endpoint](https://shields.io/badges/endpoint-badge) reading `coverage.json` from the orphan `badges` branch (updated on each push to `main` by the Coverage job; no third-party coverage SaaS or secrets).

Dependency updates: install the free [Renovate GitHub App](https://github.com/apps/renovate) on this repository so `renovate.json` can open weekly grouped patch/minor PRs (and immediate security updates). PRs are also gated by GitHub Dependency Review (`fail-on-severity: high` plus a deny-list for licenses incompatible with AGPL-3.0).

## Local development (Compose)

```bash
docker compose up --build
curl -fsS http://127.0.0.1:8080/healthz
./scripts/smoke-e2e.sh   # BASE_URL defaults to http://127.0.0.1:8080
```

Dev compose publishes MongoDB `27017` and server `8080` for local iteration. The server runs non-root with a read-only root filesystem.

## Self-host (production template)

Public templates live under [`deploy/`](deploy/):

| File | Role |
| --- | --- |
| [`deploy/compose.prod.yml`](deploy/compose.prod.yml) | MongoDB + `ghcr.io/barzoj/dad-joke-server` + Caddy (HTTPS). Does **not** publish `27017` or `8080`. |
| [`deploy/Caddyfile`](deploy/Caddyfile) | Automatic TLS template; `caddy-entrypoint.sh` reverse-proxies `${API_DOMAIN}` / `${MCP_DOMAIN}` to `server:8080`. |
| [`deploy/caddy-entrypoint.sh`](deploy/caddy-entrypoint.sh) | Dedupes domains into **one** Caddy site block (single host when equal; `a, b` when distinct). |
| [`deploy/env.example`](deploy/env.example) | Copy to `deploy/.env` and set domains / `ALLOW_SIGNUP` / `IMAGE_TAG`. |

```bash
cp deploy/env.example deploy/.env
# Edit API_DOMAIN and MCP_DOMAIN (may be identical). Prefer ALLOW_SIGNUP=false.
docker compose -f deploy/compose.prod.yml --env-file deploy/.env up -d
```

`API_DOMAIN` is the ControlAPI / AgentAPI hostname; `MCP_DOMAIN` is the Streamable HTTP MCP hostname (`/mcp`). When both are the same value, the entrypoint emits a single-host site address (`{$API_DOMAIN}`) — Caddy does **not** accept duplicate site addresses. When they differ, it emits `{$API_DOMAIN}, {$MCP_DOMAIN}` in one block.

Create the first admin user when signup is disabled:

```bash
docker compose -f deploy/compose.prod.yml --env-file deploy/.env \
  exec server /server create-user --email you@example.com --password '…'
```

MongoDB backups: [`scripts/backup-mongo.sh`](scripts/backup-mongo.sh) (`MONGO_CONTAINER=…` or `MONGO_URI=…`, retention via `RETENTION_DAYS`).

Release artifacts on each `v0.0.x` tag: Windows `dad-joke-windows-amd64.exe` (+ `.sha256`) and multi-arch (`linux/amd64`, `linux/arm64`) image `ghcr.io/barzoj/dad-joke-server` tagged `v0.0.x` and `latest`.
