//go:build !windows

package updater

import (
	"fmt"
	"time"
)

// OSService is a stub on non-Windows builds.
type OSService struct{}

// Stop implements ServiceControl.
func (OSService) Stop(string) error { return fmt.Errorf("service control only supported on Windows") }

// Start implements ServiceControl.
func (OSService) Start(string) error { return fmt.Errorf("service control only supported on Windows") }

// Wait implements ServiceControl.
func (OSService) Wait(string, ServiceState, time.Duration) error {
	return fmt.Errorf("service control only supported on Windows")
}

// Query implements ServiceControl.
func (OSService) Query(string) (ServiceState, error) {
	return "", fmt.Errorf("service control only supported on Windows")
}
