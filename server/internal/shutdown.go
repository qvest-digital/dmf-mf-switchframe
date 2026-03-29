package internal

import (
	"sync"
	"time"
)

// WaitWithTimeout waits for a WaitGroup with a timeout.
// Returns true if the WaitGroup completed, false if timed out.
func WaitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// WaitChanTimeout waits for a channel to close with a timeout.
// Returns true if the channel closed, false if timed out.
func WaitChanTimeout(ch <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}
