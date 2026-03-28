//go:build correctness

package pipeline

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/transition"
)

// makeYUV420Frame creates a solid-color YUV420 frame.
func makeYUV420Frame(w, h int, y, cb, cr byte) []byte {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	buf := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		buf[i] = y
	}
	for i := 0; i < uvSize; i++ {
		buf[ySize+i] = cb
		buf[ySize+uvSize+i] = cr
	}
	return buf
}

// averageCb computes the average Cb value of a YUV420 frame.
func averageCb(yuv []byte, w, h int) float64 {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	if len(yuv) < ySize+uvSize {
		return -1
	}
	var sum int64
	for i := 0; i < uvSize; i++ {
		sum += int64(yuv[ySize+i])
	}
	return float64(sum) / float64(uvSize)
}

// averageCr computes the average Cr value of a YUV420 frame.
func averageCr(yuv []byte, w, h int) float64 {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	if len(yuv) < ySize+2*uvSize {
		return -1
	}
	var sum int64
	for i := 0; i < uvSize; i++ {
		sum += int64(yuv[ySize+uvSize+i])
	}
	return float64(sum) / float64(uvSize)
}

// outputFrame stores a captured output frame from the engine.
type outputFrame struct {
	yuv        []byte
	width      int
	height     int
	pts        int64
	isKeyframe bool
}

// engineTestHarness wraps the transition engine with output capture.
type engineTestHarness struct {
	engine      *transition.Engine
	mu          sync.Mutex
	outputs     []outputFrame
	completions []bool // true = completed normally, false = aborted
}

func newEngineTestHarness(t *testing.T, w, h int, opts ...func(*transition.EngineConfig)) *engineTestHarness {
	t.Helper()
	h2 := &engineTestHarness{}

	cfg := transition.EngineConfig{
		// No DecoderFactory needed -- we use IngestRawFrame.
		HintWidth:  w,
		HintHeight: h,
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			h2.mu.Lock()
			h2.outputs = append(h2.outputs, outputFrame{
				yuv:        cp,
				width:      width,
				height:     height,
				pts:        pts,
				isKeyframe: isKeyframe,
			})
			h2.mu.Unlock()
		},
		OnComplete: func(aborted bool) {
			h2.mu.Lock()
			h2.completions = append(h2.completions, !aborted)
			h2.mu.Unlock()
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	h2.engine = transition.NewEngine(cfg)
	return h2
}

func (h *engineTestHarness) getOutputs() []outputFrame {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]outputFrame, len(h.outputs))
	copy(cp, h.outputs)
	return cp
}

func (h *engineTestHarness) outputCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.outputs)
}

func (h *engineTestHarness) lastOutput() *outputFrame {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.outputs) == 0 {
		return nil
	}
	o := h.outputs[len(h.outputs)-1]
	return &o
}

func (h *engineTestHarness) clearOutputs() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outputs = nil
}

// feedFrameAtPosition sets the manual position, then ingests frames from both sources.
// For non-FTB transitions, source A is ingested first (stored), then source B (triggers blend).
func (h *engineTestHarness) feedFrameAtPosition(
	pos float64,
	srcA, srcB string,
	yuvA, yuvB []byte,
	w, h2 int,
	pts int64,
) {
	// Setting position >= 0.999 triggers completion, so clamp just below for testing.
	if pos < 0.999 {
		h.engine.SetPosition(pos)
	}
	h.engine.IngestRawFrame(srcA, yuvA, w, h2, pts)
	h.engine.IngestRawFrame(srcB, yuvB, w, h2, pts)
}

// feedFTBFrameAtPosition feeds a frame for FTB transition (only FROM source triggers blend).
func (h *engineTestHarness) feedFTBFrameAtPosition(
	pos float64,
	src string,
	yuv []byte,
	w, h2 int,
	pts int64,
) {
	if pos < 0.999 {
		h.engine.SetPosition(pos)
	}
	h.engine.IngestRawFrame(src, yuv, w, h2, pts)
}

