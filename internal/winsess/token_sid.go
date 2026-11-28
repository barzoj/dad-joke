package winsess

import "fmt"

// TokenUserSIDString returns the user SID string for a primary token obtained
// via QueryUserToken. Platform-specific.
func TokenUserSIDString(token uintptr) (string, error) {
	return tokenUserSIDString(token)
}

// CurrentSessionID returns the Windows session id of the calling process.
func CurrentSessionID() (uint32, error) {
	return currentSessionID()
}

// ErrNoSession is returned when the process has no interactive session id.
var ErrNoSession = fmt.Errorf("winsess: no interactive session")
