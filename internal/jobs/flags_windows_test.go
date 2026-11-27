//go:build windows

package jobs

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestProcessCreationFlagBits_MatchXSys(t *testing.T) {
	t.Parallel()
	checks := []struct {
		name string
		local, sys uint32
	}{
		{"CREATE_SUSPENDED", flagCreateSuspended, windows.CREATE_SUSPENDED},
		{"CREATE_NO_WINDOW", flagCreateNoWindow, windows.CREATE_NO_WINDOW},
		{"CREATE_UNICODE_ENVIRONMENT", flagCreateUnicodeEnvironment, windows.CREATE_UNICODE_ENVIRONMENT},
		{"EXTENDED_STARTUPINFO_PRESENT", flagExtendedStartupInfoPresent, windows.EXTENDED_STARTUPINFO_PRESENT},
		{"CREATE_NEW_PROCESS_GROUP", flagCreateNewProcessGroup, windows.CREATE_NEW_PROCESS_GROUP},
	}
	for _, tc := range checks {
		if tc.local != tc.sys {
			t.Fatalf("%s constant drift: local=%#x sys=%#x", tc.name, tc.local, tc.sys)
		}
	}
}
