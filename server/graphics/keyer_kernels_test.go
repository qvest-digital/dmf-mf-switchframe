package graphics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChromaKeyMaskChroma_AllTransparent(t *testing.T) {
	t.Parallel()
	// All chroma pixels match key color exactly → all mask values 0.
	n := 64
	cb := make([]byte, n)
	cr := make([]byte, n)
	mask := make([]byte, n)

	keyCb, keyCr := 30, 12
	for i := range cb {
		cb[i] = byte(keyCb)
		cr[i] = byte(keyCr)
	}

	// simThreshSq = 100 (much larger than distance 0), invRange doesn't matter
	chromaKeyMaskChroma(&mask[0], &cb[0], &cr[0], keyCb, keyCr, 100, 200, 0, n)

	for i, v := range mask {
		require.Equal(t, byte(0), v, "pixel %d: expected 0 (transparent), got %d", i, v)
	}
}

func TestChromaKeyMaskChroma_AllOpaque(t *testing.T) {
	t.Parallel()
	// All chroma pixels far from key → all mask values 255.
	n := 64
	cb := make([]byte, n)
	cr := make([]byte, n)
	mask := make([]byte, n)

	keyCb, keyCr := 30, 12
	for i := range cb {
		cb[i] = 200 // far from keyCb=30
		cr[i] = 180 // far from keyCr=12
	}

	// simThreshSq and totalThreshSq are small, so all pixels are opaque
	chromaKeyMaskChroma(&mask[0], &cb[0], &cr[0], keyCb, keyCr, 100, 200, 0, n)

	for i, v := range mask {
		require.Equal(t, byte(255), v, "pixel %d: expected 255 (opaque), got %d", i, v)
	}
}

func TestChromaKeyMaskChroma_Smoothness(t *testing.T) {
	t.Parallel()
	// Test feathering zone produces intermediate values.
	n := 32
	cb := make([]byte, n)
	cr := make([]byte, n)
	mask := make([]byte, n)

	keyCb, keyCr := 128, 128

	// Place pixels at varying distances from key.
	// distSq = dCb^2 + dCr^2
	for i := 0; i < n; i++ {
		// dCb varies from 0 to 31, dCr = 0
		// distSq = i*i, ranging from 0 to 961
		cb[i] = byte(128 + i)
		cr[i] = 128
	}

	simThreshSq := 100   // distances < 10 are transparent
	totalThreshSq := 900 // distances >= 30 are opaque
	rangeSq := totalThreshSq - simThreshSq
	invRange := 255 * 65536 / rangeSq

	chromaKeyMaskChroma(&mask[0], &cb[0], &cr[0], keyCb, keyCr, simThreshSq, totalThreshSq, invRange, n)

	// Pixels with distSq < 100 (i < 10) → 0
	for i := 0; i < 10; i++ {
		require.Equal(t, byte(0), mask[i], "pixel %d (distSq=%d) expected transparent", i, i*i)
	}
	// Pixels with distSq >= 900 (i >= 30) → 255
	for i := 30; i < n; i++ {
		require.Equal(t, byte(255), mask[i], "pixel %d (distSq=%d) expected opaque", i, i*i)
	}
	// Pixels in smoothness zone (10 <= i < 30) → intermediate
	hasIntermediate := false
	for i := 10; i < 30; i++ {
		if mask[i] > 0 && mask[i] < 255 {
			hasIntermediate = true
			break
		}
	}
	require.True(t, hasIntermediate, "smoothness zone should produce intermediate alpha values")
}

