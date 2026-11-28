//go:build e2e && windows

package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestT21_SessionAgentLaunchAndSupervision covers Issue #44 acceptance:
// launch on interactive session, kill→restart + session.restarted, service
// restart uniqueness, service stop cleanup, and no secrets for the session
// agent. Logoff/sleep/FUS automation is covered by unit tests (stated in the PR).
func TestT21_SessionAgentLaunchAndSupervision(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockFactsAPI(t)
	token := "e2e-session-token-" + randomSuffix(8)

	pinNoSelfUpdate(t)
	out, err := runAdmin(t, bin,
		"install",
		"--token", token,
		"--backend-url", mock.URL(),
		"--heartbeat-interval", "2",
		"--sample-interval", "15",
	)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	waitServiceState(t, "Running", 60*time.Second)

	requireInteractiveSession(t)

	pid := waitSessionAgent(t, 45*time.Second)
	cmdLine := sessionAgentCommandLine(t, pid)
	if strings.Contains(strings.ToLower(cmdLine), "--token") {
		t.Fatalf("session agent command line contains --token: %s", cmdLine)
	}
	if strings.Contains(cmdLine, mock.URL()) || strings.Contains(strings.ToLower(cmdLine), "http") {
		t.Fatalf("session agent command line contains URL: %s", cmdLine)
	}
	if !strings.Contains(strings.ToLower(cmdLine), "session") {
		t.Fatalf("command line missing session: %s", cmdLine)
	}

	owner, err := processOwnerName(uint32(pid))
	if err != nil {
		t.Fatalf("process owner: %v", err)
	}
	if isSystemAccount(owner) {
		t.Fatalf("session agent owner=%q; want interactive user, not SYSTEM", owner)
	}

	hasSecret, hits, err := processEnvHasDadJokePrefix(uint32(pid))
	if err != nil {
		t.Fatalf("read session agent env: %v", err)
	}
	if hasSecret {
		t.Fatalf("session agent env has DAD_JOKE_* vars: %v", hits)
	}

	// Kill → relaunch with new PID; mock should see session.restarted.
	mock.clearHits()
	mustPowershell(t, fmt.Sprintf(`Stop-Process -Id %d -Force`, pid))
	newPID := waitSessionAgentNot(t, pid, 45*time.Second)
	if newPID == pid {
		t.Fatal("session agent PID unchanged after kill")
	}
	waitForSessionRestartedFact(t, mock, 45*time.Second)

	// Restart-Service → exactly one session agent.
	mustPowershell(t, fmt.Sprintf(`Restart-Service -Name '%s' -Force`, serviceName))
	waitServiceState(t, "Running", 60*time.Second)
	waitSessionAgent(t, 45*time.Second)
	n := countSessionAgents(t)
	if n != 1 {
		t.Fatalf("after Restart-Service: session agents=%d want 1", n)
	}

	// Stop service → no session agents remain; Stopped within budget.
	mustPowershell(t, fmt.Sprintf(`Stop-Service -Name '%s' -Force`, serviceName))
	waitServiceState(t, "Stopped", 60*time.Second)
	deadline := time.Now().Add(15 * time.Second)
	for countSessionAgents(t) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("session agents remain after service stop: %d", countSessionAgents(t))
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Secrets stay unreadable as Standard User (session agent runs as user).
	user, pass := createStandardUser(t, "djsess")
	assertDenied := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected access denied", name)
		}
		if !isAccessDeniedErr(err) {
			t.Fatalf("%s: expected access denied, got %v", name, err)
		}
	}
	assertDenied("read config", attemptReadFileAs(user, pass, configPath))
	factsDir := filepath.Join(dataDir, "facts")
	assertDenied("read facts state", attemptReadFileAs(user, pass, filepath.Join(factsDir, "state.json")))
}

