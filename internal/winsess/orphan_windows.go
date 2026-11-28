//go:build windows

package winsess

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func terminateOrphans(sessionID uint32, exePath string) error {
	if sessionID == 0 || exePath == "" {
		return nil
	}
	installed := filepath.Clean(exePath)
	wantBase := strings.ToLower(filepath.Base(installed))

	procs, err := platformSource{}.Processes(context.Background())
	if err != nil {
		return err
	}

	var snaps []OrphanSnapshot
	for _, p := range procs {
		if p.SessionID != sessionID {
			continue
		}
		if strings.ToLower(p.Exe) != wantBase {
			continue // basename is only a cheap prefilter
		}
		snap, ok := snapshotProcess(p.PID, sessionID)
		if !ok {
			continue
		}
		if !ShouldTerminateOrphan(installed, snap) {
			continue
		}
		snaps = append(snaps, snap)
	}

	for _, snap := range snaps {
		_ = terminateIfStillOrphan(snap)
	}
	return nil
}

func snapshotProcess(pid, sessionID uint32) (OrphanSnapshot, bool) {
	access := uint32(windows.PROCESS_QUERY_INFORMATION | windows.PROCESS_QUERY_LIMITED_INFORMATION)
	h, err := windows.OpenProcess(access, false, pid)
	if err != nil {
		return OrphanSnapshot{}, false
	}
	defer windows.CloseHandle(h)

	image, err := fullImagePath(h)
	if err != nil {
		return OrphanSnapshot{}, false
	}
	cmd, err := processCommandLine(h)
	if err != nil {
		return OrphanSnapshot{}, false
	}
	created, err := processCreationTime(h)
	if err != nil || created == 0 {
		return OrphanSnapshot{}, false
	}
	return OrphanSnapshot{
		PID:          pid,
		SessionID:    sessionID,
		ImagePath:    image,
		CommandLine:  cmd,
		CreationTime: created,
	}, true
}

func terminateIfStillOrphan(snap OrphanSnapshot) error {
	access := uint32(windows.PROCESS_QUERY_INFORMATION | windows.PROCESS_QUERY_LIMITED_INFORMATION |
		windows.PROCESS_TERMINATE | windows.SYNCHRONIZE)
	h, err := windows.OpenProcess(access, false, snap.PID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)

	image, err := fullImagePath(h)
	if err != nil {
		return err
	}
	cmd, err := processCommandLine(h)
	if err != nil {
		return err
	}
	created, err := processCreationTime(h)
	if err != nil {
		return err
	}
	if !OrphanReopenMatches(snap, image, cmd, created) {
		return fmt.Errorf("winsess: orphan PID %d failed re-verify (possible PID reuse)", snap.PID)
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	_, _ = windows.WaitForSingleObject(h, uint32((5 * time.Second).Milliseconds()))
	return nil
}

func fullImagePath(h windows.Handle) (string, error) {
	var buf [windows.MAX_PATH + 1]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}

func processCreationTime(h windows.Handle) (int64, error) {
	var create, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &create, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return create.Nanoseconds(), nil
}

// processCommandLine reads the process command line via
// NtQueryInformationProcess(ProcessCommandLineInformation).
func processCommandLine(h windows.Handle) (string, error) {
	var retLen uint32
	_ = windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, nil, 0, &retLen)
	if retLen == 0 {
		return "", fmt.Errorf("winsess: empty command line size")
	}
	buf := make([]byte, retLen)
	if err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), retLen, &retLen); err != nil {
		return "", err
	}
	if uintptr(retLen) < unsafe.Sizeof(windows.NTUnicodeString{}) {
		return "", fmt.Errorf("winsess: command line buffer too small")
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	if us.Length == 0 || us.Buffer == nil {
		return "", nil
	}
	return us.String(), nil
}
