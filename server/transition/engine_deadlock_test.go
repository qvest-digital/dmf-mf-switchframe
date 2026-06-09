package transition

import (
	"sync"
	"testing"
	"time"
)

// TestBlendRawIntoStopNoDeadlock reproduces the ABBA lock-order inversion
// between BlendRawInto (blendRawMu -> mu) and cleanup() (mu -> blendRawMu).
// The reference-clock sync loop calls BlendRawInto on every frame tick while
// abort/cut paths call Stop() from control goroutines; if BlendRawInto holds
// blendRawMu and parks on mu while cleanup holds mu and reaches its scratch
// release, both goroutines wedge forever. The stress loop below hits that
// window reliably before the fix; with the fix it must complete.
func TestBlendRawIntoStopNoDeadlock(t *testing.T) {
	t.Parallel()

	e := NewEngine(EngineConfig{})

	const iters = 50000
	fromW, fromH := 16, 16
	toW, toH := 8, 8
	from := make([]byte, fromW*fromH*3/2)
	// to is a different resolution so BlendRawInto exercises the
	// blendRawMu-guarded scratch buffers that cleanup() releases.
	to := make([]byte, toW*toH*3/2)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		var dst []byte
		for i := 0; i < iters; i++ {
			dst, _, _ = e.BlendRawInto(dst, from, fromW, fromH, to, toW, toH, 0.5)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			e.Stop()
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock: BlendRawInto vs Stop/cleanup wedged (blendRawMu/mu lock-order inversion)")
	}
}
