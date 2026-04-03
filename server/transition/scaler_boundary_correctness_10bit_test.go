//go:build correctness

package transition

import (
	"encoding/binary"
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers for 10-bit scaler tests
// ---------------------------------------------------------------------------

// generateSolidYUV422P10 creates a solid-color YUV422P10LE frame.
// Layout: Y plane (w*h*2) + Cb plane (w/2*h*2) + Cr plane (w/2*h*2).
func generateSolidYUV422P10(w, h int, y, cb, cr uint16) []byte {
	chromaW := w / 2
	totalBytes := w * h * 4 // Y + Cb + Cr
	frame := make([]byte, totalBytes)

	// Y plane
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(frame[i*2:], y)
	}

	// Cb plane
	cbStart := w * h * 2
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(frame[cbStart+i*2:], cb)
	}

	// Cr plane
	crStart := cbStart + chromaW*h*2
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(frame[crStart+i*2:], cr)
	}

	return frame
}

// avgUint16Plane computes the average of a 10-bit plane stored as uint16 LE.
func avgUint16Plane(data []byte, sampleCount int) float64 {
	if sampleCount == 0 {
		return 0
	}
	var sum uint64
	for i := 0; i < sampleCount; i++ {
		sum += uint64(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return float64(sum) / float64(sampleCount)
}

// ---------------------------------------------------------------------------
// TestScaleYUV422P10_SolidColorPreservation
//
// Mirror of TestScaleYUV420OddDimensions for 10-bit.
// Scale uniform Y=500, Cb=300, Cr=700 from various sizes.
// Verify output is still uniform (+/-2 for bilinear rounding).
// All dimensions must be even for YUV422.
// ---------------------------------------------------------------------------

func TestScaleYUV422P10_SolidColorPreservation(t *testing.T) {
	type dimPair struct {
		srcW, srcH, dstW, dstH int
		label                  string
	}

	cases := []dimPair{
		{2, 2, 4, 4, "2x2 -> 4x4"},
		{4, 4, 2, 2, "4x4 -> 2x2"},
		{16, 16, 32, 32, "16x16 -> 32x32"},
		{320, 240, 640, 480, "320x240 -> 640x480"},
		{640, 480, 320, 240, "640x480 -> 320x240"},
		{1920, 1080, 320, 240, "1920x1080 -> 320x240"},
		{320, 240, 1920, 1080, "320x240 -> 1920x1080"},
	}

	const (
		srcY  uint16 = 500
		srcCb uint16 = 300
		srcCr uint16 = 700
	)

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			src := generateSolidYUV422P10(tc.srcW, tc.srcH, srcY, srcCb, srcCr)
			dstSize := tc.dstW * tc.dstH * 4
			dst := make([]byte, dstSize)

			// Should not panic
			var panicked bool
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
						t.Logf("PANIC during scale %s: %v", tc.label, r)
					}
				}()
				err := ScaleYUV422P10(src, tc.srcW, tc.srcH, dst, tc.dstW, tc.dstH)
				if err != nil {
					t.Fatalf("ScaleYUV422P10 error: %v", err)
				}
			}()
			if panicked {
				t.Fatalf("ScaleYUV422P10 panicked for %s", tc.label)
			}

			// Verify Y plane average
			dstYSamples := tc.dstW * tc.dstH
			yAvg := avgUint16Plane(dst, dstYSamples)
			t.Logf("Y avg = %.2f (expected %d)", yAvg, srcY)
			if math.Abs(yAvg-float64(srcY)) > 2.0 {
				t.Errorf("Y plane average %.2f deviates from source %d by more than +-2", yAvg, srcY)
			}

			// Verify Cb plane average
			dstChromaW := tc.dstW / 2
			dstChromaSamples := dstChromaW * tc.dstH
			cbStart := dstYSamples * 2
			if dstChromaSamples > 0 {
				cbAvg := avgUint16Plane(dst[cbStart:], dstChromaSamples)
				t.Logf("Cb avg = %.2f (expected %d)", cbAvg, srcCb)
				if math.Abs(cbAvg-float64(srcCb)) > 2.0 {
					t.Errorf("Cb plane average %.2f deviates from source %d by more than +-2", cbAvg, srcCb)
				}
			}

			// Verify Cr plane average
			crStart := cbStart + dstChromaSamples*2
			if dstChromaSamples > 0 {
				crAvg := avgUint16Plane(dst[crStart:], dstChromaSamples)
				t.Logf("Cr avg = %.2f (expected %d)", crAvg, srcCr)
				if math.Abs(crAvg-float64(srcCr)) > 2.0 {
					t.Errorf("Cr plane average %.2f deviates from source %d by more than +-2", crAvg, srcCr)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestScaleYUV422P10_ChromaFullHeight
//
// Verify chroma is scaled at full height (not half height like 4:2:0).
// Check that the last chroma row has expected values.
// ---------------------------------------------------------------------------

func TestScaleYUV422P10_ChromaFullHeightVerify(t *testing.T) {
	const (
		srcW = 320
		srcH = 240
		dstW = 640
		dstH = 480

		inY  uint16 = 500
		inCb uint16 = 300
		inCr uint16 = 700
	)

	src := generateSolidYUV422P10(srcW, srcH, inY, inCb, inCr)
	dst := make([]byte, dstW*dstH*4)

	if err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH); err != nil {
		t.Fatalf("ScaleYUV422P10: %v", err)
	}

	dstChromaW := dstW / 2
	cbByteOff := dstW * dstH * 2
	crByteOff := cbByteOff + dstChromaW*dstH*2

	// Check last row of Cb plane
	lastRowCbOff := cbByteOff + (dstH-1)*dstChromaW*2
	for x := 0; x < dstChromaW; x++ {
		cbVal := binary.LittleEndian.Uint16(dst[lastRowCbOff+x*2:])
		if math.Abs(float64(cbVal)-float64(inCb)) > 2.0 {
			t.Errorf("last Cb row, x=%d: got %d, expected ~%d (chroma full height?)", x, cbVal, inCb)
			break
		}
	}

	// Check last row of Cr plane
	lastRowCrOff := crByteOff + (dstH-1)*dstChromaW*2
	for x := 0; x < dstChromaW; x++ {
		crVal := binary.LittleEndian.Uint16(dst[lastRowCrOff+x*2:])
		if math.Abs(float64(crVal)-float64(inCr)) > 2.0 {
			t.Errorf("last Cr row, x=%d: got %d, expected ~%d (chroma full height?)", x, crVal, inCr)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// TestScaleYUV422P10_ChromaPlanes
//
// Mirror of TestScaleYUV420ChromaPlanes. Scale with distinct Y/Cb/Cr values
// and verify all three planes are correct. Catches stride/offset bugs.
// ---------------------------------------------------------------------------

func TestScaleYUV422P10_ChromaPlanes(t *testing.T) {
	const (
		srcW = 320
		srcH = 240
		dstW = 640
		dstH = 480

		inY  uint16 = 500
		inCb uint16 = 200
		inCr uint16 = 800
	)

	src := generateSolidYUV422P10(srcW, srcH, inY, inCb, inCr)
	dst := make([]byte, dstW*dstH*4)

	if err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH); err != nil {
		t.Fatalf("ScaleYUV422P10: %v", err)
	}

	dstYSamples := dstW * dstH
	dstChromaW := dstW / 2
	dstChromaSamples := dstChromaW * dstH

	// Y plane
	yAvg := avgUint16Plane(dst, dstYSamples)
	t.Logf("Y plane avg = %.4f (expected %d)", yAvg, inY)
	if math.Abs(yAvg-float64(inY)) > 2.0 {
		t.Errorf("Y plane: expected avg ~%d, got %.4f", inY, yAvg)
	}

	// Cb plane
	cbStart := dstYSamples * 2
	cbAvg := avgUint16Plane(dst[cbStart:], dstChromaSamples)
	t.Logf("Cb plane avg = %.4f (expected %d)", cbAvg, inCb)
	if math.Abs(cbAvg-float64(inCb)) > 2.0 {
		t.Errorf("Cb plane: expected avg ~%d, got %.4f (WRONG STRIDE?)", inCb, cbAvg)
	}

	// Cr plane
	crStart := cbStart + dstChromaSamples*2
	crAvg := avgUint16Plane(dst[crStart:], dstChromaSamples)
	t.Logf("Cr plane avg = %.4f (expected %d)", crAvg, inCr)
	if math.Abs(crAvg-float64(inCr)) > 2.0 {
		t.Errorf("Cr plane: expected avg ~%d, got %.4f (WRONG STRIDE?)", inCr, crAvg)
	}

	// Cross-contamination check: Cb should be near 200, Cr near 800 — far enough apart
	// that any stride confusion produces wildly wrong values.
	cbMin, cbMax := uint16(1023), uint16(0)
	for i := 0; i < dstChromaSamples; i++ {
		v := binary.LittleEndian.Uint16(dst[cbStart+i*2:])
		if v < cbMin {
			cbMin = v
		}
		if v > cbMax {
			cbMax = v
		}
	}
	t.Logf("Cb range [%d, %d]", cbMin, cbMax)
	if cbMax > inCb+5 || cbMin < inCb-5 {
		t.Errorf("Cb plane values [%d,%d] deviate significantly from expected %d", cbMin, cbMax, inCb)
	}

	crMin, crMax := uint16(1023), uint16(0)
	for i := 0; i < dstChromaSamples; i++ {
		v := binary.LittleEndian.Uint16(dst[crStart+i*2:])
		if v < crMin {
			crMin = v
		}
		if v > crMax {
			crMax = v
		}
	}
	t.Logf("Cr range [%d, %d]", crMin, crMax)
	if crMax > inCr+5 || crMin < inCr-5 {
		t.Errorf("Cr plane values [%d,%d] deviate significantly from expected %d", crMin, crMax, inCr)
	}

	// Also test with Lanczos scaler
	t.Run("Lanczos", func(t *testing.T) {
		dstL := make([]byte, dstW*dstH*4)
		if err := ScaleYUV422P10Lanczos(src, srcW, srcH, dstL, dstW, dstH); err != nil {
			t.Fatalf("ScaleYUV422P10Lanczos: %v", err)
		}

		yAvgL := avgUint16Plane(dstL, dstYSamples)
		cbAvgL := avgUint16Plane(dstL[cbStart:], dstChromaSamples)
		crAvgL := avgUint16Plane(dstL[crStart:], dstChromaSamples)

		t.Logf("Lanczos Y avg = %.4f (expected %d)", yAvgL, inY)
		t.Logf("Lanczos Cb avg = %.4f (expected %d)", cbAvgL, inCb)
		t.Logf("Lanczos Cr avg = %.4f (expected %d)", crAvgL, inCr)

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
// TestScaleYUV422P10_ExtremeRatios
//
// 10:1 downscale (1920x1080 -> 192x108) and 10:1 upscale (192x108 -> 1920x1080).
// Verify output isn't black or garbage.
// ---------------------------------------------------------------------------

func TestScaleYUV422P10_ExtremeRatios(t *testing.T) {
	const (
		srcY  uint16 = 600
		srcCb uint16 = 400
		srcCr uint16 = 700
	)

	t.Run("10x_downscale", func(t *testing.T) {
		const (
			srcW = 1920
			srcH = 1080
			dstW = 192
			dstH = 108
		)

		src := generateSolidYUV422P10(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*4)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("PANIC during extreme downscale: %v", r)
				}
			}()
			if err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH); err != nil {
				t.Fatalf("ScaleYUV422P10: %v", err)
			}
		}()
		if panicked {
			t.Fatal("ScaleYUV422P10 panicked during extreme downscale")
		}

		dstYSamples := dstW * dstH
		yAvg := avgUint16Plane(dst, dstYSamples)
		t.Logf("downscale Y avg = %.2f (expected ~%d)", yAvg, srcY)

		if yAvg < 5.0 {
			t.Errorf("downscale output appears black: Y avg = %.2f", yAvg)
		}
		if math.Abs(yAvg-float64(srcY)) > 10.0 {
			t.Errorf("downscale Y avg %.2f too far from source %d", yAvg, srcY)
		}
	})

	t.Run("10x_upscale", func(t *testing.T) {
		const (
			srcW = 192
			srcH = 108
			dstW = 1920
			dstH = 1080
		)

		src := generateSolidYUV422P10(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*4)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("PANIC during extreme upscale: %v", r)
				}
			}()
			if err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH); err != nil {
				t.Fatalf("ScaleYUV422P10: %v", err)
			}
		}()
		if panicked {
			t.Fatal("ScaleYUV422P10 panicked during extreme upscale")
		}

		dstYSamples := dstW * dstH
		yAvg := avgUint16Plane(dst, dstYSamples)
		t.Logf("upscale Y avg = %.2f (expected ~%d)", yAvg, srcY)

		if math.Abs(yAvg-float64(srcY)) > 5.0 {
			t.Errorf("upscale Y avg %.2f too far from source %d", yAvg, srcY)
		}

		// Check for corruption: count pixels that deviate more than 10 from expected
		corrupt := 0
		for i := 0; i < dstYSamples; i++ {
			v := binary.LittleEndian.Uint16(dst[i*2:])
			if math.Abs(float64(v)-float64(srcY)) > 10 {
				corrupt++
			}
		}
		corruptPct := float64(corrupt) / float64(dstYSamples) * 100
		t.Logf("upscale corrupt pixels: %d / %d (%.2f%%)", corrupt, dstYSamples, corruptPct)
		if corruptPct > 1.0 {
			t.Errorf("upscale has %.2f%% corrupt pixels (>10 deviation from source)", corruptPct)
		}
	})

	// Lanczos extreme ratios
	t.Run("10x_downscale_lanczos", func(t *testing.T) {
		const (
			srcW = 1920
			srcH = 1080
			dstW = 192
			dstH = 108
		)

		src := generateSolidYUV422P10(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*4)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("PANIC during Lanczos extreme downscale: %v", r)
				}
			}()
			if err := ScaleYUV422P10Lanczos(src, srcW, srcH, dst, dstW, dstH); err != nil {
				t.Fatalf("ScaleYUV422P10Lanczos: %v", err)
			}
		}()
		if panicked {
			t.Fatal("ScaleYUV422P10Lanczos panicked during extreme downscale")
		}

		dstYSamples := dstW * dstH
		yAvg := avgUint16Plane(dst, dstYSamples)
		t.Logf("Lanczos downscale Y avg = %.2f (expected ~%d)", yAvg, srcY)

		if math.Abs(yAvg-float64(srcY)) > 10.0 {
			t.Errorf("Lanczos downscale Y avg %.2f too far from source %d", yAvg, srcY)
		}
	})

	t.Run("10x_upscale_lanczos", func(t *testing.T) {
		const (
			srcW = 192
			srcH = 108
			dstW = 1920
			dstH = 1080
		)

		src := generateSolidYUV422P10(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*4)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("PANIC during Lanczos extreme upscale: %v", r)
				}
			}()
			if err := ScaleYUV422P10Lanczos(src, srcW, srcH, dst, dstW, dstH); err != nil {
				t.Fatalf("ScaleYUV422P10Lanczos: %v", err)
			}
		}()
		if panicked {
			t.Fatal("ScaleYUV422P10Lanczos panicked during extreme upscale")
		}

		dstYSamples := dstW * dstH
		yAvg := avgUint16Plane(dst, dstYSamples)
		t.Logf("Lanczos upscale Y avg = %.2f (expected ~%d)", yAvg, srcY)

		if math.Abs(yAvg-float64(srcY)) > 5.0 {
			t.Errorf("Lanczos upscale Y avg %.2f too far from source %d", yAvg, srcY)
		}
	})
}

