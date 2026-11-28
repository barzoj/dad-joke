//go:build windows

package winsess

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type platformLauncher struct {
	exePath string
}

func newPlatformLauncher(exePath string) Launcher {
	return &platformLauncher{exePath: exePath}
}

func (l *platformLauncher) HasUserToken(sessionID uint32) bool {
	tok, err := QueryUserToken(sessionID)
	if err != nil {
		return false
	}
	_ = windows.Token(tok).Close()
	return true
}

func (l *platformLauncher) Launch(ctx context.Context, sessionID uint32) (Child, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sessionID == 0 {
		return nil, fmt.Errorf("winsess: refuse to launch into session 0")
	}
	if l.exePath == "" {
		return nil, fmt.Errorf("winsess: empty exe path")
	}
	// Re-adopt after Restart-Service: terminate prior session agents we do not
	// hold handles for (opened fresh — never trust a stale remembered PID).
	_ = terminateOrphans(sessionID, l.exePath)

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tok, err := QueryUserToken(sessionID)
	if err != nil {
		return nil, err
	}
	defer windows.Token(tok).Close()

	// Last chance to skip CreateProcessAsUser after cancel (service stop).
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	child, err := CreateProcessAsUser(tok, l.exePath, []string{l.exePath, "session"}, sessionID)
	return FinalizeLaunch(ctx, child, err)
}

// QueryUserToken wraps WTSQueryUserToken for sessionID.
func QueryUserToken(sessionID uint32) (uintptr, error) {
	if sessionID == 0 {
		return 0, fmt.Errorf("winsess: no user token for session 0")
	}
	var token windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &token); err != nil {
		return 0, fmt.Errorf("WTSQueryUserToken(%d): %w", sessionID, err)
	}
	return uintptr(token), nil
}

// CreateProcessAsUser starts exePath with args in the user's session using the
// given primary token. CREATE_NO_WINDOW; user's environment block; no secrets
// on the command line.
func CreateProcessAsUser(token uintptr, exePath string, args []string, sessionID uint32) (Child, error) {
	if sessionID == 0 {
		return nil, fmt.Errorf("winsess: refuse CreateProcessAsUser into session 0")
	}
	tok := windows.Token(token)
	if tok == 0 {
		return nil, fmt.Errorf("winsess: empty token")
	}

	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, tok, false); err != nil {
		return nil, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)

	appName, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		args = []string{exePath, "session"}
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return nil, err
	}
	desktop, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return nil, err
	}

	si := &windows.StartupInfo{
		Cb:      uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Desktop: desktop,
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT)
	err = windows.CreateProcessAsUser(
		tok,
		appName,
		cmdLine,
		nil,
		nil,
		false,
		flags,
		env,
		nil,
		si,
		&pi,
	)
	if err != nil {
		return nil, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)
	return &winChild{h: pi.Process, pid: pi.ProcessId}, nil
}

type winChild struct {
	h   windows.Handle
	pid uint32
}

func (c *winChild) PID() uint32 { return c.pid }

func (c *winChild) Wait(ctx context.Context) error {
	if c == nil || c.h == 0 {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ev, err := windows.WaitForSingleObject(c.h, 200)
		if err != nil {
			return err
		}
		switch ev {
		case windows.WAIT_OBJECT_0:
			return nil
		case uint32(windows.WAIT_TIMEOUT):
			continue
		default:
			return fmt.Errorf("WaitForSingleObject: %#x", ev)
		}
	}
}

func (c *winChild) Terminate() error {
	if c == nil || c.h == 0 {
		return nil
	}
	return windows.TerminateProcess(c.h, 1)
}

func (c *winChild) Close() error {
	if c == nil || c.h == 0 {
		return nil
	}
	err := windows.CloseHandle(c.h)
	c.h = 0
	return err
}
