//go:build spike && windows

package policyspike

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalGPO_RegistryPol_UserPolicies(t *testing.T) {
	requireAdmin(t)
	backupLocalGPO(t)
	u := createStandardUser(t)

	entries := []PolEntry{
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\System`, Value: "DisableTaskMgr", Type: regDWORD, Data: dwordData(1)},
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`, Value: "NoControlPanel", Type: regDWORD, Data: dwordData(1)},
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`, Value: "DisallowRun", Type: regDWORD, Data: dwordData(1)},
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer\DisallowRun`, Value: "1", Type: regSZ, Data: szData("notepad.exe")},
	}
	appendUserPol(t, entries)
	bumpGPT(t, false, true, "", "")

	start := time.Now()
	lat, via, err := refreshPolicy(t)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = via
	applyLat := time.Since(start)
	if applyLat < lat {
		applyLat = lat
	}

	// Machine Policies hive should NOT hold user settings; user hive / GP cache may.
	hklmDisable, _, _ := regDWORDExists(t, `HKLM:\Software\Microsoft\Windows\CurrentVersion\Policies\System`, "DisableTaskMgr")
	hklmPolicies, _, _ := regDWORDExists(t, `HKLM:\Software\Policies\Microsoft\Windows\System`, "DisableTaskMgr")

	// Load user hive and check classic Policies paths.
	mount, unload := loadUserHive(t, u)
	hkcuPath := `Registry::` + mount + `\Software\Microsoft\Windows\CurrentVersion\Policies\System`
	hkcuExplorer := `Registry::` + mount + `\Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`
	inHive, val, err := regDWORDExists(t, hkcuPath, "DisableTaskMgr")
	if err != nil {
		t.Fatalf("hive check: %v", err)
	}
	noCP, _, _ := regDWORDExists(t, hkcuExplorer, "NoControlPanel")
	disallow, _, _ := regDWORDExists(t, hkcuExplorer, "DisallowRun")
	unload()

	// Also check Group Policy applied cache under HKLM for the user SID.
	gpCache := fmt.Sprintf(`HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Group Policy\State\%s\Software\Microsoft\Windows\CurrentVersion\Policies\System`, u.SID)
	inCache, cacheVal, _ := regDWORDExists(t, gpCache, "DisableTaskMgr")

	gpVisible := gpresultHas(t, u.Name, "DisableTaskMgr") || gpresultHas(t, u.Name, "Registry")

	// Effect: launch notepad as user while DisallowRun is set. Shell-enforced;
	// CreateProcessAsUser often still succeeds — record both.
	started, detail := tryLaunchAsUser(t, u, `C:\Windows\System32\notepad.exe`)
	taskmgrStarted, taskmgrDetail := tryLaunchAsUser(t, u, `C:\Windows\System32\taskmgr.exe`)

	works := "partial"
	gotchas := fmt.Sprintf("user Registry.pol written; DisableTaskMgr in hive=%v(val=%d) cache=%v(val=%d) HKLM classic=%v HKLM Soft\\Policies=%v; notepad launch started=%v (%s); taskmgr started=%v (%s); refresh=%s",
		inHive, val, inCache, cacheVal, hklmDisable, hklmPolicies, started, detail, taskmgrStarted, taskmgrDetail, formatLatency(applyLat))
	if inHive && val == 1 && noCP && disallow {
		works = "yes"
	} else if inCache && cacheVal == 1 {
		works = "yes"
		gotchas += "; values landed in GP State cache not NTUSER until interactive logon"
	} else if !inHive && !inCache {
		works = "no"
	}

	needsRelogon := "unknown"
	if inHive {
		needsRelogon = "no (present in hive after refresh)"
	} else if inCache {
		needsRelogon = "likely yes for HKCU materialization"
	}

	recordFinding(Finding{
		Mechanism:       "Local GPO Registry.pol",
		Variant:         "User: DisableTaskMgr+NoControlPanel+DisallowRun",
		Works:           works,
		Latency:         formatLatency(applyLat),
		NeedsRelogon:    needsRelogon,
		NeedsReboot:     "no",
		VisibleGpresult: boolYN(gpVisible),
		Gotchas:         gotchas,
		ClientSKUNote:   "Server 2025; Win11 Home lacks gpedit but Registry.pol+gpsvc path may still apply — verify on self-hosted client",
	})

	if works == "no" {
		t.Fatalf("expected user policies to apply via local GPO; %s", gotchas)
	}
}

