//go:build spike && windows

package policyspike

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFirewall_GPORegistryRule(t *testing.T) {
	requireAdmin(t)
	backupLocalGPO(t)

	ruleID := "{" + strings.ToUpper(guidV4()) + "}"
	// Outbound block for a specific program (MS-GPFAS REG_SZ grammar).
	ruleVal := fmt.Sprintf("v2.31|Action=Block|Active=TRUE|Dir=Out|App=C:\\Windows\\System32\\curl.exe|Name=DadJokeSpikeBlockCurl|")
	key := `Software\Policies\Microsoft\WindowsFirewall\FirewallRules`

	// Baseline: curl should reach example.com before the policy rule is applied.
	beforeOut, beforeErr := curlHTTPCode(t)
	beforeOK := beforeErr == nil && beforeOut != "" && beforeOut != "000"

	appendMachinePol(t, []PolEntry{
		{Key: key, Value: ruleID, Type: regSZ, Data: szData(ruleVal)},
	})
	bumpGPT(t, true, false, []csePair{{cseFirewall, toolFW}}, nil)

	start := time.Now()
	lat, _, err := refreshPolicy(t)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = start

	regPath := `HKLM:\Software\Policies\Microsoft\WindowsFirewall\FirewallRules`
	present := mustPS(t, fmt.Sprintf(`
$ErrorActionPreference='SilentlyContinue'
$v = Get-ItemProperty -Path %s -Name %s -ErrorAction SilentlyContinue
if ($null -eq $v) { 'missing' } else { 'present' }
`, quotePS(regPath), quotePS(ruleID)))

	storeOut, storeErr := powershell(t, `
$ErrorActionPreference='SilentlyContinue'
# Policy store names vary; probe common ones.
$stores = @('localhost','PersistentStore','RSOP','System.GPO')
foreach ($s in $stores) {
  try {
    $r = Get-NetFirewallRule -PolicyStore $s -DisplayName 'DadJokeSpikeBlockCurl' -ErrorAction Stop
    if ($r) { Write-Output ("found:" + $s); break }
  } catch {}
}
$r2 = Get-NetFirewallRule -DisplayName 'DadJokeSpikeBlockCurl' -ErrorAction SilentlyContinue
if ($r2) { Write-Output 'found:active' }
if (-not $r2) {
  Get-NetFirewallRule -PolicyStore DynamicStore -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName -like '*DadJokeSpike*' } |
    ForEach-Object { Write-Output ('found:dynamic:' + $_.DisplayName) }
}
`)
	visibleStore := strings.TrimSpace(storeOut)
	if storeErr != nil {
		visibleStore += " err=" + storeErr.Error()
	}

	afterOut, afterErr := curlHTTPCode(t)
	// Enforcement: baseline succeeded and post-rule curl failed / returned 000.
	enforced := beforeOK && (afterErr != nil || afterOut == "000" || afterOut == "")
	enforceProbe := fmt.Sprintf("before_http=%s before_err=%v after_http=%s after_err=%v enforced=%v",
		beforeOut, beforeErr, afterOut, afterErr, enforced)

	t.Cleanup(func() {
		deleteRegValue(t, regPath, ruleID)
		_, _ = powershell(t, `Get-NetFirewallRule -DisplayName 'DadJokeSpikeBlockCurl' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue`)
	})

	works := "no"
	if present == "present" {
		works = "partial"
	}
	if enforced {
		works = "yes"
	}

	recordFinding(Finding{
		Mechanism:       "Firewall GPO registry (MS-GPFAS)",
		Variant:         "Outbound block App=curl.exe via Registry.pol",
		Works:           works,
		Latency:         formatLatency(lat),
		NeedsRelogon:    "no",
		NeedsReboot:     "no",
		VisibleGpresult: boolYN(gpresultHas(t, "", "Firewall") || gpresultHas(t, "", "Windows Firewall")),
		Gotchas:         fmt.Sprintf("registry=%s Get-NetFirewallRule=%q %s; Firewall CSE GUID %s listed in GPT", present, visibleStore, enforceProbe, cseFirewall),
		ClientSKUNote:   "Policy-store visibility differs; Home still honors HKLM firewall policy keys when the firewall service reads them",
	})

	if present != "present" {
		t.Fatalf("firewall rule registry value missing after refresh")
	}
	if !beforeOK {
		t.Logf("baseline curl did not succeed (%s / %v); cannot claim outbound block enforcement", beforeOut, beforeErr)
	}
	if beforeOK && !enforced {
		t.Logf("registry present but outbound curl still succeeded after rule (http=%s); Works=partial", afterOut)
	}
}

// curlHTTPCode runs curl.exe and returns the %{http_code} write-out (or "" on hard failure).
func curlHTTPCode(t testing.TB) (code string, err error) {
	t.Helper()
	// PowerShell single-quoted -w argument must be %{http_code} (not batch-style %%).
	out, err := powershell(t, `
$ErrorActionPreference='Continue'
$code = & curl.exe -s -o NUL -w '%{http_code}' --connect-timeout 5 --max-time 15 https://example.com/ 2>$null
if ($LASTEXITCODE -ne 0 -and [string]::IsNullOrWhiteSpace($code)) {
  throw ("curl exit=" + $LASTEXITCODE)
}
Write-Output $code
`)
	return strings.TrimSpace(out), err
}
