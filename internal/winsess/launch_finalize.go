package winsess

import (
	"context"
	"time"
)

// FinalizeLaunch applies a post-create cancel check. If ctx is already done
// after CreateProcessAsUser returned a child, the child is terminated and
// closed so a cancelled service stop does not leave a late session agent.
func FinalizeLaunch(ctx context.Context, child Child, err error) (Child, error) {
	if err != nil {
		return nil, err
	}
	if child == nil {
		return nil, nil
	}
	if cerr := ctx.Err(); cerr != nil {
		_ = child.Terminate()
		waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = child.Wait(waitCtx)
		cancel()
		_ = child.Close()
		return nil, cerr
	}
	return child, nil
}
