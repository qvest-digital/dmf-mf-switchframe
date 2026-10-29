package switcher

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func liveTapPicture(w, h int, fill byte) []byte {
	return bytes.Repeat([]byte{fill}, ColorYUV420_8bit.FrameSize(w, h))
}

// readLiveTap reads into a buffer of its own, as the playout tap does with
// one it takes from the frame pool.
func readLiveTap(sw *Switcher, key string) *LiveTapFrame {
	f, ok := sw.ReadLiveTap(key, nil)
	if !ok {
		return nil
	}
	return &f
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

	require.Nil(t, readLiveTap(sw, "mxl:a"), "nothing has arrived yet")

	in := liveTapPicture(w, h, 0x51)
	ingestLiveTap(sw, "mxl:a", in, ColorYUV420_8bit, w, h, 100)
	got := readLiveTap(sw, "mxl:a")
	require.NotNil(t, got)
	require.Equal(t, liveTapPicture(w, h, 0x51), got.Data)
	require.Equal(t, w, got.Width)
	require.Equal(t, h, got.Height)
	require.Equal(t, int64(100), got.PTS)

	// The producer's buffer goes back to its pool and is overwritten.
	in[0] = 0xFF
	// The reader ingests what it got into the pipeline, which may draw on it.
	got.Data[1] = 0xEE
	again := readLiveTap(sw, "mxl:a")
	require.Equal(t, liveTapPicture(w, h, 0x51), again.Data, "each read must be a copy of its own")

	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x62), ColorYUV420_8bit, w, h, 200)
	latest := readLiveTap(sw, "mxl:a")
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

	require.Nil(t, readLiveTap(sw, "mxl:a"))
	v, _ := sw.liveTaps.entries.Load("mxl:a")
	v.(*liveTapEntry).lastRead.Store(time.Now().Add(-liveTapIdle - time.Second).UnixNano())
	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x62), ColorYUV420_8bit, w, h, 2)
	_, tracked = sw.liveTaps.entries.Load("mxl:a")
	require.False(t, tracked, "a source no item has read for liveTapIdle must stop being copied")
}

// Playout carries 8-bit YUV420 only; a 10-bit picture read as one would
// air as noise. The 10-bit picture here has the byte length of an 8-bit
// 4:2:0 one, so only its format tells them apart.
func TestLiveTap_SkipsTenBitPictures(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	const w, h = 64, 36

	require.Nil(t, readLiveTap(sw, "mxl:a"))
	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x51), ColorYUV422_10bit, w, h, 1)
	require.Nil(t, readLiveTap(sw, "mxl:a"))
}

// Without the warning a 10-bit pipeline's live item airs idle while the
// channel reports the source as missing or unconnected.
func TestLiveTap_WarnsOnceAboutATenBitSource(t *testing.T) {
	var buf bytes.Buffer
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.log = slog.New(slog.NewTextHandler(&buf, nil))
	sw.RegisterMXLSource("mxl:a")
	const w, h = 64, 36

	ingestLiveTap(sw, "mxl:a", make([]byte, ColorYUV422_10bit.FrameSize(w, h)), ColorYUV422_10bit, w, h, 1)
	require.NotContains(t, buf.String(), "playout live item", "a source no item reads is none of playout's business")

	sw.ArmLiveTap("mxl:a")
	for pts := int64(2); pts < 5; pts++ {
		ingestLiveTap(sw, "mxl:a", make([]byte, ColorYUV422_10bit.FrameSize(w, h)), ColorYUV422_10bit, w, h, pts)
	}
	require.Equal(t, 1, strings.Count(buf.String(), "playout live item cannot air a source of this format without a GPU"))
	require.Contains(t, buf.String(), "source=mxl:a")
}

// A live item that starts next must air its input on its first tick, not
// the idle picture while the first read starts the copying.
func TestLiveTap_ArmedSourceHasAPictureOnTheFirstRead(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	const w, h = 64, 36

	sw.ArmLiveTap("mxl:a")
	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x51), ColorYUV420_8bit, w, h, 1)
	got := readLiveTap(sw, "mxl:a")
	require.NotNil(t, got)
	require.Equal(t, liveTapPicture(w, h, 0x51), got.Data)
}

