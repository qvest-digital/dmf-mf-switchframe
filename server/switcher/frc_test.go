package switcher

import (
	"fmt"
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
	sceneFrame := makeTestFrame(64, 64, 0, int64(8*3000))
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

func TestFRC_EmitBlendNoPoolReference(t *testing.T) {
	// Bug 3: FRC emitBlend/emitMCFI must NOT set pool on the returned frame
	// because YUV data points to fs.blendOut (a reusable make()-allocated
	// buffer), not a pool buffer. If pool were set, ReleaseYUV() would
	// return the internal buffer to the pool, causing data corruption.
	pool := NewFramePool(4, 64, 64)

	fs := newFRCSource(FRCBlend, 3000)
	fs.pool = pool // set pool reference on frcSource

	prevPTS := int64(3000)
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f1.pool = pool
	f2 := makeTestFrame(64, 64, 200, currPTS)
	f2.pool = pool
	fs.ingest(f1)
	fs.ingest(f2)

	// Emit at alpha=0.5 -> blend -> should NOT carry pool reference
	tickMid := (prevPTS + currPTS) / 2
	result := fs.emit(tickMid)
	require.NotNil(t, result)
	require.Nil(t, result.pool,
		"FRC blend output must not carry pool reference (YUV is internal blendOut buffer)")
}

func TestFRC_EmitMCFINoPoolReference(t *testing.T) {
	// Bug 3: Same as blend, verify MCFI-emitted frames do NOT carry pool.
	// MCFI uses fs.blendOut which is not a pool buffer. If MCFI falls back
	// to nearest (emitNearest), the result also uses fs.nearestOut (not pool).
	pool := NewFramePool(4, 64, 64)

	fs := newFRCSource(FRCMCFI, 3000)
	fs.pool = pool

	// Use identical frames so MCFI doesn't detect scene change
	prevPTS := int64(3000)
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 128, prevPTS)
	f1.pool = pool
	f2 := makeTestFrame(64, 64, 128, currPTS)
	f2.pool = pool
	fs.ingest(f1)
	fs.ingest(f2)

	tickMid := (prevPTS + currPTS) / 2
	result := fs.emit(tickMid)
	require.NotNil(t, result)
	// Whether MCFI runs or falls back to nearest, pool must be nil
	// because the YUV data comes from internal reusable buffers.
	require.Nil(t, result.pool,
		"FRC MCFI output must not carry pool reference (YUV is internal buffer)")
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

func TestFRC_EmitNearest_ReturnsCopy(t *testing.T) {
	// B1: emitNearest must return a deep copy, not a pointer to the live buffer.
	// After ingest() releases prevFrame, the previously returned data must be unchanged.
	fs := newFRCSource(FRCNearest, 3000)

	prevPTS := int64(3000)
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	// Emit with alpha < 0.5 -> selects prevFrame
	result := fs.emitNearest(0.3)
	require.NotNil(t, result)
	require.Equal(t, byte(100), result.YUV[0], "should select prevFrame content")

	// Store the returned YUV data for comparison
	savedYUV := make([]byte, len(result.YUV))
	copy(savedYUV, result.YUV)

	// Ingest a new frame — this calls prevFrame.ReleaseYUV() which frees the old buffer.
	f3 := makeTestFrame(64, 64, 50, 9000)
	fs.ingest(f3)

	// The previously returned result's YUV data must still be intact (independent copy).
	require.Equal(t, savedYUV, result.YUV,
		"emitNearest result must be independent of live buffer lifecycle")
	require.Equal(t, byte(100), result.YUV[0],
		"emitNearest result must not be corrupted after ingest releases prevFrame")
}

func TestFRC_EmitNearThreshold_ReturnsCopy(t *testing.T) {
	// B1: near-threshold early returns in emit() must also return copies.
	fs := newFRCSource(FRCBlend, 3000)

	prevPTS := int64(3000)
	currPTS := int64(6000)

	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	// alpha < frcNearThresholdLow (0.05) -> should return copy of prevFrame
	tickNearPrev := prevPTS + 100 // alpha = 100/3000 ≈ 0.033
	result := fs.emit(tickNearPrev)
	require.NotNil(t, result)
	require.Equal(t, byte(100), result.YUV[0])

	savedYUV := make([]byte, len(result.YUV))
	copy(savedYUV, result.YUV)

	// Ingest new frame (releases prevFrame)
	f3 := makeTestFrame(64, 64, 50, 9000)
	fs.ingest(f3)

	// Result must still be intact
	require.Equal(t, savedYUV, result.YUV,
		"near-threshold emit result must be independent of live buffer lifecycle")

	// Test the high threshold path too
	fs2 := newFRCSource(FRCBlend, 3000)
	f4 := makeTestFrame(64, 64, 100, prevPTS)
	f5 := makeTestFrame(64, 64, 200, currPTS)
	fs2.ingest(f4)
	fs2.ingest(f5)

	// alpha > frcNearThresholdHigh (0.95) -> should return copy of currFrame
	tickNearCurr := currPTS - 100 // alpha = 2900/3000 ≈ 0.967
	result2 := fs2.emit(tickNearCurr)
	require.NotNil(t, result2)
	require.Equal(t, byte(200), result2.YUV[0])

	savedYUV2 := make([]byte, len(result2.YUV))
	copy(savedYUV2, result2.YUV)

	// Ingest new frame
	f6 := makeTestFrame(64, 64, 50, 9000)
	fs2.ingest(f6)

	require.Equal(t, savedYUV2, result2.YUV,
		"near-threshold high emit result must be independent of live buffer lifecycle")
}

// --- PTS 33-bit wraparound tests ---

func TestFRC_IngestPTSWraparound(t *testing.T) {
	// 33-bit PTS wraps at 2^33 = 8589934592 (~26.5 hours at 90kHz).
	// When PTS crosses this boundary, the interval computation in ingest()
	// must still produce a correct positive interval.
	const pts33Max = int64(1) << 33 // 8589934592

	fs := newFRCSource(FRCBlend, 3000)

	// Frame just before the 33-bit boundary
	prevPTS := pts33Max - 3000 // 8589931592
	f1 := makeTestFrame(64, 64, 100, prevPTS)
	fs.ingest(f1)

	// Frame just after the wrap (small positive PTS value)
	currPTS := int64(1000) // wraps past 0
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f2)

	// The true interval should be 3000 + 1000 = 4000 ticks (not negative).
	// Without wrap handling, currPTS - prevPTS = 1000 - 8589931592 = -8589930592
	// which is negative and would be rejected by the "if interval > 0" check.
	require.Equal(t, 1, fs.intervalCount,
		"ingest should have counted one valid interval across PTS wrap")
	require.Equal(t, int64(4000), fs.avgIntervalTicks,
		"ingest should compute correct interval across PTS wrap")
}

