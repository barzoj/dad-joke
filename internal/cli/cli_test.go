package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/service"
	"github.com/barzoj/dad-joke/internal/updater"
)

func TestVersionOutput(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := Run([]string{"version"}, Dependencies{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Version: "1.2.3",
		Commit:  "abcdef0",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitOK, stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	want := "dad-joke 1.2.3 (commit abcdef0)"
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestVersionDefaultsWhenEmpty(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	code := Run([]string{"version"}, Dependencies{Stdout: &stdout})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	got := strings.TrimSpace(stdout.String())
	want := "dad-joke 0.0.1-dev (commit unknown)"
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestVersionExtraArgs(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := Run([]string{"version", "extra"}, Dependencies{Stderr: &stderr})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Fatalf("stderr = %q, want unexpected argument", stderr.String())
	}
}

func TestMissingCommand(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := Run(nil, Dependencies{Stderr: &stderr})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "missing command") {
		t.Fatalf("stderr = %q, want missing command", stderr.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := Run([]string{"frobnicate"}, Dependencies{Stderr: &stderr})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), `unknown command "frobnicate"`) {
		t.Fatalf("stderr = %q, want unknown command", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestHelpFlags(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"--help", "-h", "help"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := Run([]string{arg}, Dependencies{Stdout: &stdout, Stderr: &stderr})
			if code != ExitOK {
				t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitOK, stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Fatalf("stdout = %q, want usage", stdout.String())
			}
		})
	}
}

func TestInjectedStubCommandsReturnUnimplemented(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "install", args: []string{"install"}, want: installer.ErrNotImplemented.Error()},
		{name: "uninstall", args: []string{"uninstall"}, want: installer.ErrNotImplemented.Error()},
		{name: "run", args: []string{"run"}, want: service.ErrNotImplemented.Error()},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := Run(tt.args, Dependencies{
				Stderr:    &stderr,
				Installer: installer.Stub{},
				Runner:    service.Stub{},
			})
			if code != ExitError {
				t.Fatalf("exit = %d, want %d", code, ExitError)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), tt.want)
			}
			if !strings.HasPrefix(stderr.String(), "error: ") {
				t.Fatalf("stderr = %q, want structured error: prefix", stderr.String())
			}
		})
	}
}

func TestStubCommandsRejectExtraArgs(t *testing.T) {
	t.Parallel()

	for _, cmd := range []string{"install", "uninstall", "run"} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := Run([]string{cmd, "--force"}, Dependencies{Stderr: &stderr})
			if code != ExitUsage {
				t.Fatalf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(stderr.String(), "unexpected argument") {
				t.Fatalf("stderr = %q, want unexpected argument", stderr.String())
			}
		})
	}
}

func TestInstallParsesTokenFlag(t *testing.T) {
	t.Parallel()
	fi := &fakeInstaller{}
	var stdout bytes.Buffer
	code := Run([]string{"install", "--token", "backend-secret"}, Dependencies{
		Stdout: &stdout, Installer: fi, Runner: &fakeRunner{},
	})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !fi.lastOpts.TokenSet || fi.lastOpts.Token != "backend-secret" {
		t.Fatalf("opts = %+v", fi.lastOpts)
	}
	if !strings.Contains(stdout.String(), "machine_id=") || !strings.Contains(stdout.String(), "token=(set from --token)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInstallRejectsEmptyTokenFlag(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	code := Run([]string{"install", "--token", ""}, Dependencies{
		Stderr: &stderr, Installer: &fakeInstaller{}, Runner: &fakeRunner{},
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want usage; stderr=%q", code, stderr.String())
	}
}

func TestInstallParsesBackendURLAndHeartbeatInterval(t *testing.T) {
	t.Parallel()
	fi := &fakeInstaller{}
	code := Run([]string{"install", "--backend-url", "http://127.0.0.1:9", "--heartbeat-interval", "30"}, Dependencies{
		Installer: fi, Runner: &fakeRunner{},
	})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !fi.lastOpts.BackendURLSet || fi.lastOpts.BackendURL != "http://127.0.0.1:9" {
		t.Fatalf("opts = %+v", fi.lastOpts)
	}
	if !fi.lastOpts.HeartbeatIntervalSet || fi.lastOpts.HeartbeatIntervalSeconds != 30 {
		t.Fatalf("opts = %+v", fi.lastOpts)
	}
}