// TestTransitionEngineDissolve verifies that a mix (dissolve) transition
// produces the correct blended pixel values at various positions.
func TestTransitionEngineDissolve(t *testing.T) {
	const w, h = 64, 48
	const srcAY, srcBY byte = 40, 220 // Distinct Y values for clear blending
	const srcACb, srcBCb byte = 90, 200
	const srcACr, srcBCr byte = 200, 60

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000) // long duration
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	frameA := makeYUV420Frame(w, h, srcAY, srcACb, srcACr)
	frameB := makeYUV420Frame(w, h, srcBY, srcBCb, srcBCr)

	// Test at various positions
	positions := []float64{0.0, 0.25, 0.5, 0.75, 0.99}
	yValues := make([]float64, len(positions))

	for i, pos := range positions {
		harness.clearOutputs()
		harness.feedFrameAtPosition(pos, "srcA", "srcB", frameA, frameB, w, h, int64(i*3000))
		out := harness.lastOutput()
		if out == nil {
			t.Fatalf("No output at position %.2f", pos)
		}
		yValues[i] = averageY(out.yuv, w, h)
	}

	t.Logf("MEASURE: Y at pos 0.0=%.1f, 0.25=%.1f, 0.5=%.1f, 0.75=%.1f, 0.99=%.1f",
		yValues[0], yValues[1], yValues[2], yValues[3], yValues[4])

	// At position 0.0: output should be source A (Y=40)
	expectedA := float64(srcAY)
	t.Logf("MEASURE: Y at pos 0.0 = %.1f, expected ~%.1f (source A)", yValues[0], expectedA)
	if math.Abs(yValues[0]-expectedA) > 2.0 {
		t.Errorf("BUG: At position 0.0, Y=%.1f but expected ~%.1f (source A). Blend is not identity at start.",
			yValues[0], expectedA)
	}

	// At position 0.5: output Y should be midpoint
	expectedMid := (float64(srcAY) + float64(srcBY)) / 2.0
	t.Logf("MEASURE: Y at pos 0.5 = %.1f, expected ~%.1f (midpoint)", yValues[2], expectedMid)
	if math.Abs(yValues[2]-expectedMid) > 5.0 {
		t.Errorf("BUG: At position 0.5, Y=%.1f but expected ~%.1f (midpoint). Blend is off at midpoint.",
			yValues[2], expectedMid)
	}

	// At position 0.99: output should be close to source B (Y=220)
	expectedB := float64(srcBY)
	t.Logf("MEASURE: Y at pos 0.99 = %.1f, expected ~%.1f (source B)", yValues[4], expectedB)
	if math.Abs(yValues[4]-expectedB) > 5.0 {
		t.Errorf("BUG: At position 0.99, Y=%.1f but expected ~%.1f (source B). Blend doesn't reach target.",
			yValues[4], expectedB)
	}

	// Monotonicity: Y should increase from A to B across all positions
	for i := 1; i < len(yValues); i++ {
		t.Logf("MEASURE: Y[%.2f]=%.1f -> Y[%.2f]=%.1f (delta=%.1f)",
			positions[i-1], yValues[i-1], positions[i], yValues[i], yValues[i]-yValues[i-1])
		if yValues[i] < yValues[i-1] {
			t.Errorf("BUG: Non-monotonic blend: Y at pos %.2f (%.1f) < Y at pos %.2f (%.1f)",
				positions[i], yValues[i], positions[i-1], yValues[i-1])
		}
	}
}

// TestTransitionEngineDipToBlack verifies that a dip transition fades through
// broadcast black at the midpoint.
func TestTransitionEngineDipToBlack(t *testing.T) {
	const w, h = 64, 48
	const srcAY byte = 180
	const srcBY byte = 100

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Dip, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	frameA := makeYUV420Frame(w, h, srcAY, 128, 128)
	frameB := makeYUV420Frame(w, h, srcBY, 128, 128)

	// Test at various positions
	positions := []float64{0.0, 0.25, 0.5, 0.75, 0.99}
	yValues := make([]float64, len(positions))
	cbValues := make([]float64, len(positions))
	crValues := make([]float64, len(positions))

	for i, pos := range positions {
		harness.clearOutputs()
		harness.feedFrameAtPosition(pos, "srcA", "srcB", frameA, frameB, w, h, int64(i*3000))
		out := harness.lastOutput()
		if out == nil {
			t.Fatalf("No output at position %.2f", pos)
		}
		yValues[i] = averageY(out.yuv, w, h)
		cbValues[i] = averageCb(out.yuv, w, h)
		crValues[i] = averageCr(out.yuv, w, h)
	}

	t.Logf("MEASURE: Dip Y values: 0.0=%.1f, 0.25=%.1f, 0.5=%.1f, 0.75=%.1f, 0.99=%.1f",
		yValues[0], yValues[1], yValues[2], yValues[3], yValues[4])
	t.Logf("MEASURE: Dip Cb values: 0.0=%.1f, 0.25=%.1f, 0.5=%.1f, 0.75=%.1f, 0.99=%.1f",
		cbValues[0], cbValues[1], cbValues[2], cbValues[3], cbValues[4])

	// At position 0.0: should be source A (Y=180)
	t.Logf("MEASURE: Dip at pos 0.0: Y=%.1f, expected ~%.1f (source A)", yValues[0], float64(srcAY))
	if math.Abs(yValues[0]-float64(srcAY)) > 2.0 {
		t.Errorf("BUG: Dip at pos 0.0: Y=%.1f, expected ~%.1f (source A)", yValues[0], float64(srcAY))
	}

	// At position 0.5: should be broadcast black (Y=16 for BT.709 limited range)
	t.Logf("MEASURE: Dip midpoint Y = %.1f, expected ~16 (broadcast black)", yValues[2])
	if math.Abs(yValues[2]-16.0) > 3.0 {
		t.Errorf("BUG: Dip at midpoint: Y=%.1f, expected ~16 (broadcast black). Not reaching black at dip point.",
			yValues[2])
	}

	// Cb at midpoint should be 128 (neutral chroma)
	t.Logf("MEASURE: Dip midpoint Cb = %.1f, expected ~128 (neutral)", cbValues[2])
	if math.Abs(cbValues[2]-128.0) > 3.0 {
		t.Errorf("BUG: Dip at midpoint: Cb=%.1f, expected ~128 (neutral chroma)", cbValues[2])
	}

	// Cr at midpoint should be 128 (neutral chroma)
	t.Logf("MEASURE: Dip midpoint Cr = %.1f, expected ~128 (neutral)", crValues[2])
	if math.Abs(crValues[2]-128.0) > 3.0 {
		t.Errorf("BUG: Dip at midpoint: Cr=%.1f, expected ~128 (neutral chroma)", crValues[2])
	}

	// At position 0.99: should be close to source B (Y=100)
	t.Logf("MEASURE: Dip at pos 0.99: Y=%.1f, expected ~%.1f (source B)", yValues[4], float64(srcBY))
	if math.Abs(yValues[4]-float64(srcBY)) > 5.0 {
		t.Errorf("BUG: Dip at pos 0.99: Y=%.1f, expected ~%.1f (source B)", yValues[4], float64(srcBY))
	}

	// Dip shape: values should decrease to midpoint then increase
	// Phase 1 (0.0 to 0.5): Y should decrease
	if yValues[1] >= yValues[0] {
		t.Errorf("BUG: Dip phase 1 not fading: Y[0.0]=%.1f, Y[0.25]=%.1f -- should decrease", yValues[0], yValues[1])
	}
	// Phase 2 (0.5 to 1.0): Y should increase (toward source B)
	if yValues[3] <= yValues[2] {
		t.Errorf("BUG: Dip phase 2 not rising: Y[0.5]=%.1f, Y[0.75]=%.1f -- should increase", yValues[2], yValues[3])
	}
}

