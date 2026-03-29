package transition

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeSolidYUV420 creates a YUV420 frame filled with a constant Y value
// and neutral chroma (Cb=128, Cr=128).
func makeSolidYUV420(w, h int, yVal byte) []byte {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	buf := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		buf[i] = yVal
	}
	for i := ySize; i < ySize+2*uvSize; i++ {
		buf[i] = 128
	}
	return buf
}

func TestBlendRawMix(t *testing.T) {
	e := NewEngine(EngineConfig{})
	e.StartSync("", "", Mix, 1000)

	frameA := makeSolidYUV420(320, 240, 80)
	frameB := makeSolidYUV420(320, 240, 160)
	w, h := 320, 240
	ySize := w * h

	// pos=0.0: output should equal source A (Y=80)
	result, outW, outH := e.BlendRaw(frameA, w, h, frameB, w, h, 0.0)
	require.NotNil(t, result)
	assert.Equal(t, w, outW)
	assert.Equal(t, h, outH)
	assert.InDelta(t, 80, int(result[0]), 1, "pos=0.0 should be source A")
	assert.InDelta(t, 80, int(result[ySize/2]), 1, "pos=0.0 mid-frame should be source A")

	// pos=0.5: output should be midpoint Y=120
	result, outW, outH = e.BlendRaw(frameA, w, h, frameB, w, h, 0.5)
	require.NotNil(t, result)
	assert.Equal(t, w, outW)
	assert.Equal(t, h, outH)
	assert.InDelta(t, 120, int(result[0]), 1, "pos=0.5 should be midpoint")
	assert.InDelta(t, 120, int(result[ySize/2]), 1, "pos=0.5 mid-frame should be midpoint")

	// pos=1.0: output should equal source B (Y=160)
	result, outW, outH = e.BlendRaw(frameA, w, h, frameB, w, h, 1.0)
	require.NotNil(t, result)
	assert.Equal(t, w, outW)
	assert.Equal(t, h, outH)
	assert.InDelta(t, 160, int(result[0]), 1, "pos=1.0 should be source B")
	assert.InDelta(t, 160, int(result[ySize/2]), 1, "pos=1.0 mid-frame should be source B")
}

func TestBlendRawDip(t *testing.T) {
	e := NewEngine(EngineConfig{})
	e.StartSync("", "", Dip, 1000)

	frameA := makeSolidYUV420(320, 240, 200)
	frameB := makeSolidYUV420(320, 240, 200)
	w, h := 320, 240

	// pos=0.0: output should be source A (Y=200)
	result, _, _ := e.BlendRaw(frameA, w, h, frameB, w, h, 0.0)
	require.NotNil(t, result)
	assert.InDelta(t, 200, int(result[0]), 1, "pos=0.0 should be source A")

	// pos=0.5: output should be broadcast black (Y=16)
	result, _, _ = e.BlendRaw(frameA, w, h, frameB, w, h, 0.5)
	require.NotNil(t, result)
	assert.InDelta(t, 16, int(result[0]), 1, "pos=0.5 should be broadcast black")

	// pos=1.0: output should be source B (Y=200)
	result, _, _ = e.BlendRaw(frameA, w, h, frameB, w, h, 1.0)
	require.NotNil(t, result)
	assert.InDelta(t, 200, int(result[0]), 1, "pos=1.0 should be source B")
}

func TestBlendRawFTB(t *testing.T) {
	e := NewEngine(EngineConfig{})
	e.StartSync("", "", FTB, 1000)

	frameA := makeSolidYUV420(320, 240, 200)
	w, h := 320, 240

	// pos=0.0: output should be full source (Y=200)
	result, outW, outH := e.BlendRaw(frameA, w, h, nil, 0, 0, 0.0)
	require.NotNil(t, result)
	assert.Equal(t, w, outW)
	assert.Equal(t, h, outH)
	assert.InDelta(t, 200, int(result[0]), 1, "pos=0.0 should be full source")

	// pos=1.0: output should be broadcast black (Y=16)
	result, _, _ = e.BlendRaw(frameA, w, h, nil, 0, 0, 1.0)
	require.NotNil(t, result)
	assert.InDelta(t, 16, int(result[0]), 1, "pos=1.0 should be broadcast black")

	// pos=0.5: should be mid-fade
	result, _, _ = e.BlendRaw(frameA, w, h, nil, 0, 0, 0.5)
	require.NotNil(t, result)
	// Mid-fade: gain = 0.5, so Y = 200*0.5 + 16*0.5 = 108
	assert.InDelta(t, 108, int(result[0]), 2, "pos=0.5 should be mid-fade")
}

