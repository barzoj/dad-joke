# Security Policy

## Supported Versions

| Version | Supported |
| --- | --- |
| `v0.0.x` | Yes |
| older / untagged builds | No |

Only tagged releases of the Dad Joke Windows agent and related packages in this repository receive security fixes.

## Reporting a Vulnerability

Please use **GitHub Private Vulnerability Reporting** for this repository:

https://github.com/barzoj/dad-joke/security/advisories/new

Do **not** open a public issue for security-sensitive reports.

Include as much of the following as you can:

- Affected version / commit / release asset
- Component (agent, updater, CLI, future control-plane packages)
- Reproduction steps or a minimal proof of impact
- Impact assessment (privilege, data exposure, remote code execution)

### Response expectations

- We aim to acknowledge valid reports within **3 business days**.
- We aim to share an initial triage (accepted / needs more info / declined) within **7 business days**.
- Coordinated disclosure is preferred; please give us a reasonable window to ship a fix before public discussion.

## Scope notes

- The Windows agent runs as a service under `LocalSystem`. Reports that turn update, auth, or job channels into arbitrary fleet-wide code execution are highest priority.
- See [`docs/SECURITY_MODEL.md`](docs/SECURITY_MODEL.md) for the Zero-Trust threat model and enforceable security invariants.
