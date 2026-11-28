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
	// Delimiters must be single UTF-16 code units (no null after '[' / ';' / ']').
	if raw[8] != 0x5b || raw[9] != 0x00 {
		t.Fatalf("expected '[' as 5B 00 at offset 8, got %02x %02x", raw[8], raw[9])
	}
	if raw[10] == 0x00 && raw[11] == 0x00 {
		t.Fatalf(" '[' must not be null-terminated")
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

func TestPolKnownDWORDHex(t *testing.T) {
	// Annotated sample shape from Quid Wiki: SOFTWARE\Policies\MyApp / Enable = DWORD 1
	raw := EncodePol([]PolEntry{{
		Key:   `SOFTWARE\Policies\MyApp`,
		Value: "Enable",
		Type:  regDWORD,
		Data:  dwordData(1),
	}})
	wantTail := []byte{
		0x5B, 0x00, // [
		0x53, 0x00, 0x4F, 0x00, 0x46, 0x00, 0x54, 0x00, 0x57, 0x00, 0x41, 0x00, 0x52, 0x00, 0x45, 0x00,
		0x5C, 0x00, 0x50, 0x00, 0x6F, 0x00, 0x6C, 0x00, 0x69, 0x00, 0x63, 0x00, 0x69, 0x00, 0x65, 0x00, 0x73, 0x00,
		0x5C, 0x00, 0x4D, 0x00, 0x79, 0x00, 0x41, 0x00, 0x70, 0x00, 0x70, 0x00, 0x00, 0x00, // null
		0x3B, 0x00, // ;
		0x45, 0x00, 0x6E, 0x00, 0x61, 0x00, 0x62, 0x00, 0x6C, 0x00, 0x65, 0x00, 0x00, 0x00,
		0x3B, 0x00,
		0x04, 0x00, 0x00, 0x00,
		0x3B, 0x00,
		0x04, 0x00, 0x00, 0x00,
		0x3B, 0x00,
		0x01, 0x00, 0x00, 0x00,
		0x5D, 0x00,
	}
	if !bytes.Equal(raw[8:], wantTail) {
		t.Fatalf("encoding mismatch\ngot  %x\nwant %x", raw[8:], wantTail)
	}
}
