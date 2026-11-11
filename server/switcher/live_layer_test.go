package switcher

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/transition"
)

// liveTestSwitcher returns a switcher whose program runs through a
// pipeline into sink, with a live graphics layer on mxl:fill keyed by
// mxl:key.
func liveTestSwitcher(t *testing.T, sink func(*ProcessingFrame)) (*Switcher, *graphics.Compositor, int) {
	t.Helper()
	s := newTestSwitcherDirect(distribution.NewRelay())
	t.Cleanup(s.Close)
	s.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	if sink != nil {
		s.SetRawVideoSink(sink)
	}
	comp := graphics.NewCompositor()
	s.SetCompositor(comp)
	require.NoError(t, s.BuildPipeline())
	s.RegisterMXLSource("mxl:fill")
	s.RegisterMXLSource("mxl:key")
	id, err := comp.AddLayer()
	require.NoError(t, err)
	require.NoError(t, comp.SetLive(id, "mxl:fill", "mxl:key", graphics.LiveOptions{}))
	return s, comp, id
}

func ingestAt(s *Switcher, key string, pts int64, luma byte) {
	s.IngestRawVideo(key, &ProcessingFrame{
		Data: makeTestYUV(320, 240, luma), Width: 320, Height: 240,
		PTS: pts, Format: ColorYUV420_8bit,
	})
}

// The program tick reads the live fill and key at its read point and
// keys the fill over the program; after the tick nothing holds them.
func TestLiveLayerKeyedIntoProgram(t *testing.T) {
	var mu sync.Mutex
	var lumas []byte
	s, comp, id := liveTestSwitcher(t, func(pf *ProcessingFrame) {
		mu.Lock()
		lumas = append(lumas, pf.Data[0])
		mu.Unlock()
	})
	require.NoError(t, comp.On(id))

	const read = 900000
	ingestAt(s, "mxl:fill", read, 200)
	ingestAt(s, "mxl:key", read, 235)
	s.processVideoTick(clock.Tick{VideoPTS: read + 6006, ReadPTS: read, FrameDuration: 33366666})

	mu.Lock()
	require.Equal(t, []byte{200}, lumas, "black program under an opaque fill shows the fill")
	mu.Unlock()
	require.Empty(t, comp.SnapshotVisibleLayers(), "frames cleared after the tick")
}

// A fill a grain ahead of its key is keyed with the key of its own grain.
func TestLiveLayerFeedAligned(t *testing.T) {
	s, comp, id := liveTestSwitcher(t, nil)
	require.NoError(t, comp.On(id))

	ingestAt(s, "mxl:fill", 3003, 100)
	ingestAt(s, "mxl:fill", 6006, 110)
	ingestAt(s, "mxl:key", 3003, 235)

	release := s.feedLiveLayers(s.frameSync, clock.Tick{ReadPTS: 6006})
	snaps := comp.SnapshotVisibleLayers()
	require.Len(t, snaps, 1)
	require.Equal(t, byte(100), snaps[0].LiveFill.Data[0], "the fill of the key's grain")
	release()
	require.Empty(t, comp.SnapshotVisibleLayers())

	require.NoError(t, comp.Off(id))
	require.Empty(t, comp.LiveSources(), "an off layer is not fed")
}

// While a live layer is on, its fill is on air and its key shapes the
// program: tally reports them as a key's fill and alpha.
func TestLiveLayerTally(t *testing.T) {
	s, comp, id := liveTestSwitcher(t, nil)
	tally := func() map[string]string { return s.State().TallyState }

	require.Equal(t, string(TallyIdle), tally()["mxl:fill"])
	require.NoError(t, comp.On(id))
	require.Equal(t, string(TallyKeyFill), tally()["mxl:fill"])
	require.Equal(t, string(TallyKeyAlpha), tally()["mxl:key"])

	require.NoError(t, comp.Off(id))
	require.Equal(t, string(TallyIdle), tally()["mxl:fill"])
	require.Equal(t, string(TallyIdle), tally()["mxl:key"])
}

// A source decoded on the GPU has no CPU pixels to key: the live layer
// draws nothing, and its fill is not tallied as on air.
func TestLiveLayerGPUDecodedSourceNotTallied(t *testing.T) {
	s, comp, id := liveTestSwitcher(t, nil)
	require.NoError(t, comp.On(id))
	require.Equal(t, string(TallyKeyFill), s.State().TallyState["mxl:fill"])

	const read = 900000
	s.IngestRawVideo("mxl:fill", &ProcessingFrame{Width: 320, Height: 240, PTS: read})
	ingestAt(s, "mxl:key", read, 235)
	s.processVideoTick(clock.Tick{VideoPTS: read + 6006, ReadPTS: read, FrameDuration: 33366666})

	tally := s.State().TallyState
	require.Equal(t, string(TallyIdle), tally["mxl:fill"])
	require.Equal(t, string(TallyIdle), tally["mxl:key"])
}

