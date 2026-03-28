//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"math"
	"testing"
)

// TestColorGradeLUT3DP210_Identity verifies that a known 10-bit YUV value
// (white: Y=940, Cb=512, Cr=512) survives a round-trip through the color
// grading kernel when using an identity LUT.
func TestColorGradeLUT3DP210_Identity(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Upload a uniform white P210 frame: Y=940, Cb=512, Cr=512 (10-bit BT.709 white)
	data := makeP210TestFrame(w, h, 940, 512, 512)
	frame := uploadP210(t, ctx, pool, data, w, h)
	defer frame.Release()

	// Generate an identity 17x17x17 LUT (smaller for faster test).
	lutSize := 17
	lutData := makeIdentityLUT(lutSize)

	// Upload LUT to GPU.
	lutDev, err := UploadLUT3D(lutData)
	if err != nil {
		t.Fatal("UploadLUT3D:", err)
	}
	defer FreeLUT3D(lutDev)

	// Apply color grade at full intensity.
	err = ColorGradeLUT3DP210(ctx, frame, lutDev, lutSize, 1.0)
	if err != nil {
		t.Fatal("ColorGradeLUT3DP210:", err)
	}

	// Download and verify.
	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frame, result, w, h)
	if err != nil {
		t.Fatal("Download10:", err)
	}

	gotY := readP210YSample(result, 0)
	gotCb := readP210CbSample(result, w, h, 0)
	gotCr := readP210CrSample(result, w, h, 0)

	// Identity LUT should preserve the values within rounding tolerance.
	if math.Abs(float64(gotY)-940) > 3 {
		t.Errorf("identity LUT Y: got %d, want ~940", gotY)
	}
	if math.Abs(float64(gotCb)-512) > 3 {
		t.Errorf("identity LUT Cb: got %d, want ~512", gotCb)
	}
	if math.Abs(float64(gotCr)-512) > 3 {
		t.Errorf("identity LUT Cr: got %d, want ~512", gotCr)
	}
}

// TestColorGradeLUT3DP210_Black verifies that 10-bit black (Y=64, Cb=512, Cr=512)
// is preserved through an identity LUT.
func TestColorGradeLUT3DP210_Black(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// 10-bit black: Y=64, Cb=512, Cr=512
	data := makeP210TestFrame(w, h, 64, 512, 512)
	frame := uploadP210(t, ctx, pool, data, w, h)
	defer frame.Release()

	lutSize := 17
	lutData := makeIdentityLUT(lutSize)
	lutDev, err := UploadLUT3D(lutData)
	if err != nil {
		t.Fatal("UploadLUT3D:", err)
	}
	defer FreeLUT3D(lutDev)

	err = ColorGradeLUT3DP210(ctx, frame, lutDev, lutSize, 1.0)
	if err != nil {
		t.Fatal("ColorGradeLUT3DP210:", err)
	}

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frame, result, w, h)
	if err != nil {
		t.Fatal("Download10:", err)
	}

	gotY := readP210YSample(result, 0)
	gotCb := readP210CbSample(result, w, h, 0)

	if math.Abs(float64(gotY)-64) > 3 {
		t.Errorf("identity LUT black Y: got %d, want ~64", gotY)
	}
	if math.Abs(float64(gotCb)-512) > 3 {
		t.Errorf("identity LUT black Cb: got %d, want ~512", gotCb)
	}
}

// TestColorGradeLUT3DP210_MidGray verifies 10-bit mid-gray (Y=502, Cb=512, Cr=512)
// is preserved through an identity LUT.
func TestColorGradeLUT3DP210_MidGray(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// 10-bit mid-gray: Y=502 (half of 64..940 range), Cb=512, Cr=512
	data := makeP210TestFrame(w, h, 502, 512, 512)
	frame := uploadP210(t, ctx, pool, data, w, h)
	defer frame.Release()

	lutSize := 17
	lutData := makeIdentityLUT(lutSize)
	lutDev, err := UploadLUT3D(lutData)
	if err != nil {
		t.Fatal("UploadLUT3D:", err)
	}
	defer FreeLUT3D(lutDev)

	err = ColorGradeLUT3DP210(ctx, frame, lutDev, lutSize, 1.0)
	if err != nil {
		t.Fatal("ColorGradeLUT3DP210:", err)
	}

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frame, result, w, h)
	if err != nil {
		t.Fatal("Download10:", err)
	}

	gotY := readP210YSample(result, 0)
	if math.Abs(float64(gotY)-502) > 3 {
		t.Errorf("identity LUT mid-gray Y: got %d, want ~502", gotY)
	}
}