func TestBlendRawWipe(t *testing.T) {
	e := NewEngine(EngineConfig{
		WipeDirection: WipeHLeft,
	})
	e.StartSync("", "", Wipe, 1000)

	frameA := makeSolidYUV420(320, 240, 80)
	frameB := makeSolidYUV420(320, 240, 200)
	w, h := 320, 240

	// pos=0.5: left half should be B (Y=200), right half should be A (Y=80)
	// For h-left wipe, B reveals from the left.
	result, outW, outH := e.BlendRaw(frameA, w, h, frameB, w, h, 0.5)
	require.NotNil(t, result)
	assert.Equal(t, w, outW)
	assert.Equal(t, h, outH)

	// Sample a pixel in the left quarter (should be fully B = 200)
	leftPixel := result[w/4]
	assert.InDelta(t, 200, int(leftPixel), 5, "left quarter should be source B")

	// Sample a pixel in the right quarter (should be fully A = 80)
	rightPixel := result[3*w/4]
	assert.InDelta(t, 80, int(rightPixel), 5, "right quarter should be source A")

	// pos=0.0: entirely A
	result, _, _ = e.BlendRaw(frameA, w, h, frameB, w, h, 0.0)
	require.NotNil(t, result)
	assert.InDelta(t, 80, int(result[0]), 1, "pos=0.0 should be all A")
	assert.InDelta(t, 80, int(result[w-1]), 1, "pos=0.0 right edge should be all A")

	// pos=1.0: entirely B
	result, _, _ = e.BlendRaw(frameA, w, h, frameB, w, h, 1.0)
	require.NotNil(t, result)
	assert.InDelta(t, 200, int(result[0]), 1, "pos=1.0 should be all B")
	assert.InDelta(t, 200, int(result[w-1]), 1, "pos=1.0 right edge should be all B")
}

func TestBlendRawResolutionMismatch(t *testing.T) {
	e := NewEngine(EngineConfig{})
	e.StartSync("", "", Mix, 1000)

	frameA := makeSolidYUV420(320, 240, 100)
	frameB := makeSolidYUV420(640, 480, 200)

	// from=320x240, to=640x480 — output should match fromYUV dimensions
	result, outW, outH := e.BlendRaw(frameA, 320, 240, frameB, 640, 480, 0.5)
	require.NotNil(t, result, "should not crash on resolution mismatch")
	assert.Equal(t, 320, outW, "output width should match fromYUV")
	assert.Equal(t, 240, outH, "output height should match fromYUV")

	expectedSize := 320 * 240 * 3 / 2
	assert.Equal(t, expectedSize, len(result), "output buffer size should match 320x240 YUV420")

	// The blend result should be roughly midpoint between 100 and 200
	assert.InDelta(t, 150, int(result[0]), 5, "blended Y should be approximately midpoint")
}

func TestAutoAdvancePosition(t *testing.T) {
	e := NewEngine(EngineConfig{})
	e.StartSync("", "", Mix, 1000) // 1000ms duration

	frameDuration := 33.3 // ~30fps

	var pos float64
	var complete bool

	// Run 30 ticks (30 * 33.3ms = 999ms, just under 1000ms)
	for i := 0; i < 30; i++ {
		pos, complete = e.AutoAdvancePosition(frameDuration)
		if complete {
			break
		}
	}

	// After 30 ticks at 33.3ms, position = 30*33.3/1000 = 0.999
	// Should not quite be complete
	assert.InDelta(t, 0.999, pos, 0.01, "after 30 ticks should be near 1.0")

	// One more tick should push past 1.0
	pos, complete = e.AutoAdvancePosition(frameDuration)
	assert.True(t, complete, "should be complete after exceeding 1.0")
	assert.Equal(t, 1.0, pos, "final position should be clamped to 1.0")
	assert.Equal(t, StateIdle, e.State(), "engine should be idle after completion")
}

