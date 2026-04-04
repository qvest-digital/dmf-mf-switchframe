package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

const (
	wipeTestW = 100
	wipeTestH = 100
)

// absDiffByte returns the absolute difference between two byte values.
func absDiffByte(a, b byte) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}

// assertFramesMatchRegression compares two YUV420 frames for regression.
// 8-bit gradient maps can't exactly reproduce the continuous threshold used by
// the old code in the soft-edge band. This assertion verifies:
//   - No pixel differs by more than maxDiff (bounded by quantization error)
//   - At least matchRatio of pixels match exactly
func assertFramesMatchRegression(t *testing.T, name string, expected, actual []byte, maxDiff int, matchRatio float64) {
	t.Helper()
	require.Equal(t, len(expected), len(actual), "%s: frame size mismatch", name)
	exactMatch := 0
	for i := range expected {
		d := absDiffByte(expected[i], actual[i])
		if d > maxDiff {
			t.Fatalf("%s: byte[%d] differ: old=%d new=%d (maxDiff=%d)", name, i, expected[i], actual[i], maxDiff)
		}
		if d == 0 {
			exactMatch++
		}
	}
	ratio := float64(exactMatch) / float64(len(expected))
	if ratio < matchRatio {
		t.Fatalf("%s: only %.1f%% exact match (want ≥%.1f%%)", name, ratio*100, matchRatio*100)
	}
}

// ---------- Regression tests: old BlendWipe vs new BlendWipeMap ----------

// The old code uses softEdge = 2.0/float64(w) for horizontal wipes.
// For 100-wide frames: softEdge = 0.02.
// The new code uses a WipeConfig with the same softEdge to match.
func regressionConfig(patternID string, w, h int) wipemap.WipeConfig {
	// Match the old code's soft edge calculation per direction.
	var softEdge float64
	switch patternID {
	case "v-top", "v-bottom":
		softEdge = 2.0 / float64(h)
	case "box-center-out", "box-edges-in":
		dim := w
		if h > w {
			dim = h
		}
		softEdge = 2.0 / float64(dim)
	default:
		softEdge = 2.0 / float64(w)
	}
	return wipemap.WipeConfig{
		PatternID: patternID,
		SoftEdge:  softEdge,
	}
}

func TestBlendWipeMap_HLeft_Regression(t *testing.T) {
	t.Parallel()
	fb1 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	fb2 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	oldOut := fb1.BlendWipe(a, b, 0.5, WipeHLeft)
	oldCopy := make([]byte, len(oldOut))
	copy(oldCopy, oldOut)

	gradMap := legacyWipeGradient(wipeTestW, wipeTestH, WipeHLeft)
	cfg := regressionConfig("h-left", wipeTestW, wipeTestH)
	newOut := fb2.BlendWipeMap(a, b, 0.5, gradMap, cfg)

	// 8-bit gradient map quantization introduces ±5 Y error in the soft-edge band.
	// Chroma planes differ more (up to ±16) because the old code computed chroma
	// alpha at a single point while the new code downsamples from a 2x2 luma
	// block, which averages two different alpha values within the soft edge.
	assertFramesMatchRegression(t, "HLeft regression", oldCopy, newOut, 16, 0.96)
}

func TestBlendWipeMap_HRight_Regression(t *testing.T) {
	t.Parallel()
	fb1 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	fb2 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	oldOut := fb1.BlendWipe(a, b, 0.5, WipeHRight)
	oldCopy := make([]byte, len(oldOut))
	copy(oldCopy, oldOut)

	gradMap := legacyWipeGradient(wipeTestW, wipeTestH, WipeHRight)
	cfg := regressionConfig("h-right", wipeTestW, wipeTestH)
	newOut := fb2.BlendWipeMap(a, b, 0.5, gradMap, cfg)

	assertFramesMatchRegression(t, "HRight regression", oldCopy, newOut, 16, 0.96)
}

