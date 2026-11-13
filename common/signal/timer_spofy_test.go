package signal_test

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/xtls/xray-core/common/signal"
)

// A periodic check that is still in flight while SetTimeout replaces its task used to take the
// activity token meant for the new task's synchronous first check. That check then called finish()
// with the timer's mutex already held by SetTimeout and the calling goroutine hung forever, leaking
// the connection handler behind it.
func TestSpofyActivityTimerSetTimeoutDoesNotDeadlock(t *testing.T) {
	const interval = 50 * time.Microsecond
	deadline := time.Now().Add(3 * time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				ctx, cancel := context.WithCancel(context.Background())
				timer := CancelAfterInactivity(ctx, cancel, interval)
				// Call SetTimeout right when the periodic check of the previous task is due.
				for j := 0; j < 50 && ctx.Err() == nil; j++ {
					timer.Update()
					time.Sleep(interval)
					timer.SetTimeout(interval)
				}
				cancel()
			}
		}()
	}

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("a goroutine is stuck in ActivityTimer.SetTimeout")
	}
}
