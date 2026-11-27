//go:build windows

package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// CREATE_BREAKAWAY_FROM_JOB allows the helper to escape a job object that
// would otherwise be torn down when the service process exits.
const createBreakawayFromJob = 0x01000000

// OSSpawner launches a fully detached Windows helper process.
type OSSpawner struct{}

// StartDetached implements Spawner using CREATE_NEW_PROCESS_GROUP |
// DETACHED_PROCESS so the helper survives service stop. Prefers
// CREATE_BREAKAWAY_FROM_JOB when the process job allows it; falls back if
// CreateProcess returns access denied.
func (OSSpawner) StartDetached(ctx context.Context, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	argv := make([]string, 0, 1+len(args))
	argv = append(argv, name)
	argv = append(argv, args...)

	exe, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return err
	}

	dir, _ := os.Getwd()
	var dirPtr *uint16
	if dir != "" {
		dirPtr, _ = windows.UTF16PtrFromString(dir)
	}

	base := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS)
	if err := createDetached(exe, cmdLine, dirPtr, base|createBreakawayFromJob); err != nil {
		if isAccessDenied(err) {
			// Job may disallow breakaway; retry without it.
			if err2 := createDetached(exe, cmdLine, dirPtr, base); err2 != nil {
				return fmt.Errorf("CreateProcess: %w", err2)
			}
			return nil
		}
		return fmt.Errorf("CreateProcess: %w", err)
	}
	return nil
}

func createDetached(exe, cmdLine, dir *uint16, flags uint32) error {
	var si windows.StartupInfo
	var pi windows.ProcessInformation
	err := windows.CreateProcess(
		exe,
		cmdLine,
		nil,
		nil,
		false,
		flags,
		nil,
		dir,
		&si,
		&pi,
	)
	if err != nil {
		return err
	}
	_ = windows.CloseHandle(pi.Thread)
	_ = windows.CloseHandle(pi.Process)
	return nil
}

func isAccessDenied(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, syscall.EACCES)
}