// ---------------------------------------------------------------------------
// TestScaleYUV422P10_IdentityScale
//
// (w,h) -> (w,h) should be byte-exact copy.
// ---------------------------------------------------------------------------

func TestScaleYUV422P10_IdentityScale(t *testing.T) {
	dims := []struct{ w, h int }{
		{2, 2},
		{16, 16},
		{320, 240},
		{1920, 1080},
	}

	for _, d := range dims {
		t.Run("bilinear", func(t *testing.T) {
			src := generateSolidYUV422P10(d.w, d.h, 500, 300, 700)
			dst := make([]byte, d.w*d.h*4)

			if err := ScaleYUV422P10(src, d.w, d.h, dst, d.w, d.h); err != nil {
				t.Fatalf("ScaleYUV422P10: %v", err)
			}

			// Should be byte-exact
			for i := 0; i < len(src); i++ {
				if dst[i] != src[i] {
					t.Errorf("identity scale %dx%d: byte %d differs: got %d, want %d",
						d.w, d.h, i, dst[i], src[i])
					break
				}
			}
		})

		t.Run("lanczos", func(t *testing.T) {
			src := generateSolidYUV422P10(d.w, d.h, 500, 300, 700)
			dst := make([]byte, d.w*d.h*4)

			if err := ScaleYUV422P10Lanczos(src, d.w, d.h, dst, d.w, d.h); err != nil {
				t.Fatalf("ScaleYUV422P10Lanczos: %v", err)
			}

			for i := 0; i < len(src); i++ {
				if dst[i] != src[i] {
					t.Errorf("identity Lanczos scale %dx%d: byte %d differs: got %d, want %d",
						d.w, d.h, i, dst[i], src[i])
					break
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestScaleYUV422P10_MinimumDimensions
//
// Scale 2x2 -> 320x240 and 320x240 -> 2x2.
// The smallest valid YUV422P10 frame is 2x2 (chroma plane is 1x2 for 4:2:2).
// Verify no crash and correct output.
// ---------------------------------------------------------------------------

func TestScaleYUV422P10_MinimumDimensions(t *testing.T) {
	const (
		srcY  uint16 = 700
		srcCb uint16 = 300
		srcCr uint16 = 800
	)

	t.Run("2x2_to_320x240", func(t *testing.T) {
		const (
			srcW = 2
			srcH = 2
			dstW = 320
			dstH = 240
		)

		src := generateSolidYUV422P10(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*4)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("PANIC during 2x2 upscale: %v", r)
				}
			}()
			if err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH); err != nil {
				t.Fatalf("ScaleYUV422P10: %v", err)
			}
		}()
		if panicked {
			t.Fatal("ScaleYUV422P10 panicked for 2x2 -> 320x240")
		}

		dstYSamples := dstW * dstH
		yAvg := avgUint16Plane(dst, dstYSamples)
		t.Logf("Y avg after upscale = %.2f (expected %d)", yAvg, srcY)
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

		src := generateSolidYUV422P10(srcW, srcH, srcY, srcCb, srcCr)
		dst := make([]byte, dstW*dstH*4)

		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.Logf("PANIC during 320x240 -> 2x2: %v", r)
				}
			}()
			if err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH); err != nil {
				t.Fatalf("ScaleYUV422P10: %v", err)
			}
		}()
		if panicked {
			t.Fatal("ScaleYUV422P10 panicked for 320x240 -> 2x2")
		}

		// 2x2 frame: 2 Y samples per row, 1 chroma sample per row, all at full height
		dstYSamples := dstW * dstH
		yAvg := avgUint16Plane(dst, dstYSamples)
		t.Logf("Y avg after downscale = %.2f (expected %d)", yAvg, srcY)
		if math.Abs(yAvg-float64(srcY)) > 5.0 {
			t.Errorf("320x240->2x2 downscale Y avg %.2f deviates from source %d", yAvg, srcY)
		}

		// Check chroma
		dstChromaW := dstW / 2 // = 1
		cbOff := dstYSamples * 2
		cbVal := binary.LittleEndian.Uint16(dst[cbOff:])
		t.Logf("Cb = %d (expected %d)", cbVal, srcCb)
		if math.Abs(float64(cbVal)-float64(srcCb)) > 5.0 {
			t.Errorf("Cb = %d, expected ~%d", cbVal, srcCb)
		}

		crOff := cbOff + dstChromaW*dstH*2
		crVal := binary.LittleEndian.Uint16(dst[crOff:])
		t.Logf("Cr = %d (expected %d)", crVal, srcCr)
		if math.Abs(float64(crVal)-float64(srcCr)) > 5.0 {
			t.Errorf("Cr = %d, expected ~%d", crVal, srcCr)
		}
	})
}

// ---------------------------------------------------------------------------
// TestScaleYUV422P10_OddWidthRejected
//
// Verify odd widths are rejected.
// ---------------------------------------------------------------------------

func TestScaleYUV422P10_OddWidthRejected(t *testing.T) {
	// Odd src width
	err := ScaleYUV422P10(nil, 15, 8, nil, 16, 8)
	if err == nil {
		t.Error("should reject odd src width")
	}

	// Odd dst width
	err = ScaleYUV422P10(make([]byte, 16*8*4), 16, 8, nil, 15, 8)
	if err == nil {
		t.Error("should reject odd dst width")
	}
}