// TestTransitionEngineWipe verifies that a wipe transition has a spatial boundary.
func TestTransitionEngineWipe(t *testing.T) {
	const w, h = 64, 48
	const srcAY byte = 40
	const srcBY byte = 220

	harness := newEngineTestHarness(t, w, h, func(cfg *transition.EngineConfig) {
		cfg.WipeDirection = transition.WipeHLeft // B reveals from the left
	})

	err := harness.engine.Start("srcA", "srcB", transition.Wipe, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	frameA := makeYUV420Frame(w, h, srcAY, 128, 128)
	frameB := makeYUV420Frame(w, h, srcBY, 128, 128)

	// At 0.5: left half should be source B, right half should be source A
	harness.clearOutputs()
	harness.feedFrameAtPosition(0.5, "srcA", "srcB", frameA, frameB, w, h, 0)
	out := harness.lastOutput()
	if out == nil {
		t.Fatal("No output at position 0.5 for wipe")
	}

	// Measure left quarter and right quarter Y averages
	ySize := w * h
	var leftSum, rightSum int64
	var leftCount, rightCount int
	quarter := w / 4
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := int64(out.yuv[y*w+x])
			if x < quarter { // left 25%
				leftSum += v
				leftCount++
			} else if x >= w-quarter { // right 25%
				rightSum += v
				rightCount++
			}
		}
	}
	_ = ySize

	leftAvg := float64(leftSum) / float64(leftCount)
	rightAvg := float64(rightSum) / float64(rightCount)

	t.Logf("MEASURE: Wipe at pos=0.5: left quarter Y = %.1f, right quarter Y = %.1f", leftAvg, rightAvg)

	// For h-left wipe at 0.5, the left half should be source B (Y=220), right half source A (Y=40)
	// Allow some blending near the boundary
	if math.Abs(leftAvg-float64(srcBY)) > 20 {
		t.Errorf("BUG: Wipe left quarter Y=%.1f, expected ~%.1f (source B for h-left wipe)",
			leftAvg, float64(srcBY))
	}
	if math.Abs(rightAvg-float64(srcAY)) > 20 {
		t.Errorf("BUG: Wipe right quarter Y=%.1f, expected ~%.1f (source A for h-left wipe)",
			rightAvg, float64(srcAY))
	}

	// The spatial difference should be significant
	diff := math.Abs(leftAvg - rightAvg)
	t.Logf("MEASURE: Wipe spatial difference = %.1f", diff)
	if diff < 50 {
		t.Errorf("BUG: Wipe at 0.5 should have clear spatial boundary. Left=%.1f, Right=%.1f, diff=%.1f",
			leftAvg, rightAvg, diff)
	}

	// Verify at pos 0.0: should be all source A
	harness.clearOutputs()
	harness.feedFrameAtPosition(0.0, "srcA", "srcB", frameA, frameB, w, h, 3000)
	out0 := harness.lastOutput()
	if out0 != nil {
		y0 := averageY(out0.yuv, w, h)
		t.Logf("MEASURE: Wipe at pos=0.0: avg Y = %.1f, expected ~%.1f (source A)", y0, float64(srcAY))
		if math.Abs(y0-float64(srcAY)) > 5.0 {
			t.Errorf("BUG: Wipe at pos=0.0: Y=%.1f, expected ~%.1f (all source A)", y0, float64(srcAY))
		}
	}

	// Verify at pos 0.99: should be all source B
	harness.clearOutputs()
	harness.feedFrameAtPosition(0.99, "srcA", "srcB", frameA, frameB, w, h, 6000)
	out99 := harness.lastOutput()
	if out99 != nil {
		y99 := averageY(out99.yuv, w, h)
		t.Logf("MEASURE: Wipe at pos=0.99: avg Y = %.1f, expected ~%.1f (source B)", y99, float64(srcBY))
		if math.Abs(y99-float64(srcBY)) > 5.0 {
			t.Errorf("BUG: Wipe at pos=0.99: Y=%.1f, expected ~%.1f (all source B)", y99, float64(srcBY))
		}
	}
}

