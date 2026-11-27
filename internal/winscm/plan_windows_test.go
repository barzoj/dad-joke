//go:build windows

package winscm

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestWin32ErrnoConstantsMatchXSys(t *testing.T) {
	t.Parallel()
	if ErrAlreadyRunning != windows.ERROR_SERVICE_ALREADY_RUNNING {
		t.Fatalf("ErrAlreadyRunning = %d, want windows.ERROR_SERVICE_ALREADY_RUNNING (%d)",
			ErrAlreadyRunning, windows.ERROR_SERVICE_ALREADY_RUNNING)
	}
	if ErrCannotAcceptCtrl != windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL {
		t.Fatalf("ErrCannotAcceptCtrl = %d, want windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL (%d)",
			ErrCannotAcceptCtrl, windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL)
	}
	if ErrServiceNotActive != windows.ERROR_SERVICE_NOT_ACTIVE {
		t.Fatalf("ErrServiceNotActive = %d, want windows.ERROR_SERVICE_NOT_ACTIVE (%d)",
			ErrServiceNotActive, windows.ERROR_SERVICE_NOT_ACTIVE)
	}
}
