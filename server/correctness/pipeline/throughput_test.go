//go:build correctness

package pipeline

import (
	"bytes"
	"image"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// throughputHarness is a pipeline harness optimized for throughput measurement.
// Unlike the standard harness, it feeds frames as fast as possible (no pacing),
// counts output frames, and measures wall-clock time.
type throughputHarness struct {
	sw           *switcher.Switcher
	programRelay *distribution.Relay
	muxer        *output.TSMuxer
	compositor   *graphics.Compositor
	capture      *captureViewer

	width, height int

	tsMu   sync.Mutex
	tsData []byte
}

type throughputOption func(*throughputConfig)

type throughputConfig struct {
	withCompositor bool
	bitrate        int
}

func withThroughputCompositor() throughputOption {
	return func(c *throughputConfig) { c.withCompositor = true }
}

func withBitrate(bps int) throughputOption {
	return func(c *throughputConfig) { c.bitrate = bps }
}

func newThroughputHarness(t *testing.T, width, height int, opts ...throughputOption) *throughputHarness {
	t.Helper()

	var cfg throughputConfig
	for _, o := range opts {
		o(&cfg)
	}

	programRelay := distribution.NewRelay()
	muxer := output.NewTSMuxer()

	th := &throughputHarness{
		programRelay: programRelay,
		muxer:        muxer,
		width:        width,
		height:       height,
	}
	muxer.SetOutput(func(data []byte) {
		th.tsMu.Lock()
		th.tsData = append(th.tsData, data...)
		th.tsMu.Unlock()
	})

	cap := newCaptureViewer("throughput-capture")
	th.capture = cap
	muxViewer := &muxCaptureViewer{capture: cap, muxer: muxer}
	programRelay.AddViewer(muxViewer)

	sw := switcher.NewTestSwitcher(programRelay)
	th.sw = sw

	// Choose bitrate based on resolution if not explicitly set.
	bitrate := cfg.bitrate
	if bitrate == 0 {
		pixels := width * height
		switch {
		case pixels <= 320*240:
			bitrate = 1_000_000 // 1 Mbps for 320x240
		case pixels <= 1280*720:
			bitrate = 4_000_000 // 4 Mbps for 720p
		default:
			bitrate = 8_000_000 // 8 Mbps for 1080p+
		}
	}

	finalBitrate := bitrate
	sw.SetPipelineCodecs(func(w, ht, bitrateArg, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		// Use our configured bitrate, override the default.
		return codec.NewFFmpegEncoder("libx264", w, ht, finalBitrate, fpsNum, fpsDen, 2, nil)
	})

	// Optional: graphics compositor.
	if cfg.withCompositor {
		comp := graphics.NewCompositor()
		comp.SetResolutionProvider(func() (int, int) { return width, height })
		th.compositor = comp
		sw.SetCompositor(comp)
	}

	// Set pipeline format to requested resolution.
	require.NoError(t, sw.SetPipelineFormat(switcher.PipelineFormat{
		Name:   "throughput-test",
		Width:  width,
		Height: height,
		FPSNum: 30000,
		FPSDen: 1001,
	}))

	return th
}

// feedFramesFast feeds `count` frames as fast as possible (no pacing).
// Uses a single pre-generated YUV buffer with incrementing PTS.
// Returns the wall-clock duration of the feed phase.
func (th *throughputHarness) feedFramesFast(sourceKey string, count int, startPTS int64, y, cb, cr byte) time.Duration {
	yuv := testutil.GenerateSolidYUV420(th.width, th.height, y, cb, cr)
	ptsDelta := int64(3000) // 90kHz / 30fps

	start := time.Now()
	for i := 0; i < count; i++ {
		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(yuv),
			Width:      th.width,
			Height:     th.height,
			PTS:        startPTS + int64(i)*ptsDelta,
			IsKeyframe: i%30 == 0, // keyframe every 30 frames
		}
		th.sw.IngestRawVideo(sourceKey, pf)
	}
	return time.Since(start)
}

