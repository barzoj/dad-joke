//go:build e2e

package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersionCommand (AC-01) runs the built binary's `version` subcommand and
// asserts the printed Version matches DAD_JOKE_EXPECT_VERSION (the -ldflags
// value used for that build). Later issues extend this package without
// rewriting the CI workflow.
func TestVersionCommand(t *testing.T) {
	bin := os.Getenv("DAD_JOKE_BIN")
	if bin == "" {
		t.Fatal("DAD_JOKE_BIN is required (path to dad-joke.exe)")
	}
	bin, err := filepath.Abs(bin)
	if err != nil {
		t.Fatalf("abs bin: %v", err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary not found at %s: %v", bin, err)
	}

	wantVersion := os.Getenv("DAD_JOKE_EXPECT_VERSION")
	if wantVersion == "" {
		wantVersion = "0.0.1-dev"
	}
	wantCommit := os.Getenv("DAD_JOKE_EXPECT_COMMIT")
	if wantCommit == "" {
		wantCommit = "unknown"
	}

	cmd := exec.Command(bin, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\noutput: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := "dad-joke " + wantVersion + " (commit " + wantCommit + ")"
	if got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

// TestVersionExitZero ensures the process exits 0 on success.
func TestVersionExitZero(t *testing.T) {
	bin := os.Getenv("DAD_JOKE_BIN")
	if bin == "" {
		t.Skip("DAD_JOKE_BIN not set")
	}
	cmd := exec.Command(bin, "version")
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected exit 0, got %v", err)
	}
}
