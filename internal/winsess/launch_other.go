//go:build !windows

package winsess

import (
	"context"
	"fmt"
)

type platformLauncher struct {
	exePath string
}

func newPlatformLauncher(exePath string) Launcher {
	return &platformLauncher{exePath: exePath}
}

func (l *platformLauncher) Launch(context.Context, uint32) (Child, error) {
	return nil, fmt.Errorf("winsess: CreateProcessAsUser is only supported on Windows")
}

func (l *platformLauncher) HasUserToken(uint32) bool { return false }

// QueryUserToken is the OS seam (stub on non-Windows).
func QueryUserToken(uint32) (uintptr, error) {
	return 0, fmt.Errorf("winsess: WTSQueryUserToken is only supported on Windows")
}

// CreateProcessAsUser is the OS seam (stub on non-Windows).
func CreateProcessAsUser(_ uintptr, _ string, _ []string, _ uint32) (Child, error) {
	return nil, fmt.Errorf("winsess: CreateProcessAsUser is only supported on Windows")
}
