//go:build !e2ediag

package sessionagent

import "github.com/barzoj/dad-joke/internal/ipc"

func forcedProtocol() int { return 0 }

func e2eAfterHello(*ipc.Conn) error { return nil }
