package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/clock"
)

// poolFrame returns a managed 4x4 frame from pool stamped with pts.
func poolFrame(pool *FramePool, pts int64) *ProcessingFrame {
	pf := &ProcessingFrame{Data: pool.Acquire(), Width: 4, Height: 4, PTS: pts,
		Format: ColorYUV420_8bit, pool: pool}
	pf.SetRefs(1)
	return pf
}

func poolFree(pool *FramePool) int {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return len(pool.free)
}

// A frame of an unregistered source goes back to the pool at once.
func TestFrameSync_IngestUnknownSourceReleases(t *testing.T) {
	pool := NewFramePool(2, 4, 4, ColorYUV420_8bit)
	fs := NewFrameSynchronizer()
	pf := poolFrame(pool, 1000)
	fs.IngestRawVideo("unknown", pf)
	require.Nil(t, pf.Data, "frame of an unknown source not released")
	require.Equal(t, 2, poolFree(pool))
	require.Equal(t, 0, fs.Buffered("unknown"))
}

// A full ring drops its oldest frame and returns that buffer to the pool.
func TestFrameSync_RingOverflowReleasesOldest(t *testing.T) {
	pool := NewFramePool(syncRingSize+2, 4, 4, ColorYUV420_8bit)
	fs := NewFrameSynchronizer()
	fs.AddSource("cam1")
	frames := make([]*ProcessingFrame, syncRingSize+1)
	for i := range frames {
		frames[i] = poolFrame(pool, int64(i+1)*3000)
		fs.IngestRawVideo("cam1", frames[i])
	}
	require.Equal(t, syncRingSize, fs.Buffered("cam1"))
	require.Nil(t, frames[0].Data, "oldest frame not released on overflow")
	require.NotNil(t, frames[1].Data, "second-oldest frame released on overflow")
	require.Equal(t, 1+1, poolFree(pool))
	require.Equal(t, int64(1), fs.source("cam1").overflows.Load())
}

// A frame whose PTS steps back means the source's timeline restarted: the
// frames buffered from the old timeline are released, the new one kept.
func TestFrameSync_PTSStepBackFlushesRing(t *testing.T) {
	pool := NewFramePool(4, 4, 4, ColorYUV420_8bit)
	fs := NewFrameSynchronizer()
	fs.AddSource("cam1")
	old1, old2 := poolFrame(pool, 90000), poolFrame(pool, 93000)
	fs.IngestRawVideo("cam1", old1)
	fs.IngestRawVideo("cam1", old2)
	restart := poolFrame(pool, 3000)
	fs.IngestRawVideo("cam1", restart)

	require.Nil(t, old1.Data, "old-timeline frame kept after a PTS step back")
	require.Nil(t, old2.Data, "old-timeline frame kept after a PTS step back")
	require.Equal(t, 1, fs.Buffered("cam1"))
	require.Equal(t, int64(1), fs.source("cam1").reordered.Load())

	ss := fs.source("cam1")
	ss.mu.Lock()
	got := ss.popAt(3000)
	ss.mu.Unlock()
	require.Same(t, restart, got)
	got.ReleaseYUV()
}

// A source whose anchor lowers its offset steps back by less than its
// buffer: the frames placed at or after the new one came before it and
// are dropped, the ones placed before it stay due before it. Dropping
// them all would leave no frame due until the new one is.
func TestFrameSync_SmallPTSStepBackKeepsTheFramesBeforeIt(t *testing.T) {
	pool := NewFramePool(6, 4, 4, ColorYUV420_8bit)
	fs := NewFrameSynchronizer()
	fs.AddSource("cam1")
	f1, f2, f3, f4 := poolFrame(pool, 1800), poolFrame(pool, 3600), poolFrame(pool, 5400), poolFrame(pool, 7200)
	for _, f := range []*ProcessingFrame{f1, f2, f3, f4} {
		fs.IngestRawVideo("cam1", f)
	}
	stepped := poolFrame(pool, 4500)
	fs.IngestRawVideo("cam1", stepped)

	require.Nil(t, f3.Data, "frame placed after the stepped-back one kept")
	require.Nil(t, f4.Data, "frame placed after the stepped-back one kept")
	require.Equal(t, 3, fs.Buffered("cam1"))
	require.Equal(t, int64(1), fs.source("cam1").reordered.Load())

	ss := fs.source("cam1")
	ss.mu.Lock()
	got := ss.popAt(3600)
	next := ss.popAt(4500)
	ss.mu.Unlock()
	require.Same(t, f2, got, "frame due before the stepped-back one not shown")
	require.Same(t, stepped, next)
	got.ReleaseYUV()
	next.ReleaseYUV()
}

