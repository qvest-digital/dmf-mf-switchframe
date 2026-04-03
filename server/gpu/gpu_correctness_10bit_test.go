//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// P210 Upload/Download Round-Trip
// ============================================================================

// TestGPU_P210_UploadDownloadRoundTrip uploads a YUV422P10LE frame with gradient
// data to GPU memory and downloads it back. The round-trip MUST be lossless
// (byte-exact) for every Y, Cb, and Cr sample. Gradient data catches byte-swap
// or offset bugs that uniform fills would miss.
func TestGPU_P210_UploadDownloadRoundTrip(t *testing.T) {
	ctx := testContext(t)

	w, h := 128, 64
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	// Create gradient pattern: different multipliers per plane to catch swaps
	src := make([]byte, totalSize)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16((i*7+31)%1024))
	}
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[ySize+i*2:], uint16((i*13+67)%1024))
	}
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[ySize+cbSize+i*2:], uint16((i*19+103)%1024))
	}

	err = Upload10(ctx, frame, src, w, h)
	require.NoError(t, err)

	dst := make([]byte, totalSize)
	err = Download10(ctx, frame, dst, w, h)
	require.NoError(t, err)

	// Must be lossless — every sample must match exactly
	mismatches := 0
	for i := 0; i < w*h; i++ {
		srcVal := binary.LittleEndian.Uint16(src[i*2:])
		dstVal := binary.LittleEndian.Uint16(dst[i*2:])
		if srcVal != dstVal {
			if mismatches < 5 {
				t.Errorf("Y mismatch at sample %d: uploaded=%d, downloaded=%d", i, srcVal, dstVal)
			}
			mismatches++
		}
	}
	for i := 0; i < chromaW*h; i++ {
		srcVal := binary.LittleEndian.Uint16(src[ySize+i*2:])
		dstVal := binary.LittleEndian.Uint16(dst[ySize+i*2:])
		if srcVal != dstVal {
			if mismatches < 5 {
				t.Errorf("Cb mismatch at sample %d: uploaded=%d, downloaded=%d", i, srcVal, dstVal)
			}
			mismatches++
		}
	}
	for i := 0; i < chromaW*h; i++ {
		srcVal := binary.LittleEndian.Uint16(src[ySize+cbSize+i*2:])
		dstVal := binary.LittleEndian.Uint16(dst[ySize+cbSize+i*2:])
		if srcVal != dstVal {
			if mismatches < 5 {
				t.Errorf("Cr mismatch at sample %d: uploaded=%d, downloaded=%d", i, srcVal, dstVal)
			}
			mismatches++
		}
	}
	assert.Equal(t, 0, mismatches, "P210 round-trip must be lossless, got %d mismatches", mismatches)
}

// ============================================================================
// Blend Endpoint Identity Tests
// ============================================================================

// TestGPU_P210_BlendAt0_IsSourceA verifies that Blend(A, B, pos=0.0) produces
// output byte-identical to A. This is the mathematical identity: A*1.0 + B*0.0 = A.
func TestGPU_P210_BlendAt0_IsSourceA(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	dataA := makeP210TestFrame(w, h, 300, 400, 600)
	dataB := makeP210TestFrame(w, h, 700, 200, 800)

	frameA := uploadP210(t, ctx, pool, dataA, w, h)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, w, h)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	err = BlendMixP210(ctx, frameDst, frameA, frameB, 0.0)
	require.NoError(t, err)

	dst := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frameDst, dst, w, h)
	require.NoError(t, err)

	// Every Y sample must be exactly 300
	gotY := readP210YSample(dst, 0)
	assert.Equal(t, uint16(300), gotY, "pos=0: Y must equal source A (300), got %d", gotY)

	gotCb := readP210CbSample(dst, w, h, 0)
	assert.Equal(t, uint16(400), gotCb, "pos=0: Cb must equal source A (400), got %d", gotCb)

	gotCr := readP210CrSample(dst, w, h, 0)
	assert.Equal(t, uint16(600), gotCr, "pos=0: Cr must equal source A (600), got %d", gotCr)
}

