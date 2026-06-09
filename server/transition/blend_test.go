package transition

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// mustNewFrameBlender creates a FrameBlender or fails the test.
func mustNewFrameBlender(t testing.TB, w, h int) *FrameBlender {
	t.Helper()
	fb, err := NewFrameBlender(w, h)
	require.NoError(t, err)
	return fb
}

// makeYUVFrame creates a YUV420 frame with uniform Y, Cb, Cr values.
// Layout: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
func makeYUVFrame(w, h int, y, cb, cr byte) []byte {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	buf := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		buf[i] = y
	}
	for i := 0; i < uvSize; i++ {
		buf[ySize+i] = cb
	}
	for i := 0; i < uvSize; i++ {
		buf[ySize+uvSize+i] = cr
	}
	return buf
}

func TestBlenderNew(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 1920, 1080)
	require.NotNil(t, fb)
	require.Equal(t, 1920, fb.width)
	require.Equal(t, 1080, fb.height)
}

func TestBlendMixPosition0(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	a := makeYUVFrame(4, 4, 200, 90, 240) // bright reddish
	b := makeYUVFrame(4, 4, 50, 200, 60)  // dark bluish

	out := fb.BlendMix(a, b, 0.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(200), out[i], "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(90), out[ySize+i], "Cb at pixel %d", i)
		require.Equal(t, byte(240), out[ySize+uvSize+i], "Cr at pixel %d", i)
	}
}

func TestBlendMixPosition1(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	a := makeYUVFrame(4, 4, 200, 90, 240)
	b := makeYUVFrame(4, 4, 50, 200, 60)

	out := fb.BlendMix(a, b, 1.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(50), out[i], "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(200), out[ySize+i], "Cb at pixel %d", i)
		require.Equal(t, byte(60), out[ySize+uvSize+i], "Cr at pixel %d", i)
	}
}

func TestBlendMixPosition05(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	a := makeYUVFrame(4, 4, 200, 100, 200)
	b := makeYUVFrame(4, 4, 100, 200, 100)

	out := fb.BlendMix(a, b, 0.5)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 150, int(out[i]), 1, "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 150, int(out[ySize+i]), 1, "Cb at pixel %d", i)
		require.InDelta(t, 150, int(out[ySize+uvSize+i]), 1, "Cr at pixel %d", i)
	}
}

func TestBlendDipPhase1(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	fb.SetLimitedRange(false)
	a := makeYUVFrame(4, 4, 200, 100, 200)
	b := makeYUVFrame(4, 4, 100, 200, 100)

	out := fb.BlendDip(a, b, 0.25)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain = 1.0 - 2*0.25 = 0.5, invGain = 0.5
	// Y: 200*0.5 + 0*0.5 = 100
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 100, int(out[i]), 1, "Y at pixel %d", i)
	}
	// Cb: 100*0.5 + 128*0.5 = 114
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 114, int(out[ySize+i]), 1, "Cb at pixel %d", i)
	}
	// Cr: 200*0.5 + 128*0.5 = 164
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 164, int(out[ySize+uvSize+i]), 1, "Cr at pixel %d", i)
	}
}

func TestBlendDipMidpoint(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	fb.SetLimitedRange(false)
	a := makeYUVFrame(4, 4, 255, 200, 200)
	b := makeYUVFrame(4, 4, 255, 200, 200)

	out := fb.BlendDip(a, b, 0.5)
	ySize := 4 * 4
	uvSize := 2 * 2
	// At midpoint: full-range black = Y=0, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(0), out[i], "Y at pixel %d should be 0 at midpoint", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(128), out[ySize+i], "Cb at pixel %d should be 128 at midpoint", i)
		require.Equal(t, byte(128), out[ySize+uvSize+i], "Cr at pixel %d should be 128 at midpoint", i)
	}
}

func TestBlendDipPhase2(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	fb.SetLimitedRange(false)
	a := makeYUVFrame(4, 4, 100, 100, 100)
	b := makeYUVFrame(4, 4, 200, 160, 200)

	out := fb.BlendDip(a, b, 0.75)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain = 2*0.75 - 1 = 0.5, invGain = 0.5
	// Y: 200*0.5 + 0*0.5 = 100
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 100, int(out[i]), 1, "Y at pixel %d", i)
	}
	// Cb: 160*0.5 + 128*0.5 = 144
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 144, int(out[ySize+i]), 1, "Cb at pixel %d", i)
	}
	// Cr: 200*0.5 + 128*0.5 = 164
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 164, int(out[ySize+uvSize+i]), 1, "Cr at pixel %d", i)
	}
}

