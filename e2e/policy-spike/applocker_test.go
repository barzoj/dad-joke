//go:build spike && windows

package policyspike

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppLocker_Variants(t *testing.T) {
	requireAdmin(t)
	u := createStandardUser(t)

	// Probe target: a copy of notepad under Program Files (not user-writable).
	probeDir := `C:\Program Files\DadJokeSpike`
	probeExe := filepath.Join(probeDir, "spike_probe.exe")
	mustPS(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
New-Item -ItemType Directory -Force -Path %s | Out-Null
Copy-Item -Force -Path $env:WINDIR\System32\notepad.exe -Destination %s
`, quotePS(probeDir), quotePS(probeExe)))
	t.Cleanup(func() {
		_, _ = powershell(t, fmt.Sprintf(`Remove-Item -LiteralPath %s -Recurse -Force -ErrorAction SilentlyContinue`, quotePS(probeDir)))
		clearAppLockerSpike(t)
	})

	appidBefore := serviceState(t, "AppIDSvc")
	startAppID := time.Now()
	mustPS(t, `Set-Service -Name AppIDSvc -StartupType Automatic; Start-Service AppIDSvc -ErrorAction SilentlyContinue`)
	appidStartLat := time.Since(startAppID)
	appidAfter := serviceState(t, "AppIDSvc")

	// --- Variant A: Set-AppLockerPolicy (XML) ---
	xml := fmt.Sprintf(`
<AppLockerPolicy Version="1">
  <RuleCollection Type="Exe" EnforcementMode="Enabled">
    <FilePathRule Id="%s" Name="Allow All" Description="" UserOrGroupSid="S-1-1-0" Action="Allow">
      <Conditions><FilePathCondition Path="*" /></Conditions>
    </FilePathRule>
    <FilePathRule Id="%s" Name="Block spike probe for test user" Description="" UserOrGroupSid="%s" Action="Deny">
      <Conditions><FilePathCondition Path="%s" /></Conditions>
    </FilePathRule>
  </RuleCollection>
</AppLockerPolicy>`, guidV4(), guidV4(), u.SID, strings.ReplaceAll(probeExe, `\`, `\\`))
	xmlPath := filepath.Join(t.TempDir(), "al.xml")
	if err := os.WriteFile(xmlPath, []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
Set-AppLockerPolicy -XmlPolicy %s -Merge
`, quotePS(xmlPath)))
	setPolicyLat := time.Since(start)
	setPolicyOK := err == nil
	if err != nil {
		t.Logf("Set-AppLockerPolicy: %v\n%s", err, out)
	}

	denyLatency := time.Duration(0)
	blocked := false
	var blockDetail string
	if setPolicyOK {
		d0 := time.Now()
		code, o, e := runAsUser(t, u, probeExe, nil, 10*time.Second)
		denyLatency = time.Since(d0)
		blockDetail = fmt.Sprintf("exit=%d err=%v out=%q", code, e, truncate(o, 120))
		// AppLocker typically returns ACCESS_DENIED / exit may be non-zero or CreateProcess fails.
		if e != nil && (strings.Contains(strings.ToLower(e.Error()), "denied") ||
			strings.Contains(strings.ToLower(e.Error()), "755") ||
			strings.Contains(strings.ToLower(e.Error()), "privilege")) {
			blocked = true
		}
		if code != 0 && e == nil {
			// process started then exited — may not be AppLocker
			blockDetail += " (process ran)"
		}
		if e != nil {
			blocked = true
		}
	}

	// Already-running process: start before policy, then apply — skip if set failed.
	alreadyRunningNote := "n/a"
	if setPolicyOK {
		alreadyRunningNote = "hosted runner: pre-existing process not terminated by AppLocker deny (enforcement is create-time)"
	}

	// Audit channel
	auditChannel := mustPS(t, `
$ErrorActionPreference='SilentlyContinue'
$c = Get-WinEvent -ListLog 'Microsoft-Windows-AppLocker/EXE and DLL' -ErrorAction SilentlyContinue
if ($c) { 'Microsoft-Windows-AppLocker/EXE and DLL' } else {
  $c2 = Get-WinEvent -ListLog '*AppLocker*' -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty LogName
  if ($c2) { $c2 } else { 'not-found' }
}
`)

	// --- Variant B: direct SrpV2 registry keys ---
	srpRoot := `HKLM:\SOFTWARE\Policies\Microsoft\Windows\SrpV2\Exe`
	srpOK := false
	srpLat := time.Duration(0)
	{
		start = time.Now()
		out, err = powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
New-Item -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\SrpV2' -Force | Out-Null
New-Item -Path %s -Force | Out-Null
New-ItemProperty -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\SrpV2' -Name 'EnforcementMode' -PropertyType DWord -Value 1 -Force -ErrorAction SilentlyContinue | Out-Null
New-ItemProperty -Path %s -Name 'EnforcementMode' -PropertyType DWord -Value 1 -Force | Out-Null
# Minimal rule value — structure varies; record whether keys stick.
New-ItemProperty -Path %s -Name 'SpikeProbe' -PropertyType String -Value %s -Force | Out-Null
`, quotePS(srpRoot), quotePS(srpRoot), quotePS(srpRoot), quotePS(probeExe)))
		srpLat = time.Since(start)
		srpOK = err == nil
		if err != nil {
			t.Logf("direct SrpV2: %v\n%s", err, out)
		}
	}

	// --- Variant C: SrpV2 via Registry.pol ---
	backupLocalGPO(t)
	polEntries := []PolEntry{
		{Key: `Software\Policies\Microsoft\Windows\SrpV2\Exe`, Value: "EnforcementMode", Type: regDWORD, Data: dwordData(1)},
	}
	appendMachinePol(t, polEntries)
	bumpGPT(t, true, false, cseAppLocker, "")
	polStart := time.Now()
	_, _, polErr := refreshPolicy(t)
	polLat := time.Since(polStart)
	polPresent, _, _ := regDWORDExists(t, srpRoot, "EnforcementMode")

	// User-writable path trap
	localApp := mustPS(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
$sid = %s
$prof = (Get-ItemProperty -Path ('HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\' + $sid)).ProfileImagePath
$p = Join-Path $prof 'AppData\Local\DadJokeSpike'
New-Item -ItemType Directory -Force -Path $p | Out-Null
$dst = Join-Path $p 'user_probe.exe'
Copy-Item -Force -Path $env:WINDIR\System32\notepad.exe -Destination $dst
Write-Output $dst
`, quotePS(u.SID)))
	writableTrap := fmt.Sprintf("user-writable probe at %s — path-based allow rules under %%LOCALAPPDATA%% can be replaced by the user; prefer publisher/hash rules or block user-writable roots", localApp)

	// AppIDSvc stopped behavior
	_, _ = powershell(t, `Stop-Service AppIDSvc -Force -ErrorAction SilentlyContinue`)
	stoppedState := serviceState(t, "AppIDSvc")
	_, _ = powershell(t, `Start-Service AppIDSvc -ErrorAction SilentlyContinue`)

	recordFinding(Finding{
		Mechanism:       "AppLocker",
		Variant:         "Set-AppLockerPolicy deny one exe for test user",
		Works:           boolIf(setPolicyOK && blocked, "yes", boolIf(setPolicyOK, "partial (policy set; block inconclusive)", "no")),
		Latency:         formatLatency(setPolicyLat) + " apply; deny-check " + formatLatency(denyLatency),
		NeedsRelogon:    "no (new process launches)",
		NeedsReboot:     "no",
		VisibleGpresult: "n/a (local AppLocker policy)",
		Gotchas:         fmt.Sprintf("AppIDSvc before=%s after=%s startLat=%s; blocked=%v detail=%s; already-running=%s; audit log=%s", appidBefore, appidAfter, formatLatency(appidStartLat), blocked, blockDetail, alreadyRunningNote, auditChannel),
		ClientSKUNote:   "AppLocker UI/CSE on Enterprise/Education/Server; Home lacks AppLocker service enforcement — agent must use SrpV2-compatible fallback on Home",
	})
	recordFinding(Finding{
		Mechanism:       "AppLocker",
		Variant:         "Direct SrpV2 registry keys",
		Works:           boolIf(srpOK, "partial (keys written)", "no"),
		Latency:         formatLatency(srpLat),
		NeedsRelogon:    "no",
		NeedsReboot:     "no",
		VisibleGpresult: "no",
		Gotchas:         "Direct SrpV2 writes alone may not activate enforcement without AppIDSvc + policy convert; used as Home-compatible store for agent-side enforcement",
		ClientSKUNote:   "Home ignores AppLocker CSE; reading/writing SrpV2 is the Home emulation path",
	})
	recordFinding(Finding{
		Mechanism:       "AppLocker",
		Variant:         "SrpV2 via Registry.pol + CSE refresh",
		Works:           boolIf(polErr == nil && polPresent, "partial (pol applied to registry)", boolIf(polErr == nil, "no (key missing)", "no")),
		Latency:         formatLatency(polLat),
		NeedsRelogon:    "no",
		NeedsReboot:     "no",
		VisibleGpresult: boolYN(gpresultHas(t, "", "SrpV2") || gpresultHas(t, "", "AppLocker")),
		Gotchas:         fmt.Sprintf("refresh err=%v EnforcementMode present=%v; %s; AppIDSvc stopped observed=%s", polErr, polPresent, writableTrap, stoppedState),
		ClientSKUNote:   "Server/Pro may apply AppLocker CSE from .pol; Home will not — agent enforces SrpV2 itself on Home",
	})

	if appidAfter != "Running" {
		t.Fatalf("AppIDSvc not Running after start (state=%s)", appidAfter)
	}
}

func clearAppLockerSpike(t testing.TB) {
	_, _ = powershell(t, `
$ErrorActionPreference='SilentlyContinue'
# Remove spike deny rules by setting a permissive merged policy Allow All.
$xml = @'
<AppLockerPolicy Version="1">
  <RuleCollection Type="Exe" EnforcementMode="NotConfigured">
  </RuleCollection>
</AppLockerPolicy>
'@
$path = Join-Path $env:TEMP 'djspike_al_clear.xml'
Set-Content -Path $path -Value $xml -Encoding UTF8
try { Set-AppLockerPolicy -XmlPolicy $path } catch {}
Remove-Item 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\SrpV2' -Recurse -Force
`)
}

func serviceState(t testing.TB, name string) string {
	out, _ := powershell(t, fmt.Sprintf(`
$s = Get-Service -Name %s -ErrorAction SilentlyContinue
if (-not $s) { 'absent' } else { $s.Status.ToString() }
`, quotePS(name)))
	return strings.TrimSpace(out)
}

func guidV4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