// popAt returns the newest frame at or before the limit, releases the
// older ones and keeps the newer ones for later ticks.
func TestFrameSync_PopAtReleasesOlderKeepsNewer(t *testing.T) {
	pool := NewFramePool(4, 4, 4, ColorYUV420_8bit)
	ss := &syncSource{}
	f1, f2, f3 := poolFrame(pool, 1000), poolFrame(pool, 2000), poolFrame(pool, 3000)
	ss.push(f1)
	ss.push(f2)
	ss.push(f3)

	require.Nil(t, ss.popAt(999), "a frame newer than the limit was returned")
	require.Len(t, ss.ring, 3)

	got := ss.popAt(2500)
	require.Same(t, f2, got)
	require.Nil(t, f1.Data, "older frame not released by popAt")
	require.NotNil(t, f2.Data, "returned frame released")
	require.NotNil(t, f3.Data, "newer frame released before it was due")
	require.Equal(t, []*ProcessingFrame{f3}, ss.ring)
	f2.ReleaseYUV()
	f3.ReleaseYUV()
	require.Equal(t, 4, poolFree(pool))
}

// RemoveSource returns the ring's frames and the retained last frame to the
// pool, so a removed source cannot starve it.
func TestFrameSync_RemoveSourceReleasesPoolBuffers(t *testing.T) {
	s := tickSwitcher(t)
	pool := NewFramePool(3, 4, 4, ColorYUV420_8bit)
	fs := s.frameSync
	before := fs.SourceCount()
	fs.AddSource("cam1")
	fs.IngestRawVideo("cam1", poolFrame(pool, 1000))

	shown, fresh := s.popProgramFrame(fs, "cam1", clock.Tick{VideoPTS: 1000, ReadPTS: 1000})
	require.True(t, fresh)
	shown.ReleaseYUV()
	fs.IngestRawVideo("cam1", poolFrame(pool, 2000))
	require.Equal(t, 1, poolFree(pool), "one frame retained, one buffered")

	fs.RemoveSource("cam1")
	require.Equal(t, 3, poolFree(pool), "RemoveSource kept pool buffers")
	require.Equal(t, before, fs.SourceCount())
}

// A fresh frame replacing the retained last frame releases the old one.
func TestFrameSync_LastRawVideoReleasedOnReplacement(t *testing.T) {
	s := tickSwitcher(t)
	pool := NewFramePool(3, 4, 4, ColorYUV420_8bit)
	fs := s.frameSync
	fs.AddSource("cam1")

	pf1 := poolFrame(pool, 1000)
	fs.IngestRawVideo("cam1", pf1)
	shown, _ := s.popProgramFrame(fs, "cam1", clock.Tick{VideoPTS: 1000, ReadPTS: 1000})
	shown.ReleaseYUV()
	require.Equal(t, 2, poolFree(pool), "first frame retained as the last frame")

	fs.IngestRawVideo("cam1", poolFrame(pool, 2000))
	shown, fresh := s.popProgramFrame(fs, "cam1", clock.Tick{VideoPTS: 2000, ReadPTS: 2000})
	require.True(t, fresh)
	shown.ReleaseYUV()
	require.Nil(t, pf1.Data, "replaced last frame not released")
	require.Equal(t, 2, poolFree(pool), "old buffer back, new one retained")
}

