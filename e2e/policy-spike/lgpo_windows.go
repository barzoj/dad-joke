//go:build spike && windows

package policyspike

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	localGPORoot = `C:\Windows\System32\GroupPolicy`
	machinePol   = `C:\Windows\System32\GroupPolicy\Machine\Registry.pol`
	userPol      = `C:\Windows\System32\GroupPolicy\User\Registry.pol`
	gptINI       = `C:\Windows\System32\GroupPolicy\GPT.INI`

	// Registry CSE + Administrative Templates tool extension GUIDs.
	cseRegistry = `{35378EAC-683F-11D2-A89A-00C04FBBCFA2}`
	toolMachine = `{D02B1F72-3407-48AE-BA88-E94639B752F0}`
	toolUser    = `{D02B1F73-3407-48AE-BA88-E94639B752F0}`
	// Firewall CSE (MS-GPFAS); tool GUID matches CSE in common LGPO dumps.
	cseFirewall = `{0E28E233-B7C3-45e5-A7AB-5F34E1028E7C}`
	toolFW      = `{0E28E233-B7C3-45e5-A7AB-5F34E1028E7C}`
	// AppLocker / SRP CSE + AppLocker MMC administrative tool extension.
	cseAppLocker  = `{3AAE67FC-DAA8-4EE3-B85F-8161A808D5C3}`
	toolAppLocker = `{B1BE8D78-6D31-4968-90A1-6C8B6D1C2F9D}`
)

// csePair is one [{CSE}{Tool}] registration for GPT.INI extension lists.
type csePair struct {
	cse  string
	tool string
}

type gptState struct {
	Raw             string
	MachineVersion  int
	UserVersion     int
	MachineExtNames string
	UserExtNames    string
}

func readGPT(t testing.TB) gptState {
	t.Helper()
	b, err := os.ReadFile(gptINI)
	if err != nil {
		if os.IsNotExist(err) {
			return gptState{Raw: ""}
		}
		t.Fatalf("read GPT.INI: %v", err)
	}
	s := string(b)
	st := gptState{Raw: s}
	if m := regexp.MustCompile(`(?i)Version\s*=\s*(\d+)`).FindStringSubmatch(s); len(m) == 2 {
		v, _ := strconv.Atoi(m[1])
		// High word = machine, low word = user (common LGPO convention).
		st.MachineVersion = (v >> 16) & 0xffff
		st.UserVersion = v & 0xffff
	}
	if m := regexp.MustCompile(`(?i)gPCMachineExtensionNames\s*=\s*(.+)`).FindStringSubmatch(s); len(m) == 2 {
		st.MachineExtNames = strings.TrimSpace(m[1])
	}
	if m := regexp.MustCompile(`(?i)gPCUserExtensionNames\s*=\s*(.+)`).FindStringSubmatch(s); len(m) == 2 {
		st.UserExtNames = strings.TrimSpace(m[1])
	}
	return st
}

func writeGPT(t testing.TB, machineVer, userVer int, machineExt, userExt string) {
	t.Helper()
	if err := os.MkdirAll(localGPORoot, 0o755); err != nil {
		t.Fatalf("mkdir GPO: %v", err)
	}
	version := (machineVer << 16) | (userVer & 0xffff)
	body := fmt.Sprintf("[General]\r\nVersion=%d\r\ngPCMachineExtensionNames=%s\r\ngPCUserExtensionNames=%s\r\n",
		version, machineExt, userExt)
	if err := os.WriteFile(gptINI, []byte(body), 0o644); err != nil {
		t.Fatalf("write GPT.INI: %v", err)
	}
}

func ensureExtPair(list, cse, tool string) string {
	pair := "[" + strings.ToUpper(cse) + strings.ToUpper(tool) + "]"
	up := strings.ToUpper(list)
	if strings.Contains(up, strings.ToUpper(cse)) {
		return list
	}
	return list + pair
}