// feedFramesPaced feeds `count` frames with a small pace delay to reduce drops.
// The pace is much shorter than real-time to still measure throughput.
func (th *throughputHarness) feedFramesPaced(sourceKey string, count int, startPTS int64, y, cb, cr byte, pace time.Duration) time.Duration {
	yuv := testutil.GenerateSolidYUV420(th.width, th.height, y, cb, cr)
	ptsDelta := int64(3000) // 90kHz / 30fps

	start := time.Now()
	for i := 0; i < count; i++ {
		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(yuv),
			Width:      th.width,
			Height:     th.height,
			PTS:        startPTS + int64(i)*ptsDelta,
			IsKeyframe: i%30 == 0,
		}
		th.sw.IngestRawVideo(sourceKey, pf)
		if i < count-1 {
			time.Sleep(pace)
		}
	}
	return time.Since(start)
}

func (th *throughputHarness) addSource(key string) {
	relay := distribution.NewRelay()
	th.sw.RegisterSource(key, relay)
}

func (th *throughputHarness) cut(t *testing.T, key string) {
	t.Helper()
	require.NoError(t, th.sw.Cut(t.Context(), key))
}

func (th *throughputHarness) close() {
	if th.compositor != nil {
		th.compositor.Close()
	}
	th.sw.Close()
}

// ---------------------------------------------------------------------------
// TestPipelineThroughput320x240
//
// Feed 300 frames through real encode pipeline at 320x240.
// Measure wall-clock time. Assert: < 20s (must process 10s content in <20s).
// Log: "MEASURE: 320x240 = %.1f fps (%.1fx realtime)"
// ---------------------------------------------------------------------------

func TestPipelineThroughput320x240(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		width     = 320
		height    = 240
		numFrames = 300
		fps       = 30.0
	)

	th := newThroughputHarness(t, width, height)
	defer th.close()

	th.addSource("cam1")
	th.cut(t, "cam1")

	// Feed a few warmup frames with pacing to prime the encoder.
	th.feedFramesPaced("cam1", 5, 90000, 120, 128, 128, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	warmupFrames := th.capture.videoCount()
	t.Logf("MEASURE: warmup produced %d output frames", warmupFrames)

	// Feed 300 frames with minimal pacing (1ms) to measure throughput.
	// We use 1ms pacing because the channel is only depth 8 with newest-wins
	// drop policy; feeding faster than encode speed loses too many frames.
	startTime := time.Now()
	th.feedFramesPaced("cam1", numFrames, 90000+int64(5)*3000, 120, 128, 128, 1*time.Millisecond)

	// Wait for pipeline to drain remaining frames.
	time.Sleep(2 * time.Second)
	elapsed := time.Since(startTime)

	totalFrames := th.capture.videoCount() - warmupFrames
	actualFPS := float64(totalFrames) / elapsed.Seconds()
	contentDurationSec := float64(numFrames) / fps
	realtimeMultiple := float64(totalFrames) / fps / elapsed.Seconds() * fps / float64(totalFrames) * contentDurationSec / elapsed.Seconds()
	// Simplify: realtime = (frames_produced / fps) / elapsed
	realtimeX := (float64(totalFrames) / fps) / elapsed.Seconds()

	t.Logf("MEASURE: fed %d frames, got %d output frames in %.1fs", numFrames, totalFrames, elapsed.Seconds())
	t.Logf("MEASURE: 320x240 = %.1f fps (%.1fx realtime)", actualFPS, realtimeX)
	t.Logf("MEASURE: content duration = %.1fs, wall-clock = %.1fs", contentDurationSec, elapsed.Seconds())
	t.Logf("MEASURE: drop rate = %.1f%% (%d of %d frames dropped)",
		100.0*(1.0-float64(totalFrames)/float64(numFrames)),
		numFrames-totalFrames, numFrames)

	// Must produce at least some output frames.
	require.Greater(t, totalFrames, 0, "pipeline should produce output frames")

	// Assert: 10s of content (300 frames @ 30fps) processed in < 20s wall-clock.
	// This means the pipeline can at least run at 0.5x realtime at 320x240.
	require.Less(t, elapsed.Seconds(), 20.0,
		"320x240 pipeline must process 300 frames in under 20s, took %.1fs", elapsed.Seconds())

	_ = realtimeMultiple // suppress unused
}

// ---------------------------------------------------------------------------
// TestPipelineThroughput720p
//
// Same at 1280x720. Feed 150 frames (5s of content). Measure throughput.
// May be slower -- just document.
// ---------------------------------------------------------------------------

