package switcher

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func liveTapPicture(w, h int, fill byte) []byte {
	return bytes.Repeat([]byte{fill}, ColorYUV420_8bit.FrameSize(w, h))
}

func ingestLiveTap(sw *Switcher, key string, data []byte, format PipelineColorFormat, w, h int, pts int64) {
	sw.IngestRawVideo(key, &ProcessingFrame{
		Data: data, Format: format, Width: w, Height: h, PTS: pts, Codec: "h264",
	})
}

// A playout live item on an engine without a GPU aired the idle picture
// instead of the input: nothing on the CPU path kept a source's picture
// for the channel to read.
func TestLiveTap_KeepsLatestPictureOfASourceBeingRead(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	const w, h = 64, 36

	require.Nil(t, sw.ReadLiveTap("mxl:a"), "nothing has arrived yet")

	in := liveTapPicture(w, h, 0x51)
	ingestLiveTap(sw, "mxl:a", in, ColorYUV420_8bit, w, h, 100)
	got := sw.ReadLiveTap("mxl:a")
	require.NotNil(t, got)
	require.Equal(t, liveTapPicture(w, h, 0x51), got.Data)
	require.Equal(t, w, got.Width)
	require.Equal(t, h, got.Height)
	require.Equal(t, int64(100), got.PTS)

	// The producer's buffer goes back to its pool and is overwritten.
	in[0] = 0xFF
	// The reader ingests what it got into the pipeline, which may draw on it.
	got.Data[1] = 0xEE
	again := sw.ReadLiveTap("mxl:a")
	require.Equal(t, liveTapPicture(w, h, 0x51), again.Data, "each read must be a copy of its own")

	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x62), ColorYUV420_8bit, w, h, 200)
	latest := sw.ReadLiveTap("mxl:a")
	require.Equal(t, liveTapPicture(w, h, 0x62), latest.Data)
	require.Equal(t, int64(200), latest.PTS)
}

// Copying every source's every picture would cost a 1080p copy per frame
// per input on an engine that airs none of them.
func TestLiveTap_CopiesOnlySourcesBeingRead(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	const w, h = 64, 36

	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x51), ColorYUV420_8bit, w, h, 1)
	_, tracked := sw.liveTaps.entries.Load("mxl:a")
	require.False(t, tracked, "a source nothing reads must not be copied")

	require.Nil(t, sw.ReadLiveTap("mxl:a"))
	v, _ := sw.liveTaps.entries.Load("mxl:a")
	v.(*liveTapEntry).lastRead.Store(time.Now().Add(-liveTapIdle - time.Second).UnixNano())
	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x62), ColorYUV420_8bit, w, h, 2)
	_, tracked = sw.liveTaps.entries.Load("mxl:a")
	require.False(t, tracked, "a source no item has read for liveTapIdle must stop being copied")
}

// Playout carries 8-bit YUV420 only; a 10-bit picture read as one would
// air as noise.
func TestLiveTap_SkipsTenBitPictures(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	const w, h = 64, 36

	require.Nil(t, sw.ReadLiveTap("mxl:a"))
	ingestLiveTap(sw, "mxl:a", make([]byte, ColorYUV422_10bit.FrameSize(w, h)), ColorYUV422_10bit, w, h, 1)
	require.Nil(t, sw.ReadLiveTap("mxl:a"))
}

// A source registered again under the same key must not show the former
// registration's last picture.
func TestLiveTap_UnregisterDropsPicture(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	const w, h = 64, 36

	require.Nil(t, sw.ReadLiveTap("mxl:a"))
	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x51), ColorYUV420_8bit, w, h, 1)
	require.NotNil(t, sw.ReadLiveTap("mxl:a"))

	sw.UnregisterSource("mxl:a")
	sw.RegisterMXLSource("mxl:a")
	require.Nil(t, sw.ReadLiveTap("mxl:a"))
}

// A relay source's decoded pictures bypass IngestRawVideo.
func TestLiveTap_KeepsDecodedRelaySourcePicture(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterSource("cam1", newTestRelay())
	const w, h = 64, 36

	require.Nil(t, sw.ReadLiveTap("cam1"))
	sw.makeDecoderCallback("cam1")("cam1", &ProcessingFrame{
		Data: liveTapPicture(w, h, 0x51), Format: ColorYUV420_8bit, Width: w, Height: h, PTS: 1, Codec: "h264",
	})
	got := sw.ReadLiveTap("cam1")
	require.NotNil(t, got)
	require.Equal(t, liveTapPicture(w, h, 0x51), got.Data)
}