// TestTransitionEngineFTB verifies that FTB (fade to black) produces broadcast
// black at position 1.0 and verifies ALL pixels.
func TestTransitionEngineFTB(t *testing.T) {
	const w, h = 64, 48
	const srcAY byte = 200

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "", transition.FTB, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	frameA := makeYUV420Frame(w, h, srcAY, 100, 180)

	// At position 0.0: should be source A
	harness.clearOutputs()
	harness.feedFTBFrameAtPosition(0.0, "srcA", frameA, w, h, 0)
	out0 := harness.lastOutput()
	if out0 == nil {
		t.Fatal("No output at position 0.0 for FTB")
	}
	y0 := averageY(out0.yuv, w, h)
	t.Logf("MEASURE: FTB at pos=0.0: Y=%.1f, expected ~%.1f (source A)", y0, float64(srcAY))
	if math.Abs(y0-float64(srcAY)) > 2.0 {
		t.Errorf("BUG: FTB at pos=0.0: Y=%.1f, expected ~%.1f (source A)", y0, float64(srcAY))
	}

	// At position 0.5: should be halfway to black
	harness.clearOutputs()
	harness.feedFTBFrameAtPosition(0.5, "srcA", frameA, w, h, 3000)
	out50 := harness.lastOutput()
	if out50 == nil {
		t.Fatal("No output at position 0.5 for FTB")
	}
	y50 := averageY(out50.yuv, w, h)
	// Halfway between srcAY (200) and black (16) = ~108
	expectedMid := (float64(srcAY) + 16.0) / 2.0
	t.Logf("MEASURE: FTB at pos=0.5: Y=%.1f, expected ~%.1f (halfway to black)", y50, expectedMid)
	if math.Abs(y50-expectedMid) > 10.0 {
		t.Errorf("BUG: FTB at pos=0.5: Y=%.1f, expected ~%.1f (halfway to black)", y50, expectedMid)
	}

	// At position 0.99: should be very close to broadcast black
	harness.clearOutputs()
	harness.feedFTBFrameAtPosition(0.99, "srcA", frameA, w, h, 6000)
	out99 := harness.lastOutput()
	if out99 == nil {
		t.Fatal("No output at position 0.99 for FTB")
	}

	// Check ALL pixels, not just average
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	var yMin, yMax byte = 255, 0
	var cbMin, cbMax byte = 255, 0
	var crMin, crMax byte = 255, 0

	for i := 0; i < ySize; i++ {
		v := out99.yuv[i]
		if v < yMin {
			yMin = v
		}
		if v > yMax {
			yMax = v
		}
	}
	for i := 0; i < uvSize; i++ {
		cb := out99.yuv[ySize+i]
		cr := out99.yuv[ySize+uvSize+i]
		if cb < cbMin {
			cbMin = cb
		}
		if cb > cbMax {
			cbMax = cb
		}
		if cr < crMin {
			crMin = cr
		}
		if cr > crMax {
			crMax = cr
		}
	}

	t.Logf("MEASURE: FTB at pos=0.99: Y range=[%d,%d], Cb range=[%d,%d], Cr range=[%d,%d]",
		yMin, yMax, cbMin, cbMax, crMin, crMax)

	// At 0.99, Y should be very close to 16 (broadcast black)
	avgY := averageY(out99.yuv, w, h)
	t.Logf("MEASURE: FTB at pos=0.99: avg Y=%.1f", avgY)
	if avgY > 20.0 {
		t.Errorf("BUG: FTB at pos=0.99: avg Y=%.1f, expected <=20 (near broadcast black 16)", avgY)
	}

	// All Y pixels should be close to 16
	if yMax > 25 {
		t.Errorf("BUG: FTB at pos=0.99: max Y pixel = %d, expected <= 25 (near broadcast black)", yMax)
	}

	// Cb/Cr should be close to 128 (neutral chroma)
	if cbMin < 120 || cbMax > 136 {
		t.Errorf("BUG: FTB at pos=0.99: Cb range [%d,%d], expected near 128", cbMin, cbMax)
	}
	if crMin < 120 || crMax > 136 {
		t.Errorf("BUG: FTB at pos=0.99: Cr range [%d,%d], expected near 128", crMin, crMax)
	}
}

// TestTransitionEngineAbort verifies that stopping a transition returns to idle
// and the OnComplete callback fires with aborted=true.
func TestTransitionEngineAbort(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Feed a few frames at position 0.3
	frameA := makeYUV420Frame(w, h, 80, 128, 128)
	frameB := makeYUV420Frame(w, h, 160, 128, 128)
	harness.feedFrameAtPosition(0.3, "srcA", "srcB", frameA, frameB, w, h, 0)

	state := harness.engine.State()
	t.Logf("MEASURE: state before abort = %v", state)
	if state != transition.StateActive {
		t.Errorf("BUG: Expected StateActive before abort, got %v", state)
	}

	// Abort via Stop()
	harness.engine.Stop()

	state = harness.engine.State()
	t.Logf("MEASURE: state after Stop() = %v", state)
	if state != transition.StateIdle {
		t.Errorf("BUG: Expected StateIdle after Stop(), got %v", state)
	}

	// Position should be reset
	pos := harness.engine.Position()
	t.Logf("MEASURE: position after Stop() = %.3f", pos)
	if pos != 0.0 {
		t.Errorf("BUG: Expected position 0.0 after Stop(), got %.3f", pos)
	}

	// Verify engine can be restarted after abort
	err = harness.engine.Start("srcA", "srcB", transition.Mix, 5000)
	if err != nil {
		t.Errorf("BUG: Cannot restart engine after Stop(): %v", err)
	}
	harness.engine.Stop()

	// Test Abort() method (as opposed to Stop())
	err = harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start for Abort test failed: %v", err)
	}
	harness.feedFrameAtPosition(0.3, "srcA", "srcB", frameA, frameB, w, h, 3000)

	harness.mu.Lock()
	harness.completions = nil
	harness.mu.Unlock()

	harness.engine.Abort()

	state = harness.engine.State()
	t.Logf("MEASURE: state after Abort() = %v", state)
	if state != transition.StateIdle {
		t.Errorf("BUG: Expected StateIdle after Abort(), got %v", state)
	}

	// OnComplete should have been called with aborted=true
	time.Sleep(10 * time.Millisecond) // small window for callback
	harness.mu.Lock()
	completions := make([]bool, len(harness.completions))
	copy(completions, harness.completions)
	harness.mu.Unlock()

	t.Logf("MEASURE: completions after Abort() = %v", completions)
	if len(completions) == 0 {
		t.Errorf("BUG: OnComplete callback was not called after Abort()")
	} else if completions[0] {
		t.Errorf("BUG: OnComplete reported completed (true) but expected aborted (false)")
	}
}

