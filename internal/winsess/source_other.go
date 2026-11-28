//go:build !windows

package winsess

import "context"

// platformSource is a no-op stub so Linux unit tests compile without WTS.
type platformSource struct{}

func (platformSource) Sessions(context.Context) ([]Session, error) {
	return nil, nil
}

func (platformSource) Processes(context.Context) ([]Process, error) {
	return nil, nil
}
