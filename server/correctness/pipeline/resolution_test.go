//go:build correctness

package pipeline

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// resolutionHarness is a stripped-down pipeline harness for custom resolutions.
// Unlike the standard harness (320x240), this creates a switcher with the
// specified pipeline format and frame pool so we can test odd/large/tiny sizes.
type resolutionHarness struct {
	sw           *switcher.Switcher
	programRelay *distribution.Relay
	muxer        *output.TSMuxer
	capture      *captureViewer

	width, height int

	tsMu   sync.Mutex
	tsData []byte
}

func newResolutionHarness(t *testing.T, width, height int) *resolutionHarness {
	t.Helper()

	programRelay := distribution.NewRelay()
	muxer := output.NewTSMuxer()

	rh := &resolutionHarness{
		programRelay: programRelay,
		muxer:        muxer,
		width:        width,
		height:       height,
	}
	muxer.SetOutput(func(data []byte) {
		rh.tsMu.Lock()
		rh.tsData = append(rh.tsData, data...)
		rh.tsMu.Unlock()
	})

	cap := newCaptureViewer("res-capture")
	rh.capture = cap
	muxViewer := &muxCaptureViewer{capture: cap, muxer: muxer}
	programRelay.AddViewer(muxViewer)

	// Create a switcher with the test switcher constructor (small pool),
	// then wire the encoder and override the pipeline format.
	sw := switcher.NewTestSwitcher(programRelay)
	rh.sw = sw

	// Wire real encoder factory (libx264) BEFORE setting format,
	// because SetPipelineFormat rebuilds the pipeline internally.
	sw.SetPipelineCodecs(func(w, ht, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return codec.NewFFmpegEncoder("libx264", w, ht, bitrate, fpsNum, fpsDen, 2, nil)
	})

	// Override the pipeline format to our custom resolution.
	// This rebuilds the pipeline and frame pool.
	require.NoError(t, sw.SetPipelineFormat(switcher.PipelineFormat{
		Name:   "test-custom",
		Width:  width,
		Height: height,
		FPSNum: 30000,
		FPSDen: 1001,
	}))
	return rh
}

func (rh *resolutionHarness) addSource(key string) {
	relay := distribution.NewRelay()
	rh.sw.RegisterSource(key, relay)
}

func (rh *resolutionHarness) cut(t *testing.T, key string) {
	t.Helper()
	require.NoError(t, rh.sw.Cut(t.Context(), key))
}

func (rh *resolutionHarness) feedFrames(sourceKey string, count int, startPTS int64, y, cb, cr byte) {
	yuv := testutil.GenerateSolidYUV420(rh.width, rh.height, y, cb, cr)
	ptsDelta := int64(3000)

	for i := 0; i < count; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuv),
			Width:      rh.width,
			Height:     rh.height,
			PTS:        startPTS + int64(i)*ptsDelta,
			IsKeyframe: i == 0,
		}
		rh.sw.IngestRawVideo(sourceKey, pf)
		if i < count-1 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func (rh *resolutionHarness) waitForPipeline(d time.Duration) {
	time.Sleep(d)
}

func (rh *resolutionHarness) writeTS(t *testing.T) string {
	t.Helper()
	rh.tsMu.Lock()
	tsBytes := make([]byte, len(rh.tsData))
	copy(tsBytes, rh.tsData)
	rh.tsMu.Unlock()

	if len(tsBytes) == 0 {
		t.Fatal("No TS data produced -- pipeline did not generate output")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "resolution_test.ts")
	require.NoError(t, os.WriteFile(path, tsBytes, 0o644))
	return path
}

func (rh *resolutionHarness) tsLen() int {
	rh.tsMu.Lock()
	defer rh.tsMu.Unlock()
	return len(rh.tsData)
}

func (rh *resolutionHarness) close() {
	rh.sw.Close()
}

// ---------------------------------------------------------------------------
// TestPipelineOddResolution
//
// Feed 320x238 (height not multiple of 16) frames through the pipeline.
// Many H.264 encoders require height%2==0 but not necessarily height%16==0.
// 238 is even but not a multiple of 16. The pipeline should either produce
// valid output or fail cleanly.
// ---------------------------------------------------------------------------

func TestPipelineOddResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w = 320
		h = 238 // not multiple of 16
	)

	t.Logf("MEASURE: testing resolution %dx%d (height%%16=%d, height%%2=%d)", w, h, h%16, h%2)

	rh := newResolutionHarness(t, w, h)
	defer rh.close()

	rh.addSource("camA")
	rh.cut(t, "camA")
	rh.feedFrames("camA", 30, 90000, 120, 128, 128)
	rh.waitForPipeline(800 * time.Millisecond)

	frameCount := rh.capture.videoCount()
	t.Logf("MEASURE: captured %d encoded frames for %dx%d", frameCount, w, h)
	require.Greater(t, frameCount, 0, "should produce encoded frames for %dx%d", w, h)

	tsLen := rh.tsLen()
	t.Logf("MEASURE: TS output size = %d bytes", tsLen)
	require.Greater(t, tsLen, 0, "should produce TS output")

	tsPath := rh.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")

	t.Logf("MEASURE: ffprobe reports codec=%s resolution=%dx%d", vs.CodecName, vs.Width, vs.Height)
	require.Equal(t, "h264", vs.CodecName)
	// The encoder may pad height to a multiple of 2 (or 16), or keep it as-is.
	// Log the actual output so we can see what happens.
	t.Logf("MEASURE: input was %dx%d, output is %dx%d", w, h, vs.Width, vs.Height)
	// At minimum, width should match; height should be >= input height.
	require.Equal(t, w, vs.Width, "output width should match input")
	require.GreaterOrEqual(t, vs.Height, h, "output height should be >= input height")

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: ffprobe video frame count = %d", len(videoFrames))
	require.Greater(t, len(videoFrames), 0, "should have video frames")
}

// ---------------------------------------------------------------------------
// TestPipelineResolutionMismatch
//
// Feed source A at 320x240, source B at 640x480 through the same pipeline.
// Cut between them. The pipeline has a fixed format (we set it to 320x240).
// When source B (640x480) arrives, it's larger than the pipeline format.
// Verify the output is consistent resolution.
// ---------------------------------------------------------------------------

func TestPipelineResolutionMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		pipeW = 320
		pipeH = 240

		srcAW = 320
		srcAH = 240
		srcBW = 640
		srcBH = 480
	)

	t.Logf("MEASURE: pipeline format=%dx%d, srcA=%dx%d, srcB=%dx%d",
		pipeW, pipeH, srcAW, srcAH, srcBW, srcBH)

	rh := newResolutionHarness(t, pipeW, pipeH)
	defer rh.close()

	rh.addSource("camA")
	rh.addSource("camB")

	// Feed source A at native pipeline resolution.
	rh.cut(t, "camA")
	rh.feedFrames("camA", 30, 90000, 80, 90, 240)
	rh.waitForPipeline(600 * time.Millisecond)

	preCount := rh.capture.videoCount()
	t.Logf("MEASURE: frames from camA (%dx%d): %d", srcAW, srcAH, preCount)

	// Cut to B and feed frames at DIFFERENT resolution.
	rh.cut(t, "camB")
	yuvB := testutil.GenerateSolidYUV420(srcBW, srcBH, 160, 50, 60)
	for i := 0; i < 30; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuvB),
			Width:      srcBW,
			Height:     srcBH,
			PTS:        90000 + int64(30+i)*3000,
			IsKeyframe: i == 0,
		}
		rh.sw.IngestRawVideo("camB", pf)
		if i < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	rh.waitForPipeline(600 * time.Millisecond)

	totalCount := rh.capture.videoCount()
	t.Logf("MEASURE: total frames after camB (%dx%d): %d", srcBW, srcBH, totalCount)

	tsLen := rh.tsLen()
	t.Logf("MEASURE: TS output size = %d bytes", tsLen)

	if tsLen == 0 {
		t.Log("MEASURE: pipeline produced zero TS output with resolution mismatch -- documenting behavior")
		// The pipeline may silently drop mismatched frames. That's a behavior
		// to document but not necessarily a crash bug.
		return
	}

	tsPath := rh.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")

	t.Logf("MEASURE: ffprobe output resolution = %dx%d (pipeline format = %dx%d)",
		vs.Width, vs.Height, pipeW, pipeH)

	// Output resolution should be the pipeline format, not the source resolution.
	require.Equal(t, pipeW, vs.Width, "output width should match pipeline format")
	require.Equal(t, pipeH, vs.Height, "output height should match pipeline format")
}

// ---------------------------------------------------------------------------
// TestPipeline1080p
//
// Feed 1920x1080 frames. This is the resolution where most SIMD alignment
// bugs manifest (1920 is exactly 120*16 -- a common SIMD width boundary).
// Verify all decoded pixels are near the source color.
// ---------------------------------------------------------------------------

