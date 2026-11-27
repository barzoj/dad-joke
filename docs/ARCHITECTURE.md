# Architecture

This document defines the enduring architectural invariants of **Dad Joke**: a Windows Home parental-control and native policy-enforcement system. It is the canonical reference for design and review. Future work belongs in issues—not here. Security invariants and the Zero-Trust threat model live in [`SECURITY_MODEL.md`](SECURITY_MODEL.md).

## Privilege & Runtime Model

- **Target OS:** Windows Home, compatible with Pro and Enterprise.
- **Child session:** runs as a **Standard User**.
- **Agent:** runs as a **Windows Service** under `LocalSystem`.
- **Hardening:** agent binaries and configuration are protected by strict Windows ACLs—`SYSTEM` and `Administrators` only.
- **Persistent identity & AgentAPI settings:** `C:\ProgramData\DadJoke\config.json` holds a stable `machine_id` (RFC 4122 UUIDv4) and a secret `token` (auto-generated as 256-bit `crypto/rand` → 64-char lowercase hex, or an explicit install `--token` stored verbatim). Optional `backend_url` (AgentAPI base URL; empty = local-only, no heartbeat) and `heartbeat_interval_seconds` (default `60`) are set via `dad-joke.exe install --backend-url` / `--heartbeat-interval`. Identity and backend settings are created on first install and preserved across reinstalls/self-updates (unless the matching flag replaces them). Loaded at service startup. The raw `token` must never appear in service logs or outbound updater / GitHub Releases HTTP.

## Strict Two-Layer Separation

The system is split into two layers with a one-way desired-state flow. Product logic must not leak into enforcement, and enforcement must not own product state.

### Lower layer — Policy Enforcement Engine

Intentionally dumb. A **stateless / desired-state reconciler** that applies native Windows policy primitives and compatible Home-edition enforcement:

- Registry policies under `HKLM` and `HKU\<SID>`
- Windows Firewall
- Logon Hours
- Compatible enforcement for native Windows policy formats that Home ignores (for example AppLocker `SrpV2`)

The lower layer does not schedule, quota, or run interactive product flows. It reconciles whatever desired state the upper layer supplies.

### Upper layer — Control Plane / Product Logic

Owns **stateful** logic: schedules, quotas, and interactive user flows. It pushes desired state to the lower layer over **explicit contracts**. It never applies Windows policy primitives directly.

## API-First

Every boundary is an explicit, **versioned** contract defined **before** implementation:

- Control plane ↔ agent
- CLI surfaces
- Internal service interfaces
- Policy / desired-state formats

Contracts are the source of truth for compatibility and review. Implementations follow the contract; they do not invent ad-hoc cross-layer coupling.

### HTTP contract pipeline (TypeSpec → OpenAPI → Go)

Two independent HTTP APIs are defined as separate TypeSpec `@service` packages and never hand-spliced:

| Service | TypeSpec | Generated OpenAPI 3.0.0 | Generated Go package |
| --- | --- | --- | --- |
| **AgentAPI** | `api/agent` | `gen/openapi/agent/openapi.yaml` | `gen/go/agentapi` |
| **ControlAPI** | `api/control` | `gen/openapi/control/openapi.yaml` | `gen/go/controlapi` |

Pipeline (single entry point: `make generate`):

1. **TypeSpec** (`@typespec/openapi3`) emits OpenAPI **3.0.0** YAML per service (`warn-as-error`, `operation-id-strategy: explicit-only`, `omit-unreachable-types`).
2. **`oapi-codegen` v2** (`go tool oapi-codegen`, configs under `api/oapi-codegen/`) generates `models`, `client`, `std-http-server`, and `strict-server` into the Go packages above (`resolve-type-name-collisions: true`).
3. **`gen/` is gitignored**; CI runs codegen before any job that compiles packages importing `gen/go/...`.

Shared error shapes live in `api/common/errors.tsp` and are imported by both services (native TypeSpec imports only; the two OpenAPI YAML documents stay independent).

### Auth, tokens, heartbeat, and machines (current contracts)