// TestTransitionEngineConcurrentIngest tests that concurrent IngestRawFrame
// calls from two goroutines don't cause data races or panics.
func TestTransitionEngineConcurrentIngest(t *testing.T) {
	const w, h = 64, 48
	const numFrames = 200

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 30000) // long duration
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	// Set a fixed manual position so we get consistent blends
	harness.engine.SetPosition(0.5)

	frameA := makeYUV420Frame(w, h, 40, 128, 128)
	frameB := makeYUV420Frame(w, h, 220, 128, 128)

	var wg sync.WaitGroup
	var panicked atomic.Bool

	// Goroutine A: ingests source A frames
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("BUG: Panic in goroutine A: %v", r)
				panicked.Store(true)
			}
		}()
		for i := 0; i < numFrames; i++ {
			harness.engine.IngestRawFrame("srcA", frameA, w, h, int64(i*3000))
		}
	}()

	// Goroutine B: ingests source B frames
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("BUG: Panic in goroutine B: %v", r)
				panicked.Store(true)
			}
		}()
		for i := 0; i < numFrames; i++ {
			harness.engine.IngestRawFrame("srcB", frameB, w, h, int64(i*3000))
		}
	}()

	wg.Wait()

	if panicked.Load() {
		t.Fatal("BUG: Panics detected during concurrent ingest")
	}

	outputs := harness.getOutputs()
	t.Logf("MEASURE: %d output frames from %d concurrent ingest pairs", len(outputs), numFrames)

	// Should have produced some outputs (each B frame triggers a blend if A has been stored)
	if len(outputs) == 0 {
		t.Errorf("BUG: No output frames produced during concurrent ingest")
	}

	// All output frames should be valid YUV (not garbage)
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	expectedSize := ySize + 2*uvSize
	for i, out := range outputs {
		if len(out.yuv) != expectedSize {
			t.Errorf("BUG: Output frame %d has size %d, expected %d", i, len(out.yuv), expectedSize)
			continue
		}
		// Y values should be in valid range
		avgY := averageY(out.yuv, w, h)
		if avgY < 0 || avgY > 255 {
			t.Errorf("BUG: Output frame %d has invalid avg Y=%.1f", i, avgY)
		}
		// Should be roughly in between source A (40) and source B (220) since pos=0.5
		if i == 0 {
			t.Logf("MEASURE: First concurrent output avg Y=%.1f (expected ~130 at pos=0.5)", avgY)
		}
	}
}

// TestTransitionEngineAutoComplete verifies that a time-based transition
// auto-completes and fires the OnComplete callback.
func TestTransitionEngineAutoComplete(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	// Use a very short duration
	err := harness.engine.Start("srcA", "srcB", transition.Mix, 100) // 100ms
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	frameA := makeYUV420Frame(w, h, 80, 128, 128)
	frameB := makeYUV420Frame(w, h, 200, 128, 128)

	// Feed frames from A to store
	harness.engine.IngestRawFrame("srcA", frameA, w, h, 0)

	// Wait until past duration
	time.Sleep(200 * time.Millisecond)

	// Feed B frame -- should trigger auto-complete since elapsed > duration
	harness.engine.IngestRawFrame("srcB", frameB, w, h, 3000)

	// Give callback time
	time.Sleep(20 * time.Millisecond)

	state := harness.engine.State()
	t.Logf("MEASURE: state after auto-complete = %v", state)
	if state != transition.StateIdle {
		t.Errorf("BUG: Expected StateIdle after auto-complete, got %v", state)
	}

	harness.mu.Lock()
	completions := make([]bool, len(harness.completions))
	copy(completions, harness.completions)
	harness.mu.Unlock()
	t.Logf("MEASURE: completions = %v", completions)

	// Should have exactly one completion (not aborted)
	found := false
	for _, c := range completions {
		if c {
			found = true
		}
	}
	if !found {
		t.Errorf("BUG: No 'completed' (non-abort) callback fired after auto-complete. Completions: %v", completions)
	}
}

// TestTransitionEngineOutputPTS verifies that the output PTS matches the
// triggering frame's PTS (B source for non-FTB).
func TestTransitionEngineOutputPTS(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	harness.engine.SetPosition(0.5)

	frameA := makeYUV420Frame(w, h, 80, 128, 128)
	frameB := makeYUV420Frame(w, h, 160, 128, 128)

	// Feed with specific PTS values
	testPTS := []int64{90000, 93000, 96000}
	for _, pts := range testPTS {
		harness.engine.IngestRawFrame("srcA", frameA, w, h, pts-1000) // A with different PTS
		harness.engine.IngestRawFrame("srcB", frameB, w, h, pts)     // B triggers output
	}

	outputs := harness.getOutputs()
	t.Logf("MEASURE: %d outputs for %d PTS values", len(outputs), len(testPTS))

	for i, out := range outputs {
		if i < len(testPTS) {
			t.Logf("MEASURE: output[%d] PTS=%d, expected=%d", i, out.pts, testPTS[i])
			if out.pts != testPTS[i] {
				t.Errorf("BUG: Output PTS %d != expected %d (should match B source PTS)",
					out.pts, testPTS[i])
			}
		}
	}
}