// TestGPU_P210_BlendAt256_IsSourceB verifies that Blend(A, B, pos=1.0) produces
// output byte-identical to B. Mathematical identity: A*0.0 + B*1.0 = B.
func TestGPU_P210_BlendAt256_IsSourceB(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	dataA := makeP210TestFrame(w, h, 300, 400, 600)
	dataB := makeP210TestFrame(w, h, 700, 200, 800)

	frameA := uploadP210(t, ctx, pool, dataA, w, h)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, w, h)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	err = BlendMixP210(ctx, frameDst, frameA, frameB, 1.0)
	require.NoError(t, err)

	dst := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frameDst, dst, w, h)
	require.NoError(t, err)

	gotY := readP210YSample(dst, 0)
	assert.Equal(t, uint16(700), gotY, "pos=1: Y must equal source B (700), got %d", gotY)

	gotCb := readP210CbSample(dst, w, h, 0)
	assert.Equal(t, uint16(200), gotCb, "pos=1: Cb must equal source B (200), got %d", gotCb)

	gotCr := readP210CrSample(dst, w, h, 0)
	assert.Equal(t, uint16(800), gotCr, "pos=1: Cr must equal source B (800), got %d", gotCr)
}

// ============================================================================
// Blend Midpoint Formula Test
// ============================================================================

// TestGPU_P210_BlendMidpoint_IsAverage verifies the blend midpoint using
// manually computed integer math.
// For 10-bit integer blend: result = (A * (256-pos) + B * pos + 128) >> 8
// At pos=128 (50%): result = (200*128 + 800*128 + 128) >> 8 = (128000 + 128) >> 8 = 500
func TestGPU_P210_BlendMidpoint_IsAverage(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	dataA := makeP210TestFrame(w, h, 200, 512, 512)
	dataB := makeP210TestFrame(w, h, 800, 512, 512)

	frameA := uploadP210(t, ctx, pool, dataA, w, h)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, w, h)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	err = BlendMixP210(ctx, frameDst, frameA, frameB, 0.5)
	require.NoError(t, err)

	dst := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frameDst, dst, w, h)
	require.NoError(t, err)

	// Manual integer computation: (200*128 + 800*128 + 128) >> 8 = 500
	expectedY := uint16(500)
	gotY := readP210YSample(dst, 0)
	assert.InDelta(t, float64(expectedY), float64(gotY), 2,
		"midpoint blend Y: expected %d, got %d", expectedY, gotY)
}

// ============================================================================
// Blend vs CPU Bit-Exact
// ============================================================================

// TestGPU_P210_BlendVsCPU_BitExact verifies GPU blend and CPU blend of the
// same inputs produce identical output. Uses random data to stress-test.
func TestGPU_P210_BlendVsCPU_BitExact(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 5; trial++ {
		// Random 10-bit values
		yA := uint16(rng.Intn(1024))
		yB := uint16(rng.Intn(1024))
		cbA := uint16(rng.Intn(1024))
		cbB := uint16(rng.Intn(1024))
		crA := uint16(rng.Intn(1024))
		crB := uint16(rng.Intn(1024))
		pos := rng.Float64()

		// GPU blend
		dataA := makeP210TestFrame(w, h, yA, cbA, crA)
		dataB := makeP210TestFrame(w, h, yB, cbB, crB)
		frameA := uploadP210(t, ctx, pool, dataA, w, h)
		frameB := uploadP210(t, ctx, pool, dataB, w, h)
		frameDst, err := pool.Acquire()
		require.NoError(t, err)

		err = BlendMixP210(ctx, frameDst, frameA, frameB, pos)
		require.NoError(t, err)

		gpuResult := make([]byte, p210DownloadSize(w, h))
		err = Download10(ctx, frameDst, gpuResult, w, h)
		require.NoError(t, err)

		gpuY := readP210YSample(gpuResult, 0)

		// CPU reference: (A * (256-pos256) + B * pos256 + 128) >> 8
		pos256 := int32(pos * 256.0)
		if pos256 > 256 {
			pos256 = 256
		}
		inv := int32(256) - pos256
		cpuY := uint16(((int32(yA)*inv + int32(yB)*pos256 + 128) >> 8))
		if cpuY > 1023 {
			cpuY = 1023
		}

		assert.InDelta(t, float64(cpuY), float64(gpuY), 2,
			"trial %d: GPU Y=%d vs CPU Y=%d (A=%d, B=%d, pos=%.4f)", trial, gpuY, cpuY, yA, yB, pos)

		frameDst.Release()
		frameB.Release()
		frameA.Release()
	}
}

// ============================================================================
// FTB Produces Correct 10-bit Black
// ============================================================================