| Surface | Security | Notable routes |
| --- | --- | --- |
| **AgentAPI** | `AgentBearerAuth` (HTTP Bearer `dj_agt_...`); `GET /healthz` uses `@useAuth(NoAuth)` | `POST /v1/heartbeat` — machine identity + applied policy; response includes `server_time`, `desired_policy_version`, `pending_job_ids`. `GET /v1/policy?machine_id=` — versioned low-level desired policy for that `(tenant_id, machine_id)` (missing → `404`). `POST /v1/jobs/{id}/claim` and `POST /v1/jobs/{id}/result` — atomic claim and terminal result for one-shot jobs. `POST /v1/events` — compact telemetry batch ingest |
| **ControlAPI** | `ControlBearerAuth` (HTTP Bearer `dj_pat_...` **or** user session `dj_ses_...` / `dj_session` cookie); `GET /healthz`, `POST /v1/auth/register`, `POST /v1/auth/login` use `@useAuth(NoAuth)` | `POST /v1/auth/register\|login\|logout`, `GET /v1/auth/me`, `POST/GET/DELETE /v1/agent-tokens`, `POST/GET/DELETE /v1/api-keys`, `GET /v1/machines`, `GET /v1/machines/{machine_id}`, `GET/PUT /v1/machines/{machine_id}/policy`, `POST/GET /v1/machines/{machine_id}/jobs`, `GET /v1/machines/{machine_id}/events`, `GET /v1/jobs/{id}`, `POST /v1/jobs/{id}/cancel` |

Unified failures use `ErrorResponse` (`code`, `message`, `request_id`) for `400` / `401` / `403` / `404` / `409` / `413` / `429` / `500` / `503`. `429` responses declare a `Retry-After` header.

Raw token secrets appear **only** on `201 Created` models (`CreatedAgentToken`, `CreatedApiKey`) and register/login session responses (`AuthSession.access_token`); they are absent from every `GET` summary/list model. Domain endpoints are added in TypeSpec first, then regenerated—no parallel hand-written HTTP contracts.

### Remote MCP (Streamable HTTP)

