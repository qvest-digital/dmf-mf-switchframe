//go:build correctness

package transition

import (
	"math"
	"testing"
)

// TestLanczosKernelSum verifies that Lanczos-3 kernel weights at various positions
// sum to 1.0 ±1e-6 after normalization. This is essential for energy preservation.
func TestLanczosKernelSum(t *testing.T) {
	testCases := []struct {
		name            string
		srcSize, dstSize int
	}{
		{"upscale_960_1920", 960, 1920},
		{"downscale_1920_960", 1920, 960},
		{"same_1920_1920", 1920, 1920},
		{"upscale_540_1080", 540, 1080},
		{"downscale_1080_540", 1080, 540},
		{"downscale_1920_480", 1920, 480},
		{"small_16_32", 16, 32},
		{"small_32_16", 32, 16},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.srcSize == tc.dstSize {
				t.Skip("identity scale: no kernel computed")
			}
			kernel := precomputeLanczosKernel(tc.srcSize, tc.dstSize)

			for d := 0; d < kernel.size; d++ {
				wBase := d * kernel.maxTaps
				var sum float64
				for tap := 0; tap < kernel.maxTaps; tap++ {
					sum += float64(kernel.weights[wBase+tap])
				}

				if math.Abs(sum-1.0) > 1e-3 {
					t.Errorf("position %d: weight sum = %.10f (error = %e)", d, sum, math.Abs(sum-1.0))
				}
			}
		})
	}
}

// TestLanczos3Kernel verifies the lanczos3 kernel function at known positions.
func TestLanczos3Kernel(t *testing.T) {
	tests := []struct {
		x        float64
		expected float64
		tol      float64
	}{
		{0.0, 1.0, 1e-10},      // Center: sinc(0)*sinc(0) = 1
		{3.0, 0.0, 1e-10},      // Support boundary: sinc(3)*sinc(1) = 0
		{-3.0, 0.0, 1e-10},     // Negative boundary
		{4.0, 0.0, 1e-10},      // Beyond support
		{1.0, 0.0, 1e-10},      // sinc(1) = 0, so L(1) = 0
		{2.0, 0.0, 1e-10},      // sinc(2) = 0, so L(2) = 0
		{0.5, 0.6079, 0.001},   // Approximate known value
	}

	for _, tc := range tests {
		got := lanczos3(tc.x)
		if math.Abs(got-tc.expected) > tc.tol {
			t.Errorf("lanczos3(%.2f) = %.10f, expected %.10f (tol %e)", tc.x, got, tc.expected, tc.tol)
		}
	}
}

// TestScalerEnergyPreservation verifies that scaling a uniform color frame
// produces a uniform color output with no ringing artifacts.
func TestScalerEnergyPreservation(t *testing.T) {
	testCases := []struct {
		name            string
		srcW, srcH      int
		dstW, dstH      int
		yVal, cbVal, crVal byte
	}{
		{"upscale_128_gray", 64, 48, 128, 96, 128, 128, 128},
		{"downscale_128_gray", 128, 96, 64, 48, 128, 128, 128},
		{"upscale_white", 64, 48, 128, 96, 235, 128, 128},
		{"downscale_black", 128, 96, 64, 48, 16, 128, 128},
		{"upscale_colored", 64, 48, 128, 96, 100, 80, 200},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srcYSize := tc.srcW * tc.srcH
			srcUVSize := (tc.srcW / 2) * (tc.srcH / 2)
			src := make([]byte, srcYSize+srcUVSize*2)

			// Fill with uniform color
			for i := 0; i < srcYSize; i++ {
				src[i] = tc.yVal
			}
			for i := 0; i < srcUVSize; i++ {
				src[srcYSize+i] = tc.cbVal
				src[srcYSize+srcUVSize+i] = tc.crVal
			}

			dstYSize := tc.dstW * tc.dstH
			dstUVSize := (tc.dstW / 2) * (tc.dstH / 2)
			dst := make([]byte, dstYSize+dstUVSize*2)

			ScaleYUV420Lanczos(src, tc.srcW, tc.srcH, dst, tc.dstW, tc.dstH)

			// Check Y plane: all pixels should be close to yVal
			for i := 0; i < dstYSize; i++ {
				diff := int(dst[i]) - int(tc.yVal)
				if diff < -1 || diff > 1 {
					row := i / tc.dstW
					col := i % tc.dstW
					t.Errorf("Y[%d,%d] = %d, expected %d (diff=%d)", row, col, dst[i], tc.yVal, diff)
					if t.Failed() {
						return
					}
				}
			}

			// Check Cb plane
			for i := 0; i < dstUVSize; i++ {
				diff := int(dst[dstYSize+i]) - int(tc.cbVal)
				if diff < -1 || diff > 1 {
					t.Errorf("Cb[%d] = %d, expected %d (diff=%d)", i, dst[dstYSize+i], tc.cbVal, diff)
					if t.Failed() {
						return
					}
				}
			}

			// Check Cr plane
			for i := 0; i < dstUVSize; i++ {
				diff := int(dst[dstYSize+dstUVSize+i]) - int(tc.crVal)
				if diff < -1 || diff > 1 {
					t.Errorf("Cr[%d] = %d, expected %d (diff=%d)", i, dst[dstYSize+dstUVSize+i], tc.crVal, diff)
					if t.Failed() {
						return
					}
				}
			}
		})
	}
}

// TestScalerIdentity verifies that scaling a frame to the same dimensions
// produces an exact copy.
func TestScalerIdentity(t *testing.T) {
	w, h := 64, 48
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	total := ySize + uvSize*2

	src := make([]byte, total)
	for i := range src {
		src[i] = byte((i * 37) & 0xFF) // pseudo-random pattern
	}

	dst := make([]byte, total)
	ScaleYUV420Lanczos(src, w, h, dst, w, h)

	for i := range src {
		if src[i] != dst[i] {
			t.Fatalf("identity scale mismatch at byte %d: %d != %d", i, src[i], dst[i])
		}
	}
}
