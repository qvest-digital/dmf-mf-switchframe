//go:build correctness

package stinger

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

// ==========================================================================
// 10-bit YUV422P10LE stinger correctness tests.
//
// These verify stinger overlay blending (BlendStinger10) on 10-bit frames
// with various alpha values.
// ==========================================================================

// generateSolid10 creates a solid-color YUV422P10LE frame.
func generateSolid10(w, h int, y, cb, cr uint16) []byte {
	frameSize := w * h * 4
	buf := make([]byte, frameSize)

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

// readY10 reads the Y sample at luma position (x, y).
func readY10(buf []byte, w, x, y int) uint16 {
	return binary.LittleEndian.Uint16(buf[(y*w+x)*2:])
}

// ==========================================================================
// 1. TestStinger10bit_AlphaZeroShowsBase — alpha=0 shows only the base.
// ==========================================================================

func TestStinger10bit_AlphaZeroShowsBase(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	base := generateSolid10(w, h, 300, 512, 512)
	overlay := generateSolid10(w, h, 900, 512, 512)
	dst := make([]byte, w*h*4)

	// Alpha = 0: base should show through entirely.
	alpha := make([]byte, w*h)
	err := transition.BlendStinger10(dst, base, overlay, w, h, alpha)
	require.NoError(t, err)

	y := readY10(dst, w, 0, 0)
	require.InDelta(t, 300, int(y), 1,
		"stinger alpha=0: Y should be base (300), got %d", y)

	// Spot-check multiple pixels.
	for py := 0; py < h; py += 20 {
		for px := 0; px < w; px += 20 {
			val := readY10(dst, w, px, py)
			require.InDelta(t, 300, int(val), 1,
				"stinger alpha=0 at (%d,%d): Y should be 300, got %d", px, py, val)
		}
	}
}

// ==========================================================================
// 2. TestStinger10bit_AlphaFullShowsOverlay — alpha=255 shows overlay.
// ==========================================================================

func TestStinger10bit_AlphaFullShowsOverlay(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	base := generateSolid10(w, h, 300, 512, 512)
	overlay := generateSolid10(w, h, 900, 512, 512)
	dst := make([]byte, w*h*4)

	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = 255
	}

	err := transition.BlendStinger10(dst, base, overlay, w, h, alpha)
	require.NoError(t, err)

	y := readY10(dst, w, 0, 0)
	// alpha=255: result = (300*(256-255) + 900*255 + 128) >> 8
	// = (300 + 229500 + 128) >> 8 = 229928 >> 8 = 898
	require.InDelta(t, 900, int(y), 3,
		"stinger alpha=255: Y should be ~900 (overlay), got %d", y)
}

// ==========================================================================
// 3. TestStinger10bit_HalfAlphaBlend — alpha=128 produces midpoint blend.
// ==========================================================================

func TestStinger10bit_HalfAlphaBlend(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	base := generateSolid10(w, h, 200, 512, 512)
	overlay := generateSolid10(w, h, 800, 512, 512)
	dst := make([]byte, w*h*4)

	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = 128
	}

	err := transition.BlendStinger10(dst, base, overlay, w, h, alpha)
	require.NoError(t, err)

	y := readY10(dst, w, 0, 0)
	// alpha=128: result = (200*(256-128) + 800*128 + 128) >> 8
	// = (200*128 + 800*128 + 128) >> 8 = (25600 + 102400 + 128) >> 8 = 500
	require.InDelta(t, 500, int(y), 2,
		"stinger alpha=128: Y should be ~500, got %d", y)
}

// ==========================================================================
// 4. TestStinger10bit_SplitAlphaMask — Left half transparent, right opaque.
// ==========================================================================