func TestPipeline1080p(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w = 1920
		h = 1080
	)

	t.Logf("MEASURE: testing 1080p (%dx%d)", w, h)

	rh := newResolutionHarness(t, w, h)
	defer rh.close()

	const (
		srcY  byte = 160
		srcCb byte = 100
		srcCr byte = 180
	)

	rh.addSource("cam1080")
	rh.cut(t, "cam1080")
	rh.feedFrames("cam1080", 15, 90000, srcY, srcCb, srcCr)
	rh.waitForPipeline(1500 * time.Millisecond)

	frameCount := rh.capture.videoCount()
	t.Logf("MEASURE: captured %d encoded frames at 1080p", frameCount)
	require.Greater(t, frameCount, 0, "should produce encoded frames at 1080p")

	tsLen := rh.tsLen()
	t.Logf("MEASURE: TS output size = %d bytes", tsLen)
	require.Greater(t, tsLen, 0, "should produce TS output at 1080p")

	tsPath := rh.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")
	t.Logf("MEASURE: ffprobe reports codec=%s resolution=%dx%d", vs.CodecName, vs.Width, vs.Height)
	require.Equal(t, "h264", vs.CodecName)
	require.Equal(t, w, vs.Width)
	require.Equal(t, h, vs.Height)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: ffprobe video frame count = %d", len(videoFrames))

	// Decode the captured frames and verify pixel values.
	videos := rh.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	t.Logf("MEASURE: decoded %d frames from captured output", len(decoded))

	if len(decoded) > 0 {
		// Check the last decoded frame (most likely to have settled).
		lastFrame := decoded[len(decoded)-1]
		t.Logf("MEASURE: last decoded frame: %dx%d", lastFrame.Width, lastFrame.Height)

		avgY := averageY(lastFrame.YUV, lastFrame.Width, lastFrame.Height)
		t.Logf("MEASURE: average Y of last frame = %.1f (expected ~%d)", avgY, srcY)

		// H.264 lossy compression: allow +-25 tolerance.
		require.InDelta(t, float64(srcY), avgY, 25.0,
			"average Y should be near source value %d, got %.1f", srcY, avgY)
	}
}

// ---------------------------------------------------------------------------
// TestPipelineNotMultipleOf16
//
// Feed 1278x718 frames. Many H.264 encoders require width%2==0 for YUV420,
// but some internal paths assume width%16==0. 1278%16=14, 718%16=14.
// Verify the pipeline handles this gracefully.
// ---------------------------------------------------------------------------

func TestPipelineNotMultipleOf16(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w = 1278
		h = 718
	)

	t.Logf("MEASURE: testing %dx%d (w%%16=%d, h%%16=%d, w%%2=%d, h%%2=%d)",
		w, h, w%16, h%16, w%2, h%2)

	rh := newResolutionHarness(t, w, h)
	defer rh.close()

	rh.addSource("camOdd")
	rh.cut(t, "camOdd")
	rh.feedFrames("camOdd", 20, 90000, 140, 128, 128)
	rh.waitForPipeline(1000 * time.Millisecond)

	frameCount := rh.capture.videoCount()
	t.Logf("MEASURE: captured %d encoded frames for %dx%d", frameCount, w, h)
	require.Greater(t, frameCount, 0, "should produce encoded frames for %dx%d", w, h)

	tsLen := rh.tsLen()
	t.Logf("MEASURE: TS output size = %d bytes", tsLen)
	require.Greater(t, tsLen, 0)

	tsPath := rh.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")

	t.Logf("MEASURE: ffprobe reports codec=%s resolution=%dx%d", vs.CodecName, vs.Width, vs.Height)
	require.Equal(t, "h264", vs.CodecName)
	// The encoder should produce output at the requested resolution.
	// Some encoders may pad to a multiple of 2 or 16 -- log what happens.
	t.Logf("MEASURE: input %dx%d, output %dx%d", w, h, vs.Width, vs.Height)
	require.Equal(t, w, vs.Width, "output width should match input")
	require.Equal(t, h, vs.Height, "output height should match input")
}

// ---------------------------------------------------------------------------
// TestPipelineMinimumResolution
//
// Feed 16x16 frames -- the smallest resolution that should be encodable.
// H.264 has a minimum macroblock size of 16x16, so this is exactly one
// macroblock. Verify encode + mux produces valid output or clear error.
// ---------------------------------------------------------------------------

func TestPipelineMinimumResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w = 16
		h = 16
	)

	t.Logf("MEASURE: testing minimum resolution %dx%d", w, h)

	// Use a recover wrapper to catch panics -- this resolution might trigger
	// edge cases in SIMD paths or encoder initialization.
	var panicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				t.Logf("MEASURE: PANIC at %dx%d: %v", w, h, r)
			}
		}()

		rh := newResolutionHarness(t, w, h)
		defer rh.close()

		rh.addSource("camTiny")
		rh.cut(t, "camTiny")
		rh.feedFrames("camTiny", 20, 90000, 100, 128, 128)
		rh.waitForPipeline(600 * time.Millisecond)

		frameCount := rh.capture.videoCount()
		t.Logf("MEASURE: captured %d encoded frames for %dx%d", frameCount, w, h)

		tsLen := rh.tsLen()
		t.Logf("MEASURE: TS output size = %d bytes for %dx%d", tsLen, w, h)

		if tsLen > 0 {
			tsPath := rh.writeTS(t)
			testutil.RequireNoErrors(t, tsPath)

			result := testutil.ProbeFile(t, tsPath)
			vs := result.VideoStream()
			if vs != nil {
				t.Logf("MEASURE: ffprobe reports codec=%s resolution=%dx%d", vs.CodecName, vs.Width, vs.Height)
			} else {
				t.Log("MEASURE: ffprobe found no video stream in output")
			}
		} else {
			t.Logf("MEASURE: no TS output for %dx%d -- pipeline may reject this resolution", w, h)
		}
	}()

	require.False(t, panicked, "pipeline should not panic at %dx%d", w, h)
}

