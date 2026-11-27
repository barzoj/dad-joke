//go:build windows

package jobs

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
	"unsafe"

	"github.com/barzoj/dad-joke/internal/store"
	"golang.org/x/sys/windows"
)

// JobObjectExecutor runs powershell/cmd inside a Windows Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so the entire process tree dies on
// timeout or handle close.
//
// Processes are created with CREATE_SUSPENDED, assigned to the job, then
// resumed — so no grandchild can spawn before job membership is established.
type JobObjectExecutor struct{}

// Run implements Executor.
func (JobObjectExecutor) Run(ctx context.Context, jobType, payload string, timeout time.Duration) (ExecResult, error) {
	if err := ValidateClaimed(jobType, payload); err != nil {
		return ExecResult{}, err
	}
	if timeout <= 0 {
		timeout = ClampTimeout(nil)
	}

	argv, err := buildCommand(jobType, payload)
	if err != nil {
		return ExecResult{}, err
	}
	exe, err := exec.LookPath(argv[0])
	if err != nil {
		return ExecResult{}, fmt.Errorf("jobs: look path %q: %w", argv[0], err)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return ExecResult{}, fmt.Errorf("jobs: stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return ExecResult{}, fmt.Errorf("jobs: stderr pipe: %w", err)
	}

	// Child must inherit the write ends; parent keeps non-inheritable read ends.
	if err := windows.SetHandleInformation(windows.Handle(stdoutW.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		closeFiles(stdoutR, stdoutW, stderrR, stderrW)
		return ExecResult{}, fmt.Errorf("jobs: stdout inherit: %w", err)
	}
	if err := windows.SetHandleInformation(windows.Handle(stderrW.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		closeFiles(stdoutR, stdoutW, stderrR, stderrW)
		return ExecResult{}, fmt.Errorf("jobs: stderr inherit: %w", err)
	}

	nul, err := openNUL()
	if err != nil {
		closeFiles(stdoutR, stdoutW, stderrR, stderrW)
		return ExecResult{}, err
	}
	defer func() { _ = windows.CloseHandle(nul) }()

	jobHandle, err := createKillOnCloseJob()
	if err != nil {
		closeFiles(stdoutR, stdoutW, stderrR, stderrW)
		return ExecResult{}, err
	}
	jobOpen := true
	defer func() {
		if jobOpen {
			_ = windows.CloseHandle(jobHandle)
		}
	}()

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = nul
	si.StdOutput = windows.Handle(stdoutW.Fd())
	si.StdErr = windows.Handle(stderrW.Fd())

	appName, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		closeFiles(stdoutR, stdoutW, stderrR, stderrW)
		return ExecResult{}, err
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		closeFiles(stdoutR, stdoutW, stderrR, stderrW)
		return ExecResult{}, err
	}

	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_UNICODE_ENVIRONMENT)
	// #nosec G204 -- payload is an authorized remote job from the control plane
	err = windows.CreateProcess(
		appName,
		cmdLine,
		nil,
		nil,
		true, // inherit pipe write ends
		flags,
		nil,
		nil,
		&si,
		&pi,
	)
	// Parent must close write ends so readers see EOF when the child exits.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	if err != nil {
		_ = stdoutR.Close()
		_ = stderrR.Close()
		return ExecResult{}, fmt.Errorf("jobs: CreateProcess: %w", err)
	}

	// Assign while suspended — grandchildren cannot escape the job.
	if err := windows.AssignProcessToJobObject(jobHandle, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(pi.Process)
		_ = stdoutR.Close()
		_ = stderrR.Close()
		return ExecResult{}, fmt.Errorf("jobs: AssignProcessToJobObject: %w", err)
	}

	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateJobObject(jobHandle, 1)
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(pi.Process)
		_ = stdoutR.Close()
		_ = stderrR.Close()
		return ExecResult{}, fmt.Errorf("jobs: ResumeThread: %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)

	stdoutCap := newCappedWriter(MaxOutputBytes)
	stderrCap := newCappedWriter(MaxOutputBytes)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stdoutCap, stdoutR)
		_ = stdoutR.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stderrCap, stderrR)
		_ = stderrR.Close()
	}()

	start := time.Now()
	timedOut := waitProcess(runCtx, pi.Process)

	if timedOut {
		// Kill the entire tree immediately; KILL_ON_JOB_CLOSE is the safety net on close.
		_ = windows.TerminateJobObject(jobHandle, 1)
		_, _ = windows.WaitForSingleObject(pi.Process, 5000)
	}

	var exitCode uint32
	_ = windows.GetExitCodeProcess(pi.Process, &exitCode)
	_ = windows.CloseHandle(pi.Process)

	_ = windows.CloseHandle(jobHandle)
	jobOpen = false

	wg.Wait()
	dur := time.Since(start).Milliseconds()

	code := int(exitCode)
	if timedOut {
		code = -1
	}

	return ExecResult{
		ExitCode:   code,
		Stdout:     stdoutCap.String(),
		Stderr:     stderrCap.String(),
		TimedOut:   timedOut,
		DurationMS: dur,
	}, nil
}

func waitProcess(ctx context.Context, proc windows.Handle) (timedOut bool) {
	const sliceMS = 100
	for {
		if err := ctx.Err(); err != nil {
			return true
		}
		ev, err := windows.WaitForSingleObject(proc, sliceMS)
		if err != nil {
			return true
		}
		if ev == windows.WAIT_OBJECT_0 {
			return false
		}
		// WAIT_TIMEOUT — keep polling until ctx deadline/cancel.
	}
}

func buildCommand(jobType, payload string) ([]string, error) {
	switch jobType {
	case store.JobTypePowerShell:
		return []string{
			"powershell.exe",
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy", "Bypass",
			"-Command", payload,
		}, nil
	case store.JobTypeCmd:
		return []string{"cmd.exe", "/C", payload}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, jobType)
	}
}

func createKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(h)
		return 0, err
	}
	return h, nil
}

func openNUL() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(`NUL`)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("jobs: open NUL: %w", err)
	}
	return h, nil
}

func closeFiles(files ...*os.File) {
	for _, f := range files {
		if f != nil {
			_ = f.Close()
		}
	}
}

// DefaultExecutor returns the Windows Job Object executor.
func DefaultExecutor() Executor {
	return JobObjectExecutor{}
}
