//go:build !windows

package winsess

func terminateInstalledSessionAgents(string) error { return nil }