func TestBlendFTBHalf(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	fb.SetLimitedRange(false)
	a := makeYUVFrame(4, 4, 200, 100, 200)

	out := fb.BlendFTB(a, 0.5)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain=0.5, invGain=0.5
	// Y: 200*0.5 + 0*0.5 = 100
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 100, int(out[i]), 1, "Y at pixel %d", i)
	}
	// Cb: 100*0.5 + 128*0.5 = 114
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 114, int(out[ySize+i]), 1, "Cb at pixel %d", i)
	}
	// Cr: 200*0.5 + 128*0.5 = 164
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 164, int(out[ySize+uvSize+i]), 1, "Cr at pixel %d", i)
	}
}

func TestBlendFTBFull(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	fb.SetLimitedRange(false)
	a := makeYUVFrame(4, 4, 200, 200, 200)

	out := fb.BlendFTB(a, 1.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	// Full-range black: Y=0, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(0), out[i], "Y byte %d should be 0", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(128), out[ySize+i], "Cb byte %d should be 128", i)
		require.Equal(t, byte(128), out[ySize+uvSize+i], "Cr byte %d should be 128", i)
	}
}

func TestBlendFTBZero(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	a := makeYUVFrame(4, 4, 200, 100, 200)

	out := fb.BlendFTB(a, 0.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(200), out[i], "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(100), out[ySize+i], "Cb at pixel %d", i)
		require.Equal(t, byte(200), out[ySize+uvSize+i], "Cr at pixel %d", i)
	}
}

func TestBlenderReusesBuffer(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	a := makeYUVFrame(4, 4, 100, 128, 128)
	b := makeYUVFrame(4, 4, 200, 128, 128)

	out1 := fb.BlendMix(a, b, 0.5)
	ptr1 := &out1[0]
	out2 := fb.BlendMix(a, b, 0.7)
	ptr2 := &out2[0]

	require.Equal(t, ptr1, ptr2, "blender should reuse output buffer")
}

func TestBlenderYUVBufferSize(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 1920, 1080)
	// YUV420: Y=1920*1080 + Cb=960*540 + Cr=960*540 = 3110400
	expected := 1920*1080 + 2*(960*540)
	require.Equal(t, expected, len(fb.yuvBufOut))
}

// --- Wipe transition tests ---

func TestBlendWipePosition0AllA(t *testing.T) {
	t.Parallel()
	// At position 0.0, the entire frame should be source A regardless of direction.
	fb := mustNewFrameBlender(t, 8, 8)
	a := makeYUVFrame(8, 8, 200, 90, 240)
	b := makeYUVFrame(8, 8, 50, 200, 60)

	for _, dir := range []WipeDirection{WipeHLeft, WipeHRight, WipeVTop, WipeVBottom, WipeBoxCenterOut, WipeBoxEdgesIn} {
		out := fb.BlendWipe(a, b, 0.0, dir)
		ySize := 8 * 8
		uvSize := 4 * 4
		for i := 0; i < ySize; i++ {
			require.Equal(t, byte(200), out[i], "dir=%s Y at pixel %d should be A", dir, i)
		}
		for i := 0; i < uvSize; i++ {
			require.Equal(t, byte(90), out[ySize+i], "dir=%s Cb at pixel %d should be A", dir, i)
			require.Equal(t, byte(240), out[ySize+uvSize+i], "dir=%s Cr at pixel %d should be A", dir, i)
		}
	}
}

func TestBlendWipePosition1AllB(t *testing.T) {
	t.Parallel()
	// At position 1.0, the entire frame should be source B regardless of direction.
	fb := mustNewFrameBlender(t, 8, 8)
	a := makeYUVFrame(8, 8, 200, 90, 240)
	b := makeYUVFrame(8, 8, 50, 200, 60)

	for _, dir := range []WipeDirection{WipeHLeft, WipeHRight, WipeVTop, WipeVBottom, WipeBoxCenterOut, WipeBoxEdgesIn} {
		out := fb.BlendWipe(a, b, 1.0, dir)
		ySize := 8 * 8
		uvSize := 4 * 4
		for i := 0; i < ySize; i++ {
			require.Equal(t, byte(50), out[i], "dir=%s Y at pixel %d should be B", dir, i)
		}
		for i := 0; i < uvSize; i++ {
			require.Equal(t, byte(200), out[ySize+i], "dir=%s Cb at pixel %d should be B", dir, i)
			require.Equal(t, byte(60), out[ySize+uvSize+i], "dir=%s Cr at pixel %d should be B", dir, i)
		}
	}
}

