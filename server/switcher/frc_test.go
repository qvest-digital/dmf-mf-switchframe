package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- FRCQuality tests ---

func TestFRC_ParseQuality(t *testing.T) {
	tests := []struct {
		input    string
		expected FRCQuality
	}{
		{"none", FRCNone},
		{"nearest", FRCNearest},
		{"blend", FRCBlend},
		{"mcfi", FRCMCFI},
		{"bogus", FRCNone},
		{"", FRCNone},
		{"MCFI", FRCNone}, // case-sensitive
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			require.Equal(t, tc.expected, ParseFRCQuality(tc.input))
		})
	}
}

func TestFRC_QualityString(t *testing.T) {
	for _, q := range []FRCQuality{FRCNone, FRCNearest, FRCBlend, FRCMCFI} {
		s := q.String()
		require.NotEmpty(t, s, "String() should not be empty for %d", q)
		roundTrip := ParseFRCQuality(s)
		require.Equal(t, q, roundTrip, "round-trip failed for %q", s)
	}
}

// --- frcSource unit tests ---

// makeTestFrame creates a ProcessingFrame with a solid Y value for testing.
func makeTestFrame(width, height int, yVal byte, pts int64) *ProcessingFrame {
	totalSize := width * height * 3 / 2
	yuv := make([]byte, totalSize)
	ySize := width * height

	// Fill Y plane
	for i := 0; i < ySize; i++ {
		yuv[i] = yVal
	}
	// Fill Cb/Cr with 128 (neutral chroma)
	for i := ySize; i < totalSize; i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  width,
		Height: height,
		PTS:    pts,
		Codec:  "h264",
	}
}

// makeFRCGradientFrame creates a ProcessingFrame with a horizontal gradient.
func makeFRCGradientFrame(width, height int, offset byte, pts int64) *ProcessingFrame {
	totalSize := width * height * 3 / 2
	yuv := make([]byte, totalSize)
	ySize := width * height

	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			yuv[row*width+col] = byte((col + int(offset)) & 0xFF)
		}
	}
	for i := ySize; i < totalSize; i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  width,
		Height: height,
		PTS:    pts,
		Codec:  "h264",
	}
}

func TestFRC_IngestBuildsState(t *testing.T) {
	fs := newFRCSource(FRCBlend, 3000)

	f1 := makeTestFrame(64, 64, 100, 3000)
	f2 := makeTestFrame(64, 64, 110, 6000)

	fs.ingest(f1)
	require.False(t, fs.canInterpolate(), "should not interpolate with 1 frame")

	fs.ingest(f2)
	require.True(t, fs.canInterpolate(), "should interpolate with 2 frames")
	require.True(t, fs.hasTwo)
}

func TestFRC_IngestSingleFrame(t *testing.T) {
	fs := newFRCSource(FRCBlend, 3000)

	f1 := makeTestFrame(64, 64, 100, 3000)
	fs.ingest(f1)

	require.False(t, fs.canInterpolate())
	require.Nil(t, fs.emit(4500), "emit should return nil with only 1 frame")
}

func TestFRC_EmitNearest(t *testing.T) {
	fs := newFRCSource(FRCNearest, 3000)

	prevPTS := int64(3000) // at 30fps with 90kHz clock = 3000 ticks/frame
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	// alpha = 0.3 -> should return prevFrame
	tickLow := prevPTS + int64(float64(currPTS-prevPTS)*0.3) // 3900
	result := fs.emit(tickLow)
	require.NotNil(t, result)
	require.Equal(t, byte(100), result.YUV[0], "alpha=0.3 should select prevFrame")

	// alpha = 0.7 -> should return currFrame
	tickHigh := prevPTS + int64(float64(currPTS-prevPTS)*0.7) // 5100
	result = fs.emit(tickHigh)
	require.NotNil(t, result)
	require.Equal(t, byte(200), result.YUV[0], "alpha=0.7 should select currFrame")
}

func TestFRC_EmitBlend(t *testing.T) {
	fs := newFRCSource(FRCBlend, 3000)

	prevPTS := int64(3000)
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	// alpha = 0.5 -> blended result should be average of 100 and 200
	tickMid := (prevPTS + currPTS) / 2 // 4500
	result := fs.emit(tickMid)
	require.NotNil(t, result)

	// BlendUniformBytes at pos=128 with a=100, b=200:
	// dst = (100*(256-128) + 200*128) >> 8 = (12800 + 25600) >> 8 = 38400 >> 8 = 150
	expected := byte(150)
	actual := result.YUV[0]
	// Allow +/- 2 for integer rounding
	require.InDelta(t, float64(expected), float64(actual), 2.0,
		"blend at alpha=0.5: expected ~%d, got %d", expected, actual)
}