func TestChromaKeyMaskChroma_CrossValidation(t *testing.T) {
	t.Parallel()
	// Generate a test frame, run old float-based chroma key and new integer kernel,
	// compare masks with tolerance.
	width, height := 64, 64
	ySize := width * height
	uvWidth := width / 2
	uvHeight := height / 2
	uvSize := uvWidth * uvHeight

	frame := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		frame[i] = 128 // Y
	}

	// Fill chroma planes with a gradient
	for row := 0; row < uvHeight; row++ {
		for col := 0; col < uvWidth; col++ {
			idx := row*uvWidth + col
			frame[ySize+idx] = byte((col * 8) % 256)        // Cb
			frame[ySize+uvSize+idx] = byte((row * 8) % 256) // Cr
		}
	}

	keyColor := YCbCr{Y: 128, Cb: 30, Cr: 12}
	similarity := float32(0.2)
	smoothness := float32(0.15)

	// Run old implementation (on a copy since it may modify spill)
	frameCopy := make([]byte, len(frame))
	copy(frameCopy, frame)
	oldMask := ChromaKeyWithSpillColor(frameCopy, width, height, keyColor, similarity, smoothness, 0, 128, 128)

	// Run new kernel at chroma resolution
	simDist := similarity * 181.0
	smoothDist := smoothness * 181.0
	simDistSqF := simDist * simDist
	totalDist := simDist + smoothDist
	totalDistSqF := totalDist * totalDist

	simThreshSq := int(simDistSqF)
	totalThreshSq := int(totalDistSqF)
	rangeSq := totalThreshSq - simThreshSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	chromaMask := make([]byte, uvSize)
	chromaKeyMaskChroma(&chromaMask[0], &frame[ySize], &frame[ySize+uvSize],
		int(keyColor.Cb), int(keyColor.Cr), simThreshSq, totalThreshSq, invRange, uvSize)

	// Expand chroma mask to luma resolution
	newMask := make([]byte, ySize)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			uvIdx := (row/2)*uvWidth + (col / 2)
			newMask[row*width+col] = chromaMask[uvIdx]
		}
	}

	// Compare with tolerance (integer vs float rounding differences)
	maxDiff := 0
	for i := 0; i < ySize; i++ {
		diff := int(oldMask[i]) - int(newMask[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
	}

	// The integer fixed-point kernel uses (distSq - simDistSq) * invRange >> 16
	// while the float path uses (distSq - simDistSq) / (totalDistSq - simDistSq) * 255.
	// Both compute on squared distances. Allow tolerance for rounding.
	require.LessOrEqual(t, maxDiff, 3,
		"cross-validation: max difference between old and new mask is %d (expected <= 3)", maxDiff)
}

func TestChromaKeyMaskChroma_OddSizes(t *testing.T) {
	t.Parallel()
	sizes := []int{1, 3, 7, 15, 17, 33, 63}

	for _, n := range sizes {
		t.Run("", func(t *testing.T) {
			cb := make([]byte, n)
			cr := make([]byte, n)
			mask := make([]byte, n)

			keyCb, keyCr := 100, 100
			for i := range cb {
				cb[i] = byte((100 + i*3) % 256)
				cr[i] = byte((100 + i*5) % 256)
			}

			chromaKeyMaskChroma(&mask[0], &cb[0], &cr[0], keyCb, keyCr, 50, 500, 255*65536/450, n)

			// Just verify no crash and all bytes are in valid range (always true for byte)
			for i := 0; i < n; i++ {
				_ = mask[i] // access to verify no out-of-bounds
			}
		})
	}
}

func TestChromaKeyMaskChroma_ZeroN(t *testing.T) {
	t.Parallel()
	// n=0 should not crash
	chromaKeyMaskChroma(nil, nil, nil, 0, 0, 0, 0, 0, 0)
}

func TestChromaKeyMaskChroma_NoSmoothZone(t *testing.T) {
	t.Parallel()
	// When simThreshSq == totalThreshSq, no smooth zone exists.
	// All pixels are either transparent or opaque.
	n := 16
	cb := make([]byte, n)
	cr := make([]byte, n)
	mask := make([]byte, n)

	keyCb, keyCr := 128, 128
	thresh := 100

	// Half pixels close (distance 0), half far (distance 200+200=141)
	for i := 0; i < n; i++ {
		if i < n/2 {
			cb[i] = 128 // exact match
			cr[i] = 128
		} else {
			cb[i] = 200 // far
			cr[i] = 200
		}
	}

	chromaKeyMaskChroma(&mask[0], &cb[0], &cr[0], keyCb, keyCr, thresh, thresh, 0, n)

	for i := 0; i < n/2; i++ {
		require.Equal(t, byte(0), mask[i], "close pixel %d should be transparent", i)
	}
	for i := n / 2; i < n; i++ {
		require.Equal(t, byte(255), mask[i], "far pixel %d should be opaque", i)
	}
}

func TestChromaKeyMaskChroma_MonotonicSmoothing(t *testing.T) {
	t.Parallel()
	// Verify that mask values increase monotonically with distance.
	n := 100
	cb := make([]byte, n)
	cr := make([]byte, n)
	mask := make([]byte, n)

	keyCb := 128
	keyCr := 128

	// Place pixels at increasing distances (dCb = i, dCr = 0)
	for i := 0; i < n; i++ {
		if 128+i > 255 {
			cb[i] = 255
		} else {
			cb[i] = byte(128 + i)
		}
		cr[i] = 128
	}

	simThreshSq := 100    // distance < 10
	totalThreshSq := 2500 // distance < 50
	rangeSq := totalThreshSq - simThreshSq
	invRange := 255 * 65536 / rangeSq

	chromaKeyMaskChroma(&mask[0], &cb[0], &cr[0], keyCb, keyCr, simThreshSq, totalThreshSq, invRange, n)

	// Verify monotonic increase (non-decreasing)
	for i := 1; i < n; i++ {
		if int(cb[i])-keyCb > int(cb[i-1])-keyCb {
			require.GreaterOrEqual(t, mask[i], mask[i-1],
				"mask should be monotonically non-decreasing at pixel %d: mask[%d]=%d > mask[%d]=%d",
				i, i-1, mask[i-1], i, mask[i])
		}
	}
}

func TestChromaKeyMaskChroma_InvRangeAccuracy(t *testing.T) {
	t.Parallel()
	// Verify the fixed-point invRange calculation is accurate.
	simThreshSq := 1000
	totalThreshSq := 5000
	rangeSq := totalThreshSq - simThreshSq
	invRange := 255 * 65536 / rangeSq

	// Test a few distSq values in the smooth zone.
	testCases := []struct {
		distSq  int
		wantMin int
		wantMax int
	}{
		{simThreshSq, 0, 1},
		{(simThreshSq + totalThreshSq) / 2, 126, 129}, // midpoint ≈ 127.5
		{totalThreshSq - 1, 253, 255},
	}

	for _, tc := range testCases {
		got := (tc.distSq - simThreshSq) * invRange >> 16
		wantFloat := float64(tc.distSq-simThreshSq) / float64(rangeSq) * 255.0

		require.GreaterOrEqual(t, got, tc.wantMin,
			"distSq=%d: got %d, want >= %d (float: %.2f)", tc.distSq, got, tc.wantMin, wantFloat)
		require.LessOrEqual(t, got, tc.wantMax,
			"distSq=%d: got %d, want <= %d (float: %.2f)", tc.distSq, got, tc.wantMax, wantFloat)
	}
}

// BenchmarkChromaKeyMaskChroma_1080p benchmarks the kernel at 1080p chroma resolution.
// 1920x1080 → chroma 960x540 = 518400 chroma pixels.
func BenchmarkChromaKeyMaskChroma_1080p(b *testing.B) {
	n := 960 * 540 // 518400 chroma pixels
	cb := make([]byte, n)
	cr := make([]byte, n)
	mask := make([]byte, n)

	// Fill with semi-realistic data: mostly non-key pixels with some green
	keyCb, keyCr := 30, 12
	for i := range cb {
		if i%20 == 0 {
			// 5% green-ish pixels
			cb[i] = byte(keyCb + i%5)
			cr[i] = byte(keyCr + i%5)
		} else {
			cb[i] = byte(100 + i%80)
			cr[i] = byte(100 + i%80)
		}
	}

	// Typical green screen parameters: similarity=0.3, smoothness=0.1
	simDist := float32(0.3) * 181.0
	smoothDist := float32(0.1) * 181.0
	simDistSq := int(simDist * simDist)
	totalDist := simDist + smoothDist
	totalDistSq := int(totalDist * totalDist)
	rangeSq := totalDistSq - simDistSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		chromaKeyMaskChroma(&mask[0], &cb[0], &cr[0], keyCb, keyCr, simDistSq, totalDistSq, invRange, n)
	}
}