func TestBlendWipeHLeftHalfway(t *testing.T) {
	t.Parallel()
	// At position 0.5 with h-left: threshold = x/width.
	// Pixels with x/width < 0.5 (i.e., x < 4) should be B.
	// Pixels with x/width > 0.5 (i.e., x >= 4) should be A.
	// The boundary (x=3..4) has a 4px soft edge so some blending.
	fb := mustNewFrameBlender(t, 8, 8)
	a := makeYUVFrame(8, 8, 200, 90, 240)
	b := makeYUVFrame(8, 8, 50, 200, 60)

	out := fb.BlendWipe(a, b, 0.5, WipeHLeft)
	// Check left side (x=0,1) -- should be fully B
	for y := 0; y < 8; y++ {
		for x := 0; x < 2; x++ {
			idx := y*8 + x
			require.Equal(t, byte(50), out[idx], "Y at (%d,%d) should be B (left side)", x, y)
		}
	}
	// Check right side (x=6,7) -- should be fully A
	for y := 0; y < 8; y++ {
		for x := 6; x < 8; x++ {
			idx := y*8 + x
			require.Equal(t, byte(200), out[idx], "Y at (%d,%d) should be A (right side)", x, y)
		}
	}
}

func TestBlendWipeVTopHalfway(t *testing.T) {
	t.Parallel()
	// At position 0.5 with v-top: threshold = y/height.
	// Pixels with y/height < 0.5 (y < 4) should be B.
	// Pixels with y/height > 0.5 (y >= 4) should be A.
	fb := mustNewFrameBlender(t, 8, 8)
	a := makeYUVFrame(8, 8, 200, 90, 240)
	b := makeYUVFrame(8, 8, 50, 200, 60)

	out := fb.BlendWipe(a, b, 0.5, WipeVTop)
	// Check top rows (y=0,1) -- should be fully B
	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			idx := y*8 + x
			require.Equal(t, byte(50), out[idx], "Y at (%d,%d) should be B (top side)", x, y)
		}
	}
	// Check bottom rows (y=6,7) -- should be fully A
	for y := 6; y < 8; y++ {
		for x := 0; x < 8; x++ {
			idx := y*8 + x
			require.Equal(t, byte(200), out[idx], "Y at (%d,%d) should be A (bottom side)", x, y)
		}
	}
}

func TestBlendWipeBoxCenterOut(t *testing.T) {
	t.Parallel()
	// At position 0.5 with box-center-out: threshold = max(|x-cx|/cx, |y-cy|/cy).
	// Center pixels have low threshold (< 0.5) -> B. Edge pixels have high threshold -> A.
	fb := mustNewFrameBlender(t, 8, 8)
	a := makeYUVFrame(8, 8, 200, 90, 240)
	b := makeYUVFrame(8, 8, 50, 200, 60)

	out := fb.BlendWipe(a, b, 0.5, WipeBoxCenterOut)
	// Center pixel (3,3) or (4,4): threshold should be low -> B
	// For 8x8: cx=3.5, cy=3.5. At (3,3): max(|3-3.5|/3.5, |3-3.5|/3.5) = max(0.143, 0.143) = 0.143 < 0.5 -> B
	require.Equal(t, byte(50), out[3*8+3], "center pixel should be B")
	require.Equal(t, byte(50), out[4*8+4], "near-center pixel should be B")

	// Corner pixel (0,0): max(|0-3.5|/3.5, |0-3.5|/3.5) = 1.0 > 0.5 -> A
	require.Equal(t, byte(200), out[0*8+0], "corner pixel should be A")
	// Corner pixel (7,7): max(|7-3.5|/3.5, |7-3.5|/3.5) = 1.0 > 0.5 -> A
	require.Equal(t, byte(200), out[7*8+7], "corner pixel should be A")
}