// TestColorGradeLUT3DP210_ZeroIntensity verifies that intensity=0 leaves the frame unchanged.
func TestColorGradeLUT3DP210_ZeroIntensity(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	data := makeP210TestFrame(w, h, 700, 400, 600)
	frame := uploadP210(t, ctx, pool, data, w, h)
	defer frame.Release()

	lutSize := 17
	lutData := makeIdentityLUT(lutSize)
	lutDev, err := UploadLUT3D(lutData)
	if err != nil {
		t.Fatal("UploadLUT3D:", err)
	}
	defer FreeLUT3D(lutDev)

	// Intensity 0 = bypass.
	err = ColorGradeLUT3DP210(ctx, frame, lutDev, lutSize, 0.0)
	if err != nil {
		t.Fatal("ColorGradeLUT3DP210:", err)
	}

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frame, result, w, h)
	if err != nil {
		t.Fatal("Download10:", err)
	}

	gotY := readP210YSample(result, 0)
	if math.Abs(float64(gotY)-700) > 1 {
		t.Errorf("zero intensity Y: got %d, want 700", gotY)
	}

	gotCb := readP210CbSample(result, w, h, 0)
	if math.Abs(float64(gotCb)-400) > 1 {
		t.Errorf("zero intensity Cb: got %d, want 400", gotCb)
	}
}

// TestColorGradeLUT3DP210_WouldFailWith8BitScaling verifies that values
// which would be wrong with 8-bit scaling math are correct with 10-bit.
// Y=940 in 8-bit math: (940-16)/219 = 4.22 -> clamped to 1.0 -> OK (accidental pass)
// Y=500 in 8-bit math: (500-16)/219 = 2.21 -> clamped to 1.0 -> maps to white (WRONG)
// Y=500 in 10-bit math: (500-64)/876 = 0.498 -> mid-gray (CORRECT)
func TestColorGradeLUT3DP210_WouldFailWith8BitScaling(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Y=500 should map to approximately mid-gray in 10-bit limited range.
	// With 8-bit math, (500-16)/219 = 2.21 -> clamped to 1.0 -> would map to Y=940 (white).
	// With 10-bit math, (500-64)/876 = 0.498 -> identity LUT preserves it -> Y ~500.
	data := makeP210TestFrame(w, h, 500, 512, 512)
	frame := uploadP210(t, ctx, pool, data, w, h)
	defer frame.Release()

	lutSize := 17
	lutData := makeIdentityLUT(lutSize)
	lutDev, err := UploadLUT3D(lutData)
	if err != nil {
		t.Fatal("UploadLUT3D:", err)
	}
	defer FreeLUT3D(lutDev)

	err = ColorGradeLUT3DP210(ctx, frame, lutDev, lutSize, 1.0)
	if err != nil {
		t.Fatal("ColorGradeLUT3DP210:", err)
	}

	result := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frame, result, w, h)
	if err != nil {
		t.Fatal("Download10:", err)
	}

	gotY := readP210YSample(result, 0)

	// With correct 10-bit scaling, Y should stay near 500.
	// With incorrect 8-bit scaling, Y would jump to ~940 (white).
	if math.Abs(float64(gotY)-500) > 5 {
		t.Errorf("10-bit scaling Y: got %d, want ~500 (not ~940 from 8-bit math)", gotY)
	}
}

// TestColorGradeLUT3DP210_NilGuards verifies error handling for nil inputs.
func TestColorGradeLUT3DP210_NilGuards(t *testing.T) {
	err := ColorGradeLUT3DP210(nil, &GPUFrame{}, 0, 17, 1.0)
	if err == nil {
		t.Error("expected error for nil context")
	}
}

// makeIdentityLUT creates a flat float32 identity LUT for testing.
// Red-major order: index = (b * size * size + g * size + r) * 3.
func makeIdentityLUT(size int) []float32 {
	total := size * size * size * 3
	data := make([]float32, total)
	idx := 0
	for b := 0; b < size; b++ {
		for g := 0; g < size; g++ {
			for r := 0; r < size; r++ {
				data[idx] = float32(r) / float32(size-1)
				data[idx+1] = float32(g) / float32(size-1)
				data[idx+2] = float32(b) / float32(size-1)
				idx += 3
			}
		}
	}
	return data
}
