// Package cli implements the dad-joke command-line contract.
//
// Subcommands (v0.1):
//
//	version                 print embedded Version/Commit and exit 0
//	install                 install Windows Service (Issue #2)
//	uninstall               uninstall Windows Service (Issue #2)
//	run                     Windows Service entrypoint (Issues #2/#3)
//	apply-update            detached helper: replace binary, restart, health, rollback (Issue #3)
//
// Exit codes:
//
//	0  success
//	1  runtime / unimplemented error
//	2  usage error (unknown command, bad args, --help requesting usage)
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/internal/buildinfo"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/service"
	"github.com/barzoj/dad-joke/internal/updater"
)

// Exit codes for the CLI process.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Dependencies wires I/O and domain interfaces into the CLI.
type Dependencies struct {
	Stdout    io.Writer
	Stderr    io.Writer
	Version   string
	Commit    string
	Installer installer.Installer
	Runner    service.Runner
	// Applier runs apply-update; nil uses the production Applier.
	Applier UpdateApplier
}

// UpdateApplier is the apply-update contract (Issue #3 helper).
type UpdateApplier interface {
	Apply(ctx context.Context, opts updater.ApplyOptions) error
}

// usageText is the canonical help / usage string.
const usageText = `Usage: dad-joke <command> [flags]

Commands:
  version                 Print embedded build version and commit
  install [--token SECRET] Install the DadJoke Windows Service (Administrator required)
  uninstall               Uninstall the DadJoke Windows Service (Administrator required)
  run                     Run as the DadJoke Windows Service
  apply-update            Apply a verified staged binary (internal helper)

Pass --help or -h for this message.
`

// Run parses args (typically os.Args[1:]) and dispatches a subcommand.
func Run(args []string, deps Dependencies) int {
	if deps.Stdout == nil {
		deps.Stdout = io.Discard
	}
	if deps.Stderr == nil {
		deps.Stderr = io.Discard
	}
	if deps.Installer == nil {
		deps.Installer = installer.New()
	}
	if deps.Runner == nil {
		deps.Runner = service.NewRunner(service.Options{Agent: updater.NewPoller()})
	}
	if deps.Applier == nil {
		deps.Applier = &updater.Applier{
			Store:   updater.OSStore{},
			Service: updater.OSService{},
			Clock:   updater.RealClock{},
			Log:     updater.WriterLogger{W: deps.Stderr},
		}
	}

	if len(args) == 0 {
		return usage(deps.Stderr, "missing command")
	}

	cmd := args[0]
	rest := args[1:]

	if isHelp(cmd) {
		_, _ = io.WriteString(deps.Stdout, usageText)
		return ExitOK
	}

	switch cmd {
	case "version":
		return runVersion(rest, deps)
	case "install":
		return runInstall(rest, deps)
	case "uninstall":
		return runUninstall(rest, deps)
	case "run":
		return runRun(rest, deps)
	case "apply-update":
		return runApplyUpdate(rest, deps)
	default:
		return usage(deps.Stderr, fmt.Sprintf("unknown command %q", cmd))
	}
}

func runVersion(args []string, deps Dependencies) int {
	if err := rejectExtra(args); err != nil {
		return usage(deps.Stderr, err.Error())
	}
	line := buildinfo.FormatVersion(deps.Version, deps.Commit)
	_, _ = fmt.Fprintln(deps.Stdout, line)
	return ExitOK
}

func runInstall(args []string, deps Dependencies) int {
	opts, err := parseInstallArgs(args)
	if err != nil {
		return usage(deps.Stderr, err.Error())
	}
	out, err := deps.Installer.Install(context.Background(), opts)
	if err != nil {
		return writeError(deps.Stderr, err)
	}
	printInstallOutcome(deps.Stdout, out)
	return ExitOK
}

