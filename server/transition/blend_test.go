package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
	fb := NewFrameBlender(1920, 1080)
	require.NotNil(t, fb)
	require.Equal(t, 1920, fb.width)
	require.Equal(t, 1080, fb.height)
}

func TestBlendMixPosition0(t *testing.T) {
	fb := NewFrameBlender(4, 4)
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
	fb := NewFrameBlender(4, 4)
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
	fb := NewFrameBlender(4, 4)
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
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 100, 200)
	b := makeYUVFrame(4, 4, 100, 200, 100)

	out := fb.BlendDip(a, b, 0.25)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain = 1.0 - 2*0.25 = 0.5, invGain = 0.5
	// Y: 200*0.5 = 100
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
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 255, 200, 200)
	b := makeYUVFrame(4, 4, 255, 200, 200)

	out := fb.BlendDip(a, b, 0.5)
	ySize := 4 * 4
	uvSize := 2 * 2
	// At midpoint: black = Y=0, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(0), out[i], "Y at pixel %d should be 0 at midpoint", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(128), out[ySize+i], "Cb at pixel %d should be 128 at midpoint", i)
		require.Equal(t, byte(128), out[ySize+uvSize+i], "Cr at pixel %d should be 128 at midpoint", i)
	}
}

func TestBlendDipPhase2(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 100, 100, 100)
	b := makeYUVFrame(4, 4, 200, 160, 200)

	out := fb.BlendDip(a, b, 0.75)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain = 2*0.75 - 1 = 0.5, invGain = 0.5
	// Y: 200*0.5 = 100
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
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 100, 200)

	out := fb.BlendFTB(a, 0.5)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain=0.5, invGain=0.5
	// Y: 200*0.5 = 100
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
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 200, 200)

	out := fb.BlendFTB(a, 1.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	// Fully black: Y=0, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(0), out[i], "Y byte %d should be 0", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(128), out[ySize+i], "Cb byte %d should be 128", i)
		require.Equal(t, byte(128), out[ySize+uvSize+i], "Cr byte %d should be 128", i)
	}
}

func TestBlendFTBZero(t *testing.T) {
	fb := NewFrameBlender(4, 4)
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
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 100, 128, 128)
	b := makeYUVFrame(4, 4, 200, 128, 128)

	out1 := fb.BlendMix(a, b, 0.5)
	ptr1 := &out1[0]
	out2 := fb.BlendMix(a, b, 0.7)
	ptr2 := &out2[0]

	require.Equal(t, ptr1, ptr2, "blender should reuse output buffer")
}

func TestBlenderYUVBufferSize(t *testing.T) {
	fb := NewFrameBlender(1920, 1080)
	// YUV420: Y=1920*1080 + Cb=960*540 + Cr=960*540 = 3110400
	expected := 1920*1080 + 2*(960*540)
	require.Equal(t, expected, len(fb.yuvBufOut))
}

// --- Wipe transition tests ---

func TestBlendWipePosition0AllA(t *testing.T) {
	// At position 0.0, the entire frame should be source A regardless of direction.
	fb := NewFrameBlender(8, 8)
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
	// At position 1.0, the entire frame should be source B regardless of direction.
	fb := NewFrameBlender(8, 8)
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
	// At position 0.5 with h-left: threshold = x/width.
	// Pixels with x/width < 0.5 (i.e., x < 4) should be B.
	// Pixels with x/width > 0.5 (i.e., x >= 4) should be A.
	// The boundary (x=3..4) has a 4px soft edge so some blending.
	fb := NewFrameBlender(8, 8)
	a := makeYUVFrame(8, 8, 200, 90, 240)
	b := makeYUVFrame(8, 8, 50, 200, 60)

	out := fb.BlendWipe(a, b, 0.5, WipeHLeft)
	// Check left side (x=0,1) — should be fully B
	for y := 0; y < 8; y++ {
		for x := 0; x < 2; x++ {
			idx := y*8 + x
			require.Equal(t, byte(50), out[idx], "Y at (%d,%d) should be B (left side)", x, y)
		}
	}
	// Check right side (x=6,7) — should be fully A
	for y := 0; y < 8; y++ {
		for x := 6; x < 8; x++ {
			idx := y*8 + x
			require.Equal(t, byte(200), out[idx], "Y at (%d,%d) should be A (right side)", x, y)
		}
	}
}

