//go:build e2e && windows

package e2e_test

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	logonInteractive = 2
	logonNetwork     = 3
	providerDefault  = 0
)

var (
	modAdvapi32                 = windows.NewLazySystemDLL("advapi32.dll")
	procLogonUserW              = modAdvapi32.NewProc("LogonUserW")
	procImpersonateLoggedOnUser = modAdvapi32.NewProc("ImpersonateLoggedOnUser")
)

func logonUser(user, domain, pass string, logonType uint32) (windows.Token, error) {
	u, err := syscall.UTF16PtrFromString(user)
	if err != nil {
		return 0, err
	}
	d, err := syscall.UTF16PtrFromString(domain)
	if err != nil {
		return 0, err
	}
	p, err := syscall.UTF16PtrFromString(pass)
	if err != nil {
		return 0, err
	}
	var token windows.Handle
	r1, _, e1 := syscall.Syscall6(
		procLogonUserW.Addr(),
		6,
		uintptr(unsafe.Pointer(u)),
		uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(p)),
		uintptr(logonType),
		uintptr(providerDefault),
		uintptr(unsafe.Pointer(&token)),
	)
	if r1 == 0 {
		if e1 != 0 {
			return 0, error(e1)
		}
		return 0, syscall.EINVAL
	}
	return windows.Token(token), nil
}

func impersonateLoggedOnUser(token windows.Token) error {
	r1, _, e1 := syscall.Syscall(procImpersonateLoggedOnUser.Addr(), 1, uintptr(token), 0, 0)
	if r1 == 0 {
		if e1 != 0 {
			return error(e1)
		}
		return syscall.EINVAL
	}
	return nil
}

func withUserToken(user, pass string, fn func() error) error {
	// Impersonation is thread-local; pin this goroutine so the runtime cannot
	// migrate it off the impersonated OS thread before RevertToSelf.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	token, err := logonUser(user, ".", pass, logonNetwork)
	if err != nil {
		token, err = logonUser(user, ".", pass, logonInteractive)
		if err != nil {
			return fmt.Errorf("LogonUser(%s): %w", user, err)
		}
	}
	defer token.Close()

	if err := impersonateLoggedOnUser(token); err != nil {
		return fmt.Errorf("ImpersonateLoggedOnUser: %w", err)
	}
	defer windows.RevertToSelf()
	return fn()
}

func attemptReadFileAs(user, pass, path string) error {
	return withUserToken(user, pass, func() error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		buf := make([]byte, 16)
		_, err = f.Read(buf)
		return err
	})
}

func attemptWriteFileAs(user, pass, path string) error {
	return withUserToken(user, pass, func() error {
		return os.WriteFile(path, []byte("tamper"), 0o644)
	})
}

func attemptDeleteFileAs(user, pass, path string) error {
	return withUserToken(user, pass, func() error {
		return os.Remove(path)
	})
}

func attemptStopServiceAs(user, pass, name string) error {
	return withUserToken(user, pass, func() error {
		m, err := mgr.Connect()
		if err != nil {
			return err
		}
		defer m.Disconnect()
		s, err := m.OpenService(name)
		if err != nil {
			return err
		}
		defer s.Close()
		_, err = s.Control(svc.Stop)
		return err
	})
}

func isAccessDeniedErr(err error) bool {
	if err == nil {
		return false
	}
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.Errno(windows.ERROR_ACCESS_DENIED) ||
			errno == syscall.Errno(windows.ERROR_PRIVILEGE_NOT_HELD)
	}
	if errno, ok := err.(windows.Errno); ok {
		return errno == windows.ERROR_ACCESS_DENIED || errno == windows.ERROR_PRIVILEGE_NOT_HELD
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "access is denied") ||
		strings.Contains(low, "access denied") ||
		strings.Contains(low, "privilege")
}
