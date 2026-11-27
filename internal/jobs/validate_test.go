package jobs

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/barzoj/dad-joke/internal/store"
)

func TestValidateClaimed_AcceptsSupportedTypes(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{store.JobTypePowerShell, store.JobTypeCmd} {
		if err := ValidateClaimed(typ, "echo hi"); err != nil {
			t.Fatalf("type %q: %v", typ, err)
		}
	}
}

func TestValidateClaimed_RejectsUnknownType(t *testing.T) {
	t.Parallel()
	err := ValidateClaimed("bash", "echo hi")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateClaimed_RejectsOversizedPayload(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a", store.JobIOMaxBytes+1)
	err := ValidateClaimed(store.JobTypeCmd, big)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClampTimeout(t *testing.T) {
	t.Parallel()
	if d := ClampTimeout(nil); d.Seconds() != 60 {
		t.Fatalf("nil = %v, want 60s", d)
	}
	var zero int32
	if d := ClampTimeout(&zero); d.Seconds() != 60 {
		t.Fatalf("0 = %v, want 60s", d)
	}
	high := int32(999)
	if d := ClampTimeout(&high); d.Seconds() != 300 {
		t.Fatalf("999 = %v, want 300s", d)
	}
	ok := int32(120)
	if d := ClampTimeout(&ok); d.Seconds() != 120 {
		t.Fatalf("120 = %v", d)
	}
}

func TestTruncateUTF8_PreservesBoundary(t *testing.T) {
	t.Parallel()
	// Build a buffer where a 3-byte rune (€ = E2 82 AC) straddles byte 65536.
	prefix := bytes.Repeat([]byte{'x'}, MaxOutputBytes-1)
	euro := []byte("€") // 3 bytes
	raw := append(prefix, euro...)
	if len(raw) != MaxOutputBytes+2 {
		t.Fatalf("raw len = %d", len(raw))
	}
	got := TruncateUTF8(raw, MaxOutputBytes)
	if len(got) != MaxOutputBytes-1 {
		t.Fatalf("truncated len = %d, want %d (drop incomplete rune)", len(got), MaxOutputBytes-1)
	}
	if !utf8.Valid(got) {
		t.Fatal("truncated output is not valid UTF-8")
	}
	if bytes.Contains(got, []byte{0xE2}) {
		t.Fatal("incomplete euro lead byte should be stripped")
	}
}

func TestTruncateUTF8_FourByteRuneAtBoundary(t *testing.T) {
	t.Parallel()
	// 𐍈 is U+10348, UTF-8 F0 90 8D 88 (4 bytes).
	rune4 := []byte("𐍈")
	prefix := bytes.Repeat([]byte{'y'}, MaxOutputBytes-2)
	raw := append(prefix, rune4...)
	got := TruncateUTF8(raw, MaxOutputBytes)
	if !utf8.Valid(got) {
		t.Fatal("not valid UTF-8")
	}
	if len(got) > MaxOutputBytes {
		t.Fatalf("len %d > max", len(got))
	}
	if r, _ := utf8.DecodeLastRune(got); r == utf8.RuneError {
		t.Fatal("last rune incomplete")
	}
}

func TestCappedWriter_DrainsWithoutOOM(t *testing.T) {
	t.Parallel()
	w := newCappedWriter(64)
	huge := bytes.Repeat([]byte("Z"), 1024*1024)
	n, err := w.Write(huge)
	if err != nil || n != len(huge) {
		t.Fatalf("write: n=%d err=%v", n, err)
	}
	out := w.Bytes()
	if len(out) > 64 {
		t.Fatalf("captured %d bytes", len(out))
	}
	if !utf8.Valid(out) {
		t.Fatal("invalid utf8")
	}
}