func TestFRC_EmitMCFI_Static(t *testing.T) {
	// With identical (static) frames, MCFI output should be very close to source
	fs := newFRCSource(FRCMCFI, 3000)

	// Use 64x64 frames (multiple of 16 block size)
	prevPTS := int64(3000)
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 128, prevPTS)
	f2 := makeTestFrame(64, 64, 128, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	tickMid := (prevPTS + currPTS) / 2
	result := fs.emit(tickMid)
	require.NotNil(t, result)

	// Static scene: all pixels should be very close to 128
	for i := 0; i < 64*64; i++ {
		require.InDelta(t, 128.0, float64(result.YUV[i]), 3.0,
			"pixel[%d] should be ~128 for static MCFI", i)
	}
}

func TestFRC_SceneChangeDetection(t *testing.T) {
	fs := newFRCSource(FRCMCFI, 3000)

	// Build up some SAD history with similar frames
	for i := 0; i < 8; i++ {
		f := makeTestFrame(64, 64, byte(100+i), int64(i*3000))
		fs.ingest(f)
	}

	// Now ingest a dramatically different frame (scene change)
	sceneFrame := makeTestFrame(64, 64, 0, int64(8 * 3000))
	fs.ingest(sceneFrame)

	// Scene change should be detected
	require.True(t, fs.sceneChange, "should detect scene change with dramatic luminance shift")

	// emit should fall back to nearest (no MCFI)
	tickMid := int64(7*3000) + 1500
	result := fs.emit(tickMid)
	require.NotNil(t, result)
}

func TestFRC_AdaptiveThreshold(t *testing.T) {
	fs := newFRCSource(FRCMCFI, 3000)

	// Feed frames with moderate but consistent motion (gradient shift)
	// This should build up a reasonable SAD history without triggering scene change
	for i := 0; i < 12; i++ {
		f := makeFRCGradientFrame(64, 64, byte(i*3), int64(i*3000))
		fs.ingest(f)
	}

	// The threshold adapts to the content. Continuing with the same pattern
	// should NOT trigger a scene change (it's consistent motion).
	f := makeFRCGradientFrame(64, 64, byte(12*3), int64(12*3000))
	fs.ingest(f)
	require.False(t, fs.sceneChange,
		"consistent motion pattern should not false-positive as scene change")
}

func TestFRC_NearThreshold(t *testing.T) {
	fs := newFRCSource(FRCBlend, 3000)

	prevPTS := int64(3000)
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	// alpha < 0.05 -> should return prevFrame directly (no blend)
	tickNearPrev := prevPTS + 100 // alpha = 100/3000 ≈ 0.033
	result := fs.emit(tickNearPrev)
	require.NotNil(t, result)
	require.Equal(t, byte(100), result.YUV[0], "alpha < 0.05 should return prevFrame directly")

	// alpha > 0.95 -> should return currFrame directly
	tickNearCurr := currPTS - 100 // alpha = 2900/3000 ≈ 0.967
	result = fs.emit(tickNearCurr)
	require.NotNil(t, result)
	require.Equal(t, byte(200), result.YUV[0], "alpha > 0.95 should return currFrame directly")
}

func TestFRC_Reset(t *testing.T) {
	fs := newFRCSource(FRCBlend, 3000)

	f1 := makeTestFrame(64, 64, 100, 3000)
	f2 := makeTestFrame(64, 64, 200, 6000)
	fs.ingest(f1)
	fs.ingest(f2)
	require.True(t, fs.canInterpolate())

	fs.reset()
	require.False(t, fs.canInterpolate(), "canInterpolate should be false after reset")
	require.Nil(t, fs.prevFrame)
	require.Nil(t, fs.currFrame)
	require.False(t, fs.hasTwo)
}

func TestFRC_AdaptiveDegradation(t *testing.T) {
	fs := newFRCSource(FRCMCFI, 3000)

	// Ingest two frames to build initial state
	f1 := makeTestFrame(64, 64, 100, 1000)
	f2 := makeTestFrame(64, 64, 110, 2000)
	fs.ingest(f1)
	fs.ingest(f2)

	// Now set avgIntervalTicks to an absurdly small value (1 tick = ~11us)
	// so that any real ME execution time exceeds 50% of the interval.
	// At 90kHz, 1 tick = 11.1 microseconds. Even a trivial ME on 64x64
	// takes at least a few dozen microseconds.
	fs.avgIntervalTicks = 1
	fs.intervalCount = 10 // pretend we have stable tracking

	// Ingest another frame - this triggers ME, which will be "slow"
	// relative to the absurdly small avgIntervalTicks
	f3 := makeTestFrame(64, 64, 120, 2001)
	fs.ingest(f3)

	// The ME should have been slow relative to the tiny frame interval,
	// causing degradation to FRCBlend
	require.Equal(t, FRCBlend, fs.effectiveQuality,
		"should degrade to FRCBlend when ME is slow relative to source interval")
	require.Equal(t, FRCMCFI, fs.requestedQuality,
		"requestedQuality should remain FRCMCFI")
}

