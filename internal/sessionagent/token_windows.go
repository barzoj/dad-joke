//go:build windows

package sessionagent

import "golang.org/x/sys/windows"

func closeToken(tok uintptr) error {
	if tok == 0 {
		return nil
	}
	return windows.Token(tok).Close()
}
