package switcher

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestFramePacer_DeliversFrameOnTick(t *testing.T) {
	var mu sync.Mutex
	var delivered []*media.VideoFrame
	p := newFramePacer(10*time.Millisecond, func(f *media.VideoFrame) {
		mu.Lock()
		delivered = append(delivered, f)
		mu.Unlock()
	})
	defer p.stop()

	frame := &media.VideoFrame{PTS: 1000}
	p.submit(frame)

	time.Sleep(25 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(delivered), 1)
	assert.Equal(t, int64(1000), delivered[0].PTS)
}

func TestFramePacer_FIFO_DeliversAllFrames(t *testing.T) {
	// H.264-aware: all frames must be delivered in order, none dropped.
	var mu sync.Mutex
	var delivered []*media.VideoFrame
	p := newFramePacer(10*time.Millisecond, func(f *media.VideoFrame) {
		mu.Lock()
		delivered = append(delivered, f)
		mu.Unlock()
	})
	defer p.stop()

	// Submit 3 frames before any tick fires
	p.submit(&media.VideoFrame{PTS: 1000})
	p.submit(&media.VideoFrame{PTS: 2000})
	p.submit(&media.VideoFrame{PTS: 3000})

	// Wait enough for 3+ ticks to drain the queue
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	// ALL 3 frames must be delivered, in order
	require.Equal(t, 3, len(delivered))
	assert.Equal(t, int64(1000), delivered[0].PTS)
	assert.Equal(t, int64(2000), delivered[1].PTS)
	assert.Equal(t, int64(3000), delivered[2].PTS)

	snap := p.snapshot()
	assert.Equal(t, int64(0), snap.replaced, "FIFO pacer must never replace frames")
}

func TestFramePacer_EmptyTickCountsUp(t *testing.T) {
	p := newFramePacer(10*time.Millisecond, func(f *media.VideoFrame) {})
	defer p.stop()

	time.Sleep(35 * time.Millisecond)

	snap := p.snapshot()
	assert.GreaterOrEqual(t, snap.emptyTicks, int64(2))
}

func TestFramePacer_StopDrainsQueue(t *testing.T) {
	var mu sync.Mutex
	var delivered []*media.VideoFrame
	// Long interval so no tick fires before stop
	p := newFramePacer(1*time.Second, func(f *media.VideoFrame) {
		mu.Lock()
		delivered = append(delivered, f)
		mu.Unlock()
	})

	p.submit(&media.VideoFrame{PTS: 1000})
	p.submit(&media.VideoFrame{PTS: 2000})

	// Stop should drain remaining queued frames
	p.stop()
	p.stop() // double stop is safe

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 2, len(delivered))
	assert.Equal(t, int64(1000), delivered[0].PTS)
	assert.Equal(t, int64(2000), delivered[1].PTS)
}

func TestFramePacer_SubmitAfterStopIsSafe(t *testing.T) {
	p := newFramePacer(10*time.Millisecond, func(f *media.VideoFrame) {})
	p.stop()
	// Must not panic
	p.submit(&media.VideoFrame{PTS: 1000})
}

func TestFramePacer_SnapshotCounters(t *testing.T) {
	var count atomic.Int64
	p := newFramePacer(10*time.Millisecond, func(f *media.VideoFrame) {
		count.Add(1)
	})
	defer p.stop()

	for i := 0; i < 5; i++ {
		p.submit(&media.VideoFrame{PTS: int64(i * 1000)})
		time.Sleep(15 * time.Millisecond)
	}

	snap := p.snapshot()
	assert.GreaterOrEqual(t, snap.paced, int64(3))
}

func TestFramePacer_BypassMode(t *testing.T) {
	var delivered []*media.VideoFrame
	p := newBypassPacer(func(f *media.VideoFrame) {
		delivered = append(delivered, f)
	})
	defer p.stop()

	p.submit(&media.VideoFrame{PTS: 1000})
	p.submit(&media.VideoFrame{PTS: 2000})
	p.submit(&media.VideoFrame{PTS: 3000})

	// Bypass delivers synchronously — all 3 immediately available
	require.Equal(t, 3, len(delivered))
	assert.Equal(t, int64(1000), delivered[0].PTS)
	assert.Equal(t, int64(2000), delivered[1].PTS)
	assert.Equal(t, int64(3000), delivered[2].PTS)
}

func TestFramePacer_QueueDepthTracking(t *testing.T) {
	// Long tick interval so frames queue up
	p := newFramePacer(200*time.Millisecond, func(f *media.VideoFrame) {})
	defer p.stop()

	p.submit(&media.VideoFrame{PTS: 1000})
	p.submit(&media.VideoFrame{PTS: 2000})
	p.submit(&media.VideoFrame{PTS: 3000})

	snap := p.snapshot()
	assert.GreaterOrEqual(t, snap.queueDepth, int64(1), "queue should have pending frames")
}

func TestFramePacer_DrainsCatchupWhenQueueBuilds(t *testing.T) {
	// When frames arrive faster than the tick rate, the pacer should
	// release multiple frames per tick to drain the queue back to ≤1.
	// This prevents unbounded queue growth when source FPS > pacer FPS.
	var mu sync.Mutex
	var delivered []*media.VideoFrame
	p := newFramePacer(20*time.Millisecond, func(f *media.VideoFrame) {
		mu.Lock()
		delivered = append(delivered, f)
		mu.Unlock()
	})
	defer p.stop()

	// Submit 6 frames at once (simulating burst from faster source)
	for i := 0; i < 6; i++ {
		p.submit(&media.VideoFrame{PTS: int64(i * 1000)})
	}

	// Wait for 3 ticks — should be enough to drain all 6 frames
	time.Sleep(70 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	// All 6 must be delivered, in order
	require.Equal(t, 6, len(delivered), "all frames must be delivered (no drops)")
	for i := 0; i < 6; i++ {
		assert.Equal(t, int64(i*1000), delivered[i].PTS, "frame %d PTS", i)
	}

	// Queue should be drained
	snap := p.snapshot()
	assert.Equal(t, int64(0), snap.queueDepth, "queue should be fully drained")
}
