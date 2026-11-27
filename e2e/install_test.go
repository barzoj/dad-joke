//go:build e2e && windows

package e2e_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	serviceName      = "DadJoke"
	firewallRuleName = "DadJoke"
	installDir       = `C:\Program Files\DadJoke`
	installExe       = `C:\Program Files\DadJoke\dad-joke.exe`
	dataDir          = `C:\ProgramData\DadJoke`
	configPath       = `C:\ProgramData\DadJoke\config.json`
)

func testBinary(t *testing.T) string {
	t.Helper()
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
	return bin
}

func runAdmin(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func powershell(t *testing.T, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustPowershell(t *testing.T, script string) string {
	t.Helper()
	out, err := powershell(t, script)
	if err != nil {
		t.Fatalf("powershell failed: %v\n%s\n%s", err, script, out)
	}
	return out
}

func cleanupInstall(t *testing.T, bin string) {
	t.Helper()
	_, _ = runAdmin(t, bin, "uninstall")
	_, _ = powershell(t, fmt.Sprintf(`
$ErrorActionPreference = 'SilentlyContinue'
Stop-Service -Name '%s' -Force
sc.exe delete '%s' | Out-Null
netsh advfirewall firewall delete rule name='%s' | Out-Null
Remove-Item -LiteralPath '%s' -Recurse -Force
Remove-Item -LiteralPath '%s' -Recurse -Force
Remove-Item -LiteralPath '%s.old' -Force
`, serviceName, serviceName, firewallRuleName, installDir, dataDir, installExe))
}

func waitPathAbsent(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("path still present after %s: %s (%v)", timeout, path, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestTC01_InstallAsAdmin covers Issue #2 TC-01.
func TestTC01_InstallAsAdmin(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	out, err := runAdmin(t, bin, "install")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}

	status := mustPowershell(t, fmt.Sprintf(`
$s = Get-CimInstance Win32_Service -Filter "Name='%s'"
if (-not $s) { throw 'service missing' }
Write-Output ("State=" + $s.State)
Write-Output ("StartName=" + $s.StartName)
Write-Output ("StartMode=" + $s.StartMode)
`, serviceName))
	if !strings.Contains(status, "State=Running") {
		t.Fatalf("service not Running:\n%s", status)
	}
	if !strings.Contains(strings.ToLower(status), "localsystem") {
		t.Fatalf("expected LocalSystem account:\n%s", status)
	}

	delayed := mustPowershell(t, fmt.Sprintf(`
$p = 'HKLM:\SYSTEM\CurrentControlSet\Services\%s'
$v = (Get-ItemProperty -Path $p -Name DelayedAutostart -ErrorAction SilentlyContinue).DelayedAutostart
if ($null -eq $v) { $v = 0 }
Write-Output $v
`, serviceName))
	if delayed != "1" {
		t.Fatalf("DelayedAutostart = %q, want 1", delayed)
	}

	recovery := mustPowershell(t, fmt.Sprintf(`sc.exe qfailure %s`, serviceName))
	if !strings.Contains(strings.ToLower(recovery), "restart") {
		t.Fatalf("expected restart recovery actions:\n%s", recovery)
	}

	fw := mustPowershell(t, fmt.Sprintf(`
$r = Get-NetFirewallRule -Name '%s' -ErrorAction SilentlyContinue
if (-not $r) { $r = Get-NetFirewallRule -DisplayName '%s' -ErrorAction SilentlyContinue }
if (-not $r) { throw 'firewall rule missing' }
$app = Get-NetFirewallApplicationFilter -AssociatedNetFirewallRule $r
$dir = ($r | Select-Object -First 1).Direction
$act = ($r | Select-Object -First 1).Action
Write-Output ("Direction=" + $dir)
Write-Output ("Action=" + $act)
Write-Output ("Program=" + $app.Program)
`, firewallRuleName, firewallRuleName))
	if !strings.Contains(fw, "Direction=Outbound") {
		t.Fatalf("firewall not outbound:\n%s", fw)
	}
	if !strings.Contains(fw, "Action=Allow") {
		t.Fatalf("firewall not allow:\n%s", fw)
	}
	if !strings.Contains(strings.ToLower(fw), strings.ToLower(installExe)) {
		t.Fatalf("firewall program mismatch:\n%s", fw)
	}

	for _, p := range []string{installExe, configPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}

	// ACL checks via Win32 (avoid powershell.exe Get-Acl / Security module issues on GHA).
	assertStrictACL(t, dataDir)
	assertStrictACL(t, configPath)
	assertStrictACL(t, installDir)
	assertStrictACL(t, installExe)

	out, err = runAdmin(t, bin, "uninstall")
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}

	exists := mustPowershell(t, fmt.Sprintf(`
$s = Get-Service -Name '%s' -ErrorAction SilentlyContinue
if ($s) { 'present' } else { 'absent' }
`, serviceName))
	if exists != "absent" {
		t.Fatalf("service still present after uninstall")
	}
	fwAfter, _ := powershell(t, fmt.Sprintf(`
$r = Get-NetFirewallRule -Name '%s' -ErrorAction SilentlyContinue
if (-not $r) { $r = Get-NetFirewallRule -DisplayName '%s' -ErrorAction SilentlyContinue }
if ($r) { 'present' } else { 'absent' }
`, firewallRuleName, firewallRuleName))
	if fwAfter != "absent" {
		t.Fatalf("firewall rule still present: %q", fwAfter)
	}

	waitPathAbsent(t, installDir, 30*time.Second)
	waitPathAbsent(t, dataDir, 30*time.Second)
}

// TestTC02_StandardUserCannotTamper covers Issue #2 TC-02.
func TestTC02_StandardUserCannotTamper(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)

	user := "djtest_" + randomSuffix(4)
	pass := "Dj!" + randomSuffix(8) + "aA1"

	// net user /add creates a Standard User (Users group only). Do not fail if
	// removing from Administrators returns error 1377 (not a member).
	createOut, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
net user %s %s /add | Out-Null
$ErrorActionPreference = 'Continue'
net localgroup Administrators %s /delete 2>&1 | Out-Null
# Confirm not in Administrators
$admins = net localgroup Administrators
if ($admins -match ('(?i)^\s*' + [regex]::Escape('%s') + '\s*$')) {
  throw 'user unexpectedly in Administrators'
}
Write-Output 'ok'
`, user, pass, user, user))
	if err != nil {
		t.Fatalf("create user: %v\n%s", err, createOut)
	}
	t.Cleanup(func() {
		_, _ = powershell(t, fmt.Sprintf(`$ErrorActionPreference='SilentlyContinue'; net user %s /delete`, user))
		cleanupInstall(t, bin)
	})

	out, err := runAdmin(t, bin, "install")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}

	assertDenied := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected access denied, got success", name)
		}
		if !isAccessDeniedErr(err) {
			t.Fatalf("%s: expected access denied, got %v", name, err)
		}
	}

	assertDenied("stop service", attemptStopServiceAs(user, pass, serviceName))
	assertDenied("read config", attemptReadFileAs(user, pass, configPath))
	assertDenied("modify config", attemptWriteFileAs(user, pass, configPath))
	assertDenied("delete config", attemptDeleteFileAs(user, pass, configPath))
	assertDenied("overwrite exe", attemptWriteFileAs(user, pass, installExe))
	assertDenied("delete exe", attemptDeleteFileAs(user, pass, installExe))

	state := mustPowershell(t, fmt.Sprintf(`(Get-Service -Name '%s').Status`, serviceName))
	if state != "Running" {
		t.Fatalf("service status = %q, want Running", state)
	}

	out, err = runAdmin(t, bin, "uninstall")
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
}

func randomSuffix(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

var (
	uuidV4Re = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	hex64Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type agentConfig struct {
	SchemaVersion       int    `json:"schema_version"`
	ReleaseAPIURL       string `json:"release_api_url"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	MachineID           string `json:"machine_id"`
	Token               string `json:"token"`
}

func readAgentConfig(t *testing.T) agentConfig {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg agentConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config: %v\n%s", err, raw)
	}
	return cfg
}

