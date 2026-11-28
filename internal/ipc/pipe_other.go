//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"io"
)

func init() {
	DefaultDial = dialUnsupported
	DefaultListen = listenUnsupported
}

func listenUnsupported(context.Context, uint32, string) (Acceptor, error) {
	return nil, fmt.Errorf("ipc: named pipes are only supported on Windows")
}

// OpenPipeFile is a stub for ACL tests on non-Windows.
func OpenPipeFile(string) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("ipc: OpenPipeFile is Windows-only")
}