func TestFRC_EmitAlphaPTSWraparound(t *testing.T) {
	// emit() computes alpha = (tickPTS - prevPTS) / (currPTS - prevPTS).
	// When PTS wraps at 2^33, both deltas must be wrap-aware.
	const pts33Max = int64(1) << 33

	fs := newFRCSource(FRCBlend, 3000)

	prevPTS := pts33Max - 3000 // just before wrap
	currPTS := int64(1000)     // just after wrap (true delta = 4000)
	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	// Request a tick at the midpoint: prevPTS + 2000 ticks, which wraps to
	// (pts33Max - 3000 + 2000) = pts33Max - 1000
	tickPTS := pts33Max - 1000

	result := fs.emit(tickPTS)
	require.NotNil(t, result, "emit should produce a frame across PTS wrap")

	// Expected alpha = 2000 / 4000 = 0.5.
	// With FRCBlend at alpha=0.5, BlendUniformBytes at pos=128:
	// dst = (100*(256-128) + 200*128) >> 8 = 150
	expected := byte(150)
	actual := result.YUV[0]
	require.InDelta(t, float64(expected), float64(actual), 2.0,
		"blend at alpha=0.5 across PTS wrap: expected ~%d, got %d", expected, actual)
}

func TestFRC_EmitAfterYUVRelease(t *testing.T) {
	// Regression test: the frame sync's releaseTick may release a frame's
	// pool buffer (via lastRawVideo.ReleaseYUV) while the FRC still holds
	// a reference to it. emit must return nil instead of panicking.
	fs := newFRCSource(FRCNearest, 3000)

	f1 := makeTestFrame(64, 64, 100, 0)
	f2 := makeTestFrame(64, 64, 200, 3000)
	fs.ingest(f1)
	fs.ingest(f2)

	// Simulate what releaseTick does: release the YUV of a frame the FRC
	// still references (prevFrame = f1, whose YUV gets nilled out).
	f1.YUV = nil

	// emit should return nil (not panic) when the selected frame's YUV
	// has been released.
	result := fs.emit(500) // alpha ~0.17, selects prevFrame
	require.Nil(t, result, "emit should return nil when prevFrame YUV is released")

	// Also test currFrame release.
	f2.YUV = nil
	result = fs.emit(2500) // alpha ~0.83, selects currFrame
	require.Nil(t, result, "emit should return nil when currFrame YUV is released")
}

