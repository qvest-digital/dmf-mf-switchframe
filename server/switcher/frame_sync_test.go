package switcher

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
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

// programRate has the ticks read at r for the test's duration, or as
// before a realtime clock starts for a zero r, and restores the rate set
// before.
func programRate(t *testing.T, r clock.Rate) {
	t.Helper()
	t.Cleanup(clock.ResetProgramRate())
	if r.Num > 0 {
		clock.SetProgramRate(r)
	}
}

// Before a realtime clock sets the program rate, a ring keeps
// syncRingSize frames: a full one drops its oldest frame and returns that
// buffer to the pool.
func TestFrameSync_RingOverflowReleasesOldest(t *testing.T) {
	programRate(t, clock.Rate{})
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
	programRate(t, clock.Rate{})
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
	programRate(t, clock.Rate{})
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
	programRate(t, clock.Rate{})
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
	programRate(t, clock.Rate{})
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
	programRate(t, clock.Rate{})
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
	programRate(t, clock.Rate{})
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
	programRate(t, clock.Rate{})
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

// The program shows, on every tick it does not shed, the newest frame of
// its source at or before the tick's read point, at any program rate, for
// a source at that rate or faster, with a delay, and for one a
// TrackingAnchor places 450 ms after its arrival. The ticks run through
// processTick, which sheds the video of a tick taken after its grain
// ended as the sync loop has it do; the program stalls for 300 ms once
// and takes the ticks it owes when it resumes. A tick is taken at the
// start of its grain, or half or nine tenths of a period into it, as a
// busy loop takes it, with the frames that arrived before it ingested; a
// source's grid may lie half its period off the program's. The frames
// the ring keeps are the ones such a tick can still show.
func TestProgramShowsTheDueFrameOfEverySourceThroughAStall(t *testing.T) {
	type pair struct {
		program, source clock.Rate
		delayMs         int
		ahead           time.Duration // how far after its arrival a frame is placed; 0: on its grid
		phase           int           // percent of a period into its grain a tick is taken
		offset          bool          // the source's grid lies half its period off
	}
	var pairs []pair
	for _, r := range []clock.Rate{
		{Num: 15, Den: 1}, {Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1},
		{Num: 30000, Den: 1001}, {Num: 30, Den: 1}, {Num: 50, Den: 1},
		{Num: 60000, Den: 1001}, {Num: 60, Den: 1}, {Num: 100, Den: 1},
		{Num: 120000, Den: 1001}, {Num: 120, Den: 1},
		{Num: 240000, Den: 1001}, {Num: 240, Den: 1}, {Num: 999, Den: 1},
	} {
		pairs = append(pairs, pair{r, r, 0, 0, 0, false})
	}
	for _, p := range [][2]int64{{24, 60}, {24, 50}, {25, 50}, {30, 60}, {50, 100}, {24, 240}, {60, 240}} {
		for _, phase := range []int{0, 50, 90} {
			pairs = append(pairs, pair{clock.Rate{Num: p[0], Den: 1}, clock.Rate{Num: p[1], Den: 1}, 0, 0, phase, phase != 0})
		}
	}
	for _, p := range []int64{24, 60, 240} {
		pairs = append(pairs, pair{clock.Rate{Num: p, Den: 1}, clock.Rate{Num: 240, Den: 1}, 0, 450 * time.Millisecond, 0, false})
		for _, phase := range []int{0, 50, 90} {
			for _, offset := range []bool{false, true} {
				pairs = append(pairs, pair{clock.Rate{Num: p, Den: 1}, clock.Rate{Num: 240, Den: 1}, 0,
					time.Duration(clock.MaxSourceHold) * time.Second / 90000, phase, offset})
			}
		}
	}
	pairs = append(pairs,
		pair{clock.Rate{Num: 240, Den: 1}, clock.Rate{Num: 240, Den: 1}, 500, 0, 0, false},
		pair{clock.Rate{Num: 24, Den: 1}, clock.Rate{Num: 240, Den: 1}, 500, 0, 0, false},
		pair{clock.Rate{Num: 24, Den: 1}, clock.Rate{Num: 240, Den: 1}, 500, 0, 90, true},
		pair{clock.Rate{Num: 25, Den: 1}, clock.Rate{Num: 25, Den: 1}, 500, 0, 0, false},
	)
	for _, c := range pairs {
		t.Run(fmt.Sprintf("%d_%d on %d_%d delay %d ahead %v phase %d offset %v", c.source.Num, c.source.Den, c.program.Num, c.program.Den, c.delayMs, c.ahead, c.phase, c.offset), func(t *testing.T) {
			now := clock.TAINow()
			clock.Epoch()
			clock.SetTAISource(func() int64 { return now })
			t.Cleanup(func() { clock.SetTAISource(nil) })
			programRate(t, c.program)

			s, _ := newSyncTestSwitcher(t)
			defer s.Close()
			s.RegisterSource("cam", distribution.NewRelay())
			s.configMu.Lock()
			s.frameSync.AddSource("cam")
			s.configMu.Unlock()
			require.NoError(t, s.Cut(context.Background(), "cam"))
			// A frame to repeat, without which the loop sheds no tick.
			s.lastBroadcastVFrame.Store(&media.VideoFrame{WireData: []byte{0}, IsKeyframe: true})
			frameDur := time.Duration(clock.IndexToTimestamp(c.program, 1) - clock.IndexToTimestamp(c.program, 0))
			s.frameSync.SetSourceDelay("cam", c.delayMs, frameDur)
			ss := s.frameSync.source("cam")
			delay := int64(c.delayMs) * 90
			ahead := int64(c.ahead) * 9 / 100_000
			pool := s.framePool.Load()
			var offset int64 // ns the source's grid lies off
			if c.offset {
				offset = (clock.IndexToTimestamp(c.source, 1) - clock.IndexToTimestamp(c.source, 0)) / 2
			}
			arrival := func(m uint64) int64 {
				ts := clock.IndexToTimestamp(c.source, m)
				return offset + ts + (clock.IndexToTimestamp(c.source, m+1)-ts)/2
			}
			// at is when the tick of grain n is taken.
			at := func(n uint64) int64 {
				ts := clock.IndexToTimestamp(c.program, n)
				return ts + (clock.IndexToTimestamp(c.program, n+1)-ts)*int64(c.phase)/100
			}

			first := clock.TimestampToIndex(c.program, now) + 1
			grains := func(d time.Duration) uint64 {
				return uint64(int64(d) * c.program.Num / (c.program.Den * int64(time.Second)))
			}
			stall := first + grains(time.Second)           // the last tick on time
			resume := stall + grains(300*time.Millisecond) // the grain the program resumes in
			end := resume + grains(500*time.Millisecond)

			m := clock.TimestampToIndex(c.source, now)
			var arrived []int64 // PTS of the frames ingested, in order
			ticks, misses := 0, 0
			tick := func(n uint64) {
				if clock.TAINow() >= clock.IndexToTimestamp(c.program, n+1) && s.ticksBehind == 0 {
					s.ticksBehind = 1
				}
				shed := s.ticksBehind > 0
				read := clock.IndexPTS(c.program, n-clock.ReaderDelayGrains)
				s.processTick(clock.Tick{
					Index: n, Number: int64(n), VideoPTS: clock.IndexPTS(c.program, n), ReadPTS: read,
					AudioSamples: 1, FrameDuration: frameDur,
				})
				if shed {
					return
				}
				want := int64(-1)
				for _, pts := range arrived {
					if pts <= read-delay {
						want = pts
					}
				}
				if want < 0 {
					return
				}
				ticks++
				ss.mu.Lock()
				got := ss.lastRawVideo
				ss.mu.Unlock()
				if got == nil || got.PTS != want {
					misses++
				}
			}
			for n := first; n < end; {
				if ts := arrival(m); ts <= at(n) {
					now = ts
					// A source without a media clock is placed at its arrival,
					// ahead of it by its anchor; one with a clock at its grid.
					pts := clock.PTS(clock.IndexToTimestamp(c.source, m) + offset)
					if ahead > 0 {
						pts = clock.PTS(ts) + ahead
					}
					arrived = append(arrived, pts)
					pf := &ProcessingFrame{Data: pool.Acquire()[:320*240*3/2], Width: 320, Height: 240, PTS: pts, pool: pool}
					pf.SetRefs(1)
					s.frameSync.IngestRawVideo("cam", pf)
					m++
					continue
				}
				now = at(n)
				switch {
				case n > stall && n < resume:
				case n == resume:
					for k := stall + 1; k <= resume; k++ {
						tick(k)
					}
				default:
					tick(n)
				}
				n++
			}
			if misses > 0 || ticks == 0 {
				t.Fatalf("%d of %d ticks did not show the frame due; %d ring overflows", misses, ticks, ss.overflows.Load())
			}
		})
	}
}

// A source whose frames are all older than the oldest read point a tick
// the clock can still deliver reads, as one whose clock runs seconds
// behind, keeps only its newest frame, the one every tick shows, at any
// rate.
func TestFrameSyncKeepsOnlyTheNewestFrameNoTickCanStillRead(t *testing.T) {
	for _, r := range []clock.Rate{{Num: 24, Den: 1}, {Num: 60000, Den: 1001}, {Num: 240, Den: 1}} {
		t.Run(fmt.Sprintf("%d_%d", r.Num, r.Den), func(t *testing.T) {
			now := clock.TAINow()
			clock.Epoch()
			clock.SetTAISource(func() int64 { return now })
			t.Cleanup(func() { clock.SetTAISource(nil) })
			programRate(t, r)

			fs := NewFrameSynchronizer()
			fs.AddSource("cam")
			behind := clock.TimestampToIndex(r, now-int64(clock.MaxTickCatchUp)-int64(time.Second))
			var newest int64
			for i := uint64(0); i < uint64(r.Num/r.Den); i++ {
				newest = clock.IndexPTS(r, behind-uint64(r.Num/r.Den)+i)
				fs.IngestRawVideo("cam", &ProcessingFrame{PTS: newest})
			}
			require.Equal(t, 1, fs.Buffered("cam"))
			ss := fs.source("cam")
			ss.mu.Lock()
			pf := ss.popAt(clock.NowPTS())
			ss.mu.Unlock()
			require.NotNil(t, pf)
			require.Equal(t, newest, pf.PTS)
		})
	}
}
