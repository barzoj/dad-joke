package sessionagent

import (
	"context"
)

// Run is the session-agent entrypoint (`dad-joke.exe session`).
// It takes no configuration, performs no I/O against ProgramData, and opens
// no network connections. It blocks until ctx is cancelled.
func Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	<-ctx.Done()
	return ctx.Err()
}