func assertValidIdentity(t *testing.T, cfg agentConfig) {
	t.Helper()
	if !uuidV4Re.MatchString(cfg.MachineID) {
		t.Fatalf("machine_id not UUIDv4: %q", cfg.MachineID)
	}
	if !hex64Re.MatchString(cfg.Token) {
		t.Fatalf("token not 64-char hex: %q", cfg.Token)
	}
}

// TestTC06_MachineIdentityPersistence covers Issue #8.
func TestTC06_MachineIdentityPersistence(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	out, err := runAdmin(t, bin, "install")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "machine_id=") || !strings.Contains(out, "token=") {
		t.Fatalf("install stdout missing identity lines:\n%s", out)
	}
	if strings.Contains(out, "token=(preserved)") || strings.Contains(out, "token=(set from --token)") {
		t.Fatalf("fresh install should print generated token once:\n%s", out)
	}

	cfg1 := readAgentConfig(t)
	assertValidIdentity(t, cfg1)
	assertStrictACL(t, configPath)

	out2, err := runAdmin(t, bin, "install")
	if err != nil {
		t.Fatalf("reinstall: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "machine_id="+cfg1.MachineID) {
		t.Fatalf("reinstall stdout missing preserved machine_id:\n%s", out2)
	}
	if !strings.Contains(out2, "token=(preserved)") {
		t.Fatalf("reinstall should note preserved token:\n%s", out2)
	}
	cfg2 := readAgentConfig(t)
	if cfg2.MachineID != cfg1.MachineID || cfg2.Token != cfg1.Token {
		t.Fatalf("identity rotated on reinstall: %#v -> %#v", cfg1, cfg2)
	}

	newToken := "e2e-explicit-token-" + randomSuffix(8)
	out3, err := runAdmin(t, bin, "install", "--token", newToken)
	if err != nil {
		t.Fatalf("reinstall with --token: %v\n%s", err, out3)
	}
	cfg3 := readAgentConfig(t)
	if cfg3.MachineID != cfg1.MachineID {
		t.Fatalf("machine_id changed after --token reinstall: %q -> %q", cfg1.MachineID, cfg3.MachineID)
	}
	if cfg3.Token != newToken {
		t.Fatalf("token not updated: got %q want %q", cfg3.Token, newToken)
	}
	assertStrictACL(t, configPath)
}