func TestAutoAdvancePositionZeroDuration(t *testing.T) {
	e := NewEngine(EngineConfig{})
	e.StartSync("", "", Mix, 0)

	pos, complete := e.AutoAdvancePosition(33.3)
	assert.True(t, complete, "zero duration should immediately complete")
	assert.Equal(t, 1.0, pos, "zero duration position should be 1.0")
	assert.Equal(t, StateIdle, e.State())
}

func TestStartSyncDoesNotSpawnGoroutines(t *testing.T) {
	// Warm up the runtime so GC / scheduler goroutines settle.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	before := runtime.NumGoroutine()

	e := NewEngine(EngineConfig{})
	e.StartSync("", "", Mix, 1000)

	// Allow a brief moment for any goroutines to appear
	time.Sleep(20 * time.Millisecond)

	after := runtime.NumGoroutine()

	// StartSync should NOT have spawned any goroutines.
	// Allow +-1 for unrelated runtime jitter (GC, etc).
	assert.InDelta(t, before, after, 1,
		"StartSync should not spawn goroutines (before=%d, after=%d)", before, after)

	// Confirm engine is active but no watchdog was started
	assert.Equal(t, StateActive, e.State())
}

func TestExistingAsyncStillWorks(t *testing.T) {
	// Verify that the existing async Start/IngestFrame/Stop path works
	// after the BlendRaw additions. This is a sanity check that we haven't
	// broken the existing async path.
	e, mu, outputs, completions := newTestEngine(t)

	err := e.Start("cam1", "cam2", Mix, 200)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())

	// Create mock H.264 data (mockDecoder produces solid Y=128 frames)
	fakeH264 := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xff}

	// Ingest from both sources
	e.IngestFrame("cam1", fakeH264, 1000, true)
	e.IngestFrame("cam2", fakeH264, 1000, true)

	// Wait for auto-completion
	time.Sleep(300 * time.Millisecond)

	// Ingest more frames to trigger the auto-complete path
	for i := 0; i < 10; i++ {
		e.IngestFrame("cam2", fakeH264, int64(1000+i*33), false)
		time.Sleep(25 * time.Millisecond)
	}

	mu.Lock()
	outputCount := len(*outputs)
	completionCount := len(*completions)
	mu.Unlock()

	// Should have produced at least one output frame
	assert.Greater(t, outputCount, 0, "async path should produce output frames")

	// Should have completed
	assert.Greater(t, completionCount, 0, "async path should trigger completion")

	e.Stop()
}

func TestBlendRawReturnsCopy(t *testing.T) {
	// Verify that BlendRaw returns a copy, not the blender's internal buffer.
	// Two successive calls should produce independent slices.
	e := NewEngine(EngineConfig{})
	e.StartSync("", "", Mix, 1000)

	frameA := makeSolidYUV420(320, 240, 80)
	frameB := makeSolidYUV420(320, 240, 200)

	result1, _, _ := e.BlendRaw(frameA, 320, 240, frameB, 320, 240, 0.25)
	y1 := result1[0]

	result2, _, _ := e.BlendRaw(frameA, 320, 240, frameB, 320, 240, 0.75)

	// result1 should not have been modified by the second call
	assert.Equal(t, y1, result1[0], "first result should not be mutated by second call")
	// The two results should have different Y values
	assert.NotEqual(t, result1[0], result2[0], "different positions should produce different results")
}

func TestBlendRawOddDimensions(t *testing.T) {
	// Odd dimensions are invalid for YUV420 — BlendRaw should return nil.
	e := NewEngine(EngineConfig{})
	e.StartSync("", "", Mix, 1000)

	frameA := make([]byte, 321*241*3/2)
	frameB := make([]byte, 321*241*3/2)

	result, outW, outH := e.BlendRaw(frameA, 321, 241, frameB, 321, 241, 0.5)
	assert.Nil(t, result, "odd dimensions should return nil")
	assert.Equal(t, 0, outW)
	assert.Equal(t, 0, outH)
}
