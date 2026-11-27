package jobs

import (
	"slices"
	"testing"

	"github.com/barzoj/dad-joke/internal/store"
)

func TestBuildCommand_PowerShellDisablesStdinHost(t *testing.T) {
	t.Parallel()
	argv, err := buildCommand(store.JobTypePowerShell, "Write-Output hi")
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-InputFormat", "None",
		"-ExecutionPolicy", "Bypass",
		"-Command",
	}
	if len(argv) != len(wantPrefix)+1 {
		t.Fatalf("argv = %#v", argv)
	}
	if !slices.Equal(argv[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("argv prefix = %#v, want %#v", argv[:len(wantPrefix)], wantPrefix)
	}
	if argv[len(argv)-1] != "Write-Output hi" {
		t.Fatalf("payload arg = %q", argv[len(argv)-1])
	}
}

func TestBuildCommand_Cmd(t *testing.T) {
	t.Parallel()
	argv, err := buildCommand(store.JobTypeCmd, "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv, []string{"cmd.exe", "/C", "echo hi"}) {
		t.Fatalf("argv = %#v", argv)
	}
}

func TestBuildCommand_RejectsUnknown(t *testing.T) {
	t.Parallel()
	_, err := buildCommand("bash", "echo hi")
	if err == nil {
		t.Fatal("expected error")
	}
}
