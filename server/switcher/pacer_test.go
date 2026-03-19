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

	// Wait for at least one tick
	time.Sleep(25 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(delivered), 1)
	assert.Equal(t, int64(1000), delivered[0].PTS)
}

func TestFramePacer_NewestWins(t *testing.T) {
	var mu sync.Mutex
	var delivered []*media.VideoFrame
	p := newFramePacer(50*time.Millisecond, func(f *media.VideoFrame) {
		mu.Lock()
		delivered = append(delivered, f)
		mu.Unlock()
	})
	defer p.stop()

	// Submit 3 frames before a tick fires
	p.submit(&media.VideoFrame{PTS: 1000})
	p.submit(&media.VideoFrame{PTS: 2000})
	p.submit(&media.VideoFrame{PTS: 3000})

	time.Sleep(75 * time.Millisecond)

	// Only the newest should have been delivered
	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(delivered), 1)
	assert.Equal(t, int64(3000), delivered[0].PTS)

	snap := p.snapshot()
	assert.Equal(t, int64(2), snap.replaced)
}

func TestFramePacer_EmptyTickCountsUp(t *testing.T) {
	p := newFramePacer(10*time.Millisecond, func(f *media.VideoFrame) {})
	defer p.stop()

	// Don't submit any frames, let ticks run
	time.Sleep(35 * time.Millisecond)

	snap := p.snapshot()
	assert.GreaterOrEqual(t, snap.emptyTicks, int64(2))
}

func TestFramePacer_StopIsSafe(t *testing.T) {
	p := newFramePacer(10*time.Millisecond, func(f *media.VideoFrame) {})
	p.stop()
	p.stop() // double stop is safe

	// Submit after stop is safe (no panic)
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
	assert.GreaterOrEqual(t, snap.paced+snap.emptyTicks, int64(4))
}
