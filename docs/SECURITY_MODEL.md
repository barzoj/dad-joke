# Security Model

Zero-Trust / anti-botnet threat model for **Dad Joke**. This document describes the current implemented design and the security invariants every component must obey. Future work belongs in issues—not here.

## Threat model

The Windows agent runs as a **Windows Service under `LocalSystem`**. That privilege is required for policy enforcement, but it means:

- A compromised **update channel** that can push an unverified binary can take over every enrolled machine.
- A compromised **control plane** that can issue unconstrained remote jobs or policies can turn the fleet into a botnet.

Therefore the product assumes the network and backends are hostile unless cryptographic and authorization checks succeed. Least privilege, short TTLs, strict isolation, and fail-closed verification are mandatory—not optional hardening.

## Core invariants

| Invariant | Requirement |
| --- | --- |
| Tenant isolation | Every persisted record and API decision is scoped by `tenant_id`. Cross-tenant reads/writes are impossible (return not-found, never leak existence across tenants). |
| Split tokens | Agent credentials use `dj_agt_` prefixes; control-plane credentials use `dj_pat_`. Wrong prefix for an API surface is rejected. Secrets are stored as **SHA-256 hashes only**, compared in constant time, and **never logged**. See [Token formats](#token-formats-dj_agt--dj_pat_) below. |
| Transport | Outbound agent/control HTTPS uses system CA verification. Cleartext is rejected except loopback test doubles. |
| Job containment | Remote jobs have short TTLs, run inside a Windows Job Object with hard timeouts, and truncate stdout/stderr (output caps). |
| Audit | Sensitive actions append to append-only `audit_logs` (token issue/revoke, policy mutations, job launch/result). |
| Runtime hardening | Server containers run non-root with a minimal attack surface. |
| Abuse controls | Auth failures and sensitive mutations are rate-limited. |
| Update integrity | Agent self-update verifies **SHA-256** of release assets before replace; failed verification aborts and cleans staging. |
| Secure SDLC | Every change is gated by SAST and dependency vulnerability scanning in CI (CodeQL, gosec, govulncheck, golangci-lint). |

Parent-key signing of commands/policies is an additional invariant for future control-plane ↔ agent contracts: unsigned or unverifiable directives must not be applied once that control exists.

## Token formats (`dj_agt_` / `dj_pat_`)

HTTP contracts freeze a clear role split and Secret Scanning–friendly prefixes:

| Prefix | Issued by | Used on | Entropy | Role |
| --- | --- | --- | --- | --- |
| `dj_agt_` | ControlAPI `POST /v1/agent-tokens` | AgentAPI (`AgentBearerAuth`) | **256-bit** opaque secret after the prefix | On-device agent ↔ control plane |
| `dj_pat_` | ControlAPI `POST /v1/api-keys` | ControlAPI (`ControlBearerAuth`), MCP, automation | **256-bit** opaque secret after the prefix | Parent/operator control plane |

Rules that bind every implementation of these contracts:

- Unique prefixes keep agent and control credentials non-interchangeable and detectable by secret-scanning patterns.
- The **raw secret** is returned exactly once in the `201 Created` response (`CreatedAgentToken` / `CreatedApiKey`) and is **absent** from every `GET` list/summary model.
- At rest: store **SHA-256 hashes only**; compare in constant time; never log the raw value or write it to audit payloads.
- `GET /healthz` on both APIs remains unauthenticated; all `/v1/...` routes require the matching bearer scheme.

## Already implemented (`v0.0.x`)

These controls exist in the current Windows agent / repository:

- **LocalSystem service + ACL hardening** — install tree and `ProgramData` config allow `SYSTEM` and `Administrators` only.
- **Local identity secrecy** — `machine_id` + install `token` in `C:\ProgramData\DadJoke\config.json`; raw token must not appear in service logs or updater HTTP.
- **HTTPS download policy** — release API and asset URLs must be HTTPS (loopback HTTP only for local tests); redirects are re-validated.
- **Cryptographic update verification** — staged `dad-joke-windows-amd64.exe` must match the published `.sha256` sidecar before apply.
- **Apply path allowlist** — `apply-update` may only replace the installed binary from a staging path under `ProgramData\DadJoke\updates\...`.
- **CI security stack** — CodeQL (`go`, `actions`), `govulncheck`, `gosec` (SARIF → code scanning), `golangci-lint`, Renovate config validation, and Dependency Review (high/critical CVEs + AGPL-incompatible licenses) on PRs/`main`/weekly as applicable.
- **HTTP auth/token/heartbeat/machine contracts** — TypeSpec `AgentBearerAuth` / `ControlBearerAuth`, agent-token and API-key CRUD shapes (raw secret only on `201 Created`), `POST /v1/heartbeat`, and machine listing; unified `ErrorResponse` including `429` + `Retry-After`.

## Invariant contracts (enforced as components land)

The following are **binding design contracts**. New packages must implement them when the corresponding surface appears; they are not optional backlog ideas:

- Strict `tenant_id` isolation across AgentAPI, ControlAPI, and storage.
- Prefixed tokens (`dj_agt_` / `dj_pat_`), hashed at rest, never logged (HTTP shapes frozen in TypeSpec; see above).
- Append-only `audit_logs` for sensitive operations.
- Per-IP / per-token rate limits on auth failures and mutations.
- Short job TTLs, Windows Job Object kill-on-timeout, and output caps for remote script execution.
- Non-root hardened server container image.
- Fail-closed authn/authz on every new HTTP surface.

Until a contract’s owning component exists, the invariant still constrains design reviews and API contracts so a later backend cannot quietly widen the botnet blast radius.
