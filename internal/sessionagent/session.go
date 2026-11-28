package sessionagent

import (
	"context"
	"os"

	"github.com/barzoj/dad-joke/internal/buildinfo"
	"github.com/barzoj/dad-joke/internal/ipc"
	"github.com/barzoj/dad-joke/internal/winsess"
)

// Run is the session-agent entrypoint (`dad-joke.exe session`).
// It takes no configuration, performs no I/O against ProgramData, and opens
// no network connections. It connects to the service over versioned IPC and
// blocks until ctx is cancelled.
func Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID, err := winsess.CurrentSessionID()
	if err != nil {
		// Non-Windows / session 0: idle until cancel (keeps CLI tests working).
		<-ctx.Done()
		return ctx.Err()
	}
	protocol := ipc.ProtocolVersion
	if v := forcedProtocol(); v != 0 {
		protocol = v
	}
	client := ipc.NewClient(ipc.ClientConfig{
		SessionID:    sessionID,
		PID:          pidU32(os.Getpid()),
		AgentVersion: buildinfo.Version,
		Protocol:     protocol,
		Log:          func(string, ...any) {},
		AfterHello:   e2eAfterHello,
	})
	return client.Run(ctx)
}

func pidU32(pid int) uint32 {
	if pid < 0 {
		return 0
	}
	const maxUint32 = 1<<32 - 1
	if pid > maxUint32 {
		return maxUint32
	}
	return uint32(pid)
}
