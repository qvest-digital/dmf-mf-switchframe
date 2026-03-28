//go:build correctness

package transition

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

// ==========================================================================
// 10-bit YUV422P10LE transition blend correctness tests.
//
// These verify that the 10-bit transition functions (BlendMix10, BlendFTB10,
// BlendDip10, BlendWipe10) produce correct pixel values using the full
// 10-bit range (0-1023) with proper limited-range black (Y=64) and neutral
// chroma (512).
// ==========================================================================

// frameSize10 returns total bytes for a YUV422P10LE frame.
func frameSize10(w, h int) int {
	return w * h * 4 // Y(w*h*2) + Cb(w/2*h*2) + Cr(w/2*h*2)
}

// generateSolid10 creates a solid-color YUV422P10LE frame.
func generateSolid10(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, frameSize10(w, h))
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cb)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}
	return buf
}

// readY10 reads the Y sample at luma position (x, y) from a YUV422P10LE frame.
func readY10(buf []byte, w, x, y int) uint16 {
	return binary.LittleEndian.Uint16(buf[(y*w+x)*2:])
}

// readCb10 reads the Cb sample at chroma position (cx, cy) from a YUV422P10LE frame.
func readCb10(buf []byte, w, h, cx, cy int) uint16 {
	ySize := w * h
	chromaW := w / 2
	off := ySize*2 + (cy*chromaW+cx)*2
	return binary.LittleEndian.Uint16(buf[off:])
}

// readCr10 reads the Cr sample at chroma position (cx, cy) from a YUV422P10LE frame.
func readCr10(buf []byte, w, h, cx, cy int) uint16 {
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	off := ySize*2 + chromaSize*2 + (cy*chromaW+cx)*2
	return binary.LittleEndian.Uint16(buf[off:])
}

// ==========================================================================
// 1. TestTransition10bit_MixBlend — BlendMix10 at midpoint produces the
//    correct linear interpolation of two 10-bit sources.
// ==========================================================================

func TestTransition10bit_MixBlend(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 200, 512, 512)
	frameB := generateSolid10(w, h, 800, 512, 512)
	dst := make([]byte, frameSize10(w, h))

	// pos=128 is midpoint (50%).
	err := transition.BlendMix10(dst, frameA, frameB, w, h, 128)
	require.NoError(t, err)

	// Expected Y: (200 * (256-128) + 800 * 128 + 128) >> 8
	//           = (200*128 + 800*128 + 128) >> 8
	//           = (25600 + 102400 + 128) >> 8
	//           = 128128 >> 8
	//           = 500
	y := readY10(dst, w, 0, 0)
	require.InDelta(t, 500, int(y), 1, "10-bit mix at 50%%: Y should be ~500, got %d", y)

	// Chroma should remain at 512 (both sources have neutral chroma).
	cb := readCb10(dst, w, h, 0, 0)
	cr := readCr10(dst, w, h, 0, 0)
	require.InDelta(t, 512, int(cb), 1, "Cb should remain ~512, got %d", cb)
	require.InDelta(t, 512, int(cr), 1, "Cr should remain ~512, got %d", cr)
}

// ==========================================================================
// 2. TestTransition10bit_MixAt0IsSourceA — pos=0 yields source A exactly.
// ==========================================================================

func TestTransition10bit_MixAt0IsSourceA(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 300, 400, 600)
	frameB := generateSolid10(w, h, 700, 200, 800)
	dst := make([]byte, frameSize10(w, h))

	err := transition.BlendMix10(dst, frameA, frameB, w, h, 0)
	require.NoError(t, err)

	require.Equal(t, frameA, dst, "BlendMix10 at pos=0 should yield exact source A")
}

// ==========================================================================
// 3. TestTransition10bit_MixAt256IsSourceB — pos=256 yields source B exactly.
// ==========================================================================

func TestTransition10bit_MixAt256IsSourceB(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 300, 400, 600)
	frameB := generateSolid10(w, h, 700, 200, 800)
	dst := make([]byte, frameSize10(w, h))

	err := transition.BlendMix10(dst, frameA, frameB, w, h, 256)
	require.NoError(t, err)

	require.Equal(t, frameB, dst, "BlendMix10 at pos=256 should yield exact source B")
}

// ==========================================================================
// 4. TestTransition10bit_FTB — Fade to black at pos=256 yields Y=64, Cb=512, Cr=512.
// ==========================================================================

func TestTransition10bit_FTB(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	src := generateSolid10(w, h, 500, 300, 700)
	dst := make([]byte, frameSize10(w, h))

	// pos=256 means fully faded to black.
	err := transition.BlendFTB10(dst, src, w, h, 256, true)
	require.NoError(t, err)

	// Verify Y=64 (limited-range black).
	y := readY10(dst, w, 0, 0)
	require.InDelta(t, 64, int(y), 1, "FTB at pos=256: Y should be 64, got %d", y)

	// Verify Cb=512, Cr=512.
	cb := readCb10(dst, w, h, 0, 0)
	cr := readCr10(dst, w, h, 0, 0)
	require.InDelta(t, 512, int(cb), 1, "FTB Cb should be 512, got %d", cb)
	require.InDelta(t, 512, int(cr), 1, "FTB Cr should be 512, got %d", cr)
}

