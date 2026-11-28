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
	// Alphanumeric only — avoids net-user / scheduled-task quoting pitfalls.
	// Length ≤14. Meets complexity via mixed case + digit.
	pass := "Dj9" + randomHex(5) // 3+10 = 13
	if len(pass) > 14 {
		pass = pass[:14]
	}
	out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$name = %s
$pass = ConvertTo-SecureString -String %s -AsPlainText -Force
# Remove leftover account if a prior run leaked it.
Get-LocalUser -Name $name -ErrorAction SilentlyContinue | Remove-LocalUser -ErrorAction SilentlyContinue
New-LocalUser -Name $name -Password $pass -PasswordNeverExpires -UserMayNotChangePassword -AccountNeverExpires | Out-Null
Add-LocalGroupMember -Group 'Users' -Member $name -ErrorAction SilentlyContinue
# Ensure not elevated.
Remove-LocalGroupMember -Group 'Administrators' -Member $name -ErrorAction SilentlyContinue
$admins = Get-LocalGroupMember -Group 'Administrators' -ErrorAction SilentlyContinue | ForEach-Object { $_.Name }
if ($admins -match [regex]::Escape($name)) { throw 'user unexpectedly in Administrators' }
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
Get-LocalUser -Name %s -ErrorAction SilentlyContinue | Remove-LocalUser -ErrorAction SilentlyContinue
net user %s /delete 2>$null | Out-Null
Remove-Item -LiteralPath ('C:\Users\' + %s) -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath ('C:\Windows\System32\GroupPolicyUsers\' + %s) -Recurse -Force -ErrorAction SilentlyContinue
`, quotePS(u.Name), quotePS(u.Name), quotePS(u.Name), u.Name, quotePS(u.Name), quotePS(u.SID)))
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loadUserHive(t testing.TB, u *testUser) (hivePath string, unload func()) {
	t.Helper()
	// Create the profile via LoadUserProfile after an interactive LogonUser.
	out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public class NativeProfile {
  [StructLayout(LayoutKind.Sequential)]
  public struct PROFILEINFO {
    public int dwSize;
    public int dwFlags;
    [MarshalAs(UnmanagedType.LPTStr)] public string lpUserName;
    [MarshalAs(UnmanagedType.LPTStr)] public string lpProfilePath;
    [MarshalAs(UnmanagedType.LPTStr)] public string lpDefaultPath;
    [MarshalAs(UnmanagedType.LPTStr)] public string lpServerName;
    [MarshalAs(UnmanagedType.LPTStr)] public string lpPolicyPath;
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
$u = %s; $p = %s
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
# Persist token+profile handles for unload via env files is awkward; use ProfileList path + reg load instead.
[NativeProfile]::UnloadUserProfile($token, $pi.hProfile) | Out-Null
[NativeProfile]::CloseHandle($token) | Out-Null
$sid = %s
$prof = (Get-ItemProperty -Path ('HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\' + $sid)).ProfileImagePath
if (-not $prof) { throw 'profile path missing' }
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
[gc]::Collect(); Start-Sleep -Milliseconds 300
reg unload %s | Out-Null
`, quotePS(mount)))
	}
	t.Cleanup(unload)
	return mount, unload
}