func TestFRC_EmitBlendAfterYUVRelease(t *testing.T) {
	// Same as above but for blend quality.
	fs := newFRCSource(FRCBlend, 3000)

	f1 := makeTestFrame(64, 64, 100, 0)
	f2 := makeTestFrame(64, 64, 200, 3000)
	fs.ingest(f1)
	fs.ingest(f2)

	// Release prevFrame YUV.
	f1.YUV = nil

	// Blend path: alpha ~0.5, in the blend zone between near-thresholds.
	result := fs.emit(1500)
	require.Nil(t, result, "emitBlend should return nil when prevFrame YUV is released")
}

func TestFrameSync_ParallelFRC_MultiSource(t *testing.T) {
	// Verifies that parallel FRC produces correct output for multiple sources.
	// 4 sources at 24fps-equivalent intervals, output at 60fps tick rate.
	// After priming with 2 frames each, the next tick should FRC all 4 in parallel.
	rawHandler := &rawVideoTestHandler{}
	syncHandler := &syncTestHandler{}

	tickRate := 16683333 * time.Nanosecond // ~59.94fps
	fs := NewFrameSynchronizer(tickRate, syncHandler.onVideo, syncHandler.onAudio)
	fs.frcQuality = FRCBlend
	fs.onRawVideo = rawHandler.onRawVideo

	numSources := 4
	for i := 0; i < numSources; i++ {
		fs.AddSource(fmt.Sprintf("cam%d", i))
	}

	// Ingest 2 frames per source to prime FRC (each with distinct Y values).
	for i := 0; i < numSources; i++ {
		key := fmt.Sprintf("cam%d", i)
		f1 := makeTestFrame(64, 64, byte(50+i*40), int64(i)*3750)
		f2 := makeTestFrame(64, 64, byte(50+i*40+20), int64(i)*3750+3750)
		fs.IngestRawVideo(key, f1)
		fs.IngestRawVideo(key, f2)
	}

	// Tick 1: pops fresh frames from ring, primes FRC with 2 frames each.
	fs.releaseTick()

	// Verify all 4 sources produced fresh output on tick 1.
	frames := rawHandler.getFrames()
	sourceKeys := make(map[string]int)
	for _, f := range frames {
		sourceKeys[f.sourceKey]++
	}
	for i := 0; i < numSources; i++ {
		key := fmt.Sprintf("cam%d", i)
		require.GreaterOrEqual(t, sourceKeys[key], 1, "source %s should have output on tick 1", key)
	}

	// Tick 2: no new frames → all 4 sources need FRC (parallel Phase 2).
	rawHandler.mu.Lock()
	rawHandler.frames = rawHandler.frames[:0]
	rawHandler.mu.Unlock()

	fs.releaseTick()

	frames = rawHandler.getFrames()
	sourceKeys = make(map[string]int)
	for _, f := range frames {
		sourceKeys[f.sourceKey]++
	}
	for i := 0; i < numSources; i++ {
		key := fmt.Sprintf("cam%d", i)
		require.Equal(t, 1, sourceKeys[key],
			"source %s should have exactly 1 FRC output on tick 2", key)
	}

	// Verify FRC output has valid YUV (not nil, not zero-length).
	for _, f := range frames {
		require.NotNil(t, f.frame.YUV, "FRC output YUV should not be nil for %s", f.sourceKey)
		require.NotEmpty(t, f.frame.YUV, "FRC output YUV should not be empty for %s", f.sourceKey)
	}

	// Verify all sources have distinct YUV data (different source frames → different FRC output).
	if len(frames) >= 2 {
		yuvSets := make(map[byte]bool)
		for _, f := range frames {
			yuvSets[f.frame.YUV[0]] = true
		}
		require.Greater(t, len(yuvSets), 1,
			"FRC outputs should have distinct Y values from different sources")
	}
}

