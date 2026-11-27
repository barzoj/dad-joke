package updater

import (
	"strings"
	"testing"
)

func TestParseSHA256File(t *testing.T) {
	t.Parallel()
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	h, name, err := ParseSHA256File([]byte(want + "  dad-joke-windows-amd64.exe\n"))
	if err != nil {
		t.Fatal(err)
	}
	if h != want {
		t.Fatalf("hash = %q", h)
	}
	if name != "dad-joke-windows-amd64.exe" {
		t.Fatalf("name = %q", name)
	}

	h, name, err = ParseSHA256File([]byte(strings.ToUpper(want) + " *dad-joke-windows-amd64.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if h != want || name != "dad-joke-windows-amd64.exe" {
		t.Fatalf("got %s %q", h, name)
	}

	for _, bad := range []string{"", "not-a-hash  x", "abcd", want + "z  x"} {
		if _, _, err := ParseSHA256File([]byte(bad)); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestHashAndVerify(t *testing.T) {
	t.Parallel()
	data := []byte("hello")
	got := HashBytes(data)
	fromReader, err := HashReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got != fromReader {
		t.Fatalf("HashBytes != HashReader: %s vs %s", got, fromReader)
	}
	if err := VerifySHA256(got, got); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(got, strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected mismatch")
	}
}