func TestBlendWipeMap_VTop_Regression(t *testing.T) {
	t.Parallel()
	fb1 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	fb2 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	oldOut := fb1.BlendWipe(a, b, 0.5, WipeVTop)
	oldCopy := make([]byte, len(oldOut))
	copy(oldCopy, oldOut)

	gradMap := legacyWipeGradient(wipeTestW, wipeTestH, WipeVTop)
	cfg := regressionConfig("v-top", wipeTestW, wipeTestH)
	newOut := fb2.BlendWipeMap(a, b, 0.5, gradMap, cfg)

	assertFramesMatchRegression(t, "VTop regression", oldCopy, newOut, 16, 0.96)
}

func TestBlendWipeMap_VBottom_Regression(t *testing.T) {
	t.Parallel()
	fb1 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	fb2 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	oldOut := fb1.BlendWipe(a, b, 0.5, WipeVBottom)
	oldCopy := make([]byte, len(oldOut))
	copy(oldCopy, oldOut)

	gradMap := legacyWipeGradient(wipeTestW, wipeTestH, WipeVBottom)
	cfg := regressionConfig("v-bottom", wipeTestW, wipeTestH)
	newOut := fb2.BlendWipeMap(a, b, 0.5, gradMap, cfg)

	assertFramesMatchRegression(t, "VBottom regression", oldCopy, newOut, 16, 0.96)
}

func TestBlendWipeMap_BoxCenterOut_Regression(t *testing.T) {
	t.Parallel()
	fb1 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	fb2 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	oldOut := fb1.BlendWipe(a, b, 0.5, WipeBoxCenterOut)
	oldCopy := make([]byte, len(oldOut))
	copy(oldCopy, oldOut)

	gradMap := legacyWipeGradient(wipeTestW, wipeTestH, WipeBoxCenterOut)
	cfg := regressionConfig("box-center-out", wipeTestW, wipeTestH)
	newOut := fb2.BlendWipeMap(a, b, 0.5, gradMap, cfg)

	assertFramesMatchRegression(t, "BoxCenterOut regression", oldCopy, newOut, 16, 0.96)
}

func TestBlendWipeMap_BoxEdgesIn_Regression(t *testing.T) {
	t.Parallel()
	fb1 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	fb2 := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	oldOut := fb1.BlendWipe(a, b, 0.5, WipeBoxEdgesIn)
	oldCopy := make([]byte, len(oldOut))
	copy(oldCopy, oldOut)

	gradMap := legacyWipeGradient(wipeTestW, wipeTestH, WipeBoxEdgesIn)
	cfg := regressionConfig("box-edges-in", wipeTestW, wipeTestH)
	newOut := fb2.BlendWipeMap(a, b, 0.5, gradMap, cfg)

	assertFramesMatchRegression(t, "BoxEdgesIn regression", oldCopy, newOut, 16, 0.96)
}

// ---------- New feature tests ----------

func TestBlendWipeMap_SoftEdge(t *testing.T) {
	t.Parallel()
	gradMap := wipemap.GenHorizontal(false)(wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 128, 128)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 128, 128)

	// Narrow soft edge.
	fbNarrow := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	cfgNarrow := wipemap.WipeConfig{PatternID: "h-left", SoftEdge: 0.01}
	outNarrow := fbNarrow.BlendWipeMap(a, b, 0.5, gradMap, cfgNarrow)
	narrowCopy := make([]byte, len(outNarrow))
	copy(narrowCopy, outNarrow)

	// Wide soft edge.
	fbWide := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	cfgWide := wipemap.WipeConfig{PatternID: "h-left", SoftEdge: 0.2}
	outWide := fbWide.BlendWipeMap(a, b, 0.5, gradMap, cfgWide)

	// Count pixels with intermediate alpha (not fully A or B).
	ySize := wipeTestW * wipeTestH
	intermediateNarrow := 0
	intermediateWide := 0
	for i := 0; i < ySize; i++ {
		if narrowCopy[i] != 200 && narrowCopy[i] != 50 {
			intermediateNarrow++
		}
		if outWide[i] != 200 && outWide[i] != 50 {
			intermediateWide++
		}
	}
	require.Greater(t, intermediateWide, intermediateNarrow,
		"wider soft edge should produce more intermediate pixels: narrow=%d wide=%d",
		intermediateNarrow, intermediateWide)
}

