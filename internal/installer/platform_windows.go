//go:build windows

package installer

// newPlatformInstaller returns the production Windows Installer.
func newPlatformInstaller() Installer {
	return NewService(Options{
		Paths:           DefaultPaths(),
		Host:            NewWindowsHost(),
		RequireElevated: true,
	})
}
