//go:build e2e && windows

package e2e_test

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// assertStrictACL verifies path has a protected DACL granting Allow only to
// SYSTEM (SY) and Administrators (BA), with no Users / Authenticated Users.
func assertStrictACL(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s): %v", path, err)
	}

	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("Control(%s): %v", path, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("%s: DACL is not protected (SE_DACL_PROTECTED unset); control=0x%x sddl=%s",
			path, control, sd.String())
	}

	sddl := sd.String()
	up := strings.ToUpper(sddl)
	if !strings.Contains(up, "SY") {
		t.Fatalf("%s: SYSTEM (SY) missing from SDDL %q", path, sddl)
	}
	if !strings.Contains(up, "BA") {
		t.Fatalf("%s: Administrators (BA) missing from SDDL %q", path, sddl)
	}
	if hasAllowTrustee(sddl, "BU") || hasAllowTrustee(sddl, "AU") {
		t.Fatalf("%s: unexpected Users/Authenticated Users allow ACE in SDDL %q", path, sddl)
	}
}

func hasAllowTrustee(sddl, trustee string) bool {
	// SDDL allow ACE form: (A;flags;rights;object;inherit;TRUSTEE)
	parts := strings.Split(sddl, "(")
	want := ";;;" + strings.ToUpper(trustee)
	for _, p := range parts {
		p = strings.ToUpper(p)
		if !strings.HasPrefix(p, "A;") {
			continue
		}
		trimmed := strings.TrimSuffix(p, ")")
		if strings.HasSuffix(trimmed, want) {
			return true
		}
	}
	return false
}
