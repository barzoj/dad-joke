# AGENTS.md

Guidance for coding and review agents working in this repository. Canonical architecture: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Architectural invariants (summary)

1. **Privilege & runtime**
   - Target: Windows Home (compatible with Pro/Enterprise).
   - Child = Standard User; agent = Windows Service under `LocalSystem`.
   - Agent binaries and config: ACLs allow `SYSTEM` and `Administrators` only.

2. **Two-layer separation**
   - **Lower (Policy Enforcement Engine):** dumb, stateless desired-state reconciler. Applies native Windows primitives (`HKLM`, `HKU\<SID>`, Firewall, Logon Hours) and Home-compatible enforcement for formats Home ignores (e.g. AppLocker `SrpV2`).
   - **Upper (Control Plane / Product Logic):** owns schedules, quotas, interactive flows; pushes desired state via explicit contracts. Never applies OS policy primitives directly.

3. **API-first:** every boundary (control-plane ↔ agent, CLI, internal services, policy formats) has an explicit, versioned contract before implementation.

4. **Do not** put roadmaps, milestones, or temporary version-scope lists in `docs/ARCHITECTURE.md`. Future work lives in issues.

## Go / Windows service conventions

- Prefer small packages with clear ownership; keep Windows-specific code behind interfaces in dedicated packages.
- The agent process is a Windows Service under `LocalSystem`; treat privilege carefully—never weaken install/runtime ACLs.
- Desired-state reconciliation must be **idempotent**: re-applying the same desired state is a no-op when already correct.
- Fail closed on policy apply errors where safe; surface clear errors to the control plane rather than silently drifting.
- Avoid hidden global mutable state in the enforcement path.

## Engineering rules

| Rule | Expectation |
| --- | --- |
| **KISS** | Simplest direct mechanism; no speculative abstractions. |
| **Stateless-first** | Idempotent reconciliation over mutable state. |
| **SOLID** | Cohesive packages; Windows OS calls isolated behind interfaces. |
| **API-first** | Versioned contracts before code at every boundary. |
| **Test-first** | Unit tests for edge cases; Windows E2E acceptance tests for enforcement behavior. |
| **Docs in the same PR** | Any PR that changes user-visible behaviour, install/upgrade steps, config keys, env vars, CLI flags, release artifacts, or an HTTP/MCP API **must** update [`README.md`](README.md) (and [`docs/`](docs/) when design changes) in the **same** PR. Reviewers must reject PRs that ship behaviour or contract changes without matching docs. |

## Code Review Checklist

Reviewers (human or agent) must verify **all** of the following before approving:

- [ ] **Architecture alignment** — Change respects two-layer separation; no product/stateful logic in the enforcement engine; no direct Windows policy application from the control plane.
- [ ] **API / contracts** — Cross-boundary changes update or respect versioned contracts; no ad-hoc coupling.
- [ ] **KISS / SOLID / stateless** — No unnecessary abstraction; OS calls stay behind interfaces; reconciliation remains idempotent where applicable.
- [ ] **Edge cases** — Empty/partial desired state, re-apply, concurrent apply, missing user SID/profile, Home vs Pro capability differences.
- [ ] **Error & rollback paths** — Failures are handled explicitly; partial apply does not leave an unsafe or unexplained state; errors propagate clearly.
- [ ] **Windows security / ACL correctness** — Binaries and config remain `SYSTEM` + `Administrators` only; no privilege escalation or ACL weakening; Standard User cannot tamper with agent assets.
- [ ] **Test coverage** — Unit tests cover new logic and edge cases; Windows E2E acceptance coverage exists or is extended for enforcement/ACL-affecting behavior; CI is green.
- [ ] **README / docs currency** — If the PR changes user-visible behaviour, install/upgrade steps, config keys, env vars, CLI flags, release artifacts, or an HTTP/MCP API, `README.md` (and `docs/` when design changes) are updated in this PR; otherwise reject.