func TestBlendWipeSoftEdge(t *testing.T) {
	t.Parallel()
	// Verify that pixels at the wipe boundary have blended (intermediate) values.
	// Use a wider frame so the 4px soft edge is visible.
	// softEdge = 2/32 = 0.0625. Thresholds use x/(w-1) = x/31.
	fb := mustNewFrameBlender(t, 32, 8)
	a := makeYUVFrame(32, 8, 200, 128, 128)
	b := makeYUVFrame(32, 8, 50, 128, 128)

	out := fb.BlendWipe(a, b, 0.5, WipeHLeft)
	// Soft edge spans [0.5-0.0625, 0.5+0.0625] = [0.4375, 0.5625].
	// x=13: threshold = 13/31 ≈ 0.4194, below 0.4375 → fully B (50)
	// x=19: threshold = 19/31 ≈ 0.6129, above 0.5625 → fully A (200)
	// x=15 or x=16: near boundary → intermediate value
	yRow0_x13 := out[13]
	yRow0_x19 := out[19]
	require.Equal(t, byte(50), yRow0_x13, "x=13 should be fully B")
	require.Equal(t, byte(200), yRow0_x19, "x=19 should be fully A")

	// x=16 is at threshold=16/31≈0.516, near position 0.5 → should be blended
	yRow0_x16 := out[16]
	require.Greater(t, int(yRow0_x16), 50, "x=16 should be blended (not fully B)")
	require.Less(t, int(yRow0_x16), 200, "x=16 should be blended (not fully A)")
}

func TestBlendWipeThresholdSpansFullRange(t *testing.T) {
	t.Parallel()
	// Bug 24: with invW = 1/w, the last column gets threshold = (w-1)/w which
	// never reaches 1.0. With w=100, the last pixel threshold is 99/100=0.99
	// instead of 1.0. At position=0.98, softEdge=2/100=0.02:
	//
	//   Bug (threshold=0.99): 0.99 is in [0.96, 1.00] soft edge region.
	//     alpha = (0.98+0.02-0.99)/0.04 = 0.01/0.04 = 0.25, byte=64
	//   Fix (threshold=1.0):  1.0 > 0.98+0.02=1.00? No (equal). In soft edge:
	//     alpha = (0.98+0.02-1.0)/0.04 = 0.0/0.04 = 0.0, byte=0
	//
	// The last pixel should be fully A (alpha=0) at position=0.98 because
	// the wipe hasn't reached the far edge yet.
	w := 100
	fb := mustNewFrameBlender(t, w, 4)
	fb.generateWipeAlpha(0.98, WipeHLeft)

	lastPixelAlpha := fb.wipeAlphaMap[w-1]
	require.Equal(t, byte(0), lastPixelAlpha,
		"last pixel (threshold=1.0) should be fully A (alpha=0) at position=0.98, got %d", lastPixelAlpha)
}

func TestBlendWipeVerticalThresholdSpansFullRange(t *testing.T) {
	t.Parallel()
	// Same logic as horizontal but for vertical wipe.
	// With h=100, softEdge=2/100=0.02. At position=0.98:
	//   Bug (threshold=0.99): in soft edge, alpha=64
	//   Fix (threshold=1.0):  alpha=0 (fully A)
	h := 100
	fb := mustNewFrameBlender(t, 4, h)
	fb.generateWipeAlpha(0.98, WipeVTop)

	// Last row, first pixel.
	lastRowAlpha := fb.wipeAlphaMap[(h-1)*4]
	require.Equal(t, byte(0), lastRowAlpha,
		"last row (threshold=1.0) should be fully A (alpha=0) at position=0.98, got %d", lastRowAlpha)
}

func TestBlendWipeAllDirectionsValid(t *testing.T) {
	t.Parallel()
	// Ensure all 6 directions produce valid output of the correct size.
	fb := mustNewFrameBlender(t, 8, 8)
	a := makeYUVFrame(8, 8, 100, 128, 128)
	b := makeYUVFrame(8, 8, 200, 128, 128)
	expectedSize := 8*8 + 2*(4*4)

	for _, dir := range []WipeDirection{WipeHLeft, WipeHRight, WipeVTop, WipeVBottom, WipeBoxCenterOut, WipeBoxEdgesIn} {
		out := fb.BlendWipe(a, b, 0.3, dir)
		require.Equal(t, expectedSize, len(out), "dir=%s output size", dir)
		// Verify no panic and output contains meaningful data
		hasA, hasB := false, false
		for i := 0; i < 8*8; i++ {
			if out[i] == 100 {
				hasA = true
			}
			if out[i] == 200 {
				hasB = true
			}
		}
		// At pos=0.3, both A and B regions should be visible for all directions
		require.True(t, hasA || hasB, "dir=%s should produce non-zero output", dir)
	}
}