// requireInteractiveSession fails closed on Windows CI: missing interactive
// session fails the test. Opt out only with DAD_JOKE_E2E_ALLOW_NO_SESSION=1.
func requireInteractiveSession(t *testing.T) {
	t.Helper()
	ok, detail := detectInteractiveSession(t)
	if ok {
		return
	}
	if os.Getenv("DAD_JOKE_E2E_ALLOW_NO_SESSION") == "1" {
		t.Skipf("no interactive user session (%s); DAD_JOKE_E2E_ALLOW_NO_SESSION=1", detail)
	}
	t.Fatalf("no interactive user session detected (%s); set DAD_JOKE_E2E_ALLOW_NO_SESSION=1 to skip on headless hosts", detail)
}

func detectInteractiveSession(t *testing.T) (ok bool, detail string) {
	t.Helper()
	out, err := powershell(t, `
$ErrorActionPreference = 'Stop'
try {
  $sessions = @(quser 2>&1)
  if (-not $sessions -or $sessions.Count -lt 2) {
    Write-Output 'no:quser-empty'
    exit 0
  }
  Write-Output 'yes'
} catch {
  Write-Output ("no:" + $_.Exception.Message)
  exit 0
}
`)
	if err != nil {
		return false, "powershell error: " + err.Error()
	}
	out = strings.TrimSpace(out)
	if out == "yes" {
		return true, "ok"
	}
	if out == "" {
		return false, "empty detector output"
	}
	return false, out
}

func waitSessionAgent(t *testing.T, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		pids := sessionAgentPIDs(t)
		if len(pids) >= 1 {
			return pids[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for dad-joke.exe session")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func waitSessionAgentNot(t *testing.T, oldPID int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, pid := range sessionAgentPIDs(t) {
			if pid != oldPID {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for relaunched session agent (old=%d)", oldPID)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func countSessionAgents(t *testing.T) int {
	t.Helper()
	return len(sessionAgentPIDs(t))
}

func sessionAgentPIDs(t *testing.T) []int {
	t.Helper()
	out := mustPowershell(t, `
$ErrorActionPreference = 'SilentlyContinue'
$pids = @()
Get-CimInstance Win32_Process -Filter "Name='dad-joke.exe'" | ForEach-Object {
  $cl = $_.CommandLine
  if ($null -ne $cl -and $cl -match '(?i)\bsession\b' -and $cl -notmatch '(?i)\brun\b') {
    $pids += $_.ProcessId
  }
}
if ($pids.Count -eq 0) { Write-Output '' } else { $pids -join ',' }
`)
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	var pids []int
	for _, p := range strings.Split(out, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var id int
		if _, err := fmt.Sscanf(p, "%d", &id); err == nil && id > 0 {
			pids = append(pids, id)
		}
	}
	return pids
}

func sessionAgentCommandLine(t *testing.T, pid int) string {
	t.Helper()
	return mustPowershell(t, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$p = Get-CimInstance Win32_Process -Filter "ProcessId=%d"
if (-not $p) { throw 'process gone' }
Write-Output $p.CommandLine
`, pid))
}

func waitForSessionRestartedFact(t *testing.T, mock *mockFactsAPI, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, h := range mock.Hits() {
			if strings.Contains(h.Body, `"kind":"session.restarted"`) ||
				strings.Contains(h.Body, `"kind": "session.restarted"`) {
				return
			}
		}
		if factOnDiskHasRestarted(t) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for session.restarted fact; hits=%d", len(mock.Hits()))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func factOnDiskHasRestarted(t *testing.T) bool {
	t.Helper()
	dir := filepath.Join(dataDir, "facts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "state.json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), "session.restarted") {
			return true
		}
		var body struct {
			Facts []struct {
				Kind string `json:"kind"`
			} `json:"facts"`
		}
		if json.Unmarshal(raw, &body) == nil {
			for _, f := range body.Facts {
				if f.Kind == "session.restarted" {
					return true
				}
			}
		}
	}
	return false
}
