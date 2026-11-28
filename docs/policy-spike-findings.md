# Policy spike findings (T-24)

Facts observed on GitHub-hosted `windows-2025` (Windows Server 2025).
Filled from automated `go test -tags=spike ./e2e/policy-spike/...` CI output.
Client SKU (Win11 Pro/Home) differences are noted only when known or strongly suspected; those SKUs are out of scope for this spike.

| Mechanism | Variant | Works | Latency | Needs re-logon | Needs reboot | Visible in gpresult | Gotchas | Client SKU caveats |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| _(pending first green CI run — table rows are replaced from `spike-out/policy-spike-findings.generated.md`)_ | | | | | | | | |

## Notes

- Latency is wall-clock from apply/refresh start to first successful verification of the effect.
- "Works" means the spike observed the intended enforcement or registry effect on this Server SKU.
- Forced logoff of an active session is not performed by Windows for logon-hours on a standalone box; the agent must call WTS APIs itself (see session-kick rows).
- Spike code lives under `e2e/policy-spike/` (`//go:build spike`); workflow: `.github/workflows/policy-spike.yml`.
