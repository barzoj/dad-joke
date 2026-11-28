# Security Model

Zero-Trust / anti-botnet threat model for **Dad Joke**. This document describes the current implemented design and the security invariants every component must obey. Future work belongs in issues—not here.

## Threat model

The Windows agent is a **two-process** design: a **Windows Service under `LocalSystem`** (Session 0) plus a **session agent** in each interactive user session (`dad-joke.exe session`, same binary). Service privilege is required for policy enforcement and supervision, but it means:

- A compromised **update channel** that can push an unverified binary can take over every enrolled machine.
- A compromised **control plane** that can issue unconstrained remote jobs or policies can turn the fleet into a botnet.

The session agent runs as the **logged-on user**, so the service treats it as **untrusted**: it receives no agent token, backend URL, or ProgramData secrets, and nothing it sends later (IPC) must be trusted without validation. A user can kill their session agent; the service respawns it and records `session.restarted` (gaps are visible — never fabricated).

Therefore the product assumes the network and backends are hostile unless cryptographic and authorization checks succeed. Least privilege, short TTLs, strict isolation, and fail-closed verification are mandatory—not optional hardening.

## Core invariants

| Invariant | Requirement |
| --- | --- |
| Tenant isolation | Every persisted record and API decision is scoped by `tenant_id`. Cross-tenant reads/writes are impossible (return not-found, never leak existence across tenants). |
| Split tokens | Agent credentials use `dj_agt_` prefixes; control-plane API keys use `dj_pat_`; user sessions use `dj_ses_` (cookie `dj_session`). Wrong prefix for an API surface is rejected. Secrets are stored as **SHA-256 hashes only**, compared in constant time, and **never logged**. User passwords use **argon2id** (never returned in API JSON). See [Token formats](#token-formats-dj_agt_--dj_pat_) below. |
| Transport | Outbound agent/control HTTPS uses system CA verification. Cleartext is rejected except loopback test doubles. |
| Job containment | Remote jobs have short TTLs, run inside a Windows Job Object with hard timeouts, and truncate stdout/stderr (output caps). |
| Audit | Sensitive actions append to append-only `audit_logs` (token issue/revoke, policy mutations, job launch/result). |
| Runtime hardening | Server containers run non-root with a minimal attack surface. |
| Abuse controls | Auth failures and sensitive mutations are rate-limited. |
| Update integrity | Agent self-update verifies **SHA-256** of release assets before replace; failed verification aborts and cleans staging. |
| Secure SDLC | Every change is gated by SAST and dependency vulnerability scanning in CI (CodeQL, gosec, govulncheck, golangci-lint, Trivy image scan). |

Parent-key signing of commands/policies is an additional invariant for future control-plane ↔ agent contracts: unsigned or unverifiable directives must not be applied once that control exists.

## Token formats (`dj_agt_` / `dj_pat_`)

HTTP contracts freeze a clear role split and Secret Scanning–friendly prefixes:

| Prefix | Issued by | Used on | Entropy | Role |
| --- | --- | --- | --- | --- |
| `dj_agt_` | ControlAPI `POST /v1/agent-tokens` | AgentAPI (`AgentBearerAuth`) | **256-bit** opaque secret after the prefix | On-device agent ↔ control plane |
| `dj_pat_` | ControlAPI `POST /v1/api-keys` | ControlAPI (`ControlBearerAuth`), MCP, automation | **256-bit** opaque secret after the prefix | Parent/operator control plane |
| `dj_ses_` | ControlAPI `POST /v1/auth/register` / `login` | ControlAPI (Bearer and/or `dj_session` cookie) | **256-bit** opaque secret after the prefix | Interactive user session (revocable) |

Rules that bind every implementation of these contracts:

- Unique prefixes keep agent, PAT, and session credentials non-interchangeable and detectable by secret-scanning patterns.
- The **raw secret** for API keys/agent tokens is returned exactly once in the `201 Created` response (`CreatedAgentToken` / `CreatedApiKey`) and is **absent** from every `GET` list/summary model. Session secrets are returned on register/login (`AuthSession.access_token`) and as an HttpOnly cookie; `POST /v1/auth/logout` revokes them.
- At rest: store **SHA-256 hashes only** for tokens/sessions; compare in constant time; never log the raw value or write it to audit payloads. User `password_hash` is argon2id and never appears in API DTOs or logs.
- `GET /healthz` on both APIs remains unauthenticated; `POST /v1/auth/register` and `POST /v1/auth/login` are unauthenticated; other `/v1/...` routes require the matching credential.

## Already implemented (`v0.0.x`)

These controls exist in the current Windows agent / repository:

- **LocalSystem service + ACL hardening** — install tree and `ProgramData` config allow `SYSTEM` and `Administrators` only. The durable facts queue at `C:\ProgramData\DadJoke\facts\` (batch files, `state.json`, `rejected\`) uses the same `SYSTEM` + `Administrators` ACL; a Standard User cannot read, write, or delete queue files.
- **Session agent isolation** — `dad-joke.exe session` runs as the user (no elevation); command line and environment carry no token/URL/`DAD_JOKE_*` secrets; it does not open `config.json`. The service owns child process handles for terminate/relaunch.
- **Local identity secrecy** — `machine_id` + install `token` in `C:\ProgramData\DadJoke\config.json`; raw token must not appear in service logs, error strings, updater / GitHub Releases HTTP, or facts-uploader logs.
- **AgentAPI heartbeat / facts transport** — optional `backend_url` uses system CA TLS (≥1.2); cleartext only for loopback; redirects strip `Authorization` on host change; heartbeat and facts-upload failures never take down the service, updater, or sampler. Facts batches retry with identical bytes for `(stream_id, seq)` idempotency; `401`/`403` retain the batch and back off without logging the token.
- **HTTPS download policy** — release API and asset URLs must be HTTPS (loopback HTTP only for local tests); redirects are re-validated.
- **Cryptographic update verification** — staged `dad-joke-windows-amd64.exe` must match the published `.sha256` sidecar before apply.
- **Apply path allowlist** — `apply-update` may only replace the installed binary from a staging path under `ProgramData\DadJoke\updates\...`.
- **CI security stack** — CodeQL (`go`, `actions`), `govulncheck`, `gosec` (SARIF → code scanning), `golangci-lint`, Renovate config validation, and Dependency Review (high/critical CVEs + AGPL-incompatible licenses) on PRs/`main`/weekly as applicable.
- **HTTP auth/token/heartbeat/machine contracts** — TypeSpec `AgentBearerAuth` / `ControlBearerAuth`, agent-token and API-key CRUD shapes (raw secret only on `201 Created`), `POST /v1/heartbeat`, and machine listing; unified `ErrorResponse` including `429` + `Retry-After`.
- **Server DAL + bootstrap** — `internal/store` domain interfaces with MongoDB isolated in `internal/store/mongo`; `bootstrap` stores SHA-256 hashes only for `dj_pat_...` keys; hardened non-root distroless container + Trivy image scan in CI.
- **Zero-Trust HTTP auth** — middleware enforces prefix split (`dj_agt_` vs `dj_pat_` / `dj_ses_`), SHA-256 + constant-time verify via DAL, tenant/token/user context injection, immediate revoke → `401`, cross-tenant token ops → `404`, append-only `audit_logs` for token issue/revoke (with policy/script audit hooks), and in-memory per-IP / per-token rate limits returning `429` + `Retry-After`.
- **Local email/password auth** — argon2id password hashing; register creates tenant+user with orphan-tenant rollback on conflict; login anti-enumeration (dummy verify + identical `401`); failed-login rate limits per IP/email; `ALLOW_SIGNUP` gate; `create-user` CLI for ops when signup is closed.

## Invariant contracts (enforced as components land)

The following are **binding design contracts**. New packages must implement them when the corresponding surface appears; they are not optional backlog ideas:

- Strict `tenant_id` isolation across AgentAPI, ControlAPI, and storage (**implemented** for auth + token CRUD + machine reads).
- Prefixed tokens (`dj_agt_` / `dj_pat_` / `dj_ses_`), hashed at rest, never logged (**implemented** in HTTP middleware + DAL).
- Append-only `audit_logs` for sensitive operations (**implemented** for token issue/revoke; hooks ready for policy/script).
- Per-IP / per-token (and per-email login) rate limits on auth failures and mutations (**implemented**, process-local).
- Short job TTLs, Windows Job Object kill-on-timeout, and output caps for remote script execution (**implemented** in the agent job worker: claim → validate `powershell`/`cmd` → Job Object run → result/`timed_out` → local JSONL audit under `ProgramData`).
- Non-root hardened server container image (**implemented** for the server image).
- Fail-closed authn/authz on every new HTTP surface (**implemented** for current `/v1/...` routes; `/healthz`, register, and login remain unauthenticated).

Until a contract’s owning component exists, the invariant still constrains design reviews and API contracts so a later backend cannot quietly widen the botnet blast radius.
