//go:build spike && windows

package policyspike

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func powershell(t testing.TB, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustPS(t testing.TB, script string) string {
	t.Helper()
	out, err := powershell(t, script)
	if err != nil {
		t.Fatalf("powershell failed: %v\n%s", err, out)
	}
	return out
}

func psOK(t testing.TB, script string) bool {
	t.Helper()
	_, err := powershell(t, script)
	return err == nil
}

func quotePS(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func requireAdmin(t *testing.T) {
	t.Helper()
	out, err := powershell(t, `([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)`)
	if err != nil || !strings.EqualFold(strings.TrimSpace(out), "True") {
		t.Fatalf("spike tests require Administrator; got %q err=%v", out, err)
	}
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func fmtErr(out string, err error) string {
	if out == "" {
		return fmt.Sprintf("%v", err)
	}
	return fmt.Sprintf("%v: %s", err, out)
}