// A source registered again under the same key must not show the former
// registration's last picture.
func TestLiveTap_UnregisterDropsPicture(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterMXLSource("mxl:a")
	const w, h = 64, 36

	require.Nil(t, readLiveTap(sw, "mxl:a"))
	ingestLiveTap(sw, "mxl:a", liveTapPicture(w, h, 0x51), ColorYUV420_8bit, w, h, 1)
	require.NotNil(t, readLiveTap(sw, "mxl:a"))

	sw.UnregisterSource("mxl:a")
	sw.RegisterMXLSource("mxl:a")
	require.Nil(t, readLiveTap(sw, "mxl:a"))
}

// A relay source's decoded pictures bypass IngestRawVideo.
func TestLiveTap_KeepsDecodedRelaySourcePicture(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	sw.RegisterSource("cam1", newTestRelay())
	const w, h = 64, 36

	require.Nil(t, readLiveTap(sw, "cam1"))
	sw.makeDecoderCallback("cam1")("cam1", &ProcessingFrame{
		Data: liveTapPicture(w, h, 0x51), Format: ColorYUV420_8bit, Width: w, Height: h, PTS: 1, Codec: "h264",
	})
	got := readLiveTap(sw, "cam1")
	require.NotNil(t, got)
	require.Equal(t, liveTapPicture(w, h, 0x51), got.Data)
}

// A tapped source and every channel reading it copied a picture into a
// fresh buffer per frame: at 1080p that is 3 MB per picture, garbage at
// the source's and each channel's frame rate.
func TestLiveTap_SteadyStateAllocatesNothing(t *testing.T) {
	// No background goroutines: AllocsPerRun counts every allocation.
	sw := &Switcher{log: slog.Default()}
	const w, h = 1920, 1080
	pf := &ProcessingFrame{Data: liveTapPicture(w, h, 0x51), Format: ColorYUV420_8bit, Width: w, Height: h}
	dst := make([]byte, ColorYUV420_8bit.FrameSize(w, h))
	sw.ArmLiveTap("mxl:a")
	// Both of the entry's buffers exist after two pictures.
	sw.recordLiveTap("mxl:a", pf)
	sw.recordLiveTap("mxl:a", pf)

	allocs := testing.AllocsPerRun(20, func() {
		pf.PTS++
		sw.recordLiveTap("mxl:a", pf)
		f, ok := sw.ReadLiveTap("mxl:a", dst)
		if !ok || &f.Data[0] != &dst[0] {
			t.Fatal("the read must land in the caller's buffer")
		}
	})
	require.Zero(t, allocs)
}

// The writer reuses the buffers a reader copies from; a reader must never
// copy one the writer is filling, or a channel airs a picture made of two.
func TestLiveTap_ReadNeverSeesAPictureBeingWritten(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	t.Cleanup(sw.Close)
	const w, h = 320, 180
	sw.ArmLiveTap("mxl:a")

	done := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			var dst []byte
			for {
				select {
				case <-done:
					return
				default:
				}
				f, ok := sw.ReadLiveTap("mxl:a", dst)
				if !ok {
					continue
				}
				dst = f.Data
				fill := byte(f.PTS)
				if n := bytes.Count(f.Data, []byte{fill}); n != len(f.Data) {
					t.Errorf("picture %d: %d of %d bytes are its own", f.PTS, n, len(f.Data))
					return
				}
			}
		}()
	}
	pic := liveTapPicture(w, h, 0)
	for pts := int64(1); pts <= 500; pts++ {
		for i := range pic {
			pic[i] = byte(pts)
		}
		sw.recordLiveTap("mxl:a", &ProcessingFrame{Data: pic, Format: ColorYUV420_8bit, Width: w, Height: h, PTS: pts})
	}
	close(done)
	readers.Wait()
}

func BenchmarkLiveTap_Record(b *testing.B) {
	sw := &Switcher{log: slog.Default()}
	const w, h = 1920, 1080
	pf := &ProcessingFrame{Data: liveTapPicture(w, h, 0x51), Format: ColorYUV420_8bit, Width: w, Height: h}
	sw.ArmLiveTap("mxl:a")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sw.recordLiveTap("mxl:a", pf)
	}
}

func BenchmarkLiveTap_Read(b *testing.B) {
	sw := &Switcher{log: slog.Default()}
	const w, h = 1920, 1080
	pf := &ProcessingFrame{Data: liveTapPicture(w, h, 0x51), Format: ColorYUV420_8bit, Width: w, Height: h}
	sw.ArmLiveTap("mxl:a")
	sw.recordLiveTap("mxl:a", pf)
	dst := make([]byte, len(pf.Data))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := sw.ReadLiveTap("mxl:a", dst); !ok {
			b.Fatal("no picture")
		}
	}
}
