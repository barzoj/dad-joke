# Architecture

This document defines the enduring architectural invariants of **Dad Joke**: a Windows Home parental-control and native policy-enforcement system. It is the canonical reference for design and review. Future work belongs in issues—not here. Security invariants and the Zero-Trust threat model live in [`SECURITY_MODEL.md`](SECURITY_MODEL.md).

## Privilege & Runtime Model

- **Target OS:** Windows Home, compatible with Pro and Enterprise.
- **Child session:** runs as a **Standard User**.
- **Agent:** runs as a **Windows Service** under `LocalSystem`.
- **Hardening:** agent binaries and configuration are protected by strict Windows ACLs—`SYSTEM` and `Administrators` only.
- **Persistent identity:** `C:\ProgramData\DadJoke\config.json` holds a stable `machine_id` (RFC 4122 UUIDv4) and a secret `token` (auto-generated as 256-bit `crypto/rand` → 64-char lowercase hex, or an explicit install `--token` stored verbatim). Both are created on first install, preserved across reinstalls/self-updates (unless `--token` replaces the secret), and loaded at service startup. The raw `token` must never appear in service logs or outbound updater HTTP.

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
| **AgentAPI** | `AgentBearerAuth` (HTTP Bearer `dj_agt_...`); `GET /healthz` uses `@useAuth(NoAuth)` | `POST /v1/heartbeat` — machine identity + applied policy; response includes `server_time`, `desired_policy_version`, `pending_job_ids` |
| **ControlAPI** | `ControlBearerAuth` (HTTP Bearer `dj_pat_...` **or** user session `dj_ses_...` / `dj_session` cookie); `GET /healthz`, `POST /v1/auth/register`, `POST /v1/auth/login` use `@useAuth(NoAuth)` | `POST /v1/auth/register\|login\|logout`, `GET /v1/auth/me`, `POST/GET/DELETE /v1/agent-tokens`, `POST/GET/DELETE /v1/api-keys`, `GET /v1/machines`, `GET /v1/machines/{machine_id}` |

Unified failures use `ErrorResponse` (`code`, `message`, `request_id`) for `400` / `401` / `403` / `404` / `409` / `413` / `429` / `500` / `503`. `429` responses declare a `Retry-After` header.

Raw token secrets appear **only** on `201 Created` models (`CreatedAgentToken`, `CreatedApiKey`) and register/login session responses (`AuthSession.access_token`); they are absent from every `GET` summary/list model. Domain endpoints are added in TypeSpec first, then regenerated—no parallel hand-written HTTP contracts.

## Control-plane server & data layer (current)

### DAL boundary (`internal/store`)

Domain models are plain Go structs (string UUIDs, no `bson` tags, no `ObjectID`). Repository interfaces live in `internal/store`:

`TenantStore`, `UserStore`, `SessionStore`, `TokenStore`, `MachineStore`, `PolicyStore`, `JobStore`, `EventStore`, `AuditStore`, plus `Pinger` and a `Store` facade (`EnsureIndexes`, `Close`).

HTTP handlers and product logic talk only to these interfaces. An architecture test (`internal/archtest`) fails CI if any package outside `internal/store/mongo` imports `go.mongodb.org/mongo-driver`.

Collections / models of note:

- `users`: `id`, `tenant_id`, `email` (lowercased/trimmed, unique), `password_hash` (argon2id; never in JSON/logs), `created_at`
- `user_identities`: `id`, `user_id`, `provider`, `subject` (unique on `(provider, subject)`), `created_at` — schema ready for a later Google OAuth link; no OAuth handlers yet
- `sessions`: hashed `dj_ses_...` credentials with expiry and revoke timestamp

### MongoDB implementation (`internal/store/mongo`)

Built on `go.mongodb.org/mongo-driver/v2`. Private BSON mapper structs stay inside the package. `EnsureIndexes(ctx)` creates idempotent unique/compound/TTL indexes at startup (tenant name, user email, identity provider+subject, session hash, token kind+hash, event idempotency, event TTL, audit listing, etc.).

### `cmd/server`

