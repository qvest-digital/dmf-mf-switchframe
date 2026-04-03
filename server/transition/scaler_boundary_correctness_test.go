//go:build correctness

package transition

import (
	"math"
	"testing"
)

// generateSolidYUV420 creates a solid-color YUV420p frame.
// Layout: Y plane (w*h) + Cb plane (w/2 * h/2) + Cr plane (w/2 * h/2).
func generateSolidYUV420(w, h int, y, cb, cr byte) []byte {
	chromaW := w / 2
	chromaH := h / 2
	frame := make([]byte, w*h+chromaW*chromaH*2)

	// Y plane
	for i := 0; i < w*h; i++ {
		frame[i] = y
	}

	// Cb plane
	cbStart := w * h
	for i := 0; i < chromaW*chromaH; i++ {
		frame[cbStart+i] = cb
	}

	// Cr plane
	crStart := cbStart + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		frame[crStart+i] = cr
	}

	return frame
}

// avgByte computes the average of a byte slice.
func avgByte(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	var sum uint64
	for _, v := range data {
		sum += uint64(v)
	}
	return float64(sum) / float64(len(data))
}

// ---------------------------------------------------------------------------
// TestScaleYUV420OddDimensions
//
// Test ScaleYUV420 with various dimension pairs including odd values.
// For each: verify output buffer size, verify solid-color preservation.
// ---------------------------------------------------------------------------

