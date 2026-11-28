//go:build !windows

package winsess

import "fmt"

func tokenUserSIDString(uintptr) (string, error) {
	return "", fmt.Errorf("winsess: TokenUserSIDString is Windows-only")
}

func currentSessionID() (uint32, error) {
	return 0, fmt.Errorf("winsess: CurrentSessionID is Windows-only")
}
