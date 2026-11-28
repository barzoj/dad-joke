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
	_ = ensureUserProfile(t, u)

	entries := []PolEntry{
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\System`, Value: "DisableTaskMgr", Type: regDWORD, Data: dwordData(1)},
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`, Value: "NoControlPanel", Type: regDWORD, Data: dwordData(1)},
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`, Value: "DisallowRun", Type: regDWORD, Data: dwordData(1)},
		{Key: `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer\DisallowRun`, Value: "1", Type: regSZ, Data: szData("notepad.exe")},
	}
	appendUserPol(t, entries)
	bumpGPT(t, false, true, "", "")

	// Confirm User\Registry.pol is well-formed before relying on gpsvc.
	parsed, err := readPolFile(userPol)
	if err != nil || len(parsed) < len(entries) {
		t.Fatalf("user Registry.pol not written correctly: entries=%d err=%v", len(parsed), err)
	}

	start := time.Now()
	lat, via, err := refreshPolicy(t)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = via
	// User CSE runs for the calling user; also try as the Standard User so we
	// observe whether a non-interactive token is enough to materialize HKCU.
	var userGPErr error
	code, o, e := runAsUser(t, u, `C:\Windows\System32\gpupdate.exe`, []string{"/Target:User", "/Force"}, 60*time.Second)
	userGPOut := truncate(o, 200)
	if e != nil {
		userGPErr = e
	} else if code != 0 {
		userGPErr = fmt.Errorf("gpupdate exit=%d", code)
	}
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
	gotchas := fmt.Sprintf("user Registry.pol entries=%d; DisableTaskMgr in hive=%v(val=%d) cache=%v(val=%d) HKLM classic=%v HKLM Soft\\Policies=%v; notepad launch started=%v (%s); taskmgr started=%v (%s); refresh=%s; user-gpupdate err=%v out=%q",
		len(parsed), inHive, val, inCache, cacheVal, hklmDisable, hklmPolicies, started, detail, taskmgrStarted, taskmgrDetail, formatLatency(applyLat), userGPErr, userGPOut)
	if inHive && val == 1 && noCP && disallow {
		works = "yes"
	} else if inCache && cacheVal == 1 {
		works = "yes"
		gotchas += "; values landed in GP State cache not NTUSER until interactive logon"
	} else if !inHive && !inCache {
		// Observed Server fact: User CSE does not write NTUSER for a non-interactive
		// logon/token on this SKU. Pol + GPT CSE registration are the agent contract;
		// materialization happens at interactive logon / explorer session.
		works = "partial (pol+CSE ok; HKCU not materialized without interactive session)"
		gotchas += "; gpsvc did not write test-user HKCU from admin gpupdate or non-interactive user gpupdate"
	}

	needsRelogon := "yes (interactive session required for HKCU materialization on this runner)"
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
		ClientSKUNote:   "Server 2025; User Registry.pol is processed per logged-on user; Home lacks gpedit UI but gpsvc still reads Local GPO",
	})

	// Hard fail only if the spike contract (write User\\Registry.pol + bump GPT) broke.
	if len(parsed) < len(entries) {
		t.Fatalf("user Registry.pol incomplete; %s", gotchas)
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

	// Establish a known baseline version with Registry CSE present, then apply
	// an empty/no-op refresh so History matches that version.
	st := readGPT(t)
	mv, uv := st.MachineVersion, st.UserVersion
	if mv < 1 {
		mv = 1
	}
	if uv < 1 {
		uv = 1
	}
	mExt := ensureExtPair(st.MachineExtNames, cseRegistry, toolMachine)
	uExt := ensureExtPair(st.UserExtNames, cseRegistry, toolUser)
	writeGPT(t, mv, uv, mExt, uExt)
	_, _, _ = refreshPolicyOpts(t, false)

	deleteRegKey(t, `HKLM:\Software\Policies\DadJokeSpike`)
	marker := uint32(0xDAD10)
	// Change Registry.pol content but keep GPT.INI Version identical.
	appendMachinePol(t, []PolEntry{
		{Key: `Software\Policies\DadJokeSpike`, Value: "NoBump", Type: regDWORD, Data: dwordData(marker)},
	})
	writeGPT(t, mv, uv, mExt, uExt)

	// Important: gpupdate /force reapplies CSEs and bypasses the version skip.
	// Probe the version check with a non-force refresh.
	outNF, errNF := runCmd("gpupdate.exe", "/Target:Computer")
	_ = outNF
	presentNF, valNF, _ := regDWORDExists(t, `HKLM:\Software\Policies\DadJokeSpike`, "NoBump")
	appliedNoForce := presentNF && valNF == marker

	// Contrast: /force does re-apply even without a version bump.
	_, _, _ = refreshPolicyOpts(t, false)
	presentF, valF, _ := regDWORDExists(t, `HKLM:\Software\Policies\DadJokeSpike`, "NoBump")
	appliedForce := presentF && valF == marker

	recordFinding(Finding{
		Mechanism:       "Local GPO Registry.pol",
		Variant:         "GPT.INI version NOT bumped",
		Works:           boolIf(!appliedNoForce, "yes (non-force refresh skipped)", "no (applied without bump)"),
		Latency:         "n/a",
		NeedsRelogon:    "n/a",
		NeedsReboot:     "n/a",
		VisibleGpresult: "n/a",
		Gotchas: fmt.Sprintf("non-force apply=%v (val=%d, gpupdateErr=%v); gpupdate /force apply=%v (val=%d). Agent must bump GPT.INI Version; /force alone is not a substitute for correct versioning in production reconcile, but /force will re-read .pol on demand.",
			appliedNoForce, valNF, errNF, appliedForce, valF),
		ClientSKUNote: "gpsvc version check is core GP behavior; /force bypasses the skip",
	})

	t.Cleanup(func() { deleteRegKey(t, `HKLM:\Software\Policies\DadJokeSpike`) })

	if appliedNoForce {
		t.Fatalf("policy applied on non-force refresh despite no GPT.INI version bump (val=%d)", valNF)
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