// livePoolFrame returns a managed program-sized frame from pool, as a
// producer that ingests pool-backed frames makes it.
func livePoolFrame(pool *FramePool, pts int64, luma byte) *ProcessingFrame {
	pf := &ProcessingFrame{Data: pool.Acquire(), Width: 320, Height: 240, PTS: pts,
		Format: ColorYUV420_8bit, pool: pool}
	copy(pf.Data, makeTestYUV(320, 240, luma))
	pf.SetRefs(1)
	return pf
}

// The frames a tick hands a live layer are given back however the tick
// runs: a transition and an FTB hold return early, and a reference kept
// past the tick would hold the source's buffers out of the pool.
func TestLiveLayerFramesReleasedOnTransitionAndFTB(t *testing.T) {
	const read = 900000
	tick := clock.Tick{VideoPTS: read + 6006, ReadPTS: read, FrameDuration: 33366666}
	for name, enter := range map[string]func(*Switcher){
		"transition": func(s *Switcher) {
			s.SetTransitionConfig(mockTransitionCodecs())
			s.RegisterMXLSource("mxl:a")
			s.RegisterMXLSource("mxl:b")
			require.NoError(t, s.Cut(context.Background(), "mxl:a"))
			require.NoError(t, s.StartTransition(context.Background(), "mxl:b", "mix", 5000, ""))
		},
		"ftb hold": func(s *Switcher) {
			s.configMu.Lock()
			s.updateHotState(func(h *hotState) { h.state = StateFTB })
			s.configMu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, comp, id := liveTestSwitcher(t, nil)
			require.NoError(t, comp.On(id))
			enter(s)
			pool := NewFramePool(2, 320, 240, ColorYUV420_8bit)
			fill, key := livePoolFrame(pool, read, 200), livePoolFrame(pool, read, 235)
			s.IngestRawVideo("mxl:fill", fill)
			s.IngestRawVideo("mxl:key", key)

			// Fed: the tick's references are on top of the ring's.
			release := s.feedLiveLayers(s.frameSync, tick)
			require.Equal(t, int32(2), fill.Refs())
			release()

			s.processVideoTick(tick)
			require.Equal(t, int32(1), fill.Refs(), "only the ring holds the fill")
			require.Equal(t, int32(1), key.Refs(), "only the ring holds the key")
			require.Empty(t, comp.SnapshotVisibleLayers(), "no picture left on the layer")
			s.frameSync.RemoveSource("mxl:fill")
			s.frameSync.RemoveSource("mxl:key")
			require.Equal(t, 2, poolFree(pool), "both buffers back in the pool")
		})
	}
}

// A live key that is also the program source is read twice in one tick,
// once taken by program and once peeked by the layer. Both references
// are given back, and the program's in-place compositing does not reach
// the frame the synchronizer keeps, which a repeated grain reads again.
func TestLiveLayerSourceOnProgram(t *testing.T) {
	var mu sync.Mutex
	var lumas []byte
	s, comp, id := liveTestSwitcher(t, func(pf *ProcessingFrame) {
		mu.Lock()
		lumas = append(lumas, pf.Data[0])
		mu.Unlock()
	})
	require.NoError(t, comp.On(id))
	require.NoError(t, s.Cut(context.Background(), "mxl:key"))
	pool := NewFramePool(2, 320, 240, ColorYUV420_8bit)
	const read = 900000
	fill, key := livePoolFrame(pool, read, 100), livePoolFrame(pool, read, 126)
	s.IngestRawVideo("mxl:fill", fill)
	s.IngestRawVideo("mxl:key", key)

	for i := range 2 { // the second tick repeats the grain
		s.processVideoTick(clock.Tick{VideoPTS: read + 6006 + int64(i)*3003, ReadPTS: read + int64(i)*3003, FrameDuration: 33366666})
	}
	mu.Lock()
	require.Len(t, lumas, 2)
	require.Equal(t, lumas[0], lumas[1], "a repeated grain composites the same")
	require.InDelta(t, 113, int(lumas[0]), 1, "half the fill over the key's own picture")
	mu.Unlock()
	require.Equal(t, byte(126), key.Data[0], "the key the synchronizer keeps is not composited into")
	require.Equal(t, int32(1), key.Refs(), "program's and the layer's references given back")
	require.Equal(t, int32(1), fill.Refs())
}

// SetLive, ClearLive and on/off run on API goroutines while ticks read
// and feed the layer.
func TestLiveLayerSetLiveRacesTick(t *testing.T) {
	s, comp, id := liveTestSwitcher(t, nil)
	require.NoError(t, comp.On(id))
	const read = 900000
	ingestAt(s, "mxl:fill", read, 200)
	ingestAt(s, "mxl:key", read, 235)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			fill, key := "mxl:fill", "mxl:key"
			if i%2 == 1 {
				fill, key = key, fill
			}
			_ = comp.SetLive(id, fill, key, graphics.LiveOptions{})
			_ = comp.On(id)
			if i%3 == 0 {
				_ = comp.ClearLive(id)
			}
		}
	}()
	for i := range 200 {
		s.processVideoTick(clock.Tick{VideoPTS: read + 6006 + int64(i)*3003, ReadPTS: read, FrameDuration: 33366666})
		_ = s.State()
	}
	close(done)
	wg.Wait()
}