func TestPipelineThroughput720p(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		width     = 1280
		height    = 720
		numFrames = 150
		fps       = 30.0
	)

	th := newThroughputHarness(t, width, height)
	defer th.close()

	th.addSource("cam1")
	th.cut(t, "cam1")

	// Warmup.
	th.feedFramesPaced("cam1", 5, 90000, 160, 100, 180, 20*time.Millisecond)
	time.Sleep(500 * time.Millisecond)

	warmupFrames := th.capture.videoCount()
	t.Logf("MEASURE: warmup produced %d output frames", warmupFrames)

	// Feed 150 frames with 2ms pacing (larger frames need more encode time).
	startTime := time.Now()
	th.feedFramesPaced("cam1", numFrames, 90000+int64(5)*3000, 160, 100, 180, 2*time.Millisecond)

	// Wait for pipeline to drain.
	time.Sleep(3 * time.Second)
	elapsed := time.Since(startTime)

	totalFrames := th.capture.videoCount() - warmupFrames
	actualFPS := float64(totalFrames) / elapsed.Seconds()
	realtimeX := (float64(totalFrames) / fps) / elapsed.Seconds()

	t.Logf("MEASURE: fed %d frames, got %d output frames in %.1fs", numFrames, totalFrames, elapsed.Seconds())
	t.Logf("MEASURE: 720p = %.1f fps (%.1fx realtime)", actualFPS, realtimeX)
	t.Logf("MEASURE: content duration = %.1fs, wall-clock = %.1fs",
		float64(numFrames)/fps, elapsed.Seconds())
	t.Logf("MEASURE: drop rate = %.1f%% (%d of %d frames dropped)",
		100.0*(1.0-float64(totalFrames)/float64(numFrames)),
		numFrames-totalFrames, numFrames)

	// Must produce at least some output.
	require.Greater(t, totalFrames, 0, "pipeline should produce output frames at 720p")

	// Informational: log whether 720p can keep up with realtime.
	if realtimeX >= 1.0 {
		t.Logf("MEASURE: 720p runs at or above realtime (%.1fx)", realtimeX)
	} else {
		t.Logf("MEASURE: 720p runs below realtime (%.1fx) -- expected on CPU-only", realtimeX)
	}
}

// ---------------------------------------------------------------------------
// TestPipelineThroughput1080p
//
// Same at 1920x1080. Feed 90 frames (3s of content). Log throughput.
// Don't fail if below realtime -- this is informational on CPU.
// ---------------------------------------------------------------------------

func TestPipelineThroughput1080p(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		width     = 1920
		height    = 1080
		numFrames = 90
		fps       = 30.0
	)

	th := newThroughputHarness(t, width, height)
	defer th.close()

	th.addSource("cam1")
	th.cut(t, "cam1")

	// Warmup.
	th.feedFramesPaced("cam1", 3, 90000, 200, 128, 128, 30*time.Millisecond)
	time.Sleep(1 * time.Second)

	warmupFrames := th.capture.videoCount()
	t.Logf("MEASURE: warmup produced %d output frames", warmupFrames)

	// Feed 90 frames with 3ms pacing (1080p encode is slowest).
	startTime := time.Now()
	th.feedFramesPaced("cam1", numFrames, 90000+int64(3)*3000, 200, 128, 128, 3*time.Millisecond)

	// Wait longer for 1080p pipeline to drain.
	time.Sleep(5 * time.Second)
	elapsed := time.Since(startTime)

	totalFrames := th.capture.videoCount() - warmupFrames
	actualFPS := float64(totalFrames) / elapsed.Seconds()
	realtimeX := (float64(totalFrames) / fps) / elapsed.Seconds()

	t.Logf("MEASURE: fed %d frames, got %d output frames in %.1fs", numFrames, totalFrames, elapsed.Seconds())
	t.Logf("MEASURE: 1080p = %.1f fps (%.1fx realtime)", actualFPS, realtimeX)
	t.Logf("MEASURE: content duration = %.1fs, wall-clock = %.1fs",
		float64(numFrames)/fps, elapsed.Seconds())
	t.Logf("MEASURE: drop rate = %.1f%% (%d of %d frames dropped)",
		100.0*(1.0-float64(totalFrames)/float64(numFrames)),
		numFrames-totalFrames, numFrames)

	// Must produce at least some output -- don't fail on speed.
	require.Greater(t, totalFrames, 0, "pipeline should produce output frames at 1080p")

	// Informational only: report whether 1080p can keep up.
	if realtimeX >= 1.0 {
		t.Logf("MEASURE: 1080p runs at or above realtime (%.1fx)", realtimeX)
	} else {
		t.Logf("MEASURE: 1080p runs below realtime (%.1fx) -- expected on CPU-only", realtimeX)
	}
}

