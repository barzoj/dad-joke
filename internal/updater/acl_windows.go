//go:build windows

package updater

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// SYSTEM + Administrators Full Control, protected DACL (no inheritance).
const aclSDDL = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

func applyOSACL(path string) error {
	if err := requirePath(path); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(aclSDDL)
	if err != nil {
		return fmt.Errorf("parse SDDL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("get DACL: %w", err)
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
		return fmt.Errorf("SetNamedSecurityInfo(%s): %w", path, err)
	}
	return nil
}