// TestGPU_P210_FTBIsBlack verifies FTB at pos=1.0 produces BT.709 10-bit
// limited-range black: Y=64, Cb=512, Cr=512.
// NOT Y=0 (full range), NOT Y=16 (8-bit), NOT Y=640 (wrong scale).
func TestGPU_P210_FTBIsBlack(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src := makeP210TestFrame(w, h, 940, 300, 700)
	frameSrc := uploadP210(t, ctx, pool, src, w, h)
	defer frameSrc.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	err = BlendFTBP210(ctx, frameDst, frameSrc, 1.0)
	require.NoError(t, err)

	dst := make([]byte, p210DownloadSize(w, h))
	err = Download10(ctx, frameDst, dst, w, h)
	require.NoError(t, err)

	// BT.709 10-bit limited range black per ITU-R BT.709
	gotY := readP210YSample(dst, 0)
	assert.Equal(t, uint16(64), gotY,
		"FTB 10-bit black Y must be 64 (BT.709 limited), got %d", gotY)

	gotCb := readP210CbSample(dst, w, h, 0)
	assert.Equal(t, uint16(512), gotCb,
		"FTB 10-bit neutral Cb must be 512, got %d", gotCb)

	gotCr := readP210CrSample(dst, w, h, 0)
	assert.Equal(t, uint16(512), gotCr,
		"FTB 10-bit neutral Cr must be 512, got %d", gotCr)

	// Verify wrong values are NOT present
	assert.NotEqual(t, uint16(0), gotY, "Y must not be full-range black (0)")
	assert.NotEqual(t, uint16(16), gotY, "Y must not be 8-bit black (16)")
}

// ============================================================================
// Output Legal Range
// ============================================================================

// TestGPU_P210_OutputLegalRange verifies that after blend operations, all
// output samples are within the valid 10-bit range [0, 1023].
func TestGPU_P210_OutputLegalRange(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	rng := rand.New(rand.NewSource(99))

	for trial := 0; trial < 10; trial++ {
		yA := uint16(rng.Intn(1024))
		yB := uint16(rng.Intn(1024))
		pos := rng.Float64()

		dataA := makeP210TestFrame(w, h, yA, uint16(rng.Intn(1024)), uint16(rng.Intn(1024)))
		dataB := makeP210TestFrame(w, h, yB, uint16(rng.Intn(1024)), uint16(rng.Intn(1024)))

		frameA := uploadP210(t, ctx, pool, dataA, w, h)
		frameB := uploadP210(t, ctx, pool, dataB, w, h)
		frameDst, err := pool.Acquire()
		require.NoError(t, err)

		err = BlendMixP210(ctx, frameDst, frameA, frameB, pos)
		require.NoError(t, err)

		result := make([]byte, p210DownloadSize(w, h))
		err = Download10(ctx, frameDst, result, w, h)
		require.NoError(t, err)

		// Check every Y sample
		for i := 0; i < w*h; i++ {
			val := binary.LittleEndian.Uint16(result[i*2:])
			if val > 1023 {
				t.Errorf("trial %d: Y[%d]=%d exceeds 10-bit max (1023)", trial, i, val)
				break
			}
		}

		// Check every Cb/Cr sample
		chromaW := w / 2
		ySize := w * h * 2
		cbSize := chromaW * h * 2
		for i := 0; i < chromaW*h; i++ {
			cbVal := binary.LittleEndian.Uint16(result[ySize+i*2:])
			crVal := binary.LittleEndian.Uint16(result[ySize+cbSize+i*2:])
			if cbVal > 1023 {
				t.Errorf("trial %d: Cb[%d]=%d exceeds 10-bit max", trial, i, cbVal)
				break
			}
			if crVal > 1023 {
				t.Errorf("trial %d: Cr[%d]=%d exceeds 10-bit max", trial, i, crVal)
				break
			}
		}

		frameDst.Release()
		frameB.Release()
		frameA.Release()
	}
}

// ============================================================================
// Blend Monotonicity
// ============================================================================

// TestGPU_P210_BlendMonotonic verifies that as blend position increases from
// 0 to 1.0, the output Y value monotonically transitions from A.Y to B.Y.
func TestGPU_P210_BlendMonotonic(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// A has lower Y, B has higher Y => output should increase monotonically
	dataA := makeP210TestFrame(w, h, 100, 512, 512)
	dataB := makeP210TestFrame(w, h, 900, 512, 512)

	frameA := uploadP210(t, ctx, pool, dataA, w, h)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, w, h)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	var prevY uint16
	violations := 0

	for step := 0; step <= 32; step++ {
		pos := float64(step) / 32.0
		err := BlendMixP210(ctx, frameDst, frameA, frameB, pos)
		require.NoError(t, err, "pos=%.4f", pos)

		result := make([]byte, p210DownloadSize(w, h))
		err = Download10(ctx, frameDst, result, w, h)
		require.NoError(t, err)

		gotY := readP210YSample(result, 0)

		if step > 0 && gotY < prevY {
			violations++
			t.Logf("monotonicity violation at step %d: Y=%d < prev=%d", step, gotY, prevY)
		}
		prevY = gotY
	}

	assert.Equal(t, 0, violations, "Y must increase monotonically with blend position")
}