// --- FrameSynchronizer integration tests ---

// rawVideoTestHandler captures raw video frames released by the FrameSynchronizer.
type rawVideoTestHandler struct {
	mu     sync.Mutex
	frames []rawVideoTagged
}

type rawVideoTagged struct {
	sourceKey string
	frame     ProcessingFrame // copy of the frame
}

func (h *rawVideoTestHandler) onRawVideo(sourceKey string, pf *ProcessingFrame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := *pf
	// Deep copy YUV to avoid races
	if pf.YUV != nil {
		cp.YUV = make([]byte, len(pf.YUV))
		copy(cp.YUV, pf.YUV)
	}
	h.frames = append(h.frames, rawVideoTagged{sourceKey: sourceKey, frame: cp})
}

func (h *rawVideoTestHandler) getFrames() []rawVideoTagged {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]rawVideoTagged, len(h.frames))
	copy(cp, h.frames)
	return cp
}

func TestFrameSync_FRC_24to30(t *testing.T) {
	// 24fps source, 30fps output tick rate.
	// Every 5 output ticks, only 4 source frames arrive -> 1 tick needs interpolation.
	rawHandler := &rawVideoTestHandler{}
	syncHandler := &syncTestHandler{}

	tickRate := 33333 * time.Microsecond // ~30fps

	fs := NewFrameSynchronizer(tickRate, syncHandler.onVideo, syncHandler.onAudio)
	fs.frcQuality = FRCBlend
	fs.onRawVideo = rawHandler.onRawVideo
	fs.AddSource("cam1")

	// Verify FRC was created on the source
	fs.mu.Lock()
	ss := fs.sources["cam1"]
	fs.mu.Unlock()
	ss.mu.Lock()
	require.NotNil(t, ss.frc, "frc should be created when frcQuality != FRCNone")
	ss.mu.Unlock()

	fs.Start()
	defer fs.Stop()

	// Simulate 24fps source: 1 frame every 3750 ticks (90kHz / 24 = 3750)
	sourceInterval := int64(3750)
	numSourceFrames := 24 // 1 second worth of 24fps

	for i := 0; i < numSourceFrames; i++ {
		pts := int64(i) * sourceInterval
		f := makeTestFrame(64, 64, byte(100+i%50), pts)
		fs.IngestRawVideo("cam1", f)
		time.Sleep(time.Second / 24)
	}

	// Wait for all ticks to process
	time.Sleep(100 * time.Millisecond)

	frames := rawHandler.getFrames()
	require.NotEmpty(t, frames, "should have output frames")

	// At 30fps over ~1 second, we expect ~30 output ticks
	// All ticks should have produced a frame (no nil outputs)
	require.GreaterOrEqual(t, len(frames), 20,
		"should have at least 20 output frames from 30fps output")

	// Verify no nil YUV in any output frame
	for i, f := range frames {
		require.NotNil(t, f.frame.YUV, "frame[%d] YUV should not be nil", i)
	}

	// For ticks without a new source frame, the FRC should have interpolated.
	// We verify this indirectly: with FRC blend, some frames should have
	// intermediate Y values (not just source frame values).
	// This is hard to verify deterministically with timing, so we just
	// check that we got enough frames.
}

func TestFrameSync_FRC_SameRate(t *testing.T) {
	// 30fps source, 30fps ticks -> every tick has a fresh frame, FRC never emits.
	rawHandler := &rawVideoTestHandler{}
	syncHandler := &syncTestHandler{}

	tickRate := 33333 * time.Microsecond // ~30fps

	fs := NewFrameSynchronizer(tickRate, syncHandler.onVideo, syncHandler.onAudio)
	fs.frcQuality = FRCBlend
	fs.onRawVideo = rawHandler.onRawVideo
	fs.AddSource("cam1")

	fs.Start()
	defer fs.Stop()

	// Simulate 30fps source: 1 frame every ~33ms
	sourceInterval := int64(3000) // 90kHz / 30 = 3000
	numSourceFrames := 15

	for i := 0; i < numSourceFrames; i++ {
		pts := int64(i) * sourceInterval
		f := makeTestFrame(64, 64, byte(100+i*5), pts)
		fs.IngestRawVideo("cam1", f)
		time.Sleep(33 * time.Millisecond)
	}

	time.Sleep(50 * time.Millisecond)

	frames := rawHandler.getFrames()
	require.NotEmpty(t, frames, "should have output frames")

	// Every tick should have had a fresh frame from the ring buffer,
	// so FRC emit should never have been called.
	// We verify frames were output (not nil).
	require.GreaterOrEqual(t, len(frames), 10,
		"should have at least 10 output frames at same rate")
}

