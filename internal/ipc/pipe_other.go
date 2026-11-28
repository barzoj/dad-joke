//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"io"
)

// DefaultListen is the non-Windows listener (must use MemHub in tests).
var DefaultListen ListenFunc = listenUnsupported

func listenUnsupported(context.Context, uint32, string) (Acceptor, error) {
	return nil, fmt.Errorf("ipc: named pipes are only supported on Windows")
}

func init() {
	DefaultDial = dialUnsupported
}

// OpenPipeFile is a stub for ACL tests on non-Windows.
func OpenPipeFile(string) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("ipc: OpenPipeFile is Windows-only")
}
