package jobs

import (
	"fmt"

	"github.com/barzoj/dad-joke/internal/store"
)

// buildCommand returns the argv for a claimed job interpreter.
// PowerShell is invoked with -InputFormat None so a redirected/NUL stdin
// cannot leave powershell.exe blocked waiting for more input after -Command
// (a common Session 0 / service-host hang).
func buildCommand(jobType, payload string) ([]string, error) {
	switch jobType {
	case store.JobTypePowerShell:
		return []string{
			"powershell.exe",
			"-NoProfile",
			"-NonInteractive",
			"-InputFormat", "None",
			"-ExecutionPolicy", "Bypass",
			"-Command", payload,
		}, nil
	case store.JobTypeCmd:
		return []string{"cmd.exe", "/C", payload}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, jobType)
	}
}
