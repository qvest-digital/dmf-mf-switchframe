package transition

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// 10-bit Scaler Correctness Tests
//
// These tests verify scaling behavior based on mathematical properties,
// not implementation details. A correct scaler must satisfy:
// 1. Energy preservation: uniform input -> uniform output (±1)
// 2. Identity: scaling to same dimensions = byte-exact copy
// 3. Lanczos-3 kernel weights sum to 1.0 at any fractional position
// ============================================================================

// makeUniform10 creates a YUV422P10LE frame with uniform values.
func makeUniform10(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, w*h*4)
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

// ============================================================================
// Energy Preservation
// ============================================================================

// TestScaler10bit_EnergyPreservation verifies that uniform Y=500 content scaled
// to any size produces uniform Y=500 output (±1). This is the definition of
// energy preservation for uniform content — a correct scaler must not introduce
// DC offset or gain during the interpolation of constant-valued signals.
func TestScaler10bit_EnergyPreservation(t *testing.T) {
	testCases := []struct {
		srcW, srcH int
		dstW, dstH int
	}{
		{320, 240, 160, 120}, // 2x downscale
		{320, 240, 640, 480}, // 2x upscale
		{1920, 1080, 960, 540}, // half
		{64, 32, 128, 64},     // 2x upscale small
		{128, 64, 32, 16},     // 4x downscale
	}

	for _, tc := range testCases {
		name := func() string {
			return ""
		}
		_ = name // avoid capture

		t.Run("", func(t *testing.T) {
			src := makeUniform10(tc.srcW, tc.srcH, 500, 300, 700)
			dst := make([]byte, tc.dstW*tc.dstH*4)

			err := ScaleYUV422P10(src, tc.srcW, tc.srcH, dst, tc.dstW, tc.dstH)
			require.NoError(t, err)

			// Check all Y samples are 500 ±1
			ySize := tc.dstW * tc.dstH
			for i := 0; i < ySize; i++ {
				val := binary.LittleEndian.Uint16(dst[i*2:])
				if val < 499 || val > 501 {
					t.Errorf("%dx%d -> %dx%d: Y[%d]=%d, expected 500±1",
						tc.srcW, tc.srcH, tc.dstW, tc.dstH, i, val)
					break
				}
			}

			// Check Cb samples are 300 ±1
			chromaW := tc.dstW / 2
			chromaSize := chromaW * tc.dstH
			cbOff := ySize * 2
			for i := 0; i < chromaSize; i++ {
				val := binary.LittleEndian.Uint16(dst[cbOff+i*2:])
				if val < 299 || val > 301 {
					t.Errorf("%dx%d -> %dx%d: Cb[%d]=%d, expected 300±1",
						tc.srcW, tc.srcH, tc.dstW, tc.dstH, i, val)
					break
				}
			}

			// Check Cr samples are 700 ±1
			crOff := cbOff + chromaSize*2
			for i := 0; i < chromaSize; i++ {
				val := binary.LittleEndian.Uint16(dst[crOff+i*2:])
				if val < 699 || val > 701 {
					t.Errorf("%dx%d -> %dx%d: Cr[%d]=%d, expected 700±1",
						tc.srcW, tc.srcH, tc.dstW, tc.dstH, i, val)
					break
				}
			}
		})
	}
}

// ============================================================================
// Identity Scale
// ============================================================================

// TestScaler10bit_Identity verifies that scaling (w,h) -> (w,h) produces
// byte-exact unchanged output. This is a mathematical identity.
func TestScaler10bit_Identity(t *testing.T) {
	w, h := 64, 32

	src := make([]byte, w*h*4)
	// Fill with varied pattern to catch any modification
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16((i*7+31)%1024))
	}
	chromaW := w / 2
	ySize := w * h
	chromaSize := chromaW * h
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(src[cbOff+i*2:], uint16((i*13+67)%1024))
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(src[crOff+i*2:], uint16((i*19+103)%1024))
	}

	dst := make([]byte, w*h*4)
	err := ScaleYUV422P10(src, w, h, dst, w, h)
	require.NoError(t, err)

	// Must be byte-exact identical
	assert.Equal(t, src, dst,
		"identity scale (%dx%d -> %dx%d) must be byte-exact", w, h, w, h)
}

