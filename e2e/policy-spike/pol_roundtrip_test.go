//go:build spike && windows

package policyspike

import (
	"bytes"
	"testing"
)

func TestPolRoundTrip(t *testing.T) {
	entries := []PolEntry{
		{
			Key:   `Software\Microsoft\Windows\CurrentVersion\Policies\System`,
			Value: "DisableTaskMgr",
			Type:  regDWORD,
			Data:  dwordData(1),
		},
		{
			Key:   `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer\DisallowRun`,
			Value: "1",
			Type:  regSZ,
			Data:  szData("notepad.exe"),
		},
		{
			Key:   `Software\Policies\Microsoft\Windows\**del.`,
			Value: "**del.SomeValue",
			Type:  regSZ,
			Data:  szData(""),
		},
	}
	raw := EncodePol(entries)
	if !bytes.HasPrefix(raw, polSignature) {
		t.Fatalf("missing signature")
	}
	got, err := ParsePol(raw)
	if err != nil {
		t.Fatalf("ParsePol: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("len=%d want %d", len(got), len(entries))
	}
	for i := range entries {
		if got[i].Key != entries[i].Key || got[i].Value != entries[i].Value || got[i].Type != entries[i].Type {
			t.Fatalf("entry %d mismatch: %+v vs %+v", i, got[i], entries[i])
		}
		if !bytes.Equal(got[i].Data, entries[i].Data) {
			t.Fatalf("entry %d data mismatch", i)
		}
	}
}