func TestBlendStinger_BoundsCheck(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)

	validBase := makeYUVFrame(4, 4, 128, 128, 128)
	validStinger := makeYUVFrame(4, 4, 200, 128, 128)
	validAlpha := make([]byte, 4*4)
	for i := range validAlpha {
		validAlpha[i] = 128
	}

	// Valid inputs should produce output.
	result := fb.BlendStinger(validBase, validStinger, validAlpha)
	require.NotNil(t, result, "valid inputs should produce output")

	// Undersized base should return nil.
	result = fb.BlendStinger(validBase[:5], validStinger, validAlpha)
	require.Nil(t, result, "undersized base should return nil")

	// Undersized stinger should return nil.
	result = fb.BlendStinger(validBase, validStinger[:5], validAlpha)
	require.Nil(t, result, "undersized stinger should return nil")

	// Undersized alpha should return nil.
	result = fb.BlendStinger(validBase, validStinger, validAlpha[:5])
	require.Nil(t, result, "undersized alpha should return nil")

	// Empty slices should return nil.
	result = fb.BlendStinger(nil, validStinger, validAlpha)
	require.Nil(t, result, "nil base should return nil")
}

// --- Limited-range YUV tests ---

func TestBlendFTBFull_LimitedRange(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	fb.SetLimitedRange(true)
	a := makeYUVFrame(4, 4, 200, 200, 200)

	out := fb.BlendFTB(a, 1.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	// Limited-range black: Y=16, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(16), out[i], "Y byte %d should be 16 (limited-range black)", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(128), out[ySize+i], "Cb byte %d should be 128", i)
		require.Equal(t, byte(128), out[ySize+uvSize+i], "Cr byte %d should be 128", i)
	}
}

func TestBlendFTBHalf_LimitedRange(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	fb.SetLimitedRange(true)
	a := makeYUVFrame(4, 4, 200, 128, 128)

	out := fb.BlendFTB(a, 0.5)
	ySize := 4 * 4
	// Y = 200*0.5 + 16*0.5 = 108
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 108, int(out[i]), 1, "Y at pixel %d", i)
	}
}

func TestBlendDipMidpoint_LimitedRange(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	fb.SetLimitedRange(true)
	a := makeYUVFrame(4, 4, 200, 128, 128)
	b := makeYUVFrame(4, 4, 200, 128, 128)

	out := fb.BlendDip(a, b, 0.5)
	ySize := 4 * 4
	// At midpoint, Y should be limited-range black (16)
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 16, int(out[i]), 1, "Y at pixel %d should be 16 at dip midpoint", i)
	}
}

func TestNewFrameBlenderDefaultsToLimitedRange(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 16, 16)
	require.Equal(t, byte(16), fb.blackY)
}

func TestWipeAlphaLinearCorrectness(t *testing.T) {
	t.Parallel()
	w, h := 64, 48
	fb := mustNewFrameBlender(t, w, h)

	for _, pos := range []float64{0.0, 0.25, 0.5, 0.75, 1.0} {
		// Horizontal left: alpha varies along X only.
		// Thresholds span [0, 1] using x/(w-1) so the last pixel reaches threshold=1.0.
		fb.generateWipeAlpha(pos, WipeHLeft)
		softEdge := 2.0 / float64(w)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				threshold := float64(x) / float64(w-1)
				expected := wipeAlphaByte(threshold, pos, softEdge)
				got := fb.wipeAlphaMap[y*w+x]
				require.Equal(t, expected, got,
					"HLeft mismatch at (%d,%d) pos=%f", x, y, pos)
			}
		}

		// Vertical top: alpha varies along Y only.
		// Thresholds span [0, 1] using y/(h-1) so the last row reaches threshold=1.0.
		fb.generateWipeAlpha(pos, WipeVTop)
		softEdge = 2.0 / float64(h)
		for y := 0; y < h; y++ {
			threshold := float64(y) / float64(h-1)
			expected := wipeAlphaByte(threshold, pos, softEdge)
			for x := 0; x < w; x++ {
				got := fb.wipeAlphaMap[y*w+x]
				require.Equal(t, expected, got,
					"VTop mismatch at (%d,%d) pos=%f", x, y, pos)
			}
		}
	}
}

