//go:build spike

// Package policyspike holds the T-24 native Windows policy application spike.
//
// Enable with: go test -tags=spike ./e2e/policy-spike/...
// Requires a Windows host with admin rights (GitHub-hosted windows-2025).
//
// These tests are intentionally isolated from the production agent and from the
// regular e2e suite (build tag `spike`, not `e2e`). Each scope item applies a
// real OS policy, verifies the effect, measures latency, and rolls back.
package policyspike
