//go:build windows

package jobs

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"github.com/barzoj/dad-joke/internal/store"
	"golang.org/x/sys/windows"
)

// JobObjectExecutor runs powershell/cmd inside a Windows Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so the entire process tree dies on
// timeout or handle close.
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

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...) // #nosec G204 -- payload is an authorized remote job
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}

	stdout := newCappedWriter(MaxOutputBytes)
	stderr := newCappedWriter(MaxOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	jobHandle, err := createKillOnCloseJob()
	if err != nil {
		return ExecResult{}, err
	}
	// KILL_ON_JOB_CLOSE: closing this handle terminates the whole process tree.
	defer func() { _ = windows.CloseHandle(jobHandle) }()

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return ExecResult{}, fmt.Errorf("jobs: start: %w", err)
	}
	if err := assignProcessToJob(jobHandle, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return ExecResult{}, fmt.Errorf("jobs: assign job object: %w", err)
	}

	waitErr := cmd.Wait()
	dur := time.Since(start).Milliseconds()
	timedOut := runCtx.Err() == context.DeadlineExceeded

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if waitErr != nil && !timedOut {
		exitCode = 1
	}
	if timedOut {
		exitCode = -1
	}

	return ExecResult{
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		TimedOut:   timedOut,
		DurationMS: dur,
	}, nil
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

func assignProcessToJob(job windows.Handle, pid int) error {
	p, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(p)
	return windows.AssignProcessToJobObject(job, p)
}

// DefaultExecutor returns the Windows Job Object executor.
func DefaultExecutor() Executor {
	return JobObjectExecutor{}
}