func bumpGPT(t testing.TB, bumpMachine, bumpUser bool, machineExtras, userExtras []csePair) {
	t.Helper()
	st := readGPT(t)
	mv, uv := st.MachineVersion, st.UserVersion
	if bumpMachine {
		mv++
	}
	if bumpUser {
		uv++
	}
	if mv == 0 {
		mv = 1
	}
	if uv == 0 {
		uv = 1
	}
	mExt := st.MachineExtNames
	uExt := st.UserExtNames
	mExt = ensureExtPair(mExt, cseRegistry, toolMachine)
	uExt = ensureExtPair(uExt, cseRegistry, toolUser)
	for _, p := range machineExtras {
		mExt = ensureExtPair(mExt, p.cse, p.tool)
	}
	for _, p := range userExtras {
		uExt = ensureExtPair(uExt, p.cse, p.tool)
	}
	writeGPT(t, mv, uv, mExt, uExt)
}

type lgpoBackup struct {
	gpt        []byte
	machinePol []byte
	userPol    []byte
	hadGPT     bool
	hadMach    bool
	hadUser    bool
}

func backupLocalGPO(t testing.TB) *lgpoBackup {
	t.Helper()
	b := &lgpoBackup{}
	if raw, err := os.ReadFile(gptINI); err == nil {
		b.gpt = raw
		b.hadGPT = true
	}
	if raw, err := os.ReadFile(machinePol); err == nil {
		b.machinePol = raw
		b.hadMach = true
	}
	if raw, err := os.ReadFile(userPol); err == nil {
		b.userPol = raw
		b.hadUser = true
	}
	t.Cleanup(func() { restoreLocalGPO(t, b) })
	return b
}

func restoreLocalGPO(t testing.TB, b *lgpoBackup) {
	if b == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(machinePol), 0o755)
	_ = os.MkdirAll(filepath.Dir(userPol), 0o755)
	if b.hadGPT {
		_ = os.WriteFile(gptINI, b.gpt, 0o644)
	} else {
		_ = os.Remove(gptINI)
	}
	if b.hadMach {
		_ = os.WriteFile(machinePol, b.machinePol, 0o644)
	} else {
		_ = os.Remove(machinePol)
	}
	if b.hadUser {
		_ = os.WriteFile(userPol, b.userPol, 0o644)
	} else {
		_ = os.Remove(userPol)
	}
	_, _ = runCmd("gpupdate.exe", "/force")
}

func refreshPolicy(t testing.TB) (latency time.Duration, via string, err error) {
	t.Helper()
	return refreshPolicyOpts(t, false)
}

func refreshPolicyOpts(t testing.TB, clearHistory bool) (latency time.Duration, via string, err error) {
	t.Helper()
	if clearHistory {
		// Only when callers intentionally force re-apply; clearing History bypasses
		// the GPT.INI version check and would invalidate the no-bump assertion.
		_, _ = powershell(t, `
$ErrorActionPreference='SilentlyContinue'
Remove-Item -Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Group Policy\History\*' -Recurse -Force
`)
	}
	start := time.Now()
	out, err := runCmd("gpupdate.exe", "/Target:Computer", "/Force")
	out2, err2 := runCmd("gpupdate.exe", "/Target:User", "/Force")
	lat := time.Since(start)
	if err != nil && err2 != nil {
		return lat, "gpupdate", fmt.Errorf("gpupdate computer: %s; user: %s", fmtErr(out, err), fmtErr(out2, err2))
	}
	_, _ = powershell(t, `
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public class NativeGP {
  [DllImport("userenv.dll", SetLastError=true)]
  public static extern bool RefreshPolicyEx(bool bMachine, uint dwOptions);
}
"@
[void][NativeGP]::RefreshPolicyEx($true, 0)
[void][NativeGP]::RefreshPolicyEx($false, 0)
`)
	return lat, "gpupdate+RefreshPolicyEx", nil
}

