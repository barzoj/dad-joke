//go:build windows

package acl

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// FileSDDL is a protected DACL granting Full Control only to SYSTEM (SY)
// and Administrators (BA), with inheritance enabled for children.
const FileSDDL = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// Apply sets a strict SYSTEM + Administrators ACL on path.
func Apply(path string) error {
	if path == "" {
		return fmt.Errorf("acl: empty path")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("acl: stat %s: %w", path, err)
	}
	sd, err := windows.SecurityDescriptorFromString(FileSDDL)
	if err != nil {
		return fmt.Errorf("acl: parse SDDL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("acl: get DACL: %w", err)
	}
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
	if err != nil {
		return fmt.Errorf("acl: SetNamedSecurityInfo(%s): %w", path, err)
	}
	return nil
}
