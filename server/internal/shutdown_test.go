package internal

import (
	"sync"
	"testing"
	"time"
)

func TestWaitWithTimeout_Completes(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		wg.Done()
	}()
	if !WaitWithTimeout(&wg, 1*time.Second) {
		t.Error("expected completion, got timeout")
	}
}

func TestWaitWithTimeout_TimesOut(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1)
	// Never call Done during the test — should timeout.
	defer wg.Done() // cleanup after test assertion
	if WaitWithTimeout(&wg, 50*time.Millisecond) {
		t.Error("expected timeout, got completion")
	}
}

func TestWaitWithTimeout_AlreadyDone(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	// Zero WaitGroup — already done.
	if !WaitWithTimeout(&wg, 50*time.Millisecond) {
		t.Error("expected immediate completion for zero WaitGroup")
	}
}

func TestWaitWithTimeout_MultipleGoroutines(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	const n = 10
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(delay int) {
			time.Sleep(time.Duration(delay) * time.Millisecond)
			wg.Done()
		}(i * 5) // stagger completions over 0-45ms
	}
	if !WaitWithTimeout(&wg, 2*time.Second) {
		t.Error("expected all goroutines to complete within timeout")
	}
}

func TestWaitChanTimeout_Completes(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(ch)
	}()
	if !WaitChanTimeout(ch, 1*time.Second) {
		t.Error("expected completion, got timeout")
	}
}

func TestWaitChanTimeout_TimesOut(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{})
	// Never close — should timeout.
	if WaitChanTimeout(ch, 50*time.Millisecond) {
		t.Error("expected timeout, got completion")
	}
}

func TestWaitChanTimeout_AlreadyClosed(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{})
	close(ch)
	// Already-closed channel should return immediately.
	if !WaitChanTimeout(ch, 50*time.Millisecond) {
		t.Error("expected immediate completion for already-closed channel")
	}
}

func TestWaitChanTimeout_BufferedChannel(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{}, 1)
	ch <- struct{}{} // send, don't close
	// A buffered channel with a value is readable, so WaitChanTimeout
	// should return true (receives the value).
	if !WaitChanTimeout(ch, 50*time.Millisecond) {
		t.Error("expected completion from buffered channel with value")
	}
}

// TestWaitWithTimeout_TimingAccuracy verifies that the timeout is reasonably
// accurate — it should not return much earlier or later than the specified
// duration.
func TestWaitWithTimeout_TimingAccuracy(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Done()

	timeout := 100 * time.Millisecond
	start := time.Now()
	completed := WaitWithTimeout(&wg, timeout)
	elapsed := time.Since(start)

	if completed {
		t.Fatal("expected timeout, got completion")
	}
	// Allow 20% tolerance on timing (CI environments can be slow).
	if elapsed < timeout*80/100 {
		t.Errorf("timed out too early: elapsed %v, expected ~%v", elapsed, timeout)
	}
	if elapsed > timeout*3 {
		t.Errorf("timed out too late: elapsed %v, expected ~%v", elapsed, timeout)
	}
}