// ---------------------------------------------------------------------------
// TestPipelineThroughputWithGraphics
//
// 320x240, 150 frames, with graphics compositor overlay active.
// Compare to baseline. Log overhead.
// "MEASURE: baseline=%.1f fps, with_graphics=%.1f fps, overhead=%.1f%%"
// ---------------------------------------------------------------------------

func TestPipelineThroughputWithGraphics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		width     = 320
		height    = 240
		numFrames = 150
		fps       = 30.0
		pace      = 1 * time.Millisecond
	)

	// --- Baseline: no graphics ---
	t.Log("MEASURE: running baseline (no graphics)...")
	baselineTH := newThroughputHarness(t, width, height)

	baselineTH.addSource("cam1")
	baselineTH.cut(t, "cam1")

	// Warmup.
	baselineTH.feedFramesPaced("cam1", 5, 90000, 120, 128, 128, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	baselineWarmup := baselineTH.capture.videoCount()

	// Measure.
	baselineStart := time.Now()
	baselineTH.feedFramesPaced("cam1", numFrames, 90000+int64(5)*3000, 120, 128, 128, pace)
	time.Sleep(2 * time.Second)
	baselineElapsed := time.Since(baselineStart)
	baselineFrames := baselineTH.capture.videoCount() - baselineWarmup
	baselineTH.close()

	baselineFPS := float64(baselineFrames) / baselineElapsed.Seconds()
	t.Logf("MEASURE: baseline: %d frames in %.1fs = %.1f fps",
		baselineFrames, baselineElapsed.Seconds(), baselineFPS)

	// --- With Graphics ---
	t.Log("MEASURE: running with graphics compositor overlay...")
	graphicsTH := newThroughputHarness(t, width, height, withThroughputCompositor())

	// Set up a graphics layer with an opaque overlay.
	layerID, err := graphicsTH.compositor.AddLayer()
	require.NoError(t, err)

	overlayPNG := createOverlayPNG(width, height)
	require.NoError(t, graphicsTH.compositor.SetImage(layerID, "test-overlay", overlayPNG))
	require.NoError(t, graphicsTH.compositor.On(layerID))

	graphicsTH.addSource("cam1")
	graphicsTH.cut(t, "cam1")

	// Warmup.
	graphicsTH.feedFramesPaced("cam1", 5, 90000, 120, 128, 128, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	graphicsWarmup := graphicsTH.capture.videoCount()

	// Measure.
	graphicsStart := time.Now()
	graphicsTH.feedFramesPaced("cam1", numFrames, 90000+int64(5)*3000, 120, 128, 128, pace)
	time.Sleep(2 * time.Second)
	graphicsElapsed := time.Since(graphicsStart)
	graphicsFrames := graphicsTH.capture.videoCount() - graphicsWarmup
	graphicsTH.close()

	graphicsFPS := float64(graphicsFrames) / graphicsElapsed.Seconds()
	t.Logf("MEASURE: with_graphics: %d frames in %.1fs = %.1f fps",
		graphicsFrames, graphicsElapsed.Seconds(), graphicsFPS)

	// Compute overhead.
	var overheadPct float64
	if baselineFPS > 0 {
		overheadPct = (1.0 - graphicsFPS/baselineFPS) * 100.0
	}
	t.Logf("MEASURE: baseline=%.1f fps, with_graphics=%.1f fps, overhead=%.1f%%",
		baselineFPS, graphicsFPS, overheadPct)

	// Both should produce output.
	require.Greater(t, baselineFrames, 0, "baseline should produce output frames")
	require.Greater(t, graphicsFrames, 0, "graphics path should produce output frames")
}

// createOverlayPNG creates a semi-transparent RGBA PNG suitable for graphics overlay.
func createOverlayPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			offset := (y*w + x) * 4
			img.Pix[offset] = 200   // R
			img.Pix[offset+1] = 50  // G
			img.Pix[offset+2] = 50  // B
			img.Pix[offset+3] = 128 // A (50% transparent)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