func appendUserPol(t testing.TB, entries []PolEntry) {
	t.Helper()
	var existing []PolEntry
	if raw, err := os.ReadFile(userPol); err == nil {
		existing, _ = ParsePol(raw)
	}
	if err := writePolFile(userPol, mergePol(existing, entries)); err != nil {
		t.Fatalf("write user Registry.pol: %v", err)
	}
}

func appendMachinePol(t testing.TB, entries []PolEntry) {
	t.Helper()
	var existing []PolEntry
	if raw, err := os.ReadFile(machinePol); err == nil {
		existing, _ = ParsePol(raw)
	}
	if err := writePolFile(machinePol, mergePol(existing, entries)); err != nil {
		t.Fatalf("write machine Registry.pol: %v", err)
	}
}

func userLGPORoot(sid string) string {
	return filepath.Join(`C:\Windows\System32\GroupPolicyUsers`, sid)
}

func writePerUserPol(t testing.TB, sid string, entries []PolEntry) {
	t.Helper()
	root := userLGPORoot(sid)
	pol := filepath.Join(root, "User", "Registry.pol")
	ini := filepath.Join(root, "GPT.INI")
	if err := writePolFile(pol, entries); err != nil {
		t.Fatalf("per-user pol: %v", err)
	}
	body := fmt.Sprintf("[General]\r\nVersion=%d\r\ngPCUserExtensionNames=[%s%s]\r\n",
		1, strings.ToUpper(cseRegistry), strings.ToUpper(toolUser))
	if err := os.WriteFile(ini, []byte(body), 0o644); err != nil {
		t.Fatalf("per-user GPT.INI: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
	})
}

func regDWORDExists(t testing.TB, path, name string) (bool, uint32, error) {
	t.Helper()
	out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
$p = %s; $n = %s
if (-not (Test-Path -LiteralPath $p)) { Write-Output 'missing-key'; exit 0 }
$v = Get-ItemProperty -LiteralPath $p -Name $n -ErrorAction SilentlyContinue
if ($null -eq $v) { Write-Output 'missing-val'; exit 0 }
Write-Output ([string]$v.$n)
`, quotePS(path), quotePS(name)))
	if err != nil {
		return false, 0, err
	}
	switch strings.TrimSpace(out) {
	case "missing-key", "missing-val", "":
		return false, 0, nil
	default:
		n, e := strconv.ParseUint(strings.TrimSpace(out), 10, 32)
		if e != nil {
			return true, 0, nil
		}
		return true, uint32(n), nil
	}
}

func setRegDWORD(t testing.TB, path, name string, val uint32) {
	t.Helper()
	mustPS(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
if (-not (Test-Path -LiteralPath %s)) {
  $null = New-Item -Path %s -Force
}
$null = New-ItemProperty -Path %s -Name %s -PropertyType DWord -Value %d -Force
`, quotePS(path), quotePS(path), quotePS(path), quotePS(name), val))
}

func deleteRegValue(t testing.TB, path, name string) {
	t.Helper()
	_, _ = powershell(t, fmt.Sprintf(`
$ErrorActionPreference='SilentlyContinue'
Remove-ItemProperty -Path %s -Name %s -Force
`, quotePS(path), quotePS(name)))
}

func deleteRegKey(t testing.TB, path string) {
	t.Helper()
	_, _ = powershell(t, fmt.Sprintf(`
$ErrorActionPreference='SilentlyContinue'
Remove-Item -Path %s -Recurse -Force
`, quotePS(path)))
}

func gpresultHas(t testing.TB, user string, needle string) bool {
	t.Helper()
	out, err := runCmd("gpresult.exe", "/R", "/USER", user)
	if err != nil {
		// gpresult can fail for never-logged-on users; try computer scope.
		out2, err2 := runCmd("gpresult.exe", "/R")
		if err2 != nil {
			t.Logf("gpresult errors: user=%v computer=%v", err, err2)
			return false
		}
		out = out2
	}
	return strings.Contains(strings.ToLower(out), strings.ToLower(needle))
}

func formatLatency(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
