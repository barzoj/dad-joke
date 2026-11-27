# Dad Joke

Windows Home parental control & native policy enforcement engine (test-first).

[![CI](https://github.com/barzoj/dad-joke/actions/workflows/ci.yml/badge.svg)](https://github.com/barzoj/dad-joke/actions/workflows/ci.yml)
[![Security](https://github.com/barzoj/dad-joke/actions/workflows/security.yml/badge.svg)](https://github.com/barzoj/dad-joke/actions/workflows/security.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/barzoj/dad-joke/badges/coverage.json)](https://github.com/barzoj/dad-joke/actions/workflows/ci.yml)

Security policy: [`SECURITY.md`](SECURITY.md). Threat model: [`docs/SECURITY_MODEL.md`](docs/SECURITY_MODEL.md). Architecture: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

The coverage badge is a free [shields.io endpoint](https://shields.io/badges/endpoint-badge) reading `coverage.json` from the orphan `badges` branch (updated on each push to `main` by the Coverage job; no third-party coverage SaaS or secrets).

Dependency updates: install the free [Renovate GitHub App](https://github.com/apps/renovate) on this repository so `renovate.json` can open weekly grouped patch/minor PRs (and immediate security updates). PRs are also gated by GitHub Dependency Review (`fail-on-severity: high` plus a deny-list for licenses incompatible with AGPL-3.0).