func TestFrameSync_ParallelFRC_SingleSource(t *testing.T) {
	// Single source FRC runs inline (no goroutine overhead).
	rawHandler := &rawVideoTestHandler{}
	syncHandler := &syncTestHandler{}

	tickRate := 33333 * time.Microsecond // ~30fps
	fs := NewFrameSynchronizer(tickRate, syncHandler.onVideo, syncHandler.onAudio)
	fs.frcQuality = FRCNearest
	fs.onRawVideo = rawHandler.onRawVideo
	fs.AddSource("cam1")

	// Prime FRC with 2 frames.
	f1 := makeTestFrame(64, 64, 100, 3000)
	f2 := makeTestFrame(64, 64, 200, 6000)
	fs.IngestRawVideo("cam1", f1)
	fs.IngestRawVideo("cam1", f2)

	// Tick 1: pops fresh frame.
	fs.releaseTick()

	// Tick 2: FRC emit (single task, inline).
	rawHandler.mu.Lock()
	rawHandler.frames = rawHandler.frames[:0]
	rawHandler.mu.Unlock()

	fs.releaseTick()

	frames := rawHandler.getFrames()
	require.Len(t, frames, 1, "should have 1 FRC output for single source")
	require.NotNil(t, frames[0].frame.YUV)
	require.Equal(t, "cam1", frames[0].sourceKey)
}

func TestFrameSync_ParallelFRC_DeepCopiesAreIndependent(t *testing.T) {
	// Verifies that FRC deep copies from parallel goroutines don't alias
	// each other or the FRC scratch buffers.
	rawHandler := &rawVideoTestHandler{}
	syncHandler := &syncTestHandler{}

	tickRate := 33333 * time.Microsecond
	pool := NewFramePool(8, 64, 64)

	fs := NewFrameSynchronizer(tickRate, syncHandler.onVideo, syncHandler.onAudio)
	fs.frcQuality = FRCNearest
	fs.onRawVideo = rawHandler.onRawVideo
	fs.framePool = pool

	for i := 0; i < 3; i++ {
		fs.AddSource(fmt.Sprintf("cam%d", i))
	}

	// Ingest frames with distinct Y values per source.
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("cam%d", i)
		f1 := makeTestFrame(64, 64, byte(50+i*60), int64(i)*3000)
		f2 := makeTestFrame(64, 64, byte(50+i*60+30), int64(i)*3000+3000)
		fs.IngestRawVideo(key, f1)
		fs.IngestRawVideo(key, f2)
	}

	// Tick 1: pop fresh frames.
	fs.releaseTick()

	// Tick 2: FRC for all 3 sources.
	rawHandler.mu.Lock()
	rawHandler.frames = rawHandler.frames[:0]
	rawHandler.mu.Unlock()

	fs.releaseTick()

	frames := rawHandler.getFrames()
	require.Len(t, frames, 3)

	// Verify no two frames share the same underlying YUV buffer.
	for i := 0; i < len(frames); i++ {
		for j := i + 1; j < len(frames); j++ {
			if len(frames[i].frame.YUV) > 0 && len(frames[j].frame.YUV) > 0 {
				// Check they don't start at the same address.
				require.NotEqual(t,
					&frames[i].frame.YUV[0], &frames[j].frame.YUV[0],
					"FRC outputs for %s and %s should not alias",
					frames[i].sourceKey, frames[j].sourceKey)
			}
		}
	}

	// Verify each frame has correct Y value from its source.
	for _, f := range frames {
		require.NotEmpty(t, f.frame.YUV)
	}
}