func TestScaleYUV420OddDimensions(t *testing.T) {
	type dimPair struct {
		srcW, srcH, dstW, dstH int
		label                  string
	}

	cases := []dimPair{
		{2, 2, 4, 4, "2x2 -> 4x4"},
		{4, 4, 2, 2, "4x4 -> 2x2"},
		{15, 15, 16, 16, "15x15 -> 16x16 (odd->even)"},
		{16, 16, 15, 15, "16x16 -> 15x15 (even->odd)"},
		{319, 239, 320, 240, "319x239 -> 320x240"},
		{320, 240, 319, 239, "320x240 -> 319x239"},
		{1920, 1080, 320, 240, "1920x1080 -> 320x240"},
		{320, 240, 1920, 1080, "320x240 -> 1920x1080"},
	}

	const (
		srcY  byte = 140
		srcCb byte = 80
		srcCr byte = 200
	)

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Logf("MEASURE: scaling %dx%d -> %dx%d", tc.srcW, tc.srcH, tc.dstW, tc.dstH)

			src := generateSolidYUV420(tc.srcW, tc.srcH, srcY, srcCb, srcCr)
			expectedDstSize := tc.dstW * tc.dstH * 3 / 2
			dst := make([]byte, expectedDstSize)

			t.Logf("MEASURE: src buffer size = %d (expected %d)", len(src), tc.srcW*tc.srcH*3/2)
			t.Logf("MEASURE: dst buffer size = %d (expected %d)", len(dst), expectedDstSize)

			// Should not panic.
			var panicked bool
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
						t.Logf("MEASURE: PANIC during scale %s: %v", tc.label, r)
					}
				}()
				ScaleYUV420(src, tc.srcW, tc.srcH, dst, tc.dstW, tc.dstH)
			}()

			if panicked {
				t.Fatalf("ScaleYUV420 panicked for %s", tc.label)
			}

			// Verify output buffer is fully used (correct size).
			dstYSize := tc.dstW * tc.dstH
			dstUVW := tc.dstW / 2
			dstUVH := tc.dstH / 2
			dstUVSize := dstUVW * dstUVH
			actualTotalSize := dstYSize + 2*dstUVSize

			t.Logf("MEASURE: actual planes: Y=%d, UV=%d each, total=%d vs buffer=%d",
				dstYSize, dstUVSize, actualTotalSize, len(dst))

			// For YUV420, integer division means odd dimensions lose a chroma row/col.
			// The expected buffer size should match what we allocated.
			if actualTotalSize > len(dst) {
				t.Fatalf("plane sizes exceed buffer: planes need %d but buffer is %d", actualTotalSize, len(dst))
			}

			// Verify Y plane average.
			yPlane := dst[:dstYSize]
			avgYOut := avgByte(yPlane)
			t.Logf("MEASURE: Y avg = %.2f (expected %d, tolerance +-2)", avgYOut, srcY)
			if math.Abs(avgYOut-float64(srcY)) > 2.0 {
				t.Errorf("Y plane average %.2f deviates from source %d by more than +-2", avgYOut, srcY)
			}

			// Verify Cb plane average.
			if dstUVSize > 0 {
				cbPlane := dst[dstYSize : dstYSize+dstUVSize]
				avgCbOut := avgByte(cbPlane)
				t.Logf("MEASURE: Cb avg = %.2f (expected %d, tolerance +-2)", avgCbOut, srcCb)
				if math.Abs(avgCbOut-float64(srcCb)) > 2.0 {
					t.Errorf("Cb plane average %.2f deviates from source %d by more than +-2", avgCbOut, srcCb)
				}
			}

			// Verify Cr plane average.
			if dstUVSize > 0 {
				crPlane := dst[dstYSize+dstUVSize : dstYSize+2*dstUVSize]
				avgCrOut := avgByte(crPlane)
				t.Logf("MEASURE: Cr avg = %.2f (expected %d, tolerance +-2)", avgCrOut, srcCr)
				if math.Abs(avgCrOut-float64(srcCr)) > 2.0 {
					t.Errorf("Cr plane average %.2f deviates from source %d by more than +-2", avgCrOut, srcCr)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestScaleYUV420ChromaPlanes
//
// Scale a frame with distinct Y=100, Cb=50, Cr=200 from 320x240 to 640x480.
// Verify ALL three planes preserve their values after scaling.
// This catches bugs where chroma planes use wrong stride or offset.
// ---------------------------------------------------------------------------

func TestScaleYUV420ChromaPlanes(t *testing.T) {
	const (
		srcW = 320
		srcH = 240
		dstW = 640
		dstH = 480

		inY  byte = 100
		inCb byte = 50
		inCr byte = 200
	)

	src := generateSolidYUV420(srcW, srcH, inY, inCb, inCr)
	dst := make([]byte, dstW*dstH*3/2)

	t.Logf("MEASURE: scaling %dx%d -> %dx%d with Y=%d, Cb=%d, Cr=%d",
		srcW, srcH, dstW, dstH, inY, inCb, inCr)

	ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)

	dstYSize := dstW * dstH
	dstUVW := dstW / 2
	dstUVH := dstH / 2
	dstUVSize := dstUVW * dstUVH

	// Y plane
	yAvg := avgByte(dst[:dstYSize])
	t.Logf("MEASURE: Y plane avg = %.4f (expected %d)", yAvg, inY)
	if math.Abs(yAvg-float64(inY)) > 2.0 {
		t.Errorf("Y plane: expected avg ~%d, got %.4f", inY, yAvg)
	}

	// Cb plane
	cbStart := dstYSize
	cbAvg := avgByte(dst[cbStart : cbStart+dstUVSize])
	t.Logf("MEASURE: Cb plane avg = %.4f (expected %d)", cbAvg, inCb)
	if math.Abs(cbAvg-float64(inCb)) > 2.0 {
		t.Errorf("Cb plane: expected avg ~%d, got %.4f (WRONG STRIDE?)", inCb, cbAvg)
	}

	// Cr plane
	crStart := cbStart + dstUVSize
	crAvg := avgByte(dst[crStart : crStart+dstUVSize])
	t.Logf("MEASURE: Cr plane avg = %.4f (expected %d)", crAvg, inCr)
	if math.Abs(crAvg-float64(inCr)) > 2.0 {
		t.Errorf("Cr plane: expected avg ~%d, got %.4f (WRONG STRIDE?)", inCr, crAvg)
	}

	// Check for cross-contamination: verify no pixel in Cb plane has value from Cr range.
	cbMin, cbMax := byte(255), byte(0)
	for i := 0; i < dstUVSize; i++ {
		v := dst[cbStart+i]
		if v < cbMin {
			cbMin = v
		}
		if v > cbMax {
			cbMax = v
		}
	}
	t.Logf("MEASURE: Cb range [%d, %d]", cbMin, cbMax)

	crMin, crMax := byte(255), byte(0)
	for i := 0; i < dstUVSize; i++ {
		v := dst[crStart+i]
		if v < crMin {
			crMin = v
		}
		if v > crMax {
			crMax = v
		}
	}
	t.Logf("MEASURE: Cr range [%d, %d]", crMin, crMax)

	// Cb should be near 50, Cr near 200 -- these are far enough apart that
	// any stride confusion would produce wildly wrong values.
	if cbMax > inCb+5 || cbMin < inCb-5 {
		t.Errorf("Cb plane values [%d,%d] deviate significantly from expected %d", cbMin, cbMax, inCb)
	}
	if crMax > inCr+5 || crMin < inCr-5 {
		t.Errorf("Cr plane values [%d,%d] deviate significantly from expected %d", crMin, crMax, inCr)
	}

	// Also test with Lanczos scaler.
	t.Run("Lanczos", func(t *testing.T) {
		dstL := make([]byte, dstW*dstH*3/2)
		ScaleYUV420Lanczos(src, srcW, srcH, dstL, dstW, dstH)

		yAvgL := avgByte(dstL[:dstYSize])
		cbAvgL := avgByte(dstL[cbStart : cbStart+dstUVSize])
		crAvgL := avgByte(dstL[crStart : crStart+dstUVSize])

		t.Logf("MEASURE: Lanczos Y avg = %.4f (expected %d)", yAvgL, inY)
		t.Logf("MEASURE: Lanczos Cb avg = %.4f (expected %d)", cbAvgL, inCb)
		t.Logf("MEASURE: Lanczos Cr avg = %.4f (expected %d)", crAvgL, inCr)

		if math.Abs(yAvgL-float64(inY)) > 2.0 {
			t.Errorf("Lanczos Y: expected ~%d, got %.4f", inY, yAvgL)
		}
		if math.Abs(cbAvgL-float64(inCb)) > 2.0 {
			t.Errorf("Lanczos Cb: expected ~%d, got %.4f", inCb, cbAvgL)
		}
		if math.Abs(crAvgL-float64(inCr)) > 2.0 {
			t.Errorf("Lanczos Cr: expected ~%d, got %.4f", inCr, crAvgL)
		}
	})
}

// ---------------------------------------------------------------------------
// TestScaleYUV420ExtremeRatios
//
// Scale 1920x1080 -> 32x18 (60x downscale) and 32x18 -> 1920x1080 (60x upscale).
// Verify output isn't black or garbage.
// ---------------------------------------------------------------------------

func TestScaleYUV420ExtremeRatios(t *testing.T) {
	const (
		srcY  byte = 160
		srcCb byte = 100
		srcCr byte = 140
	)

	t.Run("60x_downscale", func(t *testing.T) {
		const (
			srcW = 1920
			srcH = 1080
			dstW = 32
			dstH = 18
		)

		t.Logf("MEASURE: downscale %dx%d -> %dx%d (ratio: %.1fx)",
			srcW, srcH, dstW, dstH, float64(srcW)/float64(dstW))

		src := generateSolidYUV420(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*3/2)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("MEASURE: PANIC during extreme downscale: %v", r)
				}
			}()
			ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
		}()

		if panicked {
			t.Fatal("ScaleYUV420 panicked during extreme downscale")
		}

		dstYSize := dstW * dstH
		yAvg := avgByte(dst[:dstYSize])
		t.Logf("MEASURE: downscale Y avg = %.2f (expected ~%d)", yAvg, srcY)

		// Output should not be black (Y~0) or corrupted.
		if yAvg < 5.0 {
			t.Errorf("downscale output appears black: Y avg = %.2f", yAvg)
		}
		if math.Abs(yAvg-float64(srcY)) > 10.0 {
			t.Errorf("downscale Y avg %.2f too far from source %d", yAvg, srcY)
		}
	})

	t.Run("60x_upscale", func(t *testing.T) {
		const (
			srcW = 32
			srcH = 18
			dstW = 1920
			dstH = 1080
		)

		t.Logf("MEASURE: upscale %dx%d -> %dx%d (ratio: %.1fx)",
			srcW, srcH, dstW, dstH, float64(dstW)/float64(srcW))

		src := generateSolidYUV420(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*3/2)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("MEASURE: PANIC during extreme upscale: %v", r)
				}
			}()
			ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
		}()

		if panicked {
			t.Fatal("ScaleYUV420 panicked during extreme upscale")
		}

		dstYSize := dstW * dstH
		yAvg := avgByte(dst[:dstYSize])
		t.Logf("MEASURE: upscale Y avg = %.2f (expected ~%d)", yAvg, srcY)

		// Upscaled solid color should remain solid.
		if math.Abs(yAvg-float64(srcY)) > 5.0 {
			t.Errorf("upscale Y avg %.2f too far from source %d", yAvg, srcY)
		}

		// Check for corruption: count pixels that deviate more than 10 from expected.
		corrupt := 0
		for i := 0; i < dstYSize; i++ {
			if math.Abs(float64(dst[i])-float64(srcY)) > 10 {
				corrupt++
			}
		}
		corruptPct := float64(corrupt) / float64(dstYSize) * 100
		t.Logf("MEASURE: upscale corrupt pixels: %d / %d (%.2f%%)", corrupt, dstYSize, corruptPct)
		if corruptPct > 1.0 {
			t.Errorf("upscale has %.2f%% corrupt pixels (>10 deviation from source)", corruptPct)
		}
	})

	// Also test with Lanczos for extreme ratios.
	t.Run("60x_downscale_lanczos", func(t *testing.T) {
		const (
			srcW = 1920
			srcH = 1080
			dstW = 32
			dstH = 18
		)

		t.Logf("MEASURE: Lanczos downscale %dx%d -> %dx%d", srcW, srcH, dstW, dstH)

		src := generateSolidYUV420(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*3/2)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("MEASURE: PANIC during Lanczos extreme downscale: %v", r)
				}
			}()
			ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
		}()

		if panicked {
			t.Fatal("ScaleYUV420Lanczos panicked during extreme downscale")
		}

		dstYSize := dstW * dstH
		yAvg := avgByte(dst[:dstYSize])
		t.Logf("MEASURE: Lanczos downscale Y avg = %.2f (expected ~%d)", yAvg, srcY)

		if math.Abs(yAvg-float64(srcY)) > 10.0 {
			t.Errorf("Lanczos downscale Y avg %.2f too far from source %d", yAvg, srcY)
		}
	})

	t.Run("60x_upscale_lanczos", func(t *testing.T) {
		const (
			srcW = 32
			srcH = 18
			dstW = 1920
			dstH = 1080
		)

		t.Logf("MEASURE: Lanczos upscale %dx%d -> %dx%d", srcW, srcH, dstW, dstH)

		src := generateSolidYUV420(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*3/2)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("MEASURE: PANIC during Lanczos extreme upscale: %v", r)
				}
			}()
			ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
		}()

		if panicked {
			t.Fatal("ScaleYUV420Lanczos panicked during extreme upscale")
		}

		dstYSize := dstW * dstH
		yAvg := avgByte(dst[:dstYSize])
		t.Logf("MEASURE: Lanczos upscale Y avg = %.2f (expected ~%d)", yAvg, srcY)

		if math.Abs(yAvg-float64(srcY)) > 5.0 {
			t.Errorf("Lanczos upscale Y avg %.2f too far from source %d", yAvg, srcY)
		}
	})
}

