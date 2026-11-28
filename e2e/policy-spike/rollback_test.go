//go:build spike && windows

package policyspike

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRollback_PreservesForeignPolEntries(t *testing.T) {
	requireAdmin(t)
	backupLocalGPO(t)

	foreign := PolEntry{
		Key:   `Software\Policies\ForeignSpike`,
		Value: "KeepMe",
		Type:  regDWORD,
		Data:  dwordData(0xF00D),
	}
	ours := PolEntry{
		Key:   `Software\Policies\DadJokeSpike`,
		Value: "Temp",
		Type:  regDWORD,
		Data:  dwordData(0xBEEF),
	}

	// Seed foreign entry first, then ours.
	if err := writePolFile(machinePol, []PolEntry{foreign, ours}); err != nil {
		t.Fatalf("seed pol: %v", err)
	}
	bumpGPT(t, true, false, nil, nil)
	start := time.Now()
	_, _, err := refreshPolicy(t)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	applyLat := time.Since(start)

	foreignOK, fVal, _ := regDWORDExists(t, `HKLM:\Software\Policies\ForeignSpike`, "KeepMe")
	oursOK, oVal, _ := regDWORDExists(t, `HKLM:\Software\Policies\DadJokeSpike`, "Temp")
	if !foreignOK || fVal != 0xF00D {
		t.Fatalf("foreign entry not applied: present=%v val=%d", foreignOK, fVal)
	}
	if !oursOK || oVal != 0xBEEF {
		t.Fatalf("ours not applied: present=%v val=%d", oursOK, oVal)
	}

	// Rollback: rewrite pol with only foreign preserved.
	if err := writePolFile(machinePol, []PolEntry{foreign}); err != nil {
		t.Fatalf("rollback pol: %v", err)
	}
	// Also delete our registry residue that GP may leave until next apply cleans — explicit delete.
	deleteRegKey(t, `HKLM:\Software\Policies\DadJokeSpike`)
	bumpGPT(t, true, false, nil, nil)
	rbStart := time.Now()
	_, _, err = refreshPolicy(t)
	rbLat := time.Since(rbStart)
	if err != nil {
		t.Fatalf("refresh after rollback: %v", err)
	}

	foreignAfter, fVal2, _ := regDWORDExists(t, `HKLM:\Software\Policies\ForeignSpike`, "KeepMe")
	oursAfter, _, _ := regDWORDExists(t, `HKLM:\Software\Policies\DadJokeSpike`, "Temp")

	parsed, _ := readPolFile(machinePol)
	foreignInPol := false
	oursInPol := false
	for _, e := range parsed {
		if e.Key == foreign.Key && e.Value == foreign.Value {
			foreignInPol = true
		}
		if e.Key == ours.Key && e.Value == ours.Value {
			oursInPol = true
		}
	}

	t.Cleanup(func() {
		deleteRegKey(t, `HKLM:\Software\Policies\ForeignSpike`)
		deleteRegKey(t, `HKLM:\Software\Policies\DadJokeSpike`)
	})

	works := foreignAfter && fVal2 == 0xF00D && !oursAfter && foreignInPol && !oursInPol
	recordFinding(Finding{
		Mechanism:       "Rollback",
		Variant:         "remove our .pol entries; keep foreign",
		Works:           boolIf(works, "yes", "partial"),
		Latency:         fmt.Sprintf("apply %s; rollback %s", formatLatency(applyLat), formatLatency(rbLat)),
		NeedsRelogon:    "no",
		NeedsReboot:     "no",
		VisibleGpresult: "n/a",
		Gotchas:         fmt.Sprintf("foreign reg=%v/%d ours_after=%v foreignInPol=%v oursInPol=%v; agent must merge/preserve unknown instructions", foreignAfter, fVal2, oursAfter, foreignInPol, oursInPol),
		ClientSKUNote:   "Merge semantics required on all SKUs so pre-existing admin LGPO is not destroyed",
	})

	if !foreignAfter || fVal2 != 0xF00D {
		t.Fatalf("foreign registry entry lost after rollback")
	}
	if oursInPol {
		t.Fatalf("our pol entry still present after rollback rewrite")
	}
	if oursAfter {
		t.Fatalf("our registry value still present after rollback cleanup")
	}

	// Prove original pol bytes for foreign survive rewrite (value bytes).
	raw, _ := os.ReadFile(machinePol)
	if !bytes.Contains(raw, []byte{'K', 0, 'e', 0, 'e', 0, 'p', 0, 'M', 0, 'e', 0}) {
		t.Fatalf("foreign value name missing from Registry.pol UTF-16 content")
	}
}