func TestBlendWipeMap_Border(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	gradMap := wipemap.GenHorizontal(false)(wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	cfg := wipemap.WipeConfig{
		PatternID:    "h-left",
		SoftEdge:     0.02,
		BorderWidth:  0.05,
		BorderColorY: 235, // white border
	}
	out := fb.BlendWipeMap(a, b, 0.5, gradMap, cfg)

	// Some pixels near the wipe edge should have the border color.
	ySize := wipeTestW * wipeTestH
	borderCount := 0
	for i := 0; i < ySize; i++ {
		if out[i] == 235 {
			borderCount++
		}
	}
	require.Greater(t, borderCount, 0, "should have border pixels with Y=235")
}

func TestBlendWipeMap_Reverse(t *testing.T) {
	t.Parallel()
	gradMap := wipemap.GenHorizontal(false)(wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 128, 128)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 128, 128)

	fbFwd := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	cfgFwd := wipemap.WipeConfig{PatternID: "h-left", SoftEdge: 0.02}
	outFwd := fbFwd.BlendWipeMap(a, b, 0.5, gradMap, cfgFwd)
	fwdCopy := make([]byte, len(outFwd))
	copy(fwdCopy, outFwd)

	fbRev := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	cfgRev := wipemap.WipeConfig{PatternID: "h-left", SoftEdge: 0.02, Reverse: true}
	outRev := fbRev.BlendWipeMap(a, b, 0.5, gradMap, cfgRev)

	// At position 0.5 with a horizontal gradient: forward has left=B, right=A.
	// Reversed should be left=A, right=B.
	ySize := wipeTestW * wipeTestH
	diffs := 0
	for i := 0; i < ySize; i++ {
		if fwdCopy[i] != outRev[i] {
			diffs++
		}
	}
	require.Greater(t, diffs, ySize/4, "reverse should flip wipe direction")
}

func TestBlendWipeMap_Multiply(t *testing.T) {
	t.Parallel()
	gradMap := wipemap.GenHorizontal(false)(wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 128, 128)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 128, 128)

	// With multiply=3, the gradient repeats 3 times across the frame.
	fb := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	cfg := wipemap.WipeConfig{PatternID: "h-left", SoftEdge: 0.01, Multiply: 3}
	out := fb.BlendWipeMap(a, b, 0.5, gradMap, cfg)

	// Count transitions from A-dominant to B-dominant along middle row.
	ySize := wipeTestW * wipeTestH
	_ = ySize
	mid := wipeTestH / 2
	transitions := 0
	prevIsB := out[mid*wipeTestW] < 125
	for x := 1; x < wipeTestW; x++ {
		isB := out[mid*wipeTestW+x] < 125
		if isB != prevIsB {
			transitions++
			prevIsB = isB
		}
	}
	// With multiply=3, we expect at least 4 transitions (3 repeats = 6 edges, but some merge).
	require.GreaterOrEqual(t, transitions, 3, "multiply=3 should create repeating pattern")
}

func TestBlendWipeMap_Position0(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	gradMap := wipemap.GenHorizontal(false)(wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	cfg := wipemap.WipeConfig{PatternID: "h-left", SoftEdge: 0.02}
	out := fb.BlendWipeMap(a, b, 0.0, gradMap, cfg)

	// At position 0.0, output should be fully A.
	ySize := wipeTestW * wipeTestH
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(200), out[i], "position 0 Y[%d]", i)
	}
}

func TestBlendWipeMap_Position1(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, wipeTestW, wipeTestH)
	gradMap := wipemap.GenHorizontal(false)(wipeTestW, wipeTestH)
	a := makeYUVFrame(wipeTestW, wipeTestH, 200, 100, 150)
	b := makeYUVFrame(wipeTestW, wipeTestH, 50, 200, 80)

	cfg := wipemap.WipeConfig{PatternID: "h-left", SoftEdge: 0.02}
	out := fb.BlendWipeMap(a, b, 1.0, gradMap, cfg)

	// At position 1.0, output should be fully B.
	ySize := wipeTestW * wipeTestH
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(50), out[i], "position 1 Y[%d]", i)
	}
}
