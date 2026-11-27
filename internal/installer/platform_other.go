//go:build !windows

package installer

import "context"

// newPlatformInstaller returns an Installer that rejects install/uninstall off Windows.
func newPlatformInstaller() Installer {
	return unsupported{}
}

type unsupported struct{}

func (unsupported) Install(context.Context, InstallOptions) (InstallOutcome, error) {
	return InstallOutcome{}, ErrUnsupported
}
func (unsupported) Uninstall(context.Context) error { return ErrUnsupported }