// ==========================================================================
// 5. TestTransition10bit_FTB_AtZero — FTB at pos=0 preserves source.
// ==========================================================================

func TestTransition10bit_FTB_AtZero(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	src := generateSolid10(w, h, 500, 300, 700)
	dst := make([]byte, frameSize10(w, h))

	err := transition.BlendFTB10(dst, src, w, h, 0, true)
	require.NoError(t, err)

	require.Equal(t, src, dst, "FTB at pos=0 should preserve source")
}

// ==========================================================================
// 6. TestTransition10bit_FTB_FullRange — Full-range FTB goes to Y=0.
// ==========================================================================

func TestTransition10bit_FTB_FullRange(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	src := generateSolid10(w, h, 500, 300, 700)
	dst := make([]byte, frameSize10(w, h))

	err := transition.BlendFTB10(dst, src, w, h, 256, false)
	require.NoError(t, err)

	y := readY10(dst, w, 0, 0)
	require.InDelta(t, 0, int(y), 1, "full-range FTB at pos=256: Y should be 0, got %d", y)
}

// ==========================================================================
// 7. TestTransition10bit_Dip — Dip at midpoint (pos=128) goes to black.
// ==========================================================================

func TestTransition10bit_Dip(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 500, 512, 512)
	frameB := generateSolid10(w, h, 800, 512, 512)
	dst := make([]byte, frameSize10(w, h))

	// pos=128 is the dip midpoint (fully black).
	err := transition.BlendDip10(dst, frameA, frameB, w, h, 128, true)
	require.NoError(t, err)

	// At midpoint, gain=0 so result = blackY = 64.
	y := readY10(dst, w, 0, 0)
	require.InDelta(t, 64, int(y), 1, "dip midpoint: Y should be 64, got %d", y)

	cb := readCb10(dst, w, h, 0, 0)
	cr := readCr10(dst, w, h, 0, 0)
	require.InDelta(t, 512, int(cb), 1, "dip midpoint: Cb should be 512, got %d", cb)
	require.InDelta(t, 512, int(cr), 1, "dip midpoint: Cr should be 512, got %d", cr)
}

// ==========================================================================
// 8. TestTransition10bit_DipEndpoints — Dip at pos=0 is A, pos=256 is B.
// ==========================================================================

func TestTransition10bit_DipEndpoints(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 300, 400, 600)
	frameB := generateSolid10(w, h, 700, 200, 800)
	dst := make([]byte, frameSize10(w, h))

	// pos=0: full A.
	err := transition.BlendDip10(dst, frameA, frameB, w, h, 0, true)
	require.NoError(t, err)
	require.Equal(t, frameA, dst, "dip at pos=0 should yield source A")

	// pos=256: full B.
	err = transition.BlendDip10(dst, frameA, frameB, w, h, 256, true)
	require.NoError(t, err)
	require.Equal(t, frameB, dst, "dip at pos=256 should yield source B")
}

// ==========================================================================
// 9. TestTransition10bit_WipeAlphaPreservation — Wipe with an 8-bit alpha
//    mask correctly blends 10-bit sources.
// ==========================================================================

func TestTransition10bit_WipeAlphaPreservation(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 200, 512, 512)
	frameB := generateSolid10(w, h, 800, 512, 512)
	dst := make([]byte, frameSize10(w, h))

	// Create an alpha mask: left half = 0 (show A), right half = 255 (show B).
	alpha := make([]byte, w*h)
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			if px >= w/2 {
				alpha[py*w+px] = 255
			}
		}
	}

	err := transition.BlendWipe10(dst, frameA, frameB, w, h, alpha)
	require.NoError(t, err)

	// Left side (alpha=0): should be source A (Y=200).
	yLeft := readY10(dst, w, 0, 0)
	require.InDelta(t, 200, int(yLeft), 1,
		"wipe left (alpha=0): Y should be 200, got %d", yLeft)

	// Right side (alpha=255): should be source B (Y=800).
	yRight := readY10(dst, w, w-1, 0)
	// With alpha=255: result = (200*(256-255) + 800*255 + 128) >> 8
	// = (200 + 204000 + 128) >> 8 = 204328 >> 8 = 798
	require.InDelta(t, 800, int(yRight), 2,
		"wipe right (alpha=255): Y should be ~800, got %d", yRight)
}

// ==========================================================================
// 10. TestTransition10bit_BlendVs8bit_Proportional — Verify the 4x scaling
//     relationship between 8-bit and 10-bit blend results.
// ==========================================================================