`cmd/server` mounts a **stateless Streamable HTTP** MCP endpoint at `/mcp` (`internal/mcp`). The MCP layer is a thin adapter over the generated `gen/go/controlapi` HTTP client (in-process RoundTripper to the same process's ControlAPI handler). MCP packages **must not** import `internal/store` or `go.mongodb.org/mongo-driver` (enforced by `internal/mcp` architecture tests).

| Concern | Behavior |
| --- | --- |
| **Auth** | Every MCP HTTP request requires `Authorization: Bearer dj_pat_...` **or** `dj_ses_...` Bearer / `dj_session` cookie. Credentials (including empty/missing) are probed via ControlAPI (`GET /v1/machines`) before the MCP session proceeds so ControlAPI's `AuthFailureLimiter` is charged by the real MCP peer `RemoteAddr` (propagated on the in-process RoundTrip; never from `X-Forwarded-For`). Probe outcomes map to HTTP `401`/`403`→`401`, `429`→`429` (with `Retry-After`), `5xx`→`503`, with no raw token details. The same credentials are forwarded on every tool → ControlAPI call so tenant scoping stays in ControlAPI. |
| **Tools (exactly 7)** | `list_machines`, `get_machine`, `set_policy`, `run_script`, `get_job` (read or `action=cancel`), `list_events`, `manage_agent_tokens` (`action=create\|list\|revoke`) |
| **Errors** | Upstream ControlAPI `400` / `401` / `404` / `409` / `413` / `429` (and tool argument validation) map to clean MCP tool errors: no stack traces; raw `dj_pat_` / `dj_agt_` / `dj_ses_` secrets are redacted from logs and error bodies. Streamable HTTP rejects missing/invalid `Accept` per the MCP transport. |

## Control-plane server & data layer (current)

### DAL boundary (`internal/store`)

Domain models are plain Go structs (string UUIDs, no `bson` tags, no `ObjectID`). Repository interfaces live in `internal/store`:

`TenantStore`, `UserStore`, `SessionStore`, `TokenStore`, `MachineStore`, `PolicyStore`, `JobStore`, `EventStore`, `AuditStore`, plus `Pinger` and a `Store` facade (`EnsureIndexes`, `Close`).

HTTP handlers and product logic talk only to these interfaces. An architecture test (`internal/archtest`) fails CI if any package outside `internal/store/mongo` imports `go.mongodb.org/mongo-driver`. MCP packages additionally fail CI if they import `internal/store` or `mongo-driver`.

Collections / models of note:

- `users`: `id`, `tenant_id`, `email` (lowercased/trimmed, unique), `password_hash` (argon2id; never in JSON/logs), `created_at`
- `user_identities`: `id`, `user_id`, `provider`, `subject` (unique on `(provider, subject)`), `created_at`
- `sessions`: hashed `dj_ses_...` credentials with expiry and revoke timestamp
- `policies`: one document per `(tenant_id, machine_id)` with monotonic `version`, opaque JSON `document`, and `updated_at`. `PolicyStore.Put` uses `expected_version` (0 creates version 1; otherwise the stored version must match or `ErrConflict`) so concurrent writers cannot lose updates.
- `jobs`: one-shot command/script assignments with interpreter `type` (`powershell`/`cmd`), mandatory `expires_at`, optional `timeout_seconds` (1..300), hard 64 KiB caps on `payload`/`stdout`/`stderr`, and status `pending` → `running` → terminal (`succeeded`/`failed`/`canceled`/`timed_out`)
- `events`: agent telemetry with client `event_id` idempotency key, `ts`, `type`, and opaque JSON `payload`. `EventStore.InsertBatch` skips duplicate `(tenant_id, machine_id, event_id)` idempotently (within and across batches). `EventStore.List` is tenant-scoped, ordered by `ts` ascending then `event_id`, and supports opaque cursor pagination.

### MongoDB implementation (`internal/store/mongo`)

Built on `go.mongodb.org/mongo-driver/v2`. Private BSON mapper structs stay inside the package. `EnsureIndexes(ctx)` creates idempotent unique/compound/TTL indexes at startup (tenant name, user email, identity provider+subject, session hash, token kind+hash, unique `(tenant_id, machine_id)` on machines, unique `(tenant_id, machine_id, event_id)` event idempotency, compound `(tenant_id, machine_id, ts)` event listing, TTL index `ttl_events_ts` on `ts` with `expireAfterSeconds = 30*24*3600` (30 days), audit listing, etc.).

### `cmd/server`

- HTTP server with `GET /healthz`: `200` + `{"status":"ok"}` when MongoDB `Ping` succeeds; `503` + `{"status":"unavailable"}` when it fails. Unauthenticated by design.
- **Local email/password auth** (ControlAPI):
  - `POST /v1/auth/register` atomically creates a tenant + user (`argon2id` via `golang.org/x/crypto/argon2`, random salt). Duplicate email → `409` with compensating tenant delete (no orphan). `ALLOW_SIGNUP=false` → `403`.
  - `POST /v1/auth/login` verifies with constant-time compare; unknown email runs a dummy argon2id verify so timing matches wrong-password. Identical `401` body for both cases. Failed logins are rate-limited per IP and per email (`429` + `Retry-After`); store infra errors return `503` without burning the budget.
  - Successful register/login issues a revocable `dj_ses_...` session (hashed at rest) returned as `access_token` and as an `HttpOnly; SameSite=Lax` cookie (`Secure` default `true`, overridable with `COOKIE_SECURE=false` for local HTTP).
  - `POST /v1/auth/logout` revokes the session; subsequent `/v1/auth/me` with that session is `401`.
  - `GET /v1/auth/me` returns the user profile for a session (never `password_hash`).
- **Zero-Trust auth middleware** (process-local):
  - AgentAPI routes (`POST /v1/heartbeat`, `GET /v1/policy`, `POST /v1/events`, `POST /v1/jobs/{id}/claim|result`): `Authorization: Bearer dj_agt_...` only; SHA-256 verified via `TokenStore.GetByHash` with constant-time compare; revoked/expired/wrong-prefix (including user sessions) → `401` `ErrorResponse`.
  - ControlAPI routes: `Authorization: Bearer dj_pat_...` **or** `dj_ses_...` Bearer / `dj_session` cookie; same verification; agent tokens → `401`.
  - Successful auth puts `tenant_id` and actor id on `context.Context` (`internal/authctx`); user sessions also set `user_id` / `session_id`. Handlers are strictly tenant-scoped (cross-tenant → `404`).
- **Token CRUD** (ControlAPI): create/list/delete agent tokens and API keys (session or PAT). Raw secret returned only on `201 Created`; at rest only SHA-256 hash + non-secret prefix. Issue and revoke append to `audit_logs` (`internal/audit`; hooks also exist for policy create and job create/claim/complete/cancel).
- **Low-level policy CRUD:**
  - `PUT /v1/machines/{machine_id}/policy` (ControlAPI): body `{expected_version, document}` writes an opaque JSON desired-state document. First write (`expected_version=0`) creates `version=1`; updates require the current version and return `409` on mismatch. Each successful write appends `audit_logs` (`policy.create`) and is immediately visible to heartbeats. Invalid JSON → `400`; body over the global `http.MaxBytesReader` limit (1 MiB) → `413`.
  - `GET /v1/machines/{machine_id}/policy` (ControlAPI) / `GET /v1/policy?machine_id=` (AgentAPI): tenant-scoped; missing policy → `404` (never an empty document). AgentAPI rejects `dj_pat_` / session credentials with `401`.
- **Heartbeat & machines:**
  - `POST /v1/heartbeat` (AgentAPI): tenant from auth context only. `machine_id` must be RFC 4122 UUIDv4 (version + variant bits); otherwise `400` `ErrorResponse`. Atomically upserts one `machines` document per `(tenant_id, machine_id)` (unique index; Mongo `_id` = `tenant_id|machine_id`), setting `last_seen_at` from the injectable server clock (UTC) plus `agent_version`, `hostname`, `os_version`, `applied_policy_version`. Response: `server_time`, `desired_policy_version` (from `PolicyStore`, else `0`), `pending_job_ids` (from `JobStore` non-expired pending ids, else `[]`—never JSON `null`). Newly enqueued jobs appear immediately.
  - `GET /v1/machines` / `GET /v1/machines/{machine_id}` (ControlAPI): tenant-scoped. `status` is computed at read time as `online` when `now - last_seen_at <= 3m`, else `offline` (not persisted). Missing/other-tenant machine → `404`; empty list → `[]`.
- **One-shot job queue** (`JobStore`; ControlAPI enqueue + AgentAPI claim/result):
  - State machine: `pending` → `running` → `succeeded` | `failed` | `canceled` | `timed_out`. `expires_at` is mandatory on create (TTL for unclaimed jobs). Expired `pending` jobs are omitted from `pending_job_ids`, are not claimable, and transition to `timed_out` (memory and Mongo).
  - Each job has a contract `type` of `powershell` or `cmd`, a `payload` (script/command), and optional `timeout_seconds` (1..300; agent default 60 when omitted).
  - ControlAPI: `POST/GET /v1/machines/{machine_id}/jobs`, `GET /v1/jobs/{id}`, `POST /v1/jobs/{id}/cancel` (cancel only from `pending`; running/terminal → `409`).
  - AgentAPI: `POST /v1/jobs/{id}/claim` and `POST /v1/jobs/{id}/result` require `machine_id` in the body and may act only on that `(tenant_id, machine_id)`. Atomic claim is `pending → running`; second claim → `409`. Result statuses: `succeeded` / `failed` / `timed_out`. Result on canceled/finished → `409`. Cross-tenant (and wrong-machine) access → `404` (never `403`).
  - Hard caps: `payload` and result `stdout`/`stderr` ≤ 64 KiB (`413` when exceeded); request bodies also bounded by the server `MaxBytesReader`.
  - `audit_logs` entries on create, claim, complete, and cancel (`job.create` / `job.claim` / `job.complete` / `job.cancel`).
- **Agent event / telemetry ingest** (`EventStore`; AgentAPI ingest + ControlAPI list):
  - `POST /v1/events` (AgentAPI): body `{machine_id, events:[{event_id, ts, type, payload}]}`. Hard limits: max **100** events per batch (`400` when exceeded) and max **256 KiB** request body (`413` via a route-specific `MaxBytesReader`). Validation is **all-or-nothing** before any persist; duplicate `event_id` values (within a batch or across batches) are skipped idempotently. `ts` must be a valid RFC 3339 timestamp and must not be more than **5 minutes** ahead of the injectable server clock (`400` otherwise). Success → `204`.
  - `GET /v1/machines/{machine_id}/events` (ControlAPI): filters `type`, `from` (inclusive), `to` (exclusive), `limit` (1..100, default 50; invalid → `400`), and opaque `cursor`. Results are strictly tenant-scoped (another tenant's `machine_id` yields an empty page, never their events), ordered by `ts` ascending with `event_id` as tie-breaker, and return `{events, next_cursor?}`.
  - Mongo indexes created at startup: unique `(tenant_id, machine_id, event_id)`, compound `(tenant_id, machine_id, ts)`, and TTL `ttl_events_ts` on `ts` (`expireAfterSeconds = 30*24*3600`).
- **Rate limits** (`internal/ratelimit`): in-memory sliding window per process (no Redis), injectable clock, bounded key set. Auth failures are limited per IP and per `wantKind`+token-hash (wrong-prefix probes charge IP only, so they cannot poison the other API's hash bucket). Mutating requests are limited per token/session id. Exhaustion returns `429` + `Retry-After`. DAL/infra errors from token/session lookup return `503` without burning auth-failure budget.
- Security headers (`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`), strict server timeouts, `http.MaxBytesReader` body limit, graceful shutdown on `SIGINT`/`SIGTERM`.
- **MCP** at `/mcp` (see [Remote MCP](#remote-mcp-streamable-http)): Streamable HTTP, ControlAPI-client-only, seven compact tools, PAT/session auth with tenant isolation.
- `cmd/server bootstrap --tenant <name>` creates a tenant and the first 256-bit `dj_pat_...` API key (`crypto/rand`). Only the SHA-256 hash and non-secret prefix/metadata are stored; the raw key is printed once to stdout and must never appear in DB or logs.
- `cmd/server create-user --email ... --password ... [--tenant-name ...]` creates a tenant + argon2id user in MongoDB even when `ALLOW_SIGNUP=false` (password may also come from `DAD_JOKE_CREATE_USER_PASSWORD`).

### Hardened container & self-host templates

Multi-stage `Dockerfile`: Node TypeSpec → OpenAPI, then Go `oapi-codegen` + static compile, then `gcr.io/distroless/static-debian12:nonroot` (final image has no Node/Go toolchain). Works from a fresh checkout because `gen/` is gitignored and rebuilt in-image.

| Surface | Behavior |
| --- | --- |
| **Dev compose** (`docker-compose.yml`) | Builds locally; publishes `8080` / `27017`; `COOKIE_SECURE=false`; server runs non-root (`65532`), `read_only: true`, `no-new-privileges`, `cap_drop: ALL`; MongoDB `--wiredTigerCacheSizeGB` capped. |
| **Prod compose** (`deploy/compose.prod.yml`) | Pulls `ghcr.io/barzoj/dad-joke-server:${IMAGE_TAG:-latest}`; **does not** publish `8080` or `27017`; `COOKIE_SECURE=true`; same hardening flags; Caddy terminates HTTPS on 80/443. `deploy/caddy-entrypoint.sh` renders `deploy/Caddyfile` from `${API_DOMAIN}` / `${MCP_DOMAIN}` only into **one** site block (single host when equal; comma-separated when distinct — Caddy rejects duplicate site addresses). |
| **Ops scripts** | `scripts/backup-mongo.sh` (`mongodump --archive --gzip` + retention); `scripts/smoke-e2e.sh` (healthz → signup → PAT/agent token → heartbeat → MCP `tools/list` / `list_machines`); `scripts/no-leak-check.sh` (CI sterility gate). |
| **CI / release** | Trivy `HIGH`/`CRITICAL` image scan; Compose smoke runs `smoke-e2e.sh` end-to-end; Caddyfile validated (`caddy adapt --validate`) for both equal and distinct domains; `v*` tags publish Windows `.exe`+`.sha256` and multi-arch GHCR images by pushing an immutable `sha-<commit>` candidate, smoke-testing that exact digest on `amd64`+`arm64`, then promoting the same digest to `v0.0.x` and `latest`. |

## Windows agent runtime (current)

`dad-joke.exe run` (Windows Service under `LocalSystem`) runs three independent background loops via `service.Group`:

| Loop | Package | Behavior |
| --- | --- | --- |
| **Auto-update** | `internal/updater` | Polls `release_api_url` on `poll_interval_seconds`; SHA-256–verified apply. Never sends `Authorization` / `token`. |
| **Heartbeat** | `internal/heartbeat` | When `backend_url` is non-empty, posts `POST /v1/heartbeat` via the generated `gen/go/agentapi` client with `Authorization: Bearer <token>`, `machine_id`, compile-time `Version`, hostname, OS version, and `applied_policy_version` (`0` until policy apply exists). First beat promptly on start, then every `heartbeat_interval_seconds`. On success, `pending_job_ids` are handed off asynchronously (non-blocking) to the job worker. |
| **Job worker** | `internal/jobs` | Sequential consumer of handed-off job ids. For each id: `POST /v1/jobs/{id}/claim` (skip cleanly on `409`/`404`); Zero-Trust validation (only contract types `powershell` / `cmd`; payload ≤ 64 KiB) before spawn; create the process with `CREATE_SUSPENDED`, `AssignProcessToJobObject` on `pi.Process`, then `ResumeThread` so membership is established before any instruction (and before grandchildren) can run; stdio inheritance is restricted with `PROC_THREAD_ATTRIBUTE_HANDLE_LIST` (NUL + stdout/stderr write ends only); Job Object uses `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` (plus `TerminateJobObject` on timeout); hard timeout default **60s**, max **300s** (clamped); bounded stdout/stderr capture capped at **64 KiB** with UTF-8-safe truncation; `POST /v1/jobs/{id}/result` (`succeeded` / `failed` / `timed_out`) and best-effort `POST /v1/events` (`job.result`); append a local JSONL audit line under `C:\ProgramData\DadJoke\jobs-audit.jsonl` with strict `SYSTEM` + `Administrators` ACL (`internal/acl`), scrubbing the agent token. Never logs the raw agent token. |

Independence: backend DNS/5xx/401/outage is logged (with the raw `token` redacted) and must not stop the service, the updater loop, or the job worker. A slow or timed-out job must never stall heartbeats or update polling. Empty `backend_url` disables heartbeats (and therefore job discovery) with no error noise (local-only `v0.0.1` mode).

Transport for `backend_url`: empty allowed; non-empty requires `https://` except loopback `http://` (`127.0.0.1` / `::1` / `localhost`); no URL userinfo; system CA verification; TLS `MinVersion` 1.2; redirects re-validated and strip `Authorization` on cross-host hops.

Job contract fields used by the agent (from `api/common/jobs.tsp`): `type` (`powershell` \| `cmd`), `payload`, optional `timeout_seconds` (1..300), and result status including `timed_out`.

## Engineering Principles

| Principle | Meaning |
| --- | --- |
| **KISS** | Prefer the simplest direct mechanism over speculative abstractions. |
| **Stateless-first** | Prefer idempotent desired-state reconciliation over mutable state wherever possible. |
| **SOLID & clean layering** | Small, cohesive packages with clear interfaces. Isolate all Windows OS calls behind interfaces so core logic stays testable without the OS. |
| **Test-first** | Functional requirements map to automated unit tests (including edge cases) and Windows E2E acceptance tests before or alongside implementation. |

## Independent Review & CI

Every change requires an independent review that verifies alignment with these invariants—layer boundaries, KISS/SOLID/statelessness, Windows security/ACL correctness, edge cases, and test coverage—plus green CI:

- API generate + race test/vet (live MongoDB DAL)
- Windows test/build + native `windows-latest` E2E (`-tags=e2e`: install/ACL, update, heartbeat, jobs Job Object timeout/truncation, **Restart-Service** config+token persistence)
- Linux full-stack E2E (`e2e/linux`, `-tags=e2e`: register → PAT/`dj_agt_` → heartbeat → MCP `list_machines`/`set_policy`/`run_script`/`get_job`/`list_events` → cross-tenant isolation → revocation `401` → fail-closed MongoDB)
- Compose image smoke (non-root, read-only rootfs, `/healthz`) + `scripts/smoke-e2e.sh`
- No-leak gate over public deploy/scripts/docs/workflows
- Linux race+coverage with an `internal/` floor excluding `gen/`
- Security workflow: CodeQL, govulncheck, gosec, golangci-lint, Trivy image scan