// TestTransitionEngineFirstFrameIsKeyframe verifies that the first output
// frame is marked as a keyframe.
func TestTransitionEngineFirstFrameIsKeyframe(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	harness.engine.SetPosition(0.5)

	frameA := makeYUV420Frame(w, h, 80, 128, 128)
	frameB := makeYUV420Frame(w, h, 160, 128, 128)

	// First frame pair
	harness.engine.IngestRawFrame("srcA", frameA, w, h, 0)
	harness.engine.IngestRawFrame("srcB", frameB, w, h, 0)

	// Second frame pair
	harness.engine.IngestRawFrame("srcA", frameA, w, h, 3000)
	harness.engine.IngestRawFrame("srcB", frameB, w, h, 3000)

	outputs := harness.getOutputs()
	if len(outputs) < 2 {
		t.Fatalf("Expected at least 2 outputs, got %d", len(outputs))
	}

	t.Logf("MEASURE: output[0].isKeyframe=%v, output[1].isKeyframe=%v",
		outputs[0].isKeyframe, outputs[1].isKeyframe)

	if !outputs[0].isKeyframe {
		t.Errorf("BUG: First output frame should be marked as keyframe")
	}
	if outputs[1].isKeyframe {
		t.Errorf("BUG: Second output frame should NOT be marked as keyframe (only first is)")
	}
}

// TestTransitionEngineBlendWithoutSourceA verifies behavior when source A
// hasn't been received yet (only B arrives). For non-FTB, the engine should
// skip output (not crash or produce garbage).
func TestTransitionEngineBlendWithoutSourceA(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	harness.engine.SetPosition(0.5)

	frameB := makeYUV420Frame(w, h, 200, 128, 128)

	// Only ingest source B (no source A)
	harness.engine.IngestRawFrame("srcB", frameB, w, h, 0)

	outputs := harness.getOutputs()
	t.Logf("MEASURE: outputs with only source B = %d", len(outputs))

	// The engine should either:
	// 1. Skip output (source A not available) - current behavior based on code
	// 2. Use black frame for source A
	// Based on the code, for non-FTB, if yuvA is nil, it returns early.
	if len(outputs) > 0 {
		// If it produces output, that's also acceptable as long as it's valid
		avgY := averageY(outputs[0].yuv, w, h)
		t.Logf("MEASURE: output with missing source A: avg Y=%.1f", avgY)
		t.Logf("NOTE: Engine produced output without source A. Check if this is intentional.")
	} else {
		t.Logf("MEASURE: Engine correctly skipped output when source A not yet received")
	}
}

// TestTransitionEngineHintDimensionsMismatch tests what happens when
// IngestRawFrame provides frames with dimensions different from HintWidth/HintHeight.
func TestTransitionEngineHintDimensionsMismatch(t *testing.T) {
	const hintW, hintH = 64, 48
	const frameW, frameH = 32, 24 // different from hint

	harness := newEngineTestHarness(t, hintW, hintH)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	harness.engine.SetPosition(0.5)

	frameA := makeYUV420Frame(frameW, frameH, 80, 128, 128)
	frameB := makeYUV420Frame(frameW, frameH, 200, 128, 128)

	// Feed frames with different dimensions -- should be scaled to hint dimensions
	harness.engine.IngestRawFrame("srcA", frameA, frameW, frameH, 0)
	harness.engine.IngestRawFrame("srcB", frameB, frameW, frameH, 0)

	outputs := harness.getOutputs()
	t.Logf("MEASURE: outputs with dimension mismatch = %d", len(outputs))

	if len(outputs) > 0 {
		out := outputs[0]
		t.Logf("MEASURE: output dimensions = %dx%d (hint=%dx%d, frame=%dx%d)",
			out.width, out.height, hintW, hintH, frameW, frameH)
		if out.width != hintW || out.height != hintH {
			t.Errorf("BUG: Output dimensions %dx%d don't match hint %dx%d",
				out.width, out.height, hintW, hintH)
		}

		// Output should still be a valid blend
		avgY := averageY(out.yuv, out.width, out.height)
		expectedMid := (80.0 + 200.0) / 2.0
		t.Logf("MEASURE: scaled blend avg Y=%.1f, expected ~%.1f", avgY, expectedMid)
		if math.Abs(avgY-expectedMid) > 15.0 {
			t.Errorf("BUG: Scaled blend avg Y=%.1f, expected ~%.1f (tolerance 15)", avgY, expectedMid)
		}
	} else {
		t.Errorf("BUG: No output produced with dimension mismatch (scaling should handle this)")
	}
}

// TestTransitionEngineFTBTriggeredByFromSource verifies that FTB output is
// triggered by the FROM source, not the TO source (which is empty for FTB).
func TestTransitionEngineFTBTriggeredByFromSource(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "", transition.FTB, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	harness.engine.SetPosition(0.5)

	frameA := makeYUV420Frame(w, h, 200, 100, 180)

	// Ingesting from srcA (the FROM source) should trigger output
	harness.engine.IngestRawFrame("srcA", frameA, w, h, 0)

	outputs := harness.getOutputs()
	t.Logf("MEASURE: FTB outputs from FROM source = %d", len(outputs))
	if len(outputs) == 0 {
		t.Errorf("BUG: FTB should produce output when FROM source ingests, but got 0 outputs")
	}

	// Ingesting from an unknown source should NOT produce output
	countBefore := harness.outputCount()
	harness.engine.IngestRawFrame("srcB", frameA, w, h, 3000) // srcB is not a participant
	countAfter := harness.outputCount()
	t.Logf("MEASURE: output count before unknown source = %d, after = %d", countBefore, countAfter)
	if countAfter != countBefore {
		t.Errorf("BUG: Unknown source 'srcB' produced output during FTB (should be ignored)")
	}
}

