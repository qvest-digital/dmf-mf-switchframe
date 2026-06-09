package switcher

import (
	"sync"
	"testing"

	"github.com/zsiec/prism/distribution"
)

// TestWallClockVideoPTS_ConcurrentSeedAndRead is a -race regression for the
// data race on the wall-clock PTS epoch group (videoPTSStart/videoPTSEpoch/
// videoPTS/videoPTSInited/firstVideoPTSSeeded). The per-frame read/update in
// wallClockVideoPTS (videoProcessingLoop goroutine) ran concurrently with the
// one-time seeding in handleRawVideoFrame (a frame-delivery goroutine) with no
// synchronization; a torn read of the 24-byte videoPTSEpoch (time.Time)
// corrupted the wall-clock PTS for the rest of the session. Both paths now
// serialize under videoPTSMu — run under `-race` to verify.
func TestWallClockVideoPTS_ConcurrentSeedAndRead(t *testing.T) {
	programRelay := distribution.NewRelay()
	// New(nil)/newTestSwitcher do not enable the reference clock, so the
	// wall-clock PTS path is live (EnableWallClockVideoPTS is not a no-op).
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	if sw.useRefClock.Load() {
		t.Skip("wall-clock PTS path requires no reference clock")
	}
	sw.EnableWallClockVideoPTS()

	// Register cam1 and make it the program source so handleRawVideoFrame
	// runs its seeding block (it gates on sourceKey == programSource).
	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.configMu.Lock()
	sw.programSource = "cam1"
	sw.updateHotState(func(hs *hotState) { hs.programSource = "cam1" })
	sw.configMu.Unlock()

	const iters = 2000
	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine A: per-frame read/update (videoProcessingLoop path).
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = sw.wallClockVideoPTS(int64(i * 3000))
		}
	}()

	// Goroutine B: one-time seeding path. Reset the seed flags under videoPTSMu
	// between iterations so the seeding block actually fires repeatedly,
	// maximizing the chance of overlapping with goroutine A's epoch read.
	go func() {
		defer wg.Done()
		yuvSize := ColorYUV420_8bit.FrameSize(320, 240)
		for i := 0; i < iters; i++ {
			sw.videoPTSMu.Lock()
			sw.firstVideoPTSSeeded = false
			sw.videoPTSInited = false
			sw.videoPTSMu.Unlock()
			pf := &ProcessingFrame{
				Data:   make([]byte, yuvSize),
				Width:  320,
				Height: 240,
				PTS:    int64(i * 3000),
				Format: ColorYUV420_8bit,
			}
			sw.handleRawVideoFrame("cam1", pf)
		}
	}()

	wg.Wait()
}
