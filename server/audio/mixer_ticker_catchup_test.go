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
	start := time.Now()
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

	// Run long enough that the post-stall window dominates scheduling noise.
	time.Sleep(700 * time.Millisecond)
	require.NoError(t, m.Close())
	elapsed := time.Since(start)

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
	dropped := int(m.tickerFramesDropped.Load())
	accounted := count + dropped
	interval := time.Second * 1024 / 48000
	expected := int(elapsed / interval)
	assert.GreaterOrEqual(t, accounted, expected-5,
		"produced+dropped frames must track elapsed time (expected ~%d, produced %d, dropped %d): missed ticks were not caught up",
		expected, count, dropped)
	assert.LessOrEqual(t, accounted, expected+3,
		"catch-up must not overproduce frames (expected ~%d, produced %d, dropped %d)",
		expected, count, dropped)
}
