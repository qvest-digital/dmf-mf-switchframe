package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/playout"
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

// The pipeline owns the aired buffer until it releases it: the pool must
// not hand it to another reader before then, or that reader overwrites
// the picture the pipeline is still processing.
func TestPlayoutLiveTaps_AiredBufferStaysOutOfThePoolUntilReleased(t *testing.T) {
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

	out := taps.read("playout:1", "mxl:a")
	in := taps.read("playout:1", "mxl:b")
	require.NotNil(t, out)
	require.NotNil(t, in)
	emitted := &switcher.ProcessingFrame{Data: in.YUV, Width: w, Height: h}
	taps.settle("playout:1", emitted)

	// Every buffer the pool hands out until it runs dry and allocates.
	var taken [][]byte
	_, misses := pool.Stats()
	for i := 0; i < 1024; i++ {
		buf := pool.Acquire()
		if _, m := pool.Stats(); m != misses {
			// A fresh allocation, dropped: released, it would take the
			// free slot the aired buffer goes back to.
			break
		}
		taken = append(taken, buf)
	}
	require.NotEmpty(t, taken, "the unaired buffer is back in the pool")
	for _, buf := range taken {
		require.False(t, &buf[0] == &emitted.Data[0], "the pool handed out the aired buffer while the pipeline holds it")
	}
	for _, buf := range taken {
		pool.Release(buf)
	}

	emitted.ReleaseYUV()
	buf := pool.Acquire()
	require.True(t, &buf[0] == &in.YUV[0], "the pipeline's release returns the aired buffer to the pool")
	pool.Release(buf)
}

// A source can publish an empty picture, a 0x0 frame after a real one, and
// the tap then reports a picture with no data: taking its address panicked,
// and airing it would emit the GPU alias signal on an engine without one.
func TestPlayoutLiveTaps_EmptyPictureDoesNotPanic(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	taps := &playoutLiveTaps{sw: sw}
	pool := sw.GetFramePool()

	const w, h = 64, 36
	sw.ArmLiveTap("mxl:a")
	sw.IngestRawVideo("mxl:a", &switcher.ProcessingFrame{
		Data:   bytes.Repeat([]byte{0xAA}, switcher.ColorYUV420_8bit.FrameSize(w, h)),
		Format: switcher.ColorYUV420_8bit, Width: w, Height: h, PTS: 1, Codec: "h264",
	})
	for pts := int64(2); pts <= 3; pts++ {
		sw.IngestRawVideo("mxl:a", &switcher.ProcessingFrame{
			Data: []byte{}, Format: switcher.ColorYUV420_8bit, PTS: pts, Codec: "h264",
		})
	}
	_, missesBefore := pool.Stats()

	var f *playout.DecodedFrame
	require.NotPanics(t, func() { f = taps.read("playout:1", "mxl:a") })
	require.Nil(t, f, "an empty picture is no picture to air")
	taps.settle("playout:1", nil)
	pool.Release(pool.Acquire())
	_, missesAfter := pool.Stats()
	require.Equal(t, missesBefore, missesAfter, "the buffer the read took came back")
}
