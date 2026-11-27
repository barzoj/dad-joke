package updater

import (
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/internal/installer"
)

func TestValidateApplyPathsHappy(t *testing.T) {
	t.Parallel()
	paths := installer.DefaultPaths()
	staged := AllowedStagedExample("abc123")
	if err := ValidateApplyPaths(staged, paths.BinaryPath, paths.DataDir, paths.BinaryPath); err != nil {
		t.Fatal(err)
	}
}

func TestValidateApplyPathsRejectsTraversalAndArbitrary(t *testing.T) {
	t.Parallel()
	paths := installer.DefaultPaths()
	cases := []struct {
		name   string
		staged string
		binary string
	}{
		{
			name:   "staged outside updates",
			staged: `C:\Temp\evil.exe`,
			binary: paths.BinaryPath,
		},
		{
			name:   "staged in updates root",
			staged: paths.DataDir + `\updates\` + AssetEXE,
			binary: paths.BinaryPath,
		},
		{
			name:   "staged traversal",
			staged: paths.DataDir + `\updates\x\..\..\..\Windows\System32\cmd.exe`,
			binary: paths.BinaryPath,
		},
		{
			name:   "arbitrary binary",
			staged: AllowedStagedExample("1"),
			binary: `C:\Windows\System32\notepad.exe`,
		},
		{
			name:   "binary traversal to other exe",
			staged: AllowedStagedExample("1"),
			binary: `C:\Program Files\DadJoke\..\..\Windows\System32\cmd.exe`,
		},
		{
			name:   "unc staged",
			staged: `\\evil\share\updates\1\` + AssetEXE,
			binary: paths.BinaryPath,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateApplyPaths(tt.staged, tt.binary, paths.DataDir, paths.BinaryPath)
			if err == nil {
				t.Fatalf("expected rejection for staged=%q binary=%q", tt.staged, tt.binary)
			}
		})
	}
}

func TestWindowsCleanTraversal(t *testing.T) {
	t.Parallel()
	gotEscape, err := windowsClean(`C:\ProgramData\DadJoke\updates\..\..\Windows\System32`)
	if err != nil {
		t.Fatalf("unexpected error cleaning escaped updates path: %v", err)
	}
	if strings.Contains(strings.ToLower(gotEscape), `\updates\`) {
		t.Fatalf("escaped path unexpectedly still under updates: %q", gotEscape)
	}
	got, err := windowsClean(`C:\ProgramData\DadJoke\updates\a\..\b\file.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, `C:\ProgramData\DadJoke\updates\b\file.exe`) {
		t.Fatalf("got %q", got)
	}
	if _, err := windowsClean(`C:\a\..\..\..\x`); err == nil {
		t.Fatal("expected traversal error above drive root contents")
	}
}