// ---------------------------------------------------------------------------
// TestScaleYUV420MinimumDimensions
//
// Scale 2x2 -> 320x240 and 320x240 -> 2x2.
// The smallest valid YUV420 frame is 2x2 (chroma planes are 1x1).
// Verify no crash and correct output size.
// ---------------------------------------------------------------------------

func TestScaleYUV420MinimumDimensions(t *testing.T) {
	const (
		srcY  byte = 180
		srcCb byte = 60
		srcCr byte = 220
	)

	t.Run("2x2_to_320x240", func(t *testing.T) {
		const (
			srcW = 2
			srcH = 2
			dstW = 320
			dstH = 240
		)

		t.Logf("MEASURE: scaling %dx%d -> %dx%d", srcW, srcH, dstW, dstH)

		src := generateSolidYUV420(srcW, srcH, srcY, srcCb, srcCr)
		expectedDstSize := dstW * dstH * 3 / 2
		dst := make([]byte, expectedDstSize)

		t.Logf("MEASURE: src size = %d, dst size = %d", len(src), len(dst))

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("MEASURE: PANIC during 2x2 upscale: %v", r)
				}
			}()
			ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
		}()

		if panicked {
			t.Fatal("ScaleYUV420 panicked for 2x2 -> 320x240")
		}

		// Verify output is correct size.
		dstYSize := dstW * dstH
		yAvg := avgByte(dst[:dstYSize])
		t.Logf("MEASURE: Y avg after upscale = %.2f (expected %d)", yAvg, srcY)

		if math.Abs(yAvg-float64(srcY)) > 2.0 {
			t.Errorf("2x2 upscale Y avg %.2f deviates from source %d", yAvg, srcY)
		}
	})

	t.Run("320x240_to_2x2", func(t *testing.T) {
		const (
			srcW = 320
			srcH = 240
			dstW = 2
			dstH = 2
		)

		t.Logf("MEASURE: scaling %dx%d -> %dx%d", srcW, srcH, dstW, dstH)

		src := generateSolidYUV420(srcW, srcH, srcY, srcCb, srcCr)
		expectedDstSize := dstW * dstH * 3 / 2
		dst := make([]byte, expectedDstSize)

		t.Logf("MEASURE: src size = %d, dst size = %d", len(src), len(dst))

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("MEASURE: PANIC during 320x240 downscale to 2x2: %v", r)
				}
			}()
			ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
		}()

		if panicked {
			t.Fatal("ScaleYUV420 panicked for 320x240 -> 2x2")
		}

		// Verify output: 2x2 frame = 4 Y + 1 Cb + 1 Cr = 6 bytes.
		dstYSize := dstW * dstH              // 4
		dstUVSize := (dstW / 2) * (dstH / 2) // 1

		t.Logf("MEASURE: dst Y pixels = %d, UV pixels = %d", dstYSize, dstUVSize)

		if dstYSize > 0 {
			yAvg := avgByte(dst[:dstYSize])
			t.Logf("MEASURE: Y avg after downscale = %.2f (expected %d)", yAvg, srcY)
			if math.Abs(yAvg-float64(srcY)) > 5.0 {
				t.Errorf("320x240->2x2 downscale Y avg %.2f deviates from source %d", yAvg, srcY)
			}
		}

		if dstUVSize > 0 {
			cbVal := dst[dstYSize]
			crVal := dst[dstYSize+dstUVSize]
			t.Logf("MEASURE: Cb = %d (expected %d), Cr = %d (expected %d)", cbVal, srcCb, crVal, srcCr)
			if math.Abs(float64(cbVal)-float64(srcCb)) > 5.0 {
				t.Errorf("Cb = %d, expected ~%d", cbVal, srcCb)
			}
			if math.Abs(float64(crVal)-float64(srcCr)) > 5.0 {
				t.Errorf("Cr = %d, expected ~%d", crVal, srcCr)
			}
		}
	})

	// Also test Lanczos at minimum dimensions.
	t.Run("2x2_to_320x240_lanczos", func(t *testing.T) {
		const (
			srcW = 2
			srcH = 2
			dstW = 320
			dstH = 240
		)

		src := generateSolidYUV420(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*3/2)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("MEASURE: PANIC during Lanczos 2x2 upscale: %v", r)
				}
			}()
			ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
		}()

		if panicked {
			t.Fatal("ScaleYUV420Lanczos panicked for 2x2 -> 320x240")
		}

		dstYSize := dstW * dstH
		yAvg := avgByte(dst[:dstYSize])
		t.Logf("MEASURE: Lanczos 2x2 upscale Y avg = %.2f (expected %d)", yAvg, srcY)

		if math.Abs(yAvg-float64(srcY)) > 5.0 {
			t.Errorf("Lanczos 2x2 upscale Y avg %.2f deviates from source %d", yAvg, srcY)
		}
	})

	t.Run("320x240_to_2x2_lanczos", func(t *testing.T) {
		const (
			srcW = 320
			srcH = 240
			dstW = 2
			dstH = 2
		)

		src := generateSolidYUV420(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*3/2)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("MEASURE: PANIC during Lanczos 320x240 -> 2x2: %v", r)
				}
			}()
			ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
		}()

		if panicked {
			t.Fatal("ScaleYUV420Lanczos panicked for 320x240 -> 2x2")
		}

		dstYSize := dstW * dstH
		yAvg := avgByte(dst[:dstYSize])
		t.Logf("MEASURE: Lanczos 320x240->2x2 Y avg = %.2f (expected %d)", yAvg, srcY)

		if math.Abs(yAvg-float64(srcY)) > 5.0 {
			t.Errorf("Lanczos downscale to 2x2 Y avg %.2f deviates from source %d", yAvg, srcY)
		}
	})
}