func BenchmarkWipeAlphaLinear(b *testing.B) {
	w, h := 1920, 1080
	fb := mustNewFrameBlender(b, w, h)

	b.Run("horizontal_1D", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			fb.generateWipeAlpha(0.5, WipeHLeft)
		}
	})

	b.Run("vertical_1D", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			fb.generateWipeAlpha(0.5, WipeVTop)
		}
	})

	b.Run("box_per_pixel", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			fb.generateWipeAlpha(0.5, WipeBoxCenterOut)
		}
	})
}

func TestNewFrameBlender_OddDimensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		width  int
		height int
	}{
		{"odd width", 1921, 1080},
		{"odd height", 1920, 1081},
		{"both odd", 1921, 1081},
		{"small odd width", 3, 4},
		{"small odd height", 4, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb, err := NewFrameBlender(tc.width, tc.height)
			require.Nil(t, fb, "should return nil for odd dimensions")
			require.Error(t, err)
			require.True(t, errors.Is(err, errOddDimensions), "error should wrap errOddDimensions")
		})
	}
}

func TestNewFrameBlender_EvenDimensions(t *testing.T) {
	t.Parallel()
	fb, err := NewFrameBlender(1920, 1080)
	require.NoError(t, err)
	require.NotNil(t, fb)
	require.Equal(t, 1920, fb.width)
	require.Equal(t, 1080, fb.height)
}

// --- NAM (Non-Additive Mix) tests ---

func TestBlendNAM_Position0_AllA(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	a := makeYUVFrame(4, 4, 200, 90, 240)
	b := makeYUVFrame(4, 4, 50, 200, 60)

	out := fb.BlendNAM(a, b, 0.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(200), out[i], "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(90), out[ySize+i], "Cb at pixel %d", i)
		require.Equal(t, byte(240), out[ySize+uvSize+i], "Cr at pixel %d", i)
	}
}

func TestBlendNAM_Position1_AllB(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	a := makeYUVFrame(4, 4, 200, 90, 240)
	b := makeYUVFrame(4, 4, 50, 200, 60)

	out := fb.BlendNAM(a, b, 1.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(50), out[i], "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(200), out[ySize+i], "Cb at pixel %d", i)
		require.Equal(t, byte(60), out[ySize+uvSize+i], "Cr at pixel %d", i)
	}
}

func TestBlendNAM_Position05_PerPixelMax(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	ySize := 4 * 4
	uvSize := 2 * 2

	a := make([]byte, ySize+2*uvSize)
	b := make([]byte, ySize+2*uvSize)

	// Set A brighter in first half of Y plane, B brighter in second half
	for i := 0; i < 8; i++ {
		a[i] = 200
		b[i] = 100
	}
	for i := 8; i < 16; i++ {
		a[i] = 100
		b[i] = 200
	}
	// Fill chroma with 128
	for i := ySize; i < len(a); i++ {
		a[i] = 128
		b[i] = 128
	}

	result := fb.BlendNAM(a, b, 0.5)
	// At pos 0.5, equal gains: output = max(A*0.5, B*0.5) for each pixel
	// But since gain_a and gain_b are both 0.5, whichever Y is brighter wins.
	// For pixel 0: A=200, B=100 => A*0.5=100 > B*0.5=50 => output should be A's Y=200 scaled
	// Actually the standard NAM: output = max(A * gain_a, B * gain_b) where
	// gain_a = 1-position, gain_b = position. At 0.5 both gains=0.5.
	// Per-pixel: max(200*0.5, 100*0.5) = max(100, 50) = 100
	// But the winning pixel's luma is output at its gain-scaled value.
	// Actually the spec says: for each pixel, compare gained values, output the larger.
	// pixel 0: max(200*0.5, 100*0.5) = 100
	if result[0] != 100 {
		t.Errorf("pixel 0: got %d, want 100", result[0])
	}
	// pixel 8: max(100*0.5, 200*0.5) = 100
	if result[8] != 100 {
		t.Errorf("pixel 8: got %d, want 100", result[8])
	}
}

