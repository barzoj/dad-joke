package jobs

import (
	"sync"
	"time"
)

// awaitWaitGroup waits for wg up to timeout. On expiry it runs onTimeout
// (typically closing pipe readers) and then waits for wg to finish.
// Returns whether the wait completed before the timeout.
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
		<-done
		return false
	}
}