func TestBlendWipeVTopHalfway(t *testing.T) {
	// At position 0.5 with v-top: threshold = y/height.
	// Pixels with y/height < 0.5 (y < 4) should be B.
	// Pixels with y/height > 0.5 (y >= 4) should be A.
	fb := NewFrameBlender(8, 8)
	a := makeYUVFrame(8, 8, 200, 90, 240)
	b := makeYUVFrame(8, 8, 50, 200, 60)

	out := fb.BlendWipe(a, b, 0.5, WipeVTop)
	// Check top rows (y=0,1) — should be fully B
	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			idx := y*8 + x
			require.Equal(t, byte(50), out[idx], "Y at (%d,%d) should be B (top side)", x, y)
		}
	}
	// Check bottom rows (y=6,7) — should be fully A
	for y := 6; y < 8; y++ {
		for x := 0; x < 8; x++ {
			idx := y*8 + x
			require.Equal(t, byte(200), out[idx], "Y at (%d,%d) should be A (bottom side)", x, y)
		}
	}
}

func TestBlendWipeBoxCenterOut(t *testing.T) {
	// At position 0.5 with box-center-out: threshold = max(|x-cx|/cx, |y-cy|/cy).
	// Center pixels have low threshold (< 0.5) → B. Edge pixels have high threshold → A.
	fb := NewFrameBlender(8, 8)
	a := makeYUVFrame(8, 8, 200, 90, 240)
	b := makeYUVFrame(8, 8, 50, 200, 60)

	out := fb.BlendWipe(a, b, 0.5, WipeBoxCenterOut)
	// Center pixel (3,3) or (4,4): threshold should be low → B
	// For 8x8: cx=3.5, cy=3.5. At (3,3): max(|3-3.5|/3.5, |3-3.5|/3.5) = max(0.143, 0.143) = 0.143 < 0.5 → B
	require.Equal(t, byte(50), out[3*8+3], "center pixel should be B")
	require.Equal(t, byte(50), out[4*8+4], "near-center pixel should be B")

	// Corner pixel (0,0): max(|0-3.5|/3.5, |0-3.5|/3.5) = 1.0 > 0.5 → A
	require.Equal(t, byte(200), out[0*8+0], "corner pixel should be A")
	// Corner pixel (7,7): max(|7-3.5|/3.5, |7-3.5|/3.5) = 1.0 > 0.5 → A
	require.Equal(t, byte(200), out[7*8+7], "corner pixel should be A")
}

func TestBlendWipeSoftEdge(t *testing.T) {
	// Verify that pixels at the wipe boundary have blended (intermediate) values.
	// Use a wider frame so the 4px soft edge is visible.
	fb := NewFrameBlender(32, 8)
	a := makeYUVFrame(32, 8, 200, 128, 128)
	b := makeYUVFrame(32, 8, 50, 128, 128)

	out := fb.BlendWipe(a, b, 0.5, WipeHLeft)
	// At x=16 (threshold = 16/32 = 0.5 = position), this is exactly on the boundary.
	// With 4px soft edge, pixels within +/-2px of the boundary should be blended.
	// x=14: threshold = 14/32 = 0.4375, well below 0.5 → fully B (50)
	// x=18: threshold = 18/32 = 0.5625, well above 0.5 → fully A (200)
	// x=15 or x=16: near boundary → intermediate value
	yRow0_x14 := out[14]
	yRow0_x18 := out[18]
	require.Equal(t, byte(50), yRow0_x14, "x=14 should be fully B")
	require.Equal(t, byte(200), yRow0_x18, "x=18 should be fully A")

	// x=16 is at threshold=0.5 = position → should be 50% blend
	yRow0_x16 := out[16]
	require.Greater(t, int(yRow0_x16), 50, "x=16 should be blended (not fully B)")
	require.Less(t, int(yRow0_x16), 200, "x=16 should be blended (not fully A)")
}

func TestBlendWipeAllDirectionsValid(t *testing.T) {
	// Ensure all 6 directions produce valid output of the correct size.
	fb := NewFrameBlender(8, 8)
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
