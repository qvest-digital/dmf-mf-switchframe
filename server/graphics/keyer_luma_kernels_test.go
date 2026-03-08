package graphics

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLumaKeyMaskLUT_Basic(t *testing.T) {
	t.Parallel()

	// Build a simple LUT: 0-63 -> 0, 64-191 -> 128, 192-255 -> 255
	var lut [256]byte
	for i := 0; i < 256; i++ {
		switch {
		case i < 64:
			lut[i] = 0
		case i < 192:
			lut[i] = 128
		default:
			lut[i] = 255
		}
	}

	yPlane := []byte{0, 32, 63, 64, 127, 191, 192, 255}
	mask := make([]byte, len(yPlane))

	lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], len(yPlane))

	expected := []byte{0, 0, 0, 128, 128, 128, 255, 255}
	assert.Equal(t, expected, mask)
}

func TestLumaKeyMaskLUT_Identity(t *testing.T) {
	t.Parallel()

	// Identity LUT: lut[i] = i
	var lut [256]byte
	for i := 0; i < 256; i++ {
		lut[i] = byte(i)
	}

	n := 256
	yPlane := make([]byte, n)
	for i := 0; i < n; i++ {
		yPlane[i] = byte(i)
	}
	mask := make([]byte, n)

	lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], n)

	assert.Equal(t, yPlane, mask)
}

func TestLumaKeyMaskLUT_AllZero(t *testing.T) {
	t.Parallel()

	// LUT all zeros
	var lut [256]byte // zero-initialized

	n := 128
	yPlane := make([]byte, n)
	for i := 0; i < n; i++ {
		yPlane[i] = byte(i * 2) // various Y values
	}
	mask := make([]byte, n)
	// Pre-fill mask with non-zero to verify it gets overwritten
	for i := range mask {
		mask[i] = 0xFF
	}

	lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], n)

	for i, v := range mask {
		assert.Equal(t, byte(0), v, "pixel %d should be 0", i)
	}
}

func TestLumaKeyMaskLUT_AllMax(t *testing.T) {
	t.Parallel()

	// LUT all 255
	var lut [256]byte
	for i := range lut {
		lut[i] = 255
	}

	n := 128
	yPlane := make([]byte, n)
	for i := 0; i < n; i++ {
		yPlane[i] = byte(i * 2)
	}
	mask := make([]byte, n)

	lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], n)

	for i, v := range mask {
		assert.Equal(t, byte(255), v, "pixel %d should be 255", i)
	}
}

func TestLumaKeyMaskLUT_CrossValidation(t *testing.T) {
	t.Parallel()

	// Build a 1080p frame with random Y values
	w, h := 1920, 1080
	pixelCount := w * h
	rng := rand.New(rand.NewSource(42))

	yPlane := make([]byte, pixelCount)
	for i := range yPlane {
		yPlane[i] = byte(rng.Intn(256))
	}

	// Full YUV420 frame (only Y plane used by LumaKey)
	uvSize := (w / 2) * (h / 2)
	frame := make([]byte, pixelCount+2*uvSize)
	copy(frame, yPlane)
	for i := pixelCount; i < len(frame); i++ {
		frame[i] = 128
	}

	lowClip := float32(0.2)
	highClip := float32(0.8)
	softness := float32(0.1)

	// Reference: build LUT the same way LumaKey does
	var lut [256]byte
	for y := 0; y < 256; y++ {
		luma := float32(y) / 255.0
		var alpha float32
		if luma < lowClip {
			if softness > 0 && luma > lowClip-softness {
				alpha = (lowClip - luma) / softness
				alpha = 1.0 - alpha
			} else {
				alpha = 0.0
			}
		} else if luma > highClip {
			if softness > 0 && luma < highClip+softness {
				alpha = (luma - highClip) / softness
				alpha = 1.0 - alpha
			} else {
				alpha = 0.0
			}
		} else {
			alpha = 1.0
		}
		lut[y] = uint8(clampFloat(alpha*255.0, 0, 255))
	}

	// Get assembly result via LumaKey
	maskNew := LumaKey(frame, w, h, lowClip, highClip, softness)

	// Build reference mask using LUT directly
	maskRef := make([]byte, pixelCount)
	for i := 0; i < pixelCount; i++ {
		maskRef[i] = lut[yPlane[i]]
	}

	// Compare with exact match (LUT is deterministic, no tolerance needed)
	require.Len(t, maskNew, pixelCount)
	for i := 0; i < pixelCount; i++ {
		if maskNew[i] != maskRef[i] {
			t.Fatalf("pixel %d: assembly=%d reference=%d (Y=%d)", i, maskNew[i], maskRef[i], yPlane[i])
		}
	}
}

func TestLumaKeyMaskLUT_OddSizes(t *testing.T) {
	t.Parallel()

	// Identity LUT for verification
	var lut [256]byte
	for i := 0; i < 256; i++ {
		lut[i] = byte(i)
	}

	sizes := []int{1, 3, 7, 15, 17, 31, 33, 63, 65, 100}
	for _, n := range sizes {
		t.Run("", func(t *testing.T) {
			yPlane := make([]byte, n)
			for i := 0; i < n; i++ {
				yPlane[i] = byte(i % 256)
			}
			mask := make([]byte, n)

			lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], n)

			for i := 0; i < n; i++ {
				assert.Equal(t, yPlane[i], mask[i], "size=%d pixel=%d", n, i)
			}
		})
	}
}

func TestLumaKeyMaskLUT_NZero(t *testing.T) {
	t.Parallel()

	// n=0 should not crash
	var lut [256]byte
	yPlane := []byte{42}
	mask := []byte{99}

	lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], 0)

	// mask should be unchanged
	assert.Equal(t, byte(99), mask[0])
}

func BenchmarkLumaKeyMaskLUT_1080p(b *testing.B) {
	w, h := 1920, 1080
	n := w * h

	// Build a realistic LUT
	var lut [256]byte
	lowClip := float32(0.2)
	highClip := float32(0.8)
	softness := float32(0.1)
	for y := 0; y < 256; y++ {
		luma := float32(y) / 255.0
		var alpha float32
		if luma < lowClip {
			if softness > 0 && luma > lowClip-softness {
				alpha = (lowClip - luma) / softness
				alpha = 1.0 - alpha
			} else {
				alpha = 0.0
			}
		} else if luma > highClip {
			if softness > 0 && luma < highClip+softness {
				alpha = (luma - highClip) / softness
				alpha = 1.0 - alpha
			} else {
				alpha = 0.0
			}
		} else {
			alpha = 1.0
		}
		lut[y] = uint8(clampFloat(alpha*255.0, 0, 255))
	}

	yPlane := make([]byte, n)
	rng := rand.New(rand.NewSource(42))
	for i := range yPlane {
		yPlane[i] = byte(rng.Intn(256))
	}
	mask := make([]byte, n)

	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], n)
	}
}

func BenchmarkLumaKey_1080p(b *testing.B) {
	w, h := 1920, 1080
	pixelCount := w * h
	uvSize := (w / 2) * (h / 2)

	frame := make([]byte, pixelCount+2*uvSize)
	rng := rand.New(rand.NewSource(42))
	for i := range frame[:pixelCount] {
		frame[i] = byte(rng.Intn(256))
	}
	for i := pixelCount; i < len(frame); i++ {
		frame[i] = 128
	}

	b.SetBytes(int64(pixelCount))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = LumaKey(frame, w, h, 0.2, 0.8, 0.1)
	}
}
