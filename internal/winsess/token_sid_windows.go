//go:build windows

package winsess

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func tokenUserSIDString(token uintptr) (string, error) {
	tok := windows.Token(token)
	if tok == 0 {
		return "", fmt.Errorf("winsess: empty token")
	}
	tu, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("GetTokenUser: %w", err)
	}
	if tu.User.Sid == nil {
		return "", fmt.Errorf("winsess: nil token user SID")
	}
	return tu.User.Sid.String(), nil
}

func currentSessionID() (uint32, error) {
	pid := os.Getpid()
	if pid < 0 {
		return 0, fmt.Errorf("winsess: negative pid")
	}
	const maxUint32 = 1<<32 - 1
	if pid > maxUint32 {
		return 0, fmt.Errorf("winsess: pid out of range")
	}
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(uint32(pid), &sessionID); err != nil {
		return 0, fmt.Errorf("ProcessIdToSessionId: %w", err)
	}
	if sessionID == 0 {
		return 0, ErrNoSession
	}
	return sessionID, nil
}