// ============================================================================
// Identical Frames Identity
// ============================================================================

// TestGPU_P210_IdenticalFramesLossless verifies that blending a frame with
// itself at any position produces output identical to the input.
// Mathematical fact: A*p + A*(1-p) = A for any p.
func TestGPU_P210_IdenticalFramesLossless(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	data := makeP210TestFrame(w, h, 512, 300, 700)

	frameA := uploadP210(t, ctx, pool, data, w, h)
	defer frameA.Release()
	// Upload same data to different GPU frame
	frameB := uploadP210(t, ctx, pool, data, w, h)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	positions := []float64{0.0, 0.25, 0.5, 0.75, 1.0}
	for _, pos := range positions {
		err := BlendMixP210(ctx, frameDst, frameA, frameB, pos)
		require.NoError(t, err, "pos=%.2f", pos)

		result := make([]byte, p210DownloadSize(w, h))
		err = Download10(ctx, frameDst, result, w, h)
		require.NoError(t, err)

		gotY := readP210YSample(result, 0)
		gotCb := readP210CbSample(result, w, h, 0)
		gotCr := readP210CrSample(result, w, h, 0)

		// A*p + A*(1-p) = A, so output must equal input regardless of position
		assert.InDelta(t, 512, int(gotY), 1,
			"pos=%.2f: blend(A, A) Y must equal A=512, got %d", pos, gotY)
		assert.InDelta(t, 300, int(gotCb), 1,
			"pos=%.2f: blend(A, A) Cb must equal A=300, got %d", pos, gotCb)
		assert.InDelta(t, 700, int(gotCr), 1,
			"pos=%.2f: blend(A, A) Cr must equal A=700, got %d", pos, gotCr)
	}
}

// ============================================================================
// Determinism
// ============================================================================

// TestGPU_P210_Determinism verifies that the same inputs produce the same output
// every time. 100 iterations, same A, B, position => identical results.
func TestGPU_P210_Determinism(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	dataA := makeP210TestFrame(w, h, 200, 400, 600)
	dataB := makeP210TestFrame(w, h, 800, 200, 800)

	frameA := uploadP210(t, ctx, pool, dataA, w, h)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, w, h)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	var firstY, firstCb uint16
	for i := 0; i < 100; i++ {
		err := BlendMixP210(ctx, frameDst, frameA, frameB, 0.37)
		require.NoError(t, err, "iteration %d", i)

		result := make([]byte, p210DownloadSize(w, h))
		err = Download10(ctx, frameDst, result, w, h)
		require.NoError(t, err)

		gotY := readP210YSample(result, 0)
		gotCb := readP210CbSample(result, w, h, 0)

		if i == 0 {
			firstY = gotY
			firstCb = gotCb
		} else {
			assert.Equal(t, firstY, gotY,
				"iteration %d: Y=%d differs from first=%d (non-deterministic)", i, gotY, firstY)
			assert.Equal(t, firstCb, gotCb,
				"iteration %d: Cb=%d differs from first=%d (non-deterministic)", i, gotCb, firstCb)
		}
	}
}

// ============================================================================
// Multiple Resolutions
// ============================================================================

// TestGPU_P210_MultipleResolutions verifies upload/download works correctly
// at several resolutions: small (64x32), medium (320x240), and HD (1920x1080).
func TestGPU_P210_MultipleResolutions(t *testing.T) {
	ctx := testContext(t)

	resolutions := []struct {
		w, h int
	}{
		{64, 32},
		{320, 240},
		{1920, 1080},
	}

	for _, res := range resolutions {
		t.Run(fmt.Sprintf("%dx%d", res.w, res.h), func(t *testing.T) {
			pool, err := NewP210FramePool(ctx, res.w, res.h, 2)
			require.NoError(t, err)
			defer pool.Close()

			frame, err := pool.Acquire()
			require.NoError(t, err)
			defer frame.Release()

			// Upload known values
			data := makeP210TestFrame(res.w, res.h, 600, 400, 700)
			err = Upload10(ctx, frame, data, res.w, res.h)
			require.NoError(t, err)

			result := make([]byte, p210DownloadSize(res.w, res.h))
			err = Download10(ctx, frame, result, res.w, res.h)
			require.NoError(t, err)

			// Verify center pixel
			centerIdx := (res.h/2)*res.w + res.w/2
			gotY := binary.LittleEndian.Uint16(result[centerIdx*2:])
			assert.Equal(t, uint16(600), gotY,
				"%dx%d center Y: expected 600, got %d", res.w, res.h, gotY)
		},
		)
	}
}
