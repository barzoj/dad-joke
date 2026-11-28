//go:build !windows

package service

import "fmt"

func isWindowsServiceSession() bool { return false }

func runWindowsService(name string, _ Agent, _ chan<- Event) error {
	return fmt.Errorf("%w: cannot attach to SCM as %q", ErrUnsupported, name)
}
