//go:build spike && windows

package policyspike

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	logonInteractive = 2
	logonNetwork     = 3
	providerDefault  = 0

	createUnicodeEnvironment = 0x00000400
	createNewConsole         = 0x00000010
)

var (
	modAdvapi32                 = windows.NewLazySystemDLL("advapi32.dll")
	procLogonUserW              = modAdvapi32.NewProc("LogonUserW")
	procImpersonateLoggedOnUser = modAdvapi32.NewProc("ImpersonateLoggedOnUser")
)

func impersonateLoggedOnUser(token windows.Token) error {
	r1, _, e1 := syscall.Syscall(procImpersonateLoggedOnUser.Addr(), 1, uintptr(token), 0, 0)
	if r1 == 0 {
		if e1 != 0 {
			return error(e1)
		}
		return syscall.EINVAL
	}
	return nil
}

func logonUser(user, domain, pass string, logonType uint32) (windows.Token, error) {
	u, err := syscall.UTF16PtrFromString(user)
	if err != nil {
		return 0, err
	}
	d, err := syscall.UTF16PtrFromString(domain)
	if err != nil {
		return 0, err
	}
	p, err := syscall.UTF16PtrFromString(pass)
	if err != nil {
		return 0, err
	}
	var token windows.Handle
	r1, _, e1 := syscall.Syscall6(
		procLogonUserW.Addr(),
		6,
		uintptr(unsafe.Pointer(u)),
		uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(p)),
		uintptr(logonType),
		uintptr(providerDefault),
		uintptr(unsafe.Pointer(&token)),
	)
	if r1 == 0 {
		if e1 != 0 {
			return 0, error(e1)
		}
		return 0, syscall.EINVAL
	}
	return windows.Token(token), nil
}

// logonUserResult returns the token or the raw errno from LogonUser.
func logonUserResult(user, pass string, logonType uint32) (windows.Token, error) {
	return logonUser(user, ".", pass, logonType)
}

func runAsUser(t testing.TB, u *testUser, exe string, args []string, wait time.Duration) (exitCode int, stdout string, err error) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	token, err := logonUser(u.Name, ".", u.Password, logonInteractive)
	if err != nil {
		token, err = logonUser(u.Name, ".", u.Password, logonNetwork)
		if err != nil {
			return -1, "", fmt.Errorf("LogonUser: %w", err)
		}
	}
	defer token.Close()

	if err := impersonateLoggedOnUser(token); err != nil {
		// Continue without impersonation for CreateProcessAsUser; still need primary token.
		t.Logf("ImpersonateLoggedOnUser: %v (continuing)", err)
	} else {
		defer windows.RevertToSelf()
	}

	var primary windows.Token
	if err := windows.DuplicateTokenEx(token, windows.MAXIMUM_ALLOWED, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return -1, "", fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer primary.Close()

	outDir := t.TempDir()
	outFile := filepath.Join(outDir, "out.txt")
	cmdline := buildCmdline(exe, args, outFile)

	appName, _ := syscall.UTF16PtrFromString(exe)
	cmdPtr, _ := syscall.UTF16PtrFromString(cmdline)

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation

	err = windows.CreateProcessAsUser(
		primary,
		appName,
		cmdPtr,
		nil,
		nil,
		false,
		createUnicodeEnvironment|createNewConsole,
		nil,
		nil,
		&si,
		&pi,
	)
	if err != nil {
		// Fallback: scheduled task as user (more reliable for profile/env).
		return runAsUserViaTask(t, u, exe, args, wait)
	}
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)

	ev, err := windows.WaitForSingleObject(pi.Process, uint32(wait.Milliseconds()))
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return -1, "", err
	}
	if ev == uint32(windows.WAIT_TIMEOUT) {
		_ = windows.TerminateProcess(pi.Process, 1)
		return -1, "", fmt.Errorf("timeout waiting for process")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(pi.Process, &code); err != nil {
		return -1, "", err
	}
	b, _ := os.ReadFile(outFile)
	return int(code), string(b), nil
}

func buildCmdline(exe string, args []string, outFile string) string {
	// Wrap with cmd to capture exit and redirect.
	parts := []string{quoteArg(exe)}
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	inner := strings.Join(parts, " ")
	return fmt.Sprintf(`cmd.exe /C %s > %s 2>&1`, quoteArg(inner), quoteArg(outFile))
}

func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func runAsUserViaTask(t testing.TB, u *testUser, exe string, args []string, wait time.Duration) (int, string, error) {
	t.Helper()
	outFile := filepath.Join(t.TempDir(), "task_out.txt")
	argLine := ""
	for _, a := range args {
		argLine += " " + quoteArg(a)
	}
	task := "djspike_run_" + randomHex(4)
	script := fmt.Sprintf(`
$ErrorActionPreference='Stop'
$task = %s
$u = %s; $p = %s
$exe = %s
$arg = %s
$out = %s
$action = New-ScheduledTaskAction -Execute $exe -Argument $arg
$principal = New-ScheduledTaskPrincipal -UserId $u -LogonType Password -RunLevel Limited
Register-ScheduledTask -TaskName $task -Action $action -Principal $principal -Force | Out-Null
try {
  Set-ScheduledTask -TaskName $task -User $u -Password $p | Out-Null
  # Redirect via cmd wrapper
  Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue
  $wrap = '/C "' + $exe + ' ' + $arg + ' > "' + $out + '" 2>&1 & echo EXITCODE=%%ERRORLEVEL%%>> "' + $out + '"'
  $action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument $wrap
  Register-ScheduledTask -TaskName $task -Action $action -Principal $principal -Force | Out-Null
  Set-ScheduledTask -TaskName $task -User $u -Password $p | Out-Null
  Start-ScheduledTask -TaskName $task
  $deadline = (Get-Date).AddSeconds(%d)
  do {
    Start-Sleep -Milliseconds 200
    $i = Get-ScheduledTaskInfo -TaskName $task
  } while ($i.LastTaskResult -eq 267009 -and (Get-Date) -lt $deadline)
  Start-Sleep -Milliseconds 300
  if (Test-Path -LiteralPath $out) { Get-Content -LiteralPath $out -Raw } else { Write-Output 'NO_OUT' }
} finally {
  Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue
}
`, quotePS(task), quotePS(u.Name), quotePS(u.Password), quotePS(exe), quotePS(strings.TrimSpace(argLine)), quotePS(outFile), int(wait.Seconds())+5)
	out, err := powershell(t, script)
	if err != nil {
		return -1, out, err
	}
	code := 0
	if strings.Contains(out, "EXITCODE=") {
		fmt.Sscanf(out[strings.LastIndex(out, "EXITCODE="):], "EXITCODE=%d", &code)
	}
	return code, out, nil
}

func tryLaunchAsUser(t testing.TB, u *testUser, exe string) (started bool, detail string) {
	t.Helper()
	code, out, err := runAsUser(t, u, exe, nil, 8*time.Second)
	if err != nil {
		return false, err.Error()
	}
	// Non-zero exit or empty may still mean process started then exited due to policy UI.
	detail = fmt.Sprintf("exit=%d out=%q", code, truncate(out, 200))
	started = true
	return started, detail
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
