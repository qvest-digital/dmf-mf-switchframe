package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/switcher"
)

// Every buffer a live tap read takes from the frame pool must come back:
// the one the channel airs once the pipeline is done with it, the others
// when the channel emits its frame. One that never comes back is gone from
// the pool for good, and the pool's buffers are what the pipeline runs on.
func TestPlayoutLiveTaps_ReturnEveryBufferToThePool(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	sw.RegisterMXLSource("mxl:b")
	taps := &playoutLiveTaps{sw: sw}
	pool := sw.GetFramePool()

	const w, h = 64, 36
	for key, fill := range map[string]byte{"mxl:a": 0xAA, "mxl:b": 0xBB} {
		sw.ArmLiveTap(key)
		sw.IngestRawVideo(key, &switcher.ProcessingFrame{
			Data:   bytes.Repeat([]byte{fill}, switcher.ColorYUV420_8bit.FrameSize(w, h)),
			Format: switcher.ColorYUV420_8bit, Width: w, Height: h, PTS: 1, Codec: "h264",
		})
	}
	_, missesBefore := pool.Stats()

	// Several times more ticks than the pool has buffers, each reading two
	// inputs as a transition between two live items does.
	for i := 0; i < 3000; i++ {
		out := taps.read("playout:1", "mxl:a")
		in := taps.read("playout:1", "mxl:b")
		require.NotNil(t, out)
		require.NotNil(t, in)
		require.Equal(t, byte(0xAA), out.YUV[0])
		require.Equal(t, byte(0xBB), in.YUV[0])

		var emitted *switcher.ProcessingFrame
		switch i % 3 {
		case 0: // the incoming item's picture airs as is
			emitted = &switcher.ProcessingFrame{Data: in.YUV, Width: w, Height: h}
		case 1: // a blend of both, or a scaled copy
			emitted = &switcher.ProcessingFrame{Data: make([]byte, len(in.YUV)), Width: w, Height: h}
		case 2: // the channel stops
		}
		taps.settle("playout:1", emitted)
		if emitted != nil {
			// What the pipeline does with the frame once done with it.
			emitted.ReleaseYUV()
		}
	}

	_, missesAfter := pool.Stats()
	require.Equal(t, missesBefore, missesAfter, "a buffer that did not come back drained the pool")
}