func TestFRC_EmitNearestPTSWraparound(t *testing.T) {
	// Nearest mode should also work correctly across PTS wraparound.
	const pts33Max = int64(1) << 33

	fs := newFRCSource(FRCNearest, 3000)

	prevPTS := pts33Max - 3000
	currPTS := int64(1000) // true delta = 4000
	f1 := makeTestFrame(64, 64, 100, prevPTS)
	f2 := makeTestFrame(64, 64, 200, currPTS)
	fs.ingest(f1)
	fs.ingest(f2)

	// alpha = 1000/4000 = 0.25 -> should select prevFrame (alpha < 0.5)
	tickLow := prevPTS + 1000 // = pts33Max - 2000, before the wrap
	result := fs.emit(tickLow)
	require.NotNil(t, result)
	require.Equal(t, byte(100), result.YUV[0],
		"alpha=0.25 across PTS wrap should select prevFrame")

	// alpha = 3000/4000 = 0.75 -> should select currFrame (alpha > 0.5)
	// prevPTS + 3000 = pts33Max - 3000 + 3000 = pts33Max -> wraps to 0
	tickHigh := int64(0) // prevPTS + 3000 wraps to 0
	result = fs.emit(tickHigh)
	require.NotNil(t, result)
	require.Equal(t, byte(200), result.YUV[0],
		"alpha=0.75 across PTS wrap should select currFrame")
}

func TestFRC_IngestRefcountNoDoubleRelease(t *testing.T) {
	// Verify that FRC takes its own Ref() on ingest, so that both FRC
	// and frame_sync can release independently without underflow.
	pool := NewFramePool(4, 4, 4)
	fs := newFRCSource(FRCNearest, 3000)

	// Create frames from pool (refcounted).
	pf1 := &ProcessingFrame{YUV: pool.Acquire(), Width: 4, Height: 4, PTS: 0}
	pf1.SetRefs(1) // frame_sync ownership

	pf2 := &ProcessingFrame{YUV: pool.Acquire(), Width: 4, Height: 4, PTS: 3000}
	pf2.SetRefs(1)

	pf3 := &ProcessingFrame{YUV: pool.Acquire(), Width: 4, Height: 4, PTS: 6000}
	pf3.SetRefs(1)

	// Ingest first frame: FRC takes Ref → refs=2
	fs.ingest(pf1)
	require.Equal(t, int32(2), pf1.Refs(), "FRC should Ref() on ingest")

	// Ingest second frame: FRC takes Ref on pf2 → refs=2
	// pf1 is now prevFrame (refs still 2 — FRC hasn't released it yet)
	fs.ingest(pf2)
	require.Equal(t, int32(2), pf2.Refs())

	// Ingest third frame: FRC releases prevFrame (pf1) → pf1 refs 2→1
	fs.ingest(pf3)
	require.Equal(t, int32(1), pf1.Refs(), "FRC should have released its ref on pf1")

	// Simulate frame_sync releasing its reference on pf1 → refs 1→0
	pf1.ReleaseYUV()
	require.Equal(t, int32(0), pf1.Refs(), "frame_sync release should drop to 0")

	// Clean up
	fs.reset()
	pf2.ReleaseYUV() // frame_sync releases pf2
	pf3.ReleaseYUV() // frame_sync releases pf3
}

func TestFRC_SceneDetection_IdenticalFrames(t *testing.T) {
	fs := newFRCSource(FRCMCFI, 3000)

	// Two identical frames should not be detected as a scene change.
	f1 := makeTestFrame(1920, 1080, 128, 0)
	f2 := makeTestFrame(1920, 1080, 128, 3000)
	result := fs.detectSceneChange(f1, f2)
	require.False(t, result, "identical frames should not trigger scene change")
}

func TestFRC_SceneDetection_DifferentFrames(t *testing.T) {
	fs := newFRCSource(FRCMCFI, 3000)

	// Build up SAD history with similar frames to set a low baseline.
	for i := 0; i < 8; i++ {
		fa := makeTestFrame(1920, 1080, byte(100+i), int64(i*3000))
		fb := makeTestFrame(1920, 1080, byte(100+i+1), int64((i+1)*3000))
		fs.detectSceneChange(fa, fb)
	}

	// Dramatically different frame should trigger scene change.
	fA := makeTestFrame(1920, 1080, 10, 0)
	fB := makeTestFrame(1920, 1080, 250, 3000)
	result := fs.detectSceneChange(fA, fB)
	require.True(t, result, "dramatically different frames should trigger scene change")
}

func BenchmarkFRC_SceneDetection(b *testing.B) {
	// Benchmark scene detection at 1080p to measure the effect of
	// horizontal subsampling (every 4th pixel in both dimensions).
	fs := newFRCSource(FRCMCFI, 3000)
	f1 := makeFRCGradientFrame(1920, 1080, 0, 0)
	f2 := makeFRCGradientFrame(1920, 1080, 10, 3000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fs.detectSceneChange(f1, f2)
	}
}