func TestLocalGPO_PerUserSID(t *testing.T) {
	requireAdmin(t)
	u := createStandardUser(t)
	entries := []PolEntry{
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\System`, Value: "DisableTaskMgr", Type: regDWORD, Data: dwordData(1)},
	}
	writePerUserPol(t, u.SID, entries)
	start := time.Now()
	lat, _, err := refreshPolicy(t)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = start

	root := userLGPORoot(u.SID)
	polPath := filepath.Join(root, "User", "Registry.pol")
	parsed, err := readPolFile(polPath)
	if err != nil || len(parsed) == 0 {
		t.Fatalf("per-user pol missing: %v", err)
	}

	mount, unload := loadUserHive(t, u)
	hkcuPath := `Registry::` + mount + `\Software\Microsoft\Windows\CurrentVersion\Policies\System`
	inHive, val, _ := regDWORDExists(t, hkcuPath, "DisableTaskMgr")
	unload()

	works := "partial"
	if inHive && val == 1 {
		works = "yes"
	}
	recordFinding(Finding{
		Mechanism:       "Local GPO per-user (GroupPolicyUsers\\SID)",
		Variant:         "User DisableTaskMgr",
		Works:           works,
		Latency:         formatLatency(lat),
		NeedsRelogon:    boolIf(inHive, "no", "likely yes / session required"),
		NeedsReboot:     "no",
		VisibleGpresult: "n/a (per-user LGPO)",
		Gotchas:         fmt.Sprintf("pol entries=%d hive DisableTaskMgr=%v val=%d path=%s", len(parsed), inHive, val, root),
		ClientSKUNote:   "GroupPolicyUsers is the non-admin per-user local GPO path on all SKUs that run gpsvc",
	})
	if len(parsed) == 0 {
		t.Fatal("per-user Registry.pol not written")
	}
}

func TestLocalGPO_NoVersionBump_NoApply(t *testing.T) {
	requireAdmin(t)
	backupLocalGPO(t)
	st := readGPT(t)

	// Write a distinctive machine policy without bumping GPT version.
	marker := uint32(0xDAD10)
	appendMachinePol(t, []PolEntry{
		{Key: `Software\Policies\DadJokeSpike`, Value: "NoBump", Type: regDWORD, Data: dwordData(marker)},
	})
	// Restore prior version explicitly (overwrite any incidental bump).
	writeGPT(t, st.MachineVersion, st.UserVersion, ensureExtPair(st.MachineExtNames, cseRegistry, toolMachine), st.UserExtNames)

	_, _, _ = refreshPolicy(t)
	present, val, _ := regDWORDExists(t, `HKLM:\Software\Policies\DadJokeSpike`, "NoBump")
	applied := present && val == marker

	recordFinding(Finding{
		Mechanism:       "Local GPO Registry.pol",
		Variant:         "GPT.INI version NOT bumped",
		Works:           boolIf(!applied, "yes (correctly skipped)", "no (unexpected apply)"),
		Latency:         "n/a",
		NeedsRelogon:    "n/a",
		NeedsReboot:     "n/a",
		VisibleGpresult: "n/a",
		Gotchas:         fmt.Sprintf("without version bump, apply observed=%v present=%v val=%d", applied, present, val),
		ClientSKUNote:   "gpsvc version check is core GP behavior on all SKUs",
	})

	t.Cleanup(func() { deleteRegKey(t, `HKLM:\Software\Policies\DadJokeSpike`) })

	if applied {
		t.Fatalf("policy applied despite no GPT.INI version bump (present=%v val=%d)", present, val)
	}
}

func boolYN(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func boolIf(v bool, a, b string) string {
	if v {
		return a
	}
	return b
}
