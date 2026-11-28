//go:build !windows

package sessionagent

func closeToken(uintptr) error { return nil }