func TestBlendNAM_ChromaFollowsLumaWinner(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	ySize := 4 * 4
	uvSize := 2 * 2

	// Make A uniformly brighter than B
	a := make([]byte, ySize+2*uvSize)
	b := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		a[i] = 200
		b[i] = 50
	}
	// A chroma: Cb=80, Cr=90
	for i := 0; i < uvSize; i++ {
		a[ySize+i] = 80
		a[ySize+uvSize+i] = 90
	}
	// B chroma: Cb=200, Cr=210
	for i := 0; i < uvSize; i++ {
		b[ySize+i] = 200
		b[ySize+uvSize+i] = 210
	}

	result := fb.BlendNAM(a, b, 0.5)
	// A always wins (200*0.5=100 > 50*0.5=25)
	// Chroma should follow A's contribution at gain_a scale. Cb/Cr are
	// offset-binary centered at 128, so the gain applies about the neutral
	// point: out = 128 + (C-128)*gain.
	for i := 0; i < uvSize; i++ {
		// A's Cb at gain 0.5: 128 + (80-128)*0.5 = 104
		require.InDelta(t, 104, int(result[ySize+i]), 1, "Cb at chroma pixel %d", i)
		// A's Cr at gain 0.5: 128 + (90-128)*0.5 = 109
		require.InDelta(t, 109, int(result[ySize+uvSize+i]), 1, "Cr at chroma pixel %d", i)
	}
}

// TestBlendNAM_NeutralChromaStaysNeutral pins the offset-binary invariant:
// neutral gray sources (Cb=Cr=128) must stay neutral at every transition
// position. Scaling the raw chroma byte by the gain (instead of about the
// 128 neutral point) drags 128 down to 64 at position 0.5 — a saturated
// green-yellow cast over the whole frame at mid-transition.
func TestBlendNAM_NeutralChromaStaysNeutral(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	ySize := 4 * 4
	uvSize := 2 * 2

	a := makeYUVFrame(4, 4, 200, 128, 128)
	b := makeYUVFrame(4, 4, 50, 128, 128)

	for _, pos := range []float64{0.25, 0.5, 0.75} {
		result := fb.BlendNAM(a, b, pos)
		for i := 0; i < uvSize*2; i++ {
			require.Equal(t, byte(128), result[ySize+i],
				"neutral chroma plane byte %d at position %v", i, pos)
		}
	}
}

func TestBlendNAM_MixedPixels(t *testing.T) {
	t.Parallel()
	// Use a frame where different 2x2 blocks have different brightness winners
	fb := mustNewFrameBlender(t, 4, 4)
	ySize := 4 * 4

	a := make([]byte, ySize+2*(2*2))
	b := make([]byte, ySize+2*(2*2))

	// Top-left 2x2 block: A brighter (Y=250)
	a[0], a[1], a[4], a[5] = 250, 250, 250, 250
	b[0], b[1], b[4], b[5] = 30, 30, 30, 30

	// Top-right 2x2 block: B brighter (Y=240)
	a[2], a[3], a[6], a[7] = 30, 30, 30, 30
	b[2], b[3], b[6], b[7] = 240, 240, 240, 240

	// Bottom blocks: fill with equal
	for i := 8; i < 16; i++ {
		a[i] = 128
		b[i] = 128
	}

	// Fill chroma
	for i := ySize; i < len(a); i++ {
		a[i] = 128
		b[i] = 128
	}

	result := fb.BlendNAM(a, b, 0.5)
	// Top-left: A wins (250*0.5=125 > 30*0.5=15), output=125
	require.InDelta(t, 125, int(result[0]), 1, "top-left pixel")
	// Top-right: B wins (240*0.5=120 > 30*0.5=15), output=120
	require.InDelta(t, 120, int(result[2]), 1, "top-right pixel")
}

func TestBlendNAM_ReusesBuffer(t *testing.T) {
	t.Parallel()
	fb := mustNewFrameBlender(t, 4, 4)
	a := makeYUVFrame(4, 4, 100, 128, 128)
	b := makeYUVFrame(4, 4, 200, 128, 128)

	out1 := fb.BlendNAM(a, b, 0.5)
	ptr1 := &out1[0]
	out2 := fb.BlendNAM(a, b, 0.7)
	ptr2 := &out2[0]
	require.Equal(t, ptr1, ptr2, "NAM should reuse output buffer")
}
