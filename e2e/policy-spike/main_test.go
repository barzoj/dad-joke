//go:build spike && windows

package policyspike

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	// Flush after every test. A TestZZZ_* helper is unreliable because Go
	// discovers tests in _test.go filename order, so main_test.go runs before
	// rollback_test.go / session_test.go.
	if err := writeFindingsArtifacts(); err != nil {
		fmt.Fprintf(os.Stderr, "write findings: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