func parseInstallArgs(args []string) (installer.InstallOptions, error) {
	var opts installer.InstallOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		if isHelp(a) {
			return opts, fmt.Errorf("unexpected argument %q", a)
		}
		switch a {
		case "--token":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--token requires a value")
			}
			i++
			opts.TokenSet = true
			opts.Token = args[i]
		default:
			return opts, fmt.Errorf("unexpected argument %q", a)
		}
	}
	if opts.TokenSet {
		if err := config.ValidateTokenArg(opts.Token); err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func printInstallOutcome(w io.Writer, out installer.InstallOutcome) {
	if out.MachineID == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "machine_id=%s\n", out.MachineID)
	switch out.TokenStatus {
	case installer.TokenStatusGenerated:
		_, _ = fmt.Fprintf(w, "token=%s\n", out.Token)
	case installer.TokenStatusSupplied:
		_, _ = fmt.Fprintf(w, "token=(set from --token)\n")
	case installer.TokenStatusPreserved:
		_, _ = fmt.Fprintf(w, "token=(preserved)\n")
	}
}

func runUninstall(args []string, deps Dependencies) int {
	if err := rejectExtra(args); err != nil {
		return usage(deps.Stderr, err.Error())
	}
	if err := deps.Installer.Uninstall(context.Background()); err != nil {
		return writeError(deps.Stderr, err)
	}
	return ExitOK
}

func runRun(args []string, deps Dependencies) int {
	if err := rejectExtra(args); err != nil {
		return usage(deps.Stderr, err.Error())
	}
	if err := deps.Runner.Run(context.Background()); err != nil {
		return writeError(deps.Stderr, err)
	}
	return ExitOK
}

func runApplyUpdate(args []string, deps Dependencies) int {
	opts, err := parseApplyUpdateArgs(args)
	if err != nil {
		return usage(deps.Stderr, err.Error())
	}
	if err := deps.Applier.Apply(context.Background(), opts); err != nil {
		return writeError(deps.Stderr, err)
	}
	return ExitOK
}

func parseApplyUpdateArgs(args []string) (updater.ApplyOptions, error) {
	paths := installer.DefaultPaths()
	opts := updater.ApplyOptions{
		ServiceName:  installer.ServiceName,
		BinaryPath:   paths.BinaryPath,
		DataDir:      paths.DataDir,
		HealthWait:   updater.DefaultHealthWait,
		StopTimeout:  updater.DefaultStopTimeout,
		StartTimeout: updater.DefaultStartTimeout,
	}
	binaryOverride := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if isHelp(a) {
			return opts, fmt.Errorf("unexpected argument %q", a)
		}
		switch a {
		case "--staged":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--staged requires a path")
			}
			i++
			opts.StagedExePath = args[i]
		case "--binary":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--binary requires a path")
			}
			i++
			binaryOverride = args[i]
		case "--service":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--service requires a name")
			}
			i++
			opts.ServiceName = args[i]
		case "--health-seconds":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--health-seconds requires an integer")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				return opts, fmt.Errorf("invalid --health-seconds %q", args[i])
			}
			opts.HealthWait = time.Duration(n) * time.Second
		default:
			return opts, fmt.Errorf("unexpected argument %q", a)
		}
	}
	if opts.StagedExePath == "" {
		return opts, fmt.Errorf("--staged is required")
	}
	if binaryOverride != "" {
		opts.BinaryPath = binaryOverride
	}
	// Fail closed: CLI never skips the allowlist. Tests that need arbitrary
	// paths inject ApplyOptions via the UpdateApplier interface instead.
	if err := updater.ValidateApplyPaths(opts.StagedExePath, opts.BinaryPath, opts.DataDir, paths.BinaryPath); err != nil {
		return opts, err
	}
	return opts, nil
}

func rejectExtra(args []string) error {
	for _, a := range args {
		if isHelp(a) {
			return fmt.Errorf("unexpected argument %q", a)
		}
	}
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	return nil
}

func isHelp(s string) bool {
	switch strings.ToLower(s) {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

func usage(w io.Writer, reason string) int {
	if reason != "" {
		_, _ = fmt.Fprintf(w, "error: %s\n\n", reason)
	}
	_, _ = io.WriteString(w, usageText)
	return ExitUsage
}

func writeError(w io.Writer, err error) int {
	_, _ = fmt.Fprintf(w, "error: %s\n", err.Error())
	return ExitError
}