func TestStinger10bit_SplitAlphaMask(t *testing.T) {
	t.Parallel()

	w, h := 64, 48
	base := generateSolid10(w, h, 200, 512, 512)
	overlay := generateSolid10(w, h, 800, 300, 700)
	dst := make([]byte, w*h*4)

	// Left half alpha=0 (base), right half alpha=255 (overlay).
	alpha := make([]byte, w*h)
	for py := 0; py < h; py++ {
		for px := w / 2; px < w; px++ {
			alpha[py*w+px] = 255
		}
	}

	err := transition.BlendStinger10(dst, base, overlay, w, h, alpha)
	require.NoError(t, err)

	// Left side: base visible.
	yLeft := readY10(dst, w, 0, 0)
	require.InDelta(t, 200, int(yLeft), 1,
		"left (alpha=0): Y should be base 200, got %d", yLeft)

	// Right side: overlay visible.
	yRight := readY10(dst, w, w-1, 0)
	require.InDelta(t, 800, int(yRight), 3,
		"right (alpha=255): Y should be ~800 (overlay), got %d", yRight)
}

// ==========================================================================
// 5. TestStinger10bit_ChromaBlend — Chroma values blend correctly.
// ==========================================================================

func TestStinger10bit_ChromaBlend(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	base := generateSolid10(w, h, 500, 200, 300)
	overlay := generateSolid10(w, h, 500, 800, 700)
	dst := make([]byte, w*h*4)

	// Half alpha.
	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = 128
	}

	err := transition.BlendStinger10(dst, base, overlay, w, h, alpha)
	require.NoError(t, err)

	// Check Cb: blend of 200 and 800 at 50% -> ~500.
	ySize := w * h
	chromaW := w / 2
	cbOff := ySize * 2
	cb := binary.LittleEndian.Uint16(dst[cbOff:])
	require.InDelta(t, 500, int(cb), 5,
		"stinger Cb at alpha=128: should be ~500, got %d", cb)

	// Check Cr: blend of 300 and 700 at 50% -> ~500.
	chromaSize := chromaW * h
	crOff := cbOff + chromaSize*2
	cr := binary.LittleEndian.Uint16(dst[crOff:])
	require.InDelta(t, 500, int(cr), 5,
		"stinger Cr at alpha=128: should be ~500, got %d", cr)
}

// ==========================================================================
// 6. TestStinger10bit_OutputRange — All output values within [0, 1023].
// ==========================================================================

func TestStinger10bit_OutputRange(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	base := generateSolid10(w, h, 0, 0, 0)
	overlay := generateSolid10(w, h, 1023, 1023, 1023)
	dst := make([]byte, w*h*4)

	for _, alphaVal := range []byte{0, 64, 128, 192, 255} {
		alpha := make([]byte, w*h)
		for i := range alpha {
			alpha[i] = alphaVal
		}

		err := transition.BlendStinger10(dst, base, overlay, w, h, alpha)
		require.NoError(t, err)

		// Check all uint16 samples are <= 1023.
		totalSamples := w*h + 2*(w/2)*h
		for i := 0; i < totalSamples; i++ {
			val := binary.LittleEndian.Uint16(dst[i*2:])
			require.LessOrEqual(t, val, uint16(1023),
				"alpha=%d sample[%d]=%d exceeds 10-bit max", alphaVal, i, val)
		}
	}
}

// ==========================================================================
// 7. TestStinger10bit_Determinism — Repeated blending is identical.
// ==========================================================================

func TestStinger10bit_Determinism(t *testing.T) {
	t.Parallel()

	w, h := 64, 48
	base := generateSolid10(w, h, 400, 300, 700)
	overlay := generateSolid10(w, h, 700, 600, 400)

	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = 137
	}

	dst1 := make([]byte, w*h*4)
	dst2 := make([]byte, w*h*4)

	err := transition.BlendStinger10(dst1, base, overlay, w, h, alpha)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		err = transition.BlendStinger10(dst2, base, overlay, w, h, alpha)
		require.NoError(t, err)
		require.Equal(t, dst1, dst2,
			"run %d: stinger blend should be deterministic", i)
	}
}
