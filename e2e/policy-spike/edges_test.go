//go:build spike && windows

package policyspike

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEdges_MalformedPolAndDelOps(t *testing.T) {
	requireAdmin(t)
	backupLocalGPO(t)

	// Malformed .pol: truncated after header.
	if err := os.MkdirAll(filepath.Dir(machinePol), 0o755); err != nil {
		t.Fatal(err)
	}
	malformed := append(append([]byte{}, polSignature...), 1, 0, 0, 0, 0xFF, 0xFE)
	if err := os.WriteFile(machinePol, malformed, 0o644); err != nil {
		t.Fatal(err)
	}
	bumpGPT(t, true, false, "", "")
	start := time.Now()
	_, _, refreshErr := refreshPolicy(t)
	malLat := time.Since(start)

	// Check Application event log for Group Policy / gpsvc complaints (best-effort).
	ev := mustPS(t, `
$ErrorActionPreference='SilentlyContinue'
$start = (Get-Date).AddMinutes(-10)
$events = Get-WinEvent -FilterHashtable @{LogName='System'; StartTime=$start; ProviderName='Microsoft-Windows-GroupPolicy'} -MaxEvents 20 -ErrorAction SilentlyContinue
if (-not $events) {
  $events = Get-WinEvent -FilterHashtable @{LogName='Application'; StartTime=$start} -MaxEvents 50 -ErrorAction SilentlyContinue |
    Where-Object { $_.ProviderName -match 'GroupPolicy|gpsvc|Userenv' }
}
if ($events) { ($events | Select-Object -First 3 | ForEach-Object { $_.Id.ToString() + ':' + $_.LevelDisplayName }) -join ';' } else { 'no-gp-events' }
`)

	recordFinding(Finding{
		Mechanism:       "Local GPO edge",
		Variant:         "malformed Registry.pol",
		Works:           "yes (engine did not panic; refresh completed or erred cleanly)",
		Latency:         formatLatency(malLat),
		NeedsRelogon:    "n/a",
		NeedsReboot:     "n/a",
		VisibleGpresult: "n/a",
		Gotchas:         fmt.Sprintf("refreshErr=%v event_samples=%s; gpsvc should ignore/fail closed on corrupt pol", refreshErr, ev),
		ClientSKUNote:   "Same gpsvc parsing on client SKUs",
	})

	// Restore a valid pol with **del. / **DelVals style ops.
	delEntries := []PolEntry{
		{Key: `Software\Policies\DadJokeSpike`, Value: "Temp", Type: regDWORD, Data: dwordData(1)},
	}
	if err := writePolFile(machinePol, delEntries); err != nil {
		t.Fatal(err)
	}
	bumpGPT(t, true, false, "", "")
	_, _, _ = refreshPolicy(t)
	setRegDWORD(t, `HKLM:\Software\Policies\DadJokeSpike`, "Extra", 9)

	delOps := []PolEntry{
		// Delete value instruction uses value name **del.<name> in some LGPO encodings;
		// MS-GPREG uses special value names. We exercise both spellings as data for the spike.
		{Key: `Software\Policies\DadJokeSpike`, Value: "**del.Extra", Type: regSZ, Data: szData("")},
		{Key: `Software\Policies\DadJokeSpike`, Value: "**DelVals.", Type: regSZ, Data: szData("")},
	}
	if err := writePolFile(machinePol, append(delEntries, delOps...)); err != nil {
		t.Fatal(err)
	}
	bumpGPT(t, true, false, "", "")
	d0 := time.Now()
	_, _, _ = refreshPolicy(t)
	delLat := time.Since(d0)
	extraPresent, _, _ := regDWORDExists(t, `HKLM:\Software\Policies\DadJokeSpike`, "Extra")

	recordFinding(Finding{
		Mechanism:       "Local GPO edge",
		Variant:         "**del. / **DelVals ops",
		Works:           boolIf(!extraPresent, "yes (Extra removed)", "partial (Extra still present — op may need exact GP syntax)"),
		Latency:         formatLatency(delLat),
		NeedsRelogon:    "no",
		NeedsReboot:     "no",
		VisibleGpresult: "n/a",
		Gotchas:         fmt.Sprintf("Extra present after del ops=%v; confirm MS-GPREG delete instruction syntax before relying on it in the engine", extraPresent),
		ClientSKUNote:   "",
	})

	// Duplicate entries: last writer wins vs both applied — record behavior.
	dup := []PolEntry{
		{Key: `Software\Policies\DadJokeSpike`, Value: "Dup", Type: regDWORD, Data: dwordData(1)},
		{Key: `Software\Policies\DadJokeSpike`, Value: "Dup", Type: regDWORD, Data: dwordData(2)},
	}
	if err := writePolFile(machinePol, dup); err != nil {
		t.Fatal(err)
	}
	bumpGPT(t, true, false, "", "")
	_, _, _ = refreshPolicy(t)
	_, dupVal, _ := regDWORDExists(t, `HKLM:\Software\Policies\DadJokeSpike`, "Dup")
	recordFinding(Finding{
		Mechanism:       "Local GPO edge",
		Variant:         "duplicate Registry.pol entries",
		Works:           "yes (observed)",
		Latency:         "n/a",
		NeedsRelogon:    "no",
		NeedsReboot:     "no",
		VisibleGpresult: "n/a",
		Gotchas:         fmt.Sprintf("duplicate Dup values 1 then 2 → final registry=%d (order-sensitive per MS-GPREG)", dupVal),
		ClientSKUNote:   "",
	})

	t.Cleanup(func() { deleteRegKey(t, `HKLM:\Software\Policies\DadJokeSpike`) })

	if refreshErr != nil {
		t.Logf("malformed pol refresh error (acceptable): %v", refreshErr)
	}
	if dupVal != 1 && dupVal != 2 {
		t.Fatalf("duplicate entry did not land; val=%d", dupVal)
	}
}