func TestTransition10bit_BlendVs8bit_Proportional(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight

	// 10-bit blend: A=200, B=800 at 50%.
	frameA10 := generateSolid10(w, h, 200, 512, 512)
	frameB10 := generateSolid10(w, h, 800, 512, 512)
	dst10 := make([]byte, frameSize10(w, h))
	err := transition.BlendMix10(dst10, frameA10, frameB10, w, h, 128)
	require.NoError(t, err)
	y10 := readY10(dst10, w, 0, 0)

	// 8-bit blend: A=50, B=200 at 50%.
	frameA8 := make([]byte, yuvFrameSize(w, h))
	frameB8 := make([]byte, yuvFrameSize(w, h))
	for i := 0; i < w*h; i++ {
		frameA8[i] = 50
		frameB8[i] = 200
	}
	for i := w * h; i < len(frameA8); i++ {
		frameA8[i] = 128
		frameB8[i] = 128
	}

	fb8, err := transition.NewFrameBlender(w, h)
	require.NoError(t, err)
	dst8 := fb8.BlendMix(frameA8, frameB8, 0.5)
	y8 := dst8[0]

	// 8-bit blend of 50 and 200 at 50% should be ~125.
	// 10-bit blend of 200 and 800 at 50% should be ~500.
	// The ratio should be approximately 4:1.
	ratio := float64(y10) / float64(y8)
	t.Logf("10-bit Y=%d, 8-bit Y=%d, ratio=%.2f", y10, y8, ratio)
	require.InDelta(t, 4.0, ratio, 0.2,
		"10-bit/8-bit ratio should be ~4.0, got %.2f", ratio)
}

// ==========================================================================
// 11. TestTransition10bit_BlendOutputRange — All output samples stay within
//     valid 10-bit range [0, 1023].
// ==========================================================================

func TestTransition10bit_BlendOutputRange(t *testing.T) {
	t.Parallel()

	w, h := 32, 32 // small frame for exhaustive checking

	testCases := []struct {
		name string
		aY   uint16
		bY   uint16
	}{
		{"min_to_max", 0, 1023},
		{"max_to_min", 1023, 0},
		{"black_to_white", 64, 940},
		{"white_to_black", 940, 64},
		{"mid_values", 300, 700},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			frameA := generateSolid10(w, h, tc.aY, 512, 512)
			frameB := generateSolid10(w, h, tc.bY, 512, 512)
			dst := make([]byte, frameSize10(w, h))

			// Test at multiple positions.
			for _, pos := range []int{0, 64, 128, 192, 256} {
				err := transition.BlendMix10(dst, frameA, frameB, w, h, pos)
				require.NoError(t, err)

				// Verify all Y samples are in [0, 1023].
				for i := 0; i < w*h; i++ {
					val := binary.LittleEndian.Uint16(dst[i*2:])
					require.LessOrEqual(t, val, uint16(1023),
						"Y[%d] = %d exceeds 10-bit max at pos=%d", i, val, pos)
				}
			}
		})
	}
}

// ==========================================================================
// 12. TestTransition10bit_DeterministicBlend — Repeated blending of the
//     same inputs produces identical output.
// ==========================================================================

func TestTransition10bit_DeterministicBlend(t *testing.T) {
	t.Parallel()

	w, h := 64, 48
	frameA := generateSolid10(w, h, 300, 400, 600)
	frameB := generateSolid10(w, h, 700, 200, 800)

	dst1 := make([]byte, frameSize10(w, h))
	dst2 := make([]byte, frameSize10(w, h))

	for _, pos := range []int{0, 64, 128, 192, 256} {
		err := transition.BlendMix10(dst1, frameA, frameB, w, h, pos)
		require.NoError(t, err)

		err = transition.BlendMix10(dst2, frameA, frameB, w, h, pos)
		require.NoError(t, err)

		require.Equal(t, dst1, dst2,
			"BlendMix10 at pos=%d should be deterministic", pos)
	}
}

// ==========================================================================
// 13. TestTransition10bit_BlendStinger — Stinger overlay with alpha on 10-bit.
// ==========================================================================

func TestTransition10bit_BlendStinger(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	base := generateSolid10(w, h, 200, 512, 512)
	overlay := generateSolid10(w, h, 800, 512, 512)
	dst := make([]byte, frameSize10(w, h))

	// Alpha = 0 everywhere: base should show through.
	alphaZero := make([]byte, w*h)
	err := transition.BlendStinger10(dst, base, overlay, w, h, alphaZero)
	require.NoError(t, err)

	y := readY10(dst, w, 0, 0)
	require.InDelta(t, 200, int(y), 1,
		"stinger with alpha=0: should show base Y=200, got %d", y)

	// Alpha = 255 everywhere: overlay should show through.
	alpha255 := make([]byte, w*h)
	for i := range alpha255 {
		alpha255[i] = 255
	}
	err = transition.BlendStinger10(dst, base, overlay, w, h, alpha255)
	require.NoError(t, err)

	y = readY10(dst, w, 0, 0)
	// alpha=255: result = (200*(256-255) + 800*255 + 128)>>8 = (200+204000+128)>>8 = 798
	require.InDelta(t, 800, int(y), 2,
		"stinger with alpha=255: should show overlay Y~800, got %d", y)
}