// TestTransitionEngineWatchdogTimeout verifies the watchdog aborts a transition
// that receives no frames within the timeout.
func TestTransitionEngineWatchdogTimeout(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)
	harness.engine.SetTimeout(200 * time.Millisecond)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 30000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Don't feed any frames -- watchdog should abort
	time.Sleep(500 * time.Millisecond)

	state := harness.engine.State()
	t.Logf("MEASURE: state after watchdog timeout = %v", state)
	if state != transition.StateIdle {
		t.Errorf("BUG: Expected watchdog to abort transition (StateIdle), got %v", state)
		harness.engine.Stop() // cleanup
	}

	harness.mu.Lock()
	completions := make([]bool, len(harness.completions))
	copy(completions, harness.completions)
	harness.mu.Unlock()
	t.Logf("MEASURE: completions after watchdog = %v", completions)

	// Should have fired OnComplete with aborted=true
	foundAbort := false
	for _, c := range completions {
		if !c { // false = aborted
			foundAbort = true
		}
	}
	if !foundAbort {
		t.Errorf("BUG: Watchdog abort did not fire OnComplete(aborted=true). Completions: %v", completions)
	}
}

// TestTransitionEngineTimingInstrumentation verifies that the engine's
// timing counters update during operation.
func TestTransitionEngineTimingInstrumentation(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	harness.engine.SetPosition(0.5)

	frameA := makeYUV420Frame(w, h, 80, 128, 128)
	frameB := makeYUV420Frame(w, h, 200, 128, 128)

	// Feed several frames
	for i := 0; i < 10; i++ {
		harness.engine.IngestRawFrame("srcA", frameA, w, h, int64(i*3000))
		harness.engine.IngestRawFrame("srcB", frameB, w, h, int64(i*3000))
	}

	timing := harness.engine.Timing()
	t.Logf("MEASURE: timing = %+v", timing)

	framesIngested := timing["frames_ingested"].(int64)
	framesBlended := timing["frames_blended"].(int64)
	t.Logf("MEASURE: frames_ingested=%d, frames_blended=%d", framesIngested, framesBlended)

	if framesIngested == 0 {
		t.Errorf("BUG: frames_ingested counter is 0 after feeding frames")
	}
	if framesBlended == 0 {
		t.Errorf("BUG: frames_blended counter is 0 after feeding frames")
	}

	// Ingest count should be 20 (10 A + 10 B)
	if framesIngested != 20 {
		t.Errorf("BUG: Expected 20 ingested frames, got %d", framesIngested)
	}

	// Blend count should be 10 (triggered by each B frame)
	if framesBlended != 10 {
		t.Errorf("BUG: Expected 10 blended frames, got %d", framesBlended)
	}

	// Blend timing should be non-zero
	blendLastMs := timing["blend_last_ms"].(float64)
	t.Logf("MEASURE: blend_last_ms=%.3f", blendLastMs)
	if blendLastMs <= 0 {
		t.Errorf("BUG: blend_last_ms should be > 0, got %.3f", blendLastMs)
	}

	// Ingest timing should be non-zero
	ingestLastMs := timing["ingest_last_ms"].(float64)
	t.Logf("MEASURE: ingest_last_ms=%.3f", ingestLastMs)
	if ingestLastMs <= 0 {
		t.Errorf("BUG: ingest_last_ms should be > 0, got %.3f", ingestLastMs)
	}
}

// TestTransitionEngineDissolveSymmetry verifies that the dissolve blend is
// symmetric: blend(A,B,0.5) should equal blend(B,A,0.5) when sources have
// equal chroma.
func TestTransitionEngineDissolveSymmetry(t *testing.T) {
	const w, h = 64, 48
	const yA, yB byte = 60, 200

	// Forward: srcA=60, srcB=200
	harnessF := newEngineTestHarness(t, w, h)
	err := harnessF.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start forward failed: %v", err)
	}
	harnessF.engine.SetPosition(0.5)
	frameAf := makeYUV420Frame(w, h, yA, 128, 128)
	frameBf := makeYUV420Frame(w, h, yB, 128, 128)
	harnessF.engine.IngestRawFrame("srcA", frameAf, w, h, 0)
	harnessF.engine.IngestRawFrame("srcB", frameBf, w, h, 0)
	outF := harnessF.lastOutput()
	harnessF.engine.Stop()

	// Reverse: srcA=200, srcB=60
	harnessR := newEngineTestHarness(t, w, h)
	err = harnessR.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start reverse failed: %v", err)
	}
	harnessR.engine.SetPosition(0.5)
	frameAr := makeYUV420Frame(w, h, yB, 128, 128)
	frameBr := makeYUV420Frame(w, h, yA, 128, 128)
	harnessR.engine.IngestRawFrame("srcA", frameAr, w, h, 0)
	harnessR.engine.IngestRawFrame("srcB", frameBr, w, h, 0)
	outR := harnessR.lastOutput()
	harnessR.engine.Stop()

	if outF == nil || outR == nil {
		t.Fatal("Missing output for symmetry test")
	}

	avgF := averageY(outF.yuv, w, h)
	avgR := averageY(outR.yuv, w, h)
	t.Logf("MEASURE: forward blend avg Y=%.1f, reverse blend avg Y=%.1f", avgF, avgR)

	// At position 0.5, both should yield the same midpoint
	diff := math.Abs(avgF - avgR)
	t.Logf("MEASURE: symmetry difference = %.1f", diff)
	if diff > 2.0 {
		t.Errorf("BUG: Dissolve is not symmetric at pos=0.5: forward=%.1f, reverse=%.1f, diff=%.1f",
			avgF, avgR, diff)
	}
}

// TestTransitionEngineDoubleStop verifies that calling Stop() twice doesn't panic.
func TestTransitionEngineDoubleStop(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Double stop should be safe
	harness.engine.Stop()
	harness.engine.Stop() // should not panic

	t.Logf("MEASURE: double Stop() completed without panic")

	state := harness.engine.State()
	if state != transition.StateIdle {
		t.Errorf("BUG: Expected StateIdle after double Stop(), got %v", state)
	}
}

