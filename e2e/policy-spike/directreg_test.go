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

	// Prefer Software\Policies\... (ADMX-style). Classic
	// HKLM\Software\Microsoft\Windows\CurrentVersion\Policies\System can be
	// ACL-denied for New-Item even for Administrators on Server 2025 images.
	sysClassic := `HKLM:\Software\Microsoft\Windows\CurrentVersion\Policies\System`
	expClassic := `HKLM:\Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`
	sysPolicies := `HKLM:\Software\Policies\Microsoft\Windows\System`
	expPolicies := `HKLM:\Software\Policies\Microsoft\Windows\Explorer`

	t.Cleanup(func() {
		deleteRegValue(t, sysClassic, "DisableTaskMgr")
		deleteRegValue(t, expClassic, "NoControlPanel")
		deleteRegValue(t, expClassic, "DisallowRun")
		deleteRegKey(t, expClassic+`\DisallowRun`)
		deleteRegValue(t, sysPolicies, "DisableTaskMgr")
		deleteRegValue(t, expPolicies, "NoControlPanel")
		deleteRegKey(t, `HKLM:\Software\Policies\DadJokeSpike`)
	})

	start := time.Now()
	classicOK := true
	if out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
if (-not (Test-Path -LiteralPath %s)) { New-Item -Path %s -Force | Out-Null }
New-ItemProperty -Path %s -Name 'DisableTaskMgr' -PropertyType DWord -Value 1 -Force | Out-Null
`, quotePS(sysClassic), quotePS(sysClassic), quotePS(sysClassic))); err != nil {
		classicOK = false
		t.Logf("classic HKLM Policies\\System write denied (expected on some Server images): %v\n%s", err, out)
	} else {
		setRegDWORD(t, expClassic, "NoControlPanel", 1)
		setRegDWORD(t, expClassic, "DisallowRun", 1)
		mustPS(t, fmt.Sprintf(`
New-Item -Path %s -Force | Out-Null
New-ItemProperty -Path %s -Name '1' -PropertyType String -Value 'notepad.exe' -Force | Out-Null
`, quotePS(expClassic+`\DisallowRun`), quotePS(expClassic+`\DisallowRun`)))
	}

	// Always also write Software\Policies\... and the user hive (Home path).
	setRegDWORD(t, sysPolicies, "DisableTaskMgr", 1)
	setRegDWORD(t, expPolicies, "NoControlPanel", 1)
	setRegDWORD(t, `HKLM:\Software\Policies\DadJokeSpike`, "DirectMarker", 1)
	lat := time.Since(start)

	dOK, dVal, _ := regDWORDExists(t, sysPolicies, "DisableTaskMgr")
	markerOK, _, _ := regDWORDExists(t, `HKLM:\Software\Policies\DadJokeSpike`, "DirectMarker")

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

	started, detail := tryLaunchAsUser(t, u, `C:\Windows\System32\notepad.exe`)
	taskStarted, taskDetail := tryLaunchAsUser(t, u, `C:\Windows\System32\taskmgr.exe`)

	recordFinding(Finding{
		Mechanism:       "Direct registry (Home path emulation)",
		Variant:         "DisableTaskMgr+NoControlPanel+DisallowRun without .pol",
		Works:           boolIf(dOK && markerOK && uDisable, "yes (keys present)", "partial"),
		Latency:         formatLatency(lat),
		NeedsRelogon:    "HKCU writes need loaded hive / next logon to affect interactive session",
		NeedsReboot:     "no",
		VisibleGpresult: "no (not GP-managed)",
		Gotchas: fmt.Sprintf("classic HKLM Policies\\System writable=%v; Software\\Policies DisableTaskMgr=%v(%d) marker=%v; user-hive DisableTaskMgr=%v(%d); notepad started=%v (%s); taskmgr started=%v (%s). DisallowRun is Explorer-enforced (CreateProcess often bypasses). No gpsvc required for raw keys.",
			classicOK, dOK, dVal, markerOK, uDisable, uVal, started, detail, taskStarted, taskDetail),
		ClientSKUNote: "Primary Home path: write Policies keys directly; classic HKLM\\...\\Policies\\System may be ACL-locked on Server — prefer Software\\Policies and HKCU hive",
	})

	if !(dOK && markerOK) {
		t.Fatalf("direct Software\\Policies keys missing")
	}
	if !uDisable || uVal != 1 {
		t.Fatalf("direct HKCU hive DisableTaskMgr missing")
	}
}
