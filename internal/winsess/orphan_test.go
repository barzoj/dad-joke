package winsess

import (
	"path/filepath"
	"testing"
)

func TestShouldTerminateOrphanRejectsOtherPathSameBasename(t *testing.T) {
	t.Parallel()
	installed := filepath.Join(`C:\Program Files`, "DadJoke", "dad-joke.exe")
	other := filepath.Join(`C:\Temp`, "other", "dad-joke.exe")
	c := OrphanSnapshot{
		PID:          42,
		SessionID:    2,
		ImagePath:    other,
		CommandLine:  `"` + other + `" session`,
		CreationTime: 100,
	}
	if ShouldTerminateOrphan(installed, c) {
		t.Fatal("unrelated dad-joke.exe copy must not be terminated")
	}
}

func TestShouldTerminateOrphanRequiresSessionArg(t *testing.T) {
	t.Parallel()
	installed := `C:\Program Files\DadJoke\dad-joke.exe`
	for _, cmd := range []string{
		`"` + installed + `" version`,
		`"` + installed + `" run`,
		`"` + installed + `" install`,
		`"` + installed + `" apply-update --staged x`,
		`"` + installed + `" session-extra`,
	} {
		c := OrphanSnapshot{
			PID: 1, SessionID: 1, ImagePath: installed,
			CommandLine: cmd, CreationTime: 1,
		}
		if ShouldTerminateOrphan(installed, c) {
			t.Fatalf("must reject cmdline %q", cmd)
		}
	}
}

func TestShouldTerminateOrphanAcceptsInstalledSession(t *testing.T) {
	t.Parallel()
	installed := `C:\Program Files\DadJoke\dad-joke.exe`
	c := OrphanSnapshot{
		PID:          7,
		SessionID:    3,
		ImagePath:    `c:\program files\dadjoke\dad-joke.exe`, // case
		CommandLine:  `"` + installed + `" session`,
		CreationTime: 99,
	}
	if !ShouldTerminateOrphan(installed, c) {
		t.Fatal("expected accept")
	}
}

func TestShouldTerminateOrphanRejectsSession0AndMissingCreateTime(t *testing.T) {
	t.Parallel()
	installed := `C:\Program Files\DadJoke\dad-joke.exe`
	base := OrphanSnapshot{
		PID: 1, SessionID: 1, ImagePath: installed,
		CommandLine: `"` + installed + `" session`, CreationTime: 1,
	}
	z := base
	z.SessionID = 0
	if ShouldTerminateOrphan(installed, z) {
		t.Fatal("session 0")
	}
	z = base
	z.CreationTime = 0
	if ShouldTerminateOrphan(installed, z) {
		t.Fatal("missing creation time")
	}
}

func TestOrphanReopenMatchesCreationTimeGuard(t *testing.T) {
	t.Parallel()
	snap := OrphanSnapshot{
		PID: 5, SessionID: 2,
		ImagePath:    `C:\Program Files\DadJoke\dad-joke.exe`,
		CommandLine:  `"C:\Program Files\DadJoke\dad-joke.exe" session`,
		CreationTime: 12345,
	}
	if !OrphanReopenMatches(snap, snap.ImagePath, snap.CommandLine, 12345) {
		t.Fatal("same identity should match")
	}
	// PID reused: same path later but different creation time → refuse kill.
	if OrphanReopenMatches(snap, snap.ImagePath, snap.CommandLine, 99999) {
		t.Fatal("creation time mismatch must refuse terminate")
	}
	// Path changed after open (should not happen) → refuse.
	if OrphanReopenMatches(snap, `C:\Temp\dad-joke.exe`, snap.CommandLine, 12345) {
		t.Fatal("path mismatch must refuse")
	}
	// Cmdline no longer session → refuse.
	if OrphanReopenMatches(snap, snap.ImagePath, `"C:\Program Files\DadJoke\dad-joke.exe" version`, 12345) {
		t.Fatal("cmdline mismatch must refuse")
	}
}