// SetSourceDelay applies to registered sources only.
func TestFrameSync_SetSourceDelay(t *testing.T) {
	fs := NewFrameSynchronizer()
	require.False(t, fs.SetSourceDelay("cam1", 100, 33*time.Millisecond))
	fs.AddSource("cam1")
	require.True(t, fs.SetSourceDelay("cam1", 100, 33*time.Millisecond))
	require.Equal(t, int64(100*90), fs.source("cam1").delay.Load())
}

func TestFrameSync_DebugSnapshot(t *testing.T) {
	pool := NewFramePool(2, 4, 4, ColorYUV420_8bit)
	fs := NewFrameSynchronizer()
	require.Empty(t, fs.DebugSnapshot()["sources"])

	fs.AddSource("cam1")
	fs.SetSourceDelay("cam1", 10, 33*time.Millisecond)
	fs.IngestRawVideo("cam1", poolFrame(pool, 1000))
	fs.IngestRawVideo("cam1", poolFrame(pool, 4000))

	cam1 := fs.DebugSnapshot()["sources"].(map[string]any)["cam1"].(map[string]any)
	require.Equal(t, 2, cam1["buffered"])
	require.Equal(t, int64(900), cam1["delay_90k"])
	require.Equal(t, int64(1000), cam1["oldest_pts"])
	require.Equal(t, int64(4000), cam1["newest_pts"])
	require.Equal(t, int64(0), cam1["overflows"])
	require.Equal(t, int64(0), cam1["reordered"])
	fs.RemoveSource("cam1")
}

// Ingest, add and remove from many goroutines (run under -race).
func TestFrameSync_ConcurrentIngestAddRemove(t *testing.T) {
	fs := NewFrameSynchronizer()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		key := "cam" + string(rune('A'+i%5))
		go func(pts int64) {
			defer wg.Done()
			fs.AddSource(key)
			fs.IngestRawVideo(key, &ProcessingFrame{Data: make([]byte, 24), PTS: pts})
		}(int64(i) * 3000)
		go func() {
			defer wg.Done()
			fs.RemoveSource(key)
		}()
	}
	wg.Wait()
}

// Two sources writing frames of one grain index carry one PTS; read at a
// tick both give the frame of the same grain.
func TestFrameSync_FrameAtAlignsEqualPTS(t *testing.T) {
	pool := NewFramePool(8, 4, 4, ColorYUV420_8bit)
	fs := NewFrameSynchronizer()
	fs.AddSource("fill")
	fs.AddSource("key")
	for _, pts := range []int64{3000, 6000, 9000} {
		fs.IngestRawVideo("fill", poolFrame(pool, pts))
		fs.IngestRawVideo("key", poolFrame(pool, pts))
	}
	for _, read := range []int64{6000, 7500} {
		fill, key := fs.FrameAt("fill", read), fs.FrameAt("key", read)
		require.Equal(t, int64(6000), fill.PTS)
		require.Equal(t, fill.PTS, key.PTS)
		fill.ReleaseYUV()
		key.ReleaseYUV()
	}
	require.Nil(t, fs.FrameAt("fill", 2999), "nothing due yet")
	require.Nil(t, fs.FrameAt("none", 6000))
	require.Equal(t, 3, fs.Buffered("fill"), "a peek takes no frame")
	require.Equal(t, 2, poolFree(pool), "a peek's reference released leaves the frame buffered")
}

