// Command dad-joke is the Windows parental-control agent binary.
package main

import (
	"os"

	"github.com/barzoj/dad-joke/internal/buildinfo"
	"github.com/barzoj/dad-joke/internal/cli"
	"github.com/barzoj/dad-joke/internal/heartbeat"
	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/jobs"
	"github.com/barzoj/dad-joke/internal/service"
	"github.com/barzoj/dad-joke/internal/updater"
)

func main() {
	handoff := jobs.NewHandoff()
	os.Exit(cli.Run(os.Args[1:], cli.Dependencies{
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		Installer: installer.New(),
		Runner: service.NewRunner(service.Options{
			// Updater, heartbeat, and job worker are strictly independent:
			// a slow/timed-out job must never stall heartbeats or updates.
			Agent: service.Group{
				updater.NewPoller(),
				heartbeat.New(handoff),
				jobs.New(handoff),
			},
		}),
	}))
}