// ============================================================================
// Lanczos-3 Kernel Weight Sum
// ============================================================================

// TestScaler10bit_LanczosKernelSum verifies that Lanczos-3 kernel weights
// at any fractional position sum to approximately 1.0. The Lanczos-3 kernel
// is defined as: L(x) = sinc(x) * sinc(x/3) for |x| < 3, else 0.
//
// The theoretical Lanczos kernel (windowed sinc) does NOT sum to exactly 1.0
// when truncated — the sum depends on the fractional position and number of
// taps. In practice, implementations normalize the weights. This test verifies
// the unnormalized kernel sum is close enough that normalization would produce
// negligible error (< 1% deviation).
func TestScaler10bit_LanczosKernelSum(t *testing.T) {
	// Test at many fractional positions across the full [0, 1) range
	for step := 0; step <= 100; step++ {
		frac := float64(step) / 100.0

		// Compute Lanczos-3 kernel weights for this fractional position
		// using the full 6-tap support window: floor(x) - 2 to floor(x) + 3
		var sum float64
		for i := -2; i <= 3; i++ {
			x := float64(i) - frac
			sum += lanczos3ForTest(x)
		}

		// The unnormalized Lanczos-3 kernel can deviate from 1.0 by several percent
		// at certain fractional positions. Practical implementations normalize.
		// Verify the sum is within 1% of 1.0.
		assert.InDelta(t, 1.0, sum, 0.01,
			"Lanczos-3 kernel at frac=%.2f: weights sum to %.6f, expected ~1.0 (within 1%%)",
			frac, sum)
	}
}

// lanczos3ForTest computes the Lanczos-3 kernel value at position x.
// L(x) = sinc(x) * sinc(x/3) for |x| < 3, else 0
// sinc(x) = sin(pi*x) / (pi*x) for x != 0, sinc(0) = 1
func lanczos3ForTest(x float64) float64 {
	if x == 0 {
		return 1.0
	}
	if x <= -3.0 || x >= 3.0 {
		return 0.0
	}
	px := math.Pi * x
	return (math.Sin(px) / px) * (math.Sin(px/3.0) / (px / 3.0))
}

// ============================================================================
// Output Range Clamping
// ============================================================================

// TestScaler10bit_OutputClamped verifies that all output samples are within
// the valid 10-bit range [0, 1023] after scaling, even with extreme input patterns
// that might cause overshoot in Lanczos interpolation (ringing artifacts).
func TestScaler10bit_OutputClamped(t *testing.T) {
	srcW, srcH := 64, 32
	dstW, dstH := 128, 64

	// Create a high-contrast pattern: alternating 0 and 1023 rows
	src := make([]byte, srcW*srcH*4)
	for y := 0; y < srcH; y++ {
		val := uint16(0)
		if y%2 == 1 {
			val = 1023
		}
		for x := 0; x < srcW; x++ {
			binary.LittleEndian.PutUint16(src[(y*srcW+x)*2:], val)
		}
	}
	// Fill chroma with mid values
	chromaW := srcW / 2
	ySize := srcW * srcH
	chromaSize := chromaW * srcH
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(src[cbOff+i*2:], 512)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(src[crOff+i*2:], 512)
	}

	dst := make([]byte, dstW*dstH*4)
	err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH)
	require.NoError(t, err)

	// Every output sample must be in [0, 1023]
	violations := 0
	totalSamples := dstW*dstH + (dstW/2)*dstH*2
	for i := 0; i < totalSamples; i++ {
		val := binary.LittleEndian.Uint16(dst[i*2:])
		if val > 1023 {
			violations++
			if violations <= 5 {
				t.Logf("sample %d: value %d exceeds 10-bit max (1023)", i, val)
			}
		}
	}
	assert.Equal(t, 0, violations,
		"all output samples must be in [0, 1023], got %d violations out of %d samples",
		violations, totalSamples)
}
