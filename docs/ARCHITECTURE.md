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

Both APIs currently expose **`GET /healthz`** (JSON `{ "status": "ok" }`) to prove the compile chain end-to-end. Domain endpoints are added in TypeSpec first, then regenerated—no parallel hand-written HTTP contracts.

## Engineering Principles

| Principle | Meaning |
| --- | --- |
| **KISS** | Prefer the simplest direct mechanism over speculative abstractions. |
| **Stateless-first** | Prefer idempotent desired-state reconciliation over mutable state wherever possible. |
| **SOLID & clean layering** | Small, cohesive packages with clear interfaces. Isolate all Windows OS calls behind interfaces so core logic stays testable without the OS. |
| **Test-first** | Functional requirements map to automated unit tests (including edge cases) and Windows E2E acceptance tests before or alongside implementation. |

## Independent Review & CI

Every change requires an independent review that verifies alignment with these invariants—layer boundaries, KISS/SOLID/statelessness, Windows security/ACL correctness, edge cases, and test coverage—plus green CI (API generate + race test/vet, Windows test/build, Linux race+coverage with an `internal/` floor excluding `gen/`, and the Security workflow: CodeQL, govulncheck, gosec, golangci-lint).
