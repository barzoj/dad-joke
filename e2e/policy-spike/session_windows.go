//go:build spike && windows

package policyspike

import (
	"fmt"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modWtsapi32                     = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW = modWtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory               = modWtsapi32.NewProc("WTSFreeMemory")
	procWTSLogoffSession            = modWtsapi32.NewProc("WTSLogoffSession")
	procWTSDisconnectSession        = modWtsapi32.NewProc("WTSDisconnectSession")
)

const (
	wtsUserName = 5
)

func enumerateSessions(t testing.TB) []windows.WTS_SESSION_INFO {
	t.Helper()
	var info *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &info, &count); err != nil {
		t.Fatalf("WTSEnumerateSessions: %v", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	if count == 0 || info == nil {
		return nil
	}
	return append([]windows.WTS_SESSION_INFO(nil), unsafe.Slice(info, count)...)
}

func sessionUserName(sessionID uint32) (string, error) {
	var buf *uint16
	var bytes uint32
	r1, _, e1 := procWTSQuerySessionInformationW.Call(
		0,
		uintptr(sessionID),
		uintptr(wtsUserName),
		uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&bytes)),
	)
	if r1 == 0 {
		return "", e1
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf), nil
}

func findSessionsForUser(t testing.TB, username string) []uint32 {
	t.Helper()
	var ids []uint32
	for _, s := range enumerateSessions(t) {
		name, err := sessionUserName(s.SessionID)
		if err != nil {
			continue
		}
		if equalUser(name, username) {
			ids = append(ids, s.SessionID)
		}
	}
	return ids
}

func equalUser(a, b string) bool {
	// session name may be DOMAIN\user or just user
	a = lastSlash(a)
	b = lastSlash(b)
	return len(a) > 0 && len(b) > 0 &&
		(a == b || equalFoldASCII(a, b))
}

func lastSlash(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\\' || s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func wtsLogoff(sessionID uint32) error {
	r1, _, e1 := procWTSLogoffSession.Call(0, uintptr(sessionID), 0)
	if r1 == 0 {
		return fmt.Errorf("WTSLogoffSession(%d): %v", sessionID, e1)
	}
	return nil
}

func wtsDisconnect(sessionID uint32) error {
	r1, _, e1 := procWTSDisconnectSession.Call(0, uintptr(sessionID), 0)
	if r1 == 0 {
		return fmt.Errorf("WTSDisconnectSession(%d): %v", sessionID, e1)
	}
	return nil
}

// ensureInteractiveSession attempts to create a logged-on session for the test
// user via an interactive scheduled task. Hosted runners may not produce a
// queryable WTS session; callers must assert the observed outcome.
func ensureInteractiveSession(t *testing.T, u *testUser) (sessionIDs []uint32, method string) {
	t.Helper()
	task := "djspike_sess_" + randomHex(4)
	_, _ = powershell(t, fmt.Sprintf(`
$ErrorActionPreference='SilentlyContinue'
$task = %s; $u = %s; $p = %s
Unregister-ScheduledTask -TaskName $task -Confirm:$false
$action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument '/c ping -n 30 127.0.0.1 >nul'
# Interactive token when possible.
try {
  $principal = New-ScheduledTaskPrincipal -UserId $u -LogonType Interactive -RunLevel Limited
  Register-ScheduledTask -TaskName $task -Action $action -Principal $principal -Force | Out-Null
  Set-ScheduledTask -TaskName $task -User $u -Password $p | Out-Null
} catch {
  $principal = New-ScheduledTaskPrincipal -UserId $u -LogonType Password -RunLevel Limited
  Register-ScheduledTask -TaskName $task -Action $action -Principal $principal -Force | Out-Null
  Set-ScheduledTask -TaskName $task -User $u -Password $p | Out-Null
}
Start-ScheduledTask -TaskName $task
Start-Sleep -Seconds 3
`, quotePS(task), quotePS(u.Name), quotePS(u.Password)))
	t.Cleanup(func() {
		_, _ = powershell(t, fmt.Sprintf(`
$ErrorActionPreference='SilentlyContinue'
Unregister-ScheduledTask -TaskName %s -Confirm:$false
`, quotePS(task)))
	})
	ids := findSessionsForUser(t, u.Name)
	return ids, "scheduled-task-interactive-or-password"
}
