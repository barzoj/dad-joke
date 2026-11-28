//go:build spike && windows

package policyspike

import (
	"fmt"
	"testing"
	"time"
)

func TestDirectRegistry_HomePathEmulation(t *testing.T) {
	requireAdmin(t)
	u := createStandardUser(t)

	// Same three policies written straight to Policies keys without .pol / gpsvc.
	sysPath := `HKLM:\Software\Microsoft\Windows\CurrentVersion\Policies\System`
	expPath := `HKLM:\Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`
	disallowPath := `HKLM:\Software\Microsoft\Windows\CurrentVersion\Policies\Explorer\DisallowRun`

	t.Cleanup(func() {
		deleteRegValue(t, sysPath, "DisableTaskMgr")
		deleteRegValue(t, expPath, "NoControlPanel")
		deleteRegValue(t, expPath, "DisallowRun")
		deleteRegKey(t, disallowPath)
	})

	start := time.Now()
	setRegDWORD(t, sysPath, "DisableTaskMgr", 1)
	setRegDWORD(t, expPath, "NoControlPanel", 1)
	setRegDWORD(t, expPath, "DisallowRun", 1)
	mustPS(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
New-Item -Path %s -Force | Out-Null
New-ItemProperty -Path %s -Name '1' -PropertyType String -Value 'notepad.exe' -Force | Out-Null
`, quotePS(disallowPath), quotePS(disallowPath)))
	lat := time.Since(start)

	dOK, dVal, _ := regDWORDExists(t, sysPath, "DisableTaskMgr")
	nOK, _, _ := regDWORDExists(t, expPath, "NoControlPanel")
	rOK, _, _ := regDWORDExists(t, expPath, "DisallowRun")

	// Machine-level classic Policies keys affect all users reading HKCU-equivalent
	// machine overrides for some policies; DisableTaskMgr is typically per-user (HKCU).
	// Writing HKLM\...\Policies\System\DisableTaskMgr is a known machine-wide override path.
	started, detail := tryLaunchAsUser(t, u, `C:\Windows\System32\notepad.exe`)
	taskStarted, taskDetail := tryLaunchAsUser(t, u, `C:\Windows\System32\taskmgr.exe`)

	// Also write into the user's hive directly (true Home emulation for user scope).
	mount, unload := loadUserHive(t, u)
	userSys := `Registry::` + mount + `\Software\Microsoft\Windows\CurrentVersion\Policies\System`
	userExp := `Registry::` + mount + `\Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`
	setRegDWORD(t, userSys, "DisableTaskMgr", 1)
	setRegDWORD(t, userExp, "NoControlPanel", 1)
	setRegDWORD(t, userExp, "DisallowRun", 1)
	mustPS(t, fmt.Sprintf(`
New-Item -Path %s -Force | Out-Null
New-ItemProperty -Path %s -Name '1' -PropertyType String -Value 'notepad.exe' -Force | Out-Null
`, quotePS(userExp+`\DisallowRun`), quotePS(userExp+`\DisallowRun`)))
	uDisable, uVal, _ := regDWORDExists(t, userSys, "DisableTaskMgr")
	unload()

	recordFinding(Finding{
		Mechanism:       "Direct registry (Home path emulation)",
		Variant:         "DisableTaskMgr+NoControlPanel+DisallowRun without .pol",
		Works:           boolIf(dOK && nOK && rOK && uDisable, "yes (keys present)", "partial"),
		Latency:         formatLatency(lat),
		NeedsRelogon:    "HKCU writes need loaded hive / next logon to affect interactive session",
		NeedsReboot:     "no",
		VisibleGpresult: "no (not GP-managed)",
		Gotchas: fmt.Sprintf("HKLM keys present DisableTaskMgr=%v(%d) NoControlPanel=%v DisallowRun=%v; user-hive DisableTaskMgr=%v(%d); notepad started=%v (%s); taskmgr started=%v (%s). DisallowRun is Explorer-enforced (CreateProcess often bypasses). No gpsvc / no CSE refresh required for raw keys.",
			dOK, dVal, nOK, rOK, uDisable, uVal, started, detail, taskStarted, taskDetail),
		ClientSKUNote: "Primary Home path: write Policies keys directly because local GPO UI/tools may be absent; enforcement still depends on each component reading the key",
	})

	if !(dOK && nOK && rOK) {
		t.Fatalf("direct HKLM policy keys missing")
	}
	if !uDisable || uVal != 1 {
		t.Fatalf("direct HKCU hive DisableTaskMgr missing")
	}
}
