package jobs

import (
	"sync"
	"time"
)

// awaitWaitGroup waits for wg up to timeout. On expiry it runs onTimeout
// (typically closing pipe readers) and waits again up to timeout for wg to
// finish. If the second wait also expires, it returns without blocking further
// so callers (job result POST) cannot stall forever when Close does not unblock
// a stuck pipe reader.
// Returns whether the wait completed before the first timeout.
func awaitWaitGroup(wg *sync.WaitGroup, timeout time.Duration, onTimeout func()) (finishedCleanly bool) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		if onTimeout != nil {
			onTimeout()
		}
		timer.Reset(timeout)
		select {
		case <-done:
			return false
		case <-timer.C:
			return false
		}
	}
}
