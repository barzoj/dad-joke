// Command dad-joke is the Windows parental-control agent binary.
package main

import (
	"os"

	"github.com/barzoj/dad-joke/internal/buildinfo"
	"github.com/barzoj/dad-joke/internal/cli"
	"github.com/barzoj/dad-joke/internal/heartbeat"
	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/service"
	"github.com/barzoj/dad-joke/internal/updater"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Dependencies{
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		Installer: installer.New(),
		Runner: service.NewRunner(service.Options{
			// Updater and heartbeat are strictly independent: backend outages
			// must not stop release polling, and vice versa.
			Agent: service.Group{
				updater.NewPoller(),
				heartbeat.New(),
			},
		}),
	}))
}
