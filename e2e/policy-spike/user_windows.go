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
	// Alphanumeric only, ≤14 chars. Avoid ConvertTo-SecureString — the
	// Microsoft.PowerShell.Security module often fails to autoload under the
	// hosted runner's PowerShell constraints.
	pass := "Dj9" + randomHex(5) // 13 chars
	if len(pass) > 14 {
		pass = pass[:14]
	}
	out, err := powershell(t, fmt.Sprintf(`
$name = %s
$plain = %s
# PS7 treats native-command stderr as errors under Stop; delete is best-effort.
$ErrorActionPreference = 'Continue'
cmd.exe /c ('net user ' + $name + ' /delete >nul 2>nul') | Out-Null
	$ErrorActionPreference = 'Stop'
# net user accepts a plaintext password without the Security module.
$add = 'net user ' + $name + ' ' + $plain + ' /add'
$net = cmd.exe /c $add 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) { throw ("net user /add failed: " + $net) }
# 1377 = not a member of Administrators (expected for net user /add).
$ErrorActionPreference = 'Continue'
cmd.exe /c ('net localgroup Administrators ' + $name + ' /delete') 2>&1 | Out-Null
$ErrorActionPreference = 'Stop'
$admins = net localgroup Administrators | Out-String
if ($admins -match ('(?im)^\s*' + [regex]::Escape($name) + '\s*$')) {
  throw 'user unexpectedly in Administrators'
}
$sid = (New-Object System.Security.Principal.NTAccount($name)).Translate([System.Security.Principal.SecurityIdentifier]).Value
Write-Output $sid
`, quotePS(name), quotePS(pass)))
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
Get-Process -IncludeUserName -ErrorAction SilentlyContinue |
  Where-Object { $_.UserName -and ($_.UserName -like ('*\' + %s) -or $_.UserName -eq %s) } |
  Stop-Process -Force -ErrorAction SilentlyContinue
net user %s /delete 2>$null | Out-Null
Remove-Item -LiteralPath ('C:\Users\' + %s) -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath ('C:\Windows\System32\GroupPolicyUsers\' + %s) -Recurse -Force -ErrorAction SilentlyContinue
`, quotePS(u.Name), quotePS(u.Name), u.Name, quotePS(u.Name), quotePS(u.SID)))
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ensureUserProfile creates the local profile (ProfileList + NTUSER.DAT) via
// LogonUser + LoadUserProfile, then unloads it. Idempotent.
func ensureUserProfile(t testing.TB, u *testUser) string {
	t.Helper()
	out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public class NativeProfile {
  [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
  public struct PROFILEINFO {
    public int dwSize;
    public int dwFlags;
    public string lpUserName;
    public string lpProfilePath;
    public string lpDefaultPath;
    public string lpServerName;
    public string lpPolicyPath;
    public IntPtr hProfile;
  }
  [DllImport("advapi32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
  public static extern bool LogonUser(string user, string domain, string pass, int type, int provider, out IntPtr token);
  [DllImport("userenv.dll", SetLastError=true, CharSet=CharSet.Unicode)]
  public static extern bool LoadUserProfile(IntPtr hToken, ref PROFILEINFO lpProfileInfo);
  [DllImport("userenv.dll", SetLastError=true)]
  public static extern bool UnloadUserProfile(IntPtr hToken, IntPtr hProfile);
  [DllImport("kernel32.dll", SetLastError=true)]
  public static extern bool CloseHandle(IntPtr h);
}
"@
$u = %s; $p = %s; $sid = %s
$existing = Get-ItemProperty -Path ('HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\' + $sid) -ErrorAction SilentlyContinue
if ($existing -and $existing.ProfileImagePath -and (Test-Path -LiteralPath (Join-Path $existing.ProfileImagePath 'NTUSER.DAT'))) {
  Write-Output $existing.ProfileImagePath
  exit 0
}
$token = [IntPtr]::Zero
if (-not [NativeProfile]::LogonUser($u, '.', $p, 2, 0, [ref]$token)) {
  if (-not [NativeProfile]::LogonUser($u, '.', $p, 3, 0, [ref]$token)) {
    throw "LogonUser failed: $([ComponentModel.Win32Exception]::new([Runtime.InteropServices.Marshal]::GetLastWin32Error()).Message)"
  }
}
$pi = New-Object NativeProfile+PROFILEINFO
$pi.dwSize = [Runtime.InteropServices.Marshal]::SizeOf($pi)
$pi.lpUserName = $u
if (-not [NativeProfile]::LoadUserProfile($token, [ref]$pi)) {
  $err = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
  [NativeProfile]::CloseHandle($token) | Out-Null
  throw "LoadUserProfile failed: $([ComponentModel.Win32Exception]::new($err).Message)"
}
[NativeProfile]::UnloadUserProfile($token, $pi.hProfile) | Out-Null
[NativeProfile]::CloseHandle($token) | Out-Null
$prof = (Get-ItemProperty -Path ('HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\' + $sid)).ProfileImagePath
if (-not $prof) { throw 'profile path missing' }
Write-Output $prof
`, quotePS(u.Name), quotePS(u.Password), quotePS(u.SID)))
	if err != nil {
		t.Fatalf("ensure profile: %v\n%s", err, out)
	}
	return strings.TrimSpace(out)
}

func loadUserHive(t testing.TB, u *testUser) (hivePath string, unload func()) {
	t.Helper()
	prof := ensureUserProfile(t, u)
	ntuser := prof + `\NTUSER.DAT`
	mount := `HKU\djspike_` + randomHex(3)
	out, err := powershell(t, fmt.Sprintf(`
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
[gc]::Collect(); Start-Sleep -Milliseconds 300
reg unload %s | Out-Null
`, quotePS(mount)))
	}
	t.Cleanup(unload)
	return mount, unload
}
