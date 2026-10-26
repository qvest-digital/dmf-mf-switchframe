package audio

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// TestTickerCatchUp exercises the pure deadline accounting used by
// outputTicker to recover frames that time.Ticker silently coalesces when a
// tick overruns the interval (cgo encode, lock contention with ingest).
func TestTickerCatchUp(t *testing.T) {
	t.Parallel()

	interval := 20 * time.Millisecond
	base := time.Unix(1000, 0)

	t.Run("on time produces one frame", func(t *testing.T) {
		frames, dropped, next := tickerCatchUp(base, base, interval)
		assert.Equal(t, 1, frames)
		assert.Equal(t, 0, dropped)
		assert.Equal(t, base.Add(interval), next)
	})

	t.Run("buffered early tick produces nothing", func(t *testing.T) {
		// time.Ticker buffers one tick; it can be delivered before the next
		// frame deadline after a catch-up burst. No frame is due.
		frames, dropped, next := tickerCatchUp(base, base.Add(5*time.Millisecond), interval)
		assert.Equal(t, 0, frames)
		assert.Equal(t, 0, dropped)
		assert.Equal(t, base.Add(5*time.Millisecond), next)
	})

	t.Run("late tick catches up missed frames", func(t *testing.T) {
		// Fire 2.5 intervals past the deadline: the deadline frame plus the
		// two whole intervals that elapsed are due (3 frames total).
		frames, dropped, next := tickerCatchUp(base.Add(50*time.Millisecond), base, interval)
		assert.Equal(t, 3, frames)
		assert.Equal(t, 0, dropped)
		assert.Equal(t, base.Add(3*interval), next)
		assert.True(t, next.After(base.Add(50*time.Millisecond)), "new deadline must be in the future")
	})

	t.Run("long stall is bounded and resyncs", func(t *testing.T) {
		// e.g. system suspend: don't blast hundreds of frames; produce a
		// bounded burst, report the excess as dropped, and realign the
		// deadline to the present.
		now := base.Add(1000 * interval)
		frames, dropped, next := tickerCatchUp(now, base, interval)
		assert.Equal(t, maxTickerCatchUpFrames, frames)
		assert.Equal(t, 1001-maxTickerCatchUpFrames, dropped)
		assert.Equal(t, now.Add(interval), next)
	})
}

// TestOutputTicker_CatchesUpAfterStall verifies end-to-end that a delayed tick
// (here: a one-time 200ms stall in the output callback, standing in for a
// slow cgo encode or mutex convoy) does not permanently drop output frames.
// Before the fix, time.Ticker coalesced the missed ticks and each one was
// ~21.3ms of program audio lost; with deadline-based catch-up the frame count
// tracks elapsed wall time.
func TestOutputTicker_CatchesUpAfterStall(t *testing.T) {
	t.Parallel()

	const stall = 200 * time.Millisecond

	var mu sync.Mutex
	var outputCount int
	stalled := false

	enc := &mockTickEncoder{}
	beforeStart := time.Now()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputCount++
			doStall := !stalled
			stalled = true
			mu.Unlock()
			if doStall {
				time.Sleep(stall)
			}
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	afterStart := time.Now()

	// Run long enough that the post-stall window dominates scheduling noise.
	time.Sleep(700 * time.Millisecond)
	beforeClose := time.Now()
	require.NoError(t, m.Close())
	afterClose := time.Now()

	mu.Lock()
	count := outputCount
	mu.Unlock()

	// One output frame is due every ~21.3ms of elapsed wall time. Frames a
	// stall hides are either produced by catch-up or — when a stall exceeds
	// the burst cap (possible on a badly overloaded CI runner) — explicitly
	// counted as dropped. The pre-fix bug this guards against (time.Ticker
	// silently coalescing missed ticks) loses frames with NO accounting, so
	// requiring produced+dropped to track wall time still catches it while
	// staying immune to runner scheduling stalls.
	//
	// The ticker accounts for the window from its start inside NewMixer to
	// its exit inside Close. Only the bounds of that window are observable
	// here, and on a starved runner the gaps between them run to several
	// intervals, so the window it ran is bracketed rather than estimated.
	dropped := int(m.tickerFramesDropped.Load())
	accounted := count + dropped
	interval := time.Second * 1024 / 48000
	minExpected := int(beforeClose.Sub(afterStart) / interval)
	maxExpected := int(afterClose.Sub(beforeStart) / interval)
	assert.GreaterOrEqual(t, accounted, minExpected-5,
		"produced+dropped frames must track elapsed time (expected %d..%d, produced %d, dropped %d): missed ticks were not caught up",
		minExpected, maxExpected, count, dropped)
	assert.LessOrEqual(t, accounted, maxExpected+3,
		"catch-up must not overproduce frames (expected %d..%d, produced %d, dropped %d)",
		minExpected, maxExpected, count, dropped)
}

// TestOutputTicker_CloseStopsCatchUpBurst verifies that Close does not wait
// out a catch-up burst. A mixer whose tick costs more than the interval runs
// capped bursts back to back, and select picks at random between a closed
// stopTicker and a ready ticker, so a shutdown that waits for the burst can
// exceed Close's timeout on a starved host.
func TestOutputTicker_CloseStopsCatchUpBurst(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var outputCount int
	var m *Mixer
	ready := make(chan struct{})
	enc := &mockTickEncoder{}
	m = NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			<-ready
			mu.Lock()
			outputCount++
			n := outputCount
			mu.Unlock()
			switch n {
			case 1:
				// Owe the next fire a burst of about ten frames.
				time.Sleep(200 * time.Millisecond)
			case 2:
				// First frame of that burst: hold it until Close begins.
				<-m.stopTicker
			}
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	close(ready)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return outputCount >= 2
	}, 2*time.Second, time.Millisecond, "the ticker never reached the first frame of the catch-up burst")
	require.NoError(t, m.Close())

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, outputCount, "frames of a catch-up burst were produced after Close")
}