// TestTransitionEngineFTBReverseFromBlack verifies that FTBReverse fades
// from black to the source (opposite direction of FTB).
func TestTransitionEngineFTBReverseFromBlack(t *testing.T) {
	const w, h = 64, 48
	const srcAY byte = 200

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "", transition.FTBReverse, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	frameA := makeYUV420Frame(w, h, srcAY, 100, 180)

	// At position 0.0: FTBReverse uses (1.0 - pos) for FTB blend
	// So at pos=0.0, it calls BlendFTB(A, 1.0) which is full black
	harness.clearOutputs()
	harness.feedFTBFrameAtPosition(0.0, "srcA", frameA, w, h, 0)
	out0 := harness.lastOutput()
	if out0 == nil {
		t.Fatal("No output at position 0.0 for FTBReverse")
	}
	y0 := averageY(out0.yuv, w, h)
	t.Logf("MEASURE: FTBReverse at pos=0.0: Y=%.1f (expected ~16 = black)", y0)
	if math.Abs(y0-16.0) > 3.0 {
		t.Errorf("BUG: FTBReverse at pos=0.0: Y=%.1f, expected ~16 (should start from black)", y0)
	}

	// At position 0.99: FTBReverse uses BlendFTB(A, 0.01) which is almost full source A
	harness.clearOutputs()
	harness.feedFTBFrameAtPosition(0.99, "srcA", frameA, w, h, 3000)
	out99 := harness.lastOutput()
	if out99 == nil {
		t.Fatal("No output at position 0.99 for FTBReverse")
	}
	y99 := averageY(out99.yuv, w, h)
	t.Logf("MEASURE: FTBReverse at pos=0.99: Y=%.1f (expected ~%.1f = source A)", y99, float64(srcAY))
	if math.Abs(y99-float64(srcAY)) > 5.0 {
		t.Errorf("BUG: FTBReverse at pos=0.99: Y=%.1f, expected ~%.1f (source A)", y99, float64(srcAY))
	}
}

// TestTransitionEngineSourceKeyIsolation verifies that frames from one source
// don't contaminate the other source's stored buffer.
func TestTransitionEngineSourceKeyIsolation(t *testing.T) {
	const w, h = 64, 48

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	// Position 0.0 = 100% source A, so output should match A's Y value
	harness.engine.SetPosition(0.001) // near zero

	// Feed source A with Y=40, then source B with Y=220
	frameA := makeYUV420Frame(w, h, 40, 128, 128)
	frameB := makeYUV420Frame(w, h, 220, 128, 128)
	harness.engine.IngestRawFrame("srcA", frameA, w, h, 0)
	harness.engine.IngestRawFrame("srcB", frameB, w, h, 0)

	out := harness.lastOutput()
	if out == nil {
		t.Fatal("No output at pos near 0")
	}
	avgY := averageY(out.yuv, w, h)
	t.Logf("MEASURE: at pos~0.0, avg Y = %.1f, expected ~40 (source A)", avgY)

	// At position near 0, output should be almost entirely source A (Y=40)
	if math.Abs(avgY-40.0) > 5.0 {
		t.Errorf("BUG: Source isolation: at pos~0.0, Y=%.1f, expected ~40 (source A). Source B may be leaking.",
			avgY)
	}

	// Now set position near 1.0 and check it's source B
	harness.clearOutputs()
	harness.engine.SetPosition(0.99)
	harness.engine.IngestRawFrame("srcA", frameA, w, h, 3000)
	harness.engine.IngestRawFrame("srcB", frameB, w, h, 3000)

	out2 := harness.lastOutput()
	if out2 == nil {
		t.Fatal("No output at pos near 1.0")
	}
	avgY2 := averageY(out2.yuv, w, h)
	t.Logf("MEASURE: at pos~1.0, avg Y = %.1f, expected ~220 (source B)", avgY2)

	if math.Abs(avgY2-220.0) > 5.0 {
		t.Errorf("BUG: Source isolation: at pos~1.0, Y=%.1f, expected ~220 (source B). Source A may be leaking.",
			avgY2)
	}
}

// TestTransitionEngineOutputDimensions verifies output frame dimensions
// match the engine's configured resolution.
func TestTransitionEngineOutputDimensions(t *testing.T) {
	const w, h = 128, 96

	harness := newEngineTestHarness(t, w, h)

	err := harness.engine.Start("srcA", "srcB", transition.Mix, 10000)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer harness.engine.Stop()

	harness.engine.SetPosition(0.5)

	frameA := makeYUV420Frame(w, h, 80, 128, 128)
	frameB := makeYUV420Frame(w, h, 200, 128, 128)
	harness.engine.IngestRawFrame("srcA", frameA, w, h, 0)
	harness.engine.IngestRawFrame("srcB", frameB, w, h, 0)

	out := harness.lastOutput()
	if out == nil {
		t.Fatal("No output")
	}

	t.Logf("MEASURE: output dimensions = %dx%d, expected %dx%d", out.width, out.height, w, h)
	if out.width != w || out.height != h {
		t.Errorf("BUG: Output dimensions %dx%d don't match expected %dx%d", out.width, out.height, w, h)
	}

	expectedSize := w * h * 3 / 2
	t.Logf("MEASURE: output YUV size = %d, expected %d", len(out.yuv), expectedSize)
	if len(out.yuv) != expectedSize {
		t.Errorf("BUG: Output YUV size %d != expected %d", len(out.yuv), expectedSize)
	}
}