func TestInstallRejectsExternalHTTPBackendURL(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	code := Run([]string{"install", "--backend-url", "http://example.com"}, Dependencies{
		Stderr: &stderr, Installer: &fakeInstaller{}, Runner: &fakeRunner{},
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want usage; stderr=%q", code, stderr.String())
	}
}

func TestInstallPrintsGeneratedTokenOnce(t *testing.T) {
	t.Parallel()
	fi := &fakeInstaller{}
	var stdout bytes.Buffer
	code := Run([]string{"install"}, Dependencies{Stdout: &stdout, Installer: fi})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "machine_id=") || !strings.Contains(stdout.String(), "token=generated-token-hex") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

type fakeInstaller struct {
	installErr   error
	uninstallErr error
	installed    bool
	uninstalled  bool
	lastOpts     installer.InstallOptions
	outcome      installer.InstallOutcome
}

func (f *fakeInstaller) Install(_ context.Context, opts installer.InstallOptions) (installer.InstallOutcome, error) {
	f.installed = true
	f.lastOpts = opts
	if f.installErr != nil {
		return installer.InstallOutcome{}, f.installErr
	}
	out := f.outcome
	if out.MachineID == "" {
		out = installer.InstallOutcome{
			MachineID:   "550e8400-e29b-41d4-a716-446655440000",
			Token:       "generated-token-hex",
			TokenStatus: installer.TokenStatusGenerated,
		}
		if opts.TokenSet {
			out.Token = ""
			out.TokenStatus = installer.TokenStatusSupplied
		}
	}
	return out, nil
}

func (f *fakeInstaller) Uninstall(context.Context) error {
	f.uninstalled = true
	return f.uninstallErr
}

type fakeRunner struct {
	err error
	ran bool
}

func (f *fakeRunner) Run(context.Context) error {
	f.ran = true
	return f.err
}

func TestInstallSuccessViaInterface(t *testing.T) {
	t.Parallel()

	fi := &fakeInstaller{}
	var stdout bytes.Buffer
	code := Run([]string{"install"}, Dependencies{Stdout: &stdout, Installer: fi})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !fi.installed {
		t.Fatal("expected Install to be called")
	}
	if !strings.Contains(stdout.String(), "machine_id=") {
		t.Fatalf("stdout missing machine_id: %q", stdout.String())
	}
}

func TestUninstallPropagatesError(t *testing.T) {
	t.Parallel()

	fi := &fakeInstaller{uninstallErr: errors.New("boom")}
	var stderr bytes.Buffer
	code := Run([]string{"uninstall"}, Dependencies{Installer: fi, Stderr: &stderr})
	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("stderr = %q, want boom", stderr.String())
	}
}

func TestRunSuccessViaInterface(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	code := Run([]string{"run"}, Dependencies{Runner: fr})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !fr.ran {
		t.Fatal("expected Run to be called")
	}
}

type fakeApplier struct {
	opts   updater.ApplyOptions
	err    error
	called bool
}

func (f *fakeApplier) Apply(_ context.Context, opts updater.ApplyOptions) error {
	f.called = true
	f.opts = opts
	return f.err
}

func TestApplyUpdateParsesFlags(t *testing.T) {
	t.Parallel()
	fa := &fakeApplier{}
	staged := updater.AllowedStagedExample("cli1")
	code := Run([]string{
		"apply-update",
		"--staged", staged,
		"--binary", `C:\Program Files\DadJoke\dad-joke.exe`,
		"--service", "DadJoke",
		"--health-seconds", "3",
	}, Dependencies{Applier: fa, Runner: &fakeRunner{}, Installer: installer.Stub{}})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !fa.called {
		t.Fatal("Apply not called")
	}
	if fa.opts.StagedExePath == "" || fa.opts.HealthWait != 3*time.Second {
		t.Fatalf("opts = %+v", fa.opts)
	}
}

func TestApplyUpdateRequiresStaged(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	code := Run([]string{"apply-update"}, Dependencies{
		Stderr:    &stderr,
		Applier:   &fakeApplier{},
		Runner:    &fakeRunner{},
		Installer: installer.Stub{},
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestApplyUpdateRejectsArbitraryPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "staged outside allowlist",
			args: []string{"apply-update", "--staged", `C:\Temp\evil.exe`},
		},
		{
			name: "binary override outside allowlist",
			args: []string{
				"apply-update",
				"--staged", updater.AllowedStagedExample("x"),
				"--binary", `C:\Windows\System32\cmd.exe`,
			},
		},
		{
			name: "staged path traversal",
			args: []string{
				"apply-update",
				"--staged", `C:\ProgramData\DadJoke\updates\a\..\..\..\Windows\System32\cmd.exe`,
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fa := &fakeApplier{}
			var stderr bytes.Buffer
			code := Run(tt.args, Dependencies{
				Stderr:    &stderr,
				Applier:   fa,
				Runner:    &fakeRunner{},
				Installer: installer.Stub{},
			})
			if code == ExitOK || fa.called {
				t.Fatalf("expected rejection; exit=%d called=%v stderr=%q", code, fa.called, stderr.String())
			}
		})
	}
}