- HTTP server with `GET /healthz`: `200` + `{"status":"ok"}` when MongoDB `Ping` succeeds; `503` + `{"status":"unavailable"}` when it fails. Unauthenticated by design.
- **Local email/password auth** (ControlAPI):
  - `POST /v1/auth/register` atomically creates a tenant + user (`argon2id` via `golang.org/x/crypto/argon2`, random salt). Duplicate email → `409` with compensating tenant delete (no orphan). `ALLOW_SIGNUP=false` → `403`.
  - `POST /v1/auth/login` verifies with constant-time compare; unknown email runs a dummy argon2id verify so timing matches wrong-password. Identical `401` body for both cases. Failed logins are rate-limited per IP and per email (`429` + `Retry-After`); store infra errors return `503` without burning the budget.
  - Successful register/login issues a revocable `dj_ses_...` session (hashed at rest) returned as `access_token` and as an `HttpOnly; SameSite=Lax` cookie (`Secure` default `true`, overridable with `COOKIE_SECURE=false` for local HTTP).
  - `POST /v1/auth/logout` revokes the session; subsequent `/v1/auth/me` with that session is `401`.
  - `GET /v1/auth/me` returns the user profile for a session (never `password_hash`).
- **Zero-Trust auth middleware** (process-local):
  - AgentAPI routes (`POST /v1/heartbeat`): `Authorization: Bearer dj_agt_...` only; SHA-256 verified via `TokenStore.GetByHash` with constant-time compare; revoked/expired/wrong-prefix (including user sessions) → `401` `ErrorResponse`.
  - ControlAPI routes: `Authorization: Bearer dj_pat_...` **or** `dj_ses_...` Bearer / `dj_session` cookie; same verification; agent tokens → `401`.
  - Successful auth puts `tenant_id` and actor id on `context.Context` (`internal/authctx`); user sessions also set `user_id` / `session_id`. Handlers are strictly tenant-scoped (cross-tenant → `404`).
- **Token CRUD** (ControlAPI): create/list/delete agent tokens and API keys (session or PAT). Raw secret returned only on `201 Created`; at rest only SHA-256 hash + non-secret prefix. Issue and revoke append to `audit_logs` (`internal/audit`; hooks also exist for policy create / script launch).
- **Rate limits** (`internal/ratelimit`): in-memory sliding window per process (no Redis), injectable clock, bounded key set. Auth failures are limited per IP and per `wantKind`+token-hash (wrong-prefix probes charge IP only, so they cannot poison the other API's hash bucket). Mutating requests are limited per token/session id. Exhaustion returns `429` + `Retry-After`. DAL/infra errors from token/session lookup return `503` without burning auth-failure budget.
- Security headers (`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`), strict server timeouts, `http.MaxBytesReader` body limit, graceful shutdown on `SIGINT`/`SIGTERM`.
- `cmd/server bootstrap --tenant <name>` creates a tenant and the first 256-bit `dj_pat_...` API key (`crypto/rand`). Only the SHA-256 hash and non-secret prefix/metadata are stored; the raw key is printed once to stdout and must never appear in DB or logs.
- `cmd/server create-user --email ... --password ... [--tenant-name ...]` creates a tenant + argon2id user in MongoDB even when `ALLOW_SIGNUP=false` (password may also come from `DAD_JOKE_CREATE_USER_PASSWORD`).

### Hardened container (dev)

Multi-stage `Dockerfile`: Node TypeSpec → OpenAPI, then Go `oapi-codegen` + static compile, then `gcr.io/distroless/static-debian12:nonroot` (final image has no Node/Go toolchain). Works from a fresh checkout because `gen/` is gitignored and rebuilt in-image. Dev `docker-compose.yml` runs `server` (non-root, `read_only: true`, `no-new-privileges`, `cap_drop: ALL`) with MongoDB (`--wiredTigerCacheSizeGB` capped). CI scans the image with Trivy (`HIGH`/`CRITICAL`). Production compose/Caddy/backups are not part of this repository.

## Engineering Principles

| Principle | Meaning |
| --- | --- |
| **KISS** | Prefer the simplest direct mechanism over speculative abstractions. |
| **Stateless-first** | Prefer idempotent desired-state reconciliation over mutable state wherever possible. |
| **SOLID & clean layering** | Small, cohesive packages with clear interfaces. Isolate all Windows OS calls behind interfaces so core logic stays testable without the OS. |
| **Test-first** | Functional requirements map to automated unit tests (including edge cases) and Windows E2E acceptance tests before or alongside implementation. |

## Independent Review & CI

Every change requires an independent review that verifies alignment with these invariants—layer boundaries, KISS/SOLID/statelessness, Windows security/ACL correctness, edge cases, and test coverage—plus green CI (API generate + race test/vet including live MongoDB DAL tests, Windows test/build, Linux race+coverage with an `internal/` floor excluding `gen/`, and the Security workflow: CodeQL, govulncheck, gosec, golangci-lint, Trivy image scan).