func TestEdges_RefreshWhileUserLoggedOn(t *testing.T) {
	requireAdmin(t)
	backupLocalGPO(t)
	u := createStandardUser(t)

	// Start a long-running process as the user (logged-on token).
	_, _, _ = runAsUserViaTask(t, u, "cmd.exe", []string{"/c", "ping -n 60 127.0.0.1 >nul"}, 5*time.Second)

	appendUserPol(t, []PolEntry{
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\System`, Value: "DisableTaskMgr", Type: regDWORD, Data: dwordData(1)},
	})
	bumpGPT(t, false, true, "", "")
	start := time.Now()
	lat, _, err := refreshPolicy(t)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = start

	recordFinding(Finding{
		Mechanism:       "Local GPO edge",
		Variant:         "refresh while test user has running processes",
		Works:           "yes (refresh completed)",
		Latency:         formatLatency(lat),
		NeedsRelogon:    "user-scope HKCU may need new logon to fully materialize depending on CSE",
		NeedsReboot:     "no",
		VisibleGpresult: "varies",
		Gotchas:         "gpupdate /force during active user processes completed; foreground apps may not re-read Policies until restart",
		ClientSKUNote:   "",
	})
}

func TestEdges_AppIDSvcStopped(t *testing.T) {
	requireAdmin(t)
	before := serviceState(t, "AppIDSvc")
	_, _ = powershell(t, `Stop-Service AppIDSvc -Force -ErrorAction SilentlyContinue`)
	stopped := serviceState(t, "AppIDSvc")
	t.Cleanup(func() {
		_, _ = powershell(t, `Set-Service AppIDSvc -StartupType Automatic -ErrorAction SilentlyContinue; Start-Service AppIDSvc -ErrorAction SilentlyContinue`)
	})

	// With AppIDSvc stopped, Set-AppLockerPolicy may still write rules but enforcement is off.
	xmlPath := filepath.Join(`C:\Windows\Temp`, "djspike_al_edge_"+randomHex(3)+".xml")
	_ = os.WriteFile(xmlPath, []byte(`<AppLockerPolicy Version="1"><RuleCollection Type="Exe" EnforcementMode="Enabled"></RuleCollection></AppLockerPolicy>`), 0o644)
	t.Cleanup(func() { _ = os.Remove(xmlPath) })
	out, err := powershell(t, fmt.Sprintf(`Set-AppLockerPolicy -XmlPolicy %s -ErrorAction Continue 2>&1 | Out-String`, quotePS(xmlPath)))

	recordFinding(Finding{
		Mechanism:       "AppLocker edge",
		Variant:         "AppIDSvc stopped",
		Works:           boolIf(strings.EqualFold(stopped, "Stopped"), "yes (service stoppable)", "partial"),
		Latency:         "n/a",
		NeedsRelogon:    "n/a",
		NeedsReboot:     "n/a",
		VisibleGpresult: "n/a",
		Gotchas:         fmt.Sprintf("before=%s stopped=%s Set-AppLockerPolicy err=%v out=%q; enforcement requires AppIDSvc Running", before, stopped, err, truncate(out, 160)),
		ClientSKUNote:   "Agent should ensure AppIDSvc is running before relying on AppLocker enforcement on SKUs that support it",
	})

	if !strings.EqualFold(stopped, "Stopped") && !strings.EqualFold(stopped, "StopPending") {
		t.Logf("could not stop AppIDSvc (state=%s); recorded as observation", stopped)
	}
}
