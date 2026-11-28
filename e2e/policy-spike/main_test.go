//go:build spike && windows

package policyspike

import "testing"

func TestZZZ_WriteFindings(t *testing.T) {
	// Runs last alphabetically to flush recorded findings for the CI artifact.
	writeFindingsArtifacts(t)
}
