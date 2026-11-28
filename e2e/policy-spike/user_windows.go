//go:build spike && windows

package policyspike

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// testUser is a local Standard User created for one test.
type testUser struct {
	Name     string
	Password string // never logged
	SID      string
}

func createStandardUser(t *testing.T) *testUser {
	t.Helper()
	name := "djspike_" + randomHex(4)
	// net user prompts Y/N when password > 14 chars on some images.
	pass := "Dj!" + randomHex(5) // 3+10=13 chars
	if len(pass) > 14 {
		pass = pass[:14]
	}
	out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
net user %s %s /add | Out-Null
$ErrorActionPreference = 'Continue'
net localgroup Administrators %s /delete 2>&1 | Out-Null
$admins = net localgroup Administrators
if ($admins -match ('(?i)^\s*' + [regex]::Escape(%s) + '\s*$')) {
  throw 'user unexpectedly in Administrators'
}
$sid = (New-Object System.Security.Principal.NTAccount(%s)).Translate([System.Security.Principal.SecurityIdentifier]).Value
Write-Output $sid
`, name, pass, name, quotePS(name), quotePS(name)))
	if err != nil {
		t.Fatalf("create user %s: %v\n%s", name, err, out)
	}
	u := &testUser{Name: name, Password: pass, SID: strings.TrimSpace(out)}
	t.Cleanup(func() {
		cleanupUser(t, u)
	})
	return u
}

func cleanupUser(t testing.TB, u *testUser) {
	if u == nil || u.Name == "" {
		return
	}
	_, _ = powershell(t, fmt.Sprintf(`
$ErrorActionPreference='SilentlyContinue'
# Log off any sessions owned by the user before delete.
$query = query user 2>$null
Get-Process -IncludeUserName -ErrorAction SilentlyContinue |
  Where-Object { $_.UserName -and $_.UserName -like ('*\' + %s) } |
  Stop-Process -Force -ErrorAction SilentlyContinue
net user %s /delete | Out-Null
Remove-Item -LiteralPath ('C:\Users\' + %s) -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath ('C:\Windows\System32\GroupPolicyUsers\' + %s) -Recurse -Force -ErrorAction SilentlyContinue
`, quotePS(u.Name), u.Name, quotePS(u.Name), quotePS(u.SID)))
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loadUserHive(t testing.TB, u *testUser) (hivePath string, unload func()) {
	t.Helper()
	// Ensure profile exists via a network logon / scheduled task touch.
	out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
$u = %s; $p = %s
# Create profile by running a short process as the user (scheduled task).
$task = 'djspike_prof_' + [guid]::NewGuid().ToString('N').Substring(0,8)
$action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument '/c echo ok > %%TEMP%%\djspike_prof.txt'
$principal = New-ScheduledTaskPrincipal -UserId $u -LogonType Password -RunLevel Limited
Register-ScheduledTask -TaskName $task -Action $action -Principal $principal -Force | Out-Null
try {
  Set-ScheduledTask -TaskName $task -User $u -Password $p | Out-Null
  Start-ScheduledTask -TaskName $task
  Start-Sleep -Seconds 2
} finally {
  Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue
}
$sid = %s
$prof = (Get-ItemProperty -Path ('HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\' + $sid) -ErrorAction SilentlyContinue).ProfileImagePath
if (-not $prof) { throw 'profile path missing after task' }
Write-Output $prof
`, quotePS(u.Name), quotePS(u.Password), quotePS(u.SID)))
	if err != nil {
		t.Fatalf("create profile: %v\n%s", err, out)
	}
	prof := strings.TrimSpace(out)
	ntuser := prof + `\NTUSER.DAT`
	mount := `HKU\djspike_` + randomHex(3)
	out, err = powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
reg load %s %s | Out-Null
Write-Output 'ok'
`, quotePS(mount), quotePS(ntuser)))
	if err != nil {
		t.Fatalf("reg load: %v\n%s", err, out)
	}
	unload = func() {
		_, _ = powershell(t, fmt.Sprintf(`
$ErrorActionPreference='SilentlyContinue'
[gc]::Collect(); Start-Sleep -Milliseconds 200
reg unload %s | Out-Null
`, quotePS(mount)))
	}
	t.Cleanup(unload)
	return mount, unload
}
