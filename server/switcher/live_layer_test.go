package switcher

import (
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
