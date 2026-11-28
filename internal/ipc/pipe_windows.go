//go:build windows

package ipc

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	pipeBufferSize = 64 * 1024
	// PIPE_UNLIMITED_INSTANCES is not used: we want first-instance only.
	pipeMaxInstances = 1
)

func init() {
	DefaultDial = DialWindows
	DefaultListen = ListenWindows
}

// ListenWindows creates a named pipe with FILE_FLAG_FIRST_PIPE_INSTANCE and
// PIPE_REJECT_REMOTE_CLIENTS. DACL: SYSTEM + userSID only.
func ListenWindows(_ context.Context, sessionID uint32, userSID string) (Acceptor, error) {
	if sessionID == 0 {
		return nil, fmt.Errorf("ipc: refuse pipe for session 0")
	}
	if userSID == "" {
		return nil, fmt.Errorf("ipc: empty user SID for session %d", sessionID)
	}
	name := PipeName(sessionID)
	sa, err := pipeSecurityAttributes(userSID)
	if err != nil {
		return nil, err
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	// Synchronous pipe (no FILE_FLAG_OVERLAPPED) so os.File Read/Write work.
	// ConnectNamedPipe is cancelled by closing the handle when ctx is done.
	openMode := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_FIRST_PIPE_INSTANCE)
	pipeMode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	h, err := windows.CreateNamedPipe(namePtr, openMode, pipeMode, pipeMaxInstances, pipeBufferSize, pipeBufferSize, 0, sa)
	if err != nil {
		return nil, fmt.Errorf("ipc: CreateNamedPipe(%s): %w", name, err)
	}
	return &winAcceptor{handle: h, name: name, sessionID: sessionID}, nil
}

func pipeSecurityAttributes(userSID string) (*windows.SecurityAttributes, error) {
	// Protected DACL: allow GENERIC_ALL to SYSTEM and the session user only.
	// No Administrators, Everyone, or Authenticated Users.
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;" + userSID + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("ipc: pipe SDDL: %w", err)
	}
	size := unsafe.Sizeof(windows.SecurityAttributes{})
	if size > 0xFFFFFFFF {
		return nil, fmt.Errorf("ipc: SecurityAttributes too large")
	}
	return &windows.SecurityAttributes{
		Length:             uint32(size),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}, nil
}

type winAcceptor struct {
	handle    windows.Handle
	name      string
	sessionID uint32
	mu        sync.Mutex
	closed    bool
}

func (a *winAcceptor) Name() string { return a.name }

func (a *winAcceptor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.handle == 0 {
		return nil
	}
	a.closed = true
	err := windows.CloseHandle(a.handle)
	a.handle = 0
	return err
}

func (a *winAcceptor) Accept(ctx context.Context) (io.ReadWriteCloser, ExpectedClient, error) {
	a.mu.Lock()
	h := a.handle
	closed := a.closed
	a.mu.Unlock()
	if closed || h == 0 {
		return nil, ExpectedClient{}, fmt.Errorf("ipc: acceptor closed")
	}

	type connResult struct {
		err error
	}
	done := make(chan connResult, 1)
	go func() {
		err := windows.ConnectNamedPipe(h, nil)
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			done <- connResult{err: err}
			return
		}
		done <- connResult{}
	}()

	select {
	case <-ctx.Done():
		_ = windows.CloseHandle(h)
		a.mu.Lock()
		a.handle = 0
		a.closed = true
		a.mu.Unlock()
		<-done
		return nil, ExpectedClient{}, ctx.Err()
	case r := <-done:
		if r.err != nil {
			return nil, ExpectedClient{}, fmt.Errorf("ipc: ConnectNamedPipe: %w", r.err)
		}
	}

	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(h, &pid); err != nil {
		_ = windows.CloseHandle(h)
		a.mu.Lock()
		a.handle = 0
		a.closed = true
		a.mu.Unlock()
		return nil, ExpectedClient{}, fmt.Errorf("ipc: GetNamedPipeClientProcessId: %w", err)
	}
	var sess uint32
	if err := getNamedPipeClientSessionId(h, &sess); err != nil {
		_ = windows.CloseHandle(h)
		a.mu.Lock()
		a.handle = 0
		a.closed = true
		a.mu.Unlock()
		return nil, ExpectedClient{}, fmt.Errorf("ipc: GetNamedPipeClientSessionId: %w", err)
	}

	a.mu.Lock()
	a.handle = 0
	a.closed = true
	a.mu.Unlock()

	f := os.NewFile(uintptr(h), a.name)
	return &winPipeConn{File: f, handle: h}, ExpectedClient{PID: pid, SessionID: sess}, nil
}

// getNamedPipeClientSessionId wraps GetNamedPipeClientSessionId (not yet in x/sys).
func getNamedPipeClientSessionId(pipe windows.Handle, sessionID *uint32) error {
	r1, _, e1 := procGetNamedPipeClientSessionId.Call(uintptr(pipe), uintptr(unsafe.Pointer(sessionID)))
	if r1 == 0 {
		if e1 != 0 {
			return error(e1)
		}
		return windows.EINVAL
	}
	return nil
}

var procGetNamedPipeClientSessionId = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetNamedPipeClientSessionId")

type winPipeConn struct {
	*os.File
	handle windows.Handle
}

func (c *winPipeConn) Close() error {
	if c == nil || c.File == nil {
		return nil
	}
	return c.File.Close()
}

// DialWindows opens the per-session named pipe as a client.
func DialWindows(ctx context.Context, sessionID uint32) (io.ReadWriteCloser, error) {
	name := PipeName(sessionID)
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(
			path,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err == nil {
			return os.NewFile(uintptr(h), name), nil
		}
		if err == windows.ERROR_PIPE_BUSY {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		return nil, fmt.Errorf("ipc: CreateFile(%s): %w", name, err)
	}
}

// OpenPipeFile attempts to open a pipe by name (ACL tests).
func OpenPipeFile(name string) (io.ReadWriteCloser, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		path,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}

// PipeSDDL returns the SDDL string for a session pipe (tests).
func PipeSDDL(userSID string) string {
	return "D:P(A;;GA;;;SY)(A;;GA;;;" + userSID + ")"
}
