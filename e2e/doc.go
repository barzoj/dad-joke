// Package e2e_test holds Windows acceptance tests for dad-joke.
//
// Enable with: go test -tags=e2e ./e2e/...
// Requires DAD_JOKE_BIN (and optionally DAD_JOKE_DIST / expectation env vars).
//
// Issue #2: TC-01 / TC-02 (//go:build e2e && windows).
// Issue #3: TC-03 / TC-04 / TC-05 update checker E2E (local GitHub Releases mock).
// Issue #10: TC-10 heartbeat to local AgentAPI + non-HTTPS external URL rejection.
// Issue #14: TC-14 job claim/execute/result, timeout kill, truncation, isolation, audit ACL.
// Issue #16: TC-16 Restart-Service config+token persistence; Linux suite in e2e/linux.
package e2e_test