// BenchmarkChromaKeyOld_1080p benchmarks the original ChromaKeyWithSpillColor for comparison.
func BenchmarkChromaKeyOld_1080p(b *testing.B) {
	width, height := 1920, 1080
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	frame := make([]byte, ySize+2*uvSize)

	// Fill with semi-realistic data
	for i := 0; i < ySize; i++ {
		frame[i] = 128
	}
	for i := 0; i < uvSize; i++ {
		if i%20 == 0 {
			frame[ySize+i] = 35
			frame[ySize+uvSize+i] = 15
		} else {
			frame[ySize+i] = byte(100 + i%80)
			frame[ySize+uvSize+i] = byte(100 + i%80)
		}
	}

	keyColor := YCbCr{Y: 182, Cb: 30, Cr: 12}

	b.SetBytes(int64(ySize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ChromaKeyWithSpillColor(frame, width, height, keyColor, 0.3, 0.1, 0, 128, 128)
	}
}

// BenchmarkChromaKeyNew_1080p benchmarks the new kernel-based approach for comparison.
func BenchmarkChromaKeyNew_1080p(b *testing.B) {
	width, height := 1920, 1080
	ySize := width * height
	uvWidth := width / 2
	uvHeight := height / 2
	uvSize := uvWidth * uvHeight
	frame := make([]byte, ySize+2*uvSize)

	// Fill with semi-realistic data
	for i := 0; i < ySize; i++ {
		frame[i] = 128
	}
	for i := 0; i < uvSize; i++ {
		if i%20 == 0 {
			frame[ySize+i] = 35
			frame[ySize+uvSize+i] = 15
		} else {
			frame[ySize+i] = byte(100 + i%80)
			frame[ySize+uvSize+i] = byte(100 + i%80)
		}
	}

	keyCb, keyCr := 30, 12
	similarity := float32(0.3)
	smoothness := float32(0.1)
	simDist := similarity * 181.0
	smoothDist := smoothness * 181.0
	simDistSqF := simDist * simDist
	totalDist := simDist + smoothDist
	totalDistSqF := totalDist * totalDist
	simThreshSq := int(simDistSqF)
	totalThreshSq := int(totalDistSqF)
	rangeSq := totalThreshSq - simThreshSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	chromaMask := make([]byte, uvSize)
	mask := make([]byte, ySize)

	b.SetBytes(int64(ySize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		chromaKeyMaskChroma(&chromaMask[0], &frame[ySize], &frame[ySize+uvSize],
			keyCb, keyCr, simThreshSq, totalThreshSq, invRange, uvSize)

		// Expand chroma mask to luma resolution
		for row := 0; row < height; row++ {
			for col := 0; col < width; col++ {
				uvIdx := (row/2)*uvWidth + (col / 2)
				mask[row*width+col] = chromaMask[uvIdx]
			}
		}
	}
}