func TestFrameSync_SetFRCQuality(t *testing.T) {
	syncHandler := &syncTestHandler{}

	fs := NewFrameSynchronizer(33*time.Millisecond, syncHandler.onVideo, syncHandler.onAudio)
	fs.AddSource("cam1")
	fs.AddSource("cam2")

	// Initially no FRC
	fs.mu.Lock()
	ss1 := fs.sources["cam1"]
	ss2 := fs.sources["cam2"]
	fs.mu.Unlock()

	ss1.mu.Lock()
	require.Nil(t, ss1.frc, "frc should be nil when quality is FRCNone")
	ss1.mu.Unlock()

	// Enable FRC blend
	fs.SetFRCQuality(FRCBlend)

	ss1.mu.Lock()
	require.NotNil(t, ss1.frc, "frc should be created after SetFRCQuality(FRCBlend)")
	require.Equal(t, FRCBlend, ss1.frc.effectiveQuality)
	ss1.mu.Unlock()

	ss2.mu.Lock()
	require.NotNil(t, ss2.frc, "frc should be created on all sources")
	require.Equal(t, FRCBlend, ss2.frc.effectiveQuality)
	ss2.mu.Unlock()

	// Upgrade to MCFI
	fs.SetFRCQuality(FRCMCFI)

	ss1.mu.Lock()
	require.Equal(t, FRCMCFI, ss1.frc.requestedQuality)
	require.Equal(t, FRCMCFI, ss1.frc.effectiveQuality)
	ss1.mu.Unlock()

	// Disable FRC
	fs.SetFRCQuality(FRCNone)

	ss1.mu.Lock()
	require.Nil(t, ss1.frc, "frc should be nil after SetFRCQuality(FRCNone)")
	ss1.mu.Unlock()

	ss2.mu.Lock()
	require.Nil(t, ss2.frc, "frc should be nil after SetFRCQuality(FRCNone)")
	ss2.mu.Unlock()

	// Verify frcQuality stored on FrameSynchronizer
	fs.mu.Lock()
	require.Equal(t, FRCNone, fs.frcQuality)
	fs.mu.Unlock()
}

// --- Additional edge case tests ---

func TestFRC_EmitClampsAlpha(t *testing.T) {
	fs := newFRCSource(FRCBlend, 3000)

	prevPTS := int64(3000)
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	// tickPTS before prevPTS -> alpha < 0 -> clamped to 0 -> near threshold returns prev
	result := fs.emit(1000)
	require.NotNil(t, result)
	require.Equal(t, byte(100), result.YUV[0], "alpha < 0 should return prevFrame")

	// tickPTS after currPTS -> alpha > 1 -> clamped to 1 -> near threshold returns curr
	result = fs.emit(9000)
	require.NotNil(t, result)
	require.Equal(t, byte(200), result.YUV[0], "alpha > 1 should return currFrame")
}

func TestFRC_FPSTracking(t *testing.T) {
	fs := newFRCSource(FRCBlend, 3000)

	// Simulate 30fps: 3000 ticks per frame at 90kHz
	for i := 0; i < 10; i++ {
		f := makeTestFrame(64, 64, byte(100+i), int64(i*3000))
		fs.ingest(f)
	}

	// EMA should settle near 3000
	require.InDelta(t, 3000, float64(fs.avgIntervalTicks), 500,
		"FPS tracking should converge to ~3000 ticks/frame for 30fps")
	require.Equal(t, 9, fs.intervalCount, "should have 9 interval measurements from 10 frames")
}

func TestFRC_EmitZeroPTSDelta(t *testing.T) {
	fs := newFRCSource(FRCBlend, 3000)

	// Two frames with same PTS (degenerate case)
	f1 := makeTestFrame(64, 64, 100, 3000)
	f2 := makeTestFrame(64, 64, 200, 3000) // same PTS
	fs.ingest(f1)
	fs.ingest(f2)

	// Should not panic, returns currFrame
	result := fs.emit(3000)
	require.NotNil(t, result)
	require.Equal(t, byte(200), result.YUV[0], "zero PTS delta should return currFrame")
}