// A key a grain late pairs with the fill of its own grain, not the
// fill's newest.
func TestFrameSync_LateKeyYieldsOlderAlignedPair(t *testing.T) {
	pool := NewFramePool(4, 4, 4, ColorYUV420_8bit)
	fs := NewFrameSynchronizer()
	fs.AddSource("fill")
	fs.AddSource("key")
	fs.IngestRawVideo("fill", poolFrame(pool, 3000))
	fs.IngestRawVideo("fill", poolFrame(pool, 6000))
	fs.IngestRawVideo("key", poolFrame(pool, 3000))

	fill, key := alignedLivePair(fs, "fill", "key", 6000)
	require.Equal(t, int64(3000), fill.PTS)
	require.Equal(t, int64(3000), key.PTS)
	fill.ReleaseYUV()
	key.ReleaseYUV()

	// The key arrives: the newest grain again.
	fs.IngestRawVideo("key", poolFrame(pool, 6000))
	fill, key = alignedLivePair(fs, "fill", "key", 6000)
	require.Equal(t, int64(6000), fill.PTS)
	require.Equal(t, int64(6000), key.PTS)
	fill.ReleaseYUV()
	key.ReleaseYUV()
	require.Equal(t, 0, poolFree(pool), "every peeked reference released, the frames still buffered")
}

// A source that is read for a live layer and cut to program keeps its
// program bookkeeping: the peek neither takes the frame nor clears or
// sets the time the source was lost.
func TestFrameSync_PeekLeavesProgramBookkeeping(t *testing.T) {
	s := newTestSwitcherDirect(distribution.NewRelay())
	defer s.Close()
	pool := NewFramePool(4, 4, 4, ColorYUV420_8bit)
	fs := s.frameSync
	fs.AddSource("cam")
	tickAt := func(pts int64) clock.Tick { return clock.Tick{VideoPTS: pts, ReadPTS: pts} }

	fs.IngestRawVideo("cam", poolFrame(pool, 3000))
	f := fs.FrameAt("cam", 3000)
	f.ReleaseYUV()
	pf, fresh := s.popProgramFrame(fs, "cam", tickAt(3000))
	require.True(t, fresh, "the peeked frame is still new to program")
	require.Equal(t, int64(3000), pf.PTS)
	pf.ReleaseYUV()

	// Nothing new: program repeats and marks the source lost.
	pf, fresh = s.popProgramFrame(fs, "cam", tickAt(6000))
	require.False(t, fresh)
	pf.ReleaseYUV()
	ss := fs.source("cam")
	ss.mu.Lock()
	lostAt := ss.lostAt
	ss.mu.Unlock()
	require.False(t, lostAt.IsZero())

	f = fs.FrameAt("cam", 6000)
	require.Equal(t, int64(3000), f.PTS, "the last frame a tick took")
	f.ReleaseYUV()
	fs.IngestRawVideo("cam", poolFrame(pool, 6000))
	f = fs.FrameAt("cam", 6000)
	require.Equal(t, int64(6000), f.PTS)
	f.ReleaseYUV()
	ss.mu.Lock()
	require.Equal(t, lostAt, ss.lostAt, "a peek does not count as delivery")
	ss.mu.Unlock()
	require.Equal(t, 1, fs.Buffered("cam"))

	pf, fresh = s.popProgramFrame(fs, "cam", tickAt(6000))
	require.True(t, fresh)
	require.Equal(t, int64(6000), pf.PTS)
	pf.ReleaseYUV()
}

// A live source that stopped delivering is dropped after SourceLossTimeout
// of media time rather than frozen for good.
func TestFrameSync_FrameAtStale(t *testing.T) {
	pool := NewFramePool(2, 4, 4, ColorYUV420_8bit)
	fs := NewFrameSynchronizer()
	fs.AddSource("fill")
	fs.IngestRawVideo("fill", poolFrame(pool, 3000))

	f := fs.FrameAt("fill", 3000+sourceLossTimeout90k)
	require.NotNil(t, f)
	f.ReleaseYUV()
	require.Nil(t, fs.FrameAt("fill", 3000+sourceLossTimeout90k+1))

	// The source's delay moves its read point back.
	require.True(t, fs.SetSourceDelay("fill", 100, 40*time.Millisecond))
	require.Nil(t, fs.FrameAt("fill", 3000+9000-1))
	f = fs.FrameAt("fill", 3000+9000)
	require.NotNil(t, f)
	f.ReleaseYUV()
}
