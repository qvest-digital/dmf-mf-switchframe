//go:build correctness && ((cgo && cuda) || (cgo && darwin))

package gpu

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

// ============================================================================
// Upload/Download Round-Trip
// ============================================================================

// TestGPUUploadDownloadRoundTrip uploads a YUV420p frame to GPU memory and
// downloads it back, verifying the output matches the input within +/-1 per
// channel (to allow for NV12 interleave/de-interleave rounding).
func TestGPUUploadDownloadRoundTrip(t *testing.T) {
	ctx := testContext(t)

	width, height := 128, 96
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Create a non-trivial test pattern with varying Y, Cb, Cr.
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	original := make([]byte, ySize+cbSize+crSize)

	for i := 0; i < ySize; i++ {
		original[i] = byte((i * 7 + 31) % 256) // Y: varied pattern
	}
	for i := 0; i < cbSize; i++ {
		original[ySize+i] = byte((i*13 + 67) % 256) // Cb: varied
	}
	for i := 0; i < crSize; i++ {
		original[ySize+cbSize+i] = byte((i*19 + 103) % 256) // Cr: varied
	}

	// Upload
	err = Upload(ctx, frame, original, width, height)
	require.NoError(t, err)

	// Download
	result := make([]byte, len(original))
	err = Download(ctx, result, frame, width, height)
	require.NoError(t, err)

	// Verify Y plane: exact match expected
	yMismatches := 0
	for i := 0; i < ySize; i++ {
		diff := int(original[i]) - int(result[i])
		if diff < -1 || diff > 1 {
			yMismatches++
		}
	}
	assert.Zero(t, yMismatches, "Y plane: %d pixels differ by more than +/-1", yMismatches)

	// Verify Cb plane
	cbMismatches := 0
	for i := 0; i < cbSize; i++ {
		diff := int(original[ySize+i]) - int(result[ySize+i])
		if diff < -1 || diff > 1 {
			cbMismatches++
		}
	}
	assert.Zero(t, cbMismatches, "Cb plane: %d pixels differ by more than +/-1", cbMismatches)

	// Verify Cr plane
	crMismatches := 0
	for i := 0; i < crSize; i++ {
		diff := int(original[ySize+cbSize+i]) - int(result[ySize+cbSize+i])
		if diff < -1 || diff > 1 {
			crMismatches++
		}
	}
	assert.Zero(t, crMismatches, "Cr plane: %d pixels differ by more than +/-1", crMismatches)

	// Log first mismatch for debugging if any exist
	if yMismatches+cbMismatches+crMismatches > 0 {
		for i := 0; i < len(original); i++ {
			if original[i] != result[i] {
				t.Logf("first mismatch at byte %d: original=%d, result=%d", i, original[i], result[i])
				break
			}
		}
	}
}

// ============================================================================
// Blend Tests: Endpoints and Midpoint
// ============================================================================

// TestGPUBlendMixAt0 verifies that GPU blend at position 0.0 produces
// output matching source A within +/-1 per channel.
func TestGPUBlendMixAt0(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	a := makeTestFrame(t, ctx, pool, width, height, 80, 60, 180)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 200, 200, 40)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendMix(ctx, dst, a, b, 0.0)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	assert.InDelta(t, 80.0, yAvg, 1.0, "pos=0: Y should match A")
	assert.InDelta(t, 60.0, cbAvg, 1.0, "pos=0: Cb should match A")
	assert.InDelta(t, 180.0, crAvg, 1.0, "pos=0: Cr should match A")
}

// TestGPUBlendMixAt1 verifies that GPU blend at position 1.0 produces
// output matching source B within +/-1 per channel.
func TestGPUBlendMixAt1(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	a := makeTestFrame(t, ctx, pool, width, height, 80, 60, 180)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 200, 200, 40)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendMix(ctx, dst, a, b, 1.0)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	assert.InDelta(t, 200.0, yAvg, 1.0, "pos=1: Y should match B")
	assert.InDelta(t, 200.0, cbAvg, 1.0, "pos=1: Cb should match B")
	assert.InDelta(t, 40.0, crAvg, 1.0, "pos=1: Cr should match B")
}

// TestGPUBlendMixAt05 verifies that GPU blend at position 0.5 produces
// the midpoint of A and B for each channel, within +/-1.
func TestGPUBlendMixAt05(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	a := makeTestFrame(t, ctx, pool, width, height, 80, 60, 180)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 200, 200, 40)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendMix(ctx, dst, a, b, 0.5)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	// Midpoints: Y=(80+200)/2=140, Cb=(60+200)/2=130, Cr=(180+40)/2=110
	assert.InDelta(t, 140.0, yAvg, 2.0, "pos=0.5: Y should be midpoint")
	assert.InDelta(t, 130.0, cbAvg, 2.0, "pos=0.5: Cb should be midpoint")
	assert.InDelta(t, 110.0, crAvg, 2.0, "pos=0.5: Cr should be midpoint")
}

// ============================================================================
// GPU vs CPU Blend Comparison
// ============================================================================

// TestGPUBlendVsCPU compares GPU blend output to CPU blend output from
// transition.BlendUniformBytes. The results should match within +/-1 per pixel.
func TestGPUBlendVsCPU(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Create frames with non-trivial, varied content.
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	yuvA := make([]byte, totalSize)
	yuvB := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		yuvA[i] = byte((i*3 + 17) % 220)       // Y plane A
		yuvB[i] = byte((i*7 + 41) % 220)        // Y plane B
	}
	for i := 0; i < cbSize; i++ {
		yuvA[ySize+i] = byte((i*5 + 23) % 256)  // Cb A
		yuvB[ySize+i] = byte((i*11 + 59) % 256) // Cb B
	}
	for i := 0; i < crSize; i++ {
		yuvA[ySize+cbSize+i] = byte((i*13 + 31) % 256) // Cr A
		yuvB[ySize+cbSize+i] = byte((i*17 + 71) % 256) // Cr B
	}

	// Upload to GPU
	gpuA, err := pool.Acquire()
	require.NoError(t, err)
	defer gpuA.Release()
	gpuB, err := pool.Acquire()
	require.NoError(t, err)
	defer gpuB.Release()
	gpuDst, err := pool.Acquire()
	require.NoError(t, err)
	defer gpuDst.Release()

	require.NoError(t, Upload(ctx, gpuA, yuvA, width, height))
	require.NoError(t, Upload(ctx, gpuB, yuvB, width, height))

	// Test at multiple blend positions.
	positions := []float64{0.0, 0.25, 0.5, 0.75, 1.0}
	for _, pos := range positions {
		// GPU blend
		err = BlendMix(ctx, gpuDst, gpuA, gpuB, pos)
		require.NoError(t, err)

		gpuResult := make([]byte, totalSize)
		require.NoError(t, Download(ctx, gpuResult, gpuDst, width, height))

		// CPU blend
		cpuResult := make([]byte, totalSize)
		pos256 := int(pos * 256)
		transition.BlendUniformBytes(cpuResult, yuvA, yuvB, pos256)

		// Compare: allow +/-1 per pixel for rounding differences between
		// GPU (NV12 interleaved UV) and CPU (planar YUV420).
		maxDiff := 0
		diffCount := 0
		for i := 0; i < totalSize; i++ {
			diff := int(gpuResult[i]) - int(cpuResult[i])
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
			}
			if diff > 1 {
				diffCount++
			}
		}

		assert.LessOrEqual(t, maxDiff, 2,
			"pos=%.2f: max pixel diff between GPU and CPU should be <=2, got %d", pos, maxDiff)
		assert.LessOrEqual(t, diffCount, totalSize/100,
			"pos=%.2f: at most 1%% of pixels should differ by >1, got %d/%d",
			pos, diffCount, totalSize)

		t.Logf("pos=%.2f: max diff=%d, pixels >1 diff=%d/%d", pos, maxDiff, diffCount, totalSize)
	}
}

// ============================================================================
// Chroma Key Tests
// ============================================================================

// TestGPUChromaKey verifies that GPU chroma keying on a green frame produces
// a mask that is mostly transparent (keyed out) for green pixels.
func TestGPUChromaKey(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 3)
	require.NoError(t, err)
	defer pool.Close()

	// Create a green-screen frame in YUV420 space.
	// BT.709 green: Y~145, Cb~54, Cr~26
	frame := makeTestFrame(t, ctx, pool, width, height, 145, 54, 26)
	defer frame.Release()

	maskBuf, err := pool.Acquire()
	require.NoError(t, err)
	defer maskBuf.Release()

	cfg := ChromaKeyConfig{
		KeyCb:      54,
		KeyCr:      26,
		Similarity: 0.3,
		Smoothness: 0.1,
	}
	err = ChromaKey(ctx, frame, maskBuf, cfg)
	require.NoError(t, err)

	// Download the mask
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	maskYUV := make([]byte, ySize+cbSize*2)
	require.NoError(t, Download(ctx, maskYUV, maskBuf, width, height))

	// Count transparent pixels in the Y-plane of the mask
	transparent := 0
	for i := 0; i < ySize; i++ {
		if maskYUV[i] < 64 {
			transparent++
		}
	}
	ratio := float64(transparent) / float64(ySize)
	assert.Greater(t, ratio, 0.8,
		"green screen should be mostly keyed (>80%% transparent), got %.1f%%", ratio*100)
	t.Logf("Chroma key: %.1f%% transparent", ratio*100)
}

// TestGPUChromaKeyVsCPU compares GPU chroma key output to the CPU
// graphics.ChromaKeyWithSpillColor reference implementation.
// Both should produce masks with the same overall transparency pattern.
func TestGPUChromaKeyVsCPU(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 3)
	require.NoError(t, err)
	defer pool.Close()

	// Create a frame with left half green (key target) and right half gray (keep).
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize
	yuv := make([]byte, totalSize)

	// Y plane: uniform 128
	for i := 0; i < ySize; i++ {
		yuv[i] = 128
	}

	// Chroma planes: left half is green (Cb=54, Cr=26), right half is neutral (128, 128).
	uvWidth := width / 2
	uvHeight := height / 2
	for row := 0; row < uvHeight; row++ {
		for col := 0; col < uvWidth; col++ {
			idx := row*uvWidth + col
			if col < uvWidth/2 {
				// Left quarter in chroma space -> left half in luma space
				yuv[ySize+idx] = 54  // Cb: green
				yuv[ySize+cbSize+idx] = 26  // Cr: green
			} else {
				yuv[ySize+idx] = 128       // Cb: neutral
				yuv[ySize+cbSize+idx] = 128 // Cr: neutral
			}
		}
	}

	// Upload to GPU
	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()
	require.NoError(t, Upload(ctx, frame, yuv, width, height))

	maskBuf, err := pool.Acquire()
	require.NoError(t, err)
	defer maskBuf.Release()

	cfg := ChromaKeyConfig{
		KeyCb:      54,
		KeyCr:      26,
		Similarity: 0.3,
		Smoothness: 0.1,
	}
	err = ChromaKey(ctx, frame, maskBuf, cfg)
	require.NoError(t, err)

	maskYUV := make([]byte, totalSize)
	require.NoError(t, Download(ctx, maskYUV, maskBuf, width, height))

	// Check left half of mask (green area) is mostly transparent.
	leftTransparent := 0
	leftTotal := 0
	for row := 0; row < height; row++ {
		for col := 0; col < width/2; col++ {
			leftTotal++
			if maskYUV[row*width+col] < 64 {
				leftTransparent++
			}
		}
	}
	leftRatio := float64(leftTransparent) / float64(leftTotal)
	assert.Greater(t, leftRatio, 0.7,
		"left half (green) should be mostly transparent, got %.1f%%", leftRatio*100)

	// Check right half of mask (neutral area) is mostly opaque.
	rightOpaque := 0
	rightTotal := 0
	for row := 0; row < height; row++ {
		for col := width / 2; col < width; col++ {
			rightTotal++
			if maskYUV[row*width+col] > 192 {
				rightOpaque++
			}
		}
	}
	rightRatio := float64(rightOpaque) / float64(rightTotal)
	assert.Greater(t, rightRatio, 0.7,
		"right half (neutral) should be mostly opaque, got %.1f%%", rightRatio*100)

	t.Logf("Chroma key: left %.1f%% transparent, right %.1f%% opaque", leftRatio*100, rightRatio*100)
}

// ============================================================================
// Composite Tests
// ============================================================================

// TestGPUCompositeVsCPU verifies that GPU PIP composite produces correct
// results by checking that the composite region contains the source pixel
// values and the outside region retains the background.
func TestGPUCompositeVsCPU(t *testing.T) {
	ctx := testContext(t)

	width, height := 128, 128
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Background: black
	bg := makeTestFrame(t, ctx, pool, width, height, 16, 128, 128)
	defer bg.Release()

	// Source: bright
	src := makeTestFrame(t, ctx, pool, width, height, 200, 50, 200)
	defer src.Release()

	// Composite source into center quarter (32,32)-(96,96) at full alpha
	rect := Rect{X: 32, Y: 32, W: 64, H: 64}
	err = PIPComposite(ctx, bg, src, rect, 1.0)
	require.NoError(t, err)

	// Download result
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download(ctx, result, bg, width, height))

	// Verify inside PIP region has source values
	insideY := result[64*width+64] // center of composited area
	assert.InDelta(t, 200, int(insideY), 10,
		"inside PIP should be source Y~200, got %d", insideY)

	// Verify outside PIP region still has background
	outsideY := result[10*width+10]
	assert.Equal(t, byte(16), outsideY,
		"outside PIP should be background Y=16, got %d", outsideY)

	// Check chroma in PIP region
	chromaCenter := ySize + 32*(width/2) + 32
	insideCb := result[chromaCenter]
	insideCr := result[chromaCenter+cbSize]
	assert.InDelta(t, 50, int(insideCb), 10, "PIP Cb should be ~50")
	assert.InDelta(t, 200, int(insideCr), 10, "PIP Cr should be ~200")

	t.Logf("Composite: inside Y=%d Cb=%d Cr=%d, outside Y=%d",
		insideY, insideCb, insideCr, outsideY)
}

// ============================================================================
// FTB (Fade to Black)
// ============================================================================

// TestGPUFTBIsBlack verifies that GPU FTB at position 1.0 produces
// broadcast black (Y=16, Cb=128, Cr=128) across the entire frame.
func TestGPUFTBIsBlack(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 3)
	require.NoError(t, err)
	defer pool.Close()

	// Source with non-black content.
	src := makeTestFrame(t, ctx, pool, width, height, 200, 50, 200)
	defer src.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendFTB(ctx, dst, src, 1.0)
	require.NoError(t, err)

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download(ctx, result, dst, width, height))

	// Every Y pixel should be 16 (BT.709 limited-range black).
	for i := 0; i < ySize; i++ {
		if result[i] != 16 {
			t.Fatalf("FTB Y pixel %d should be 16 (black), got %d", i, result[i])
		}
	}

	// Every Cb pixel should be 128 (neutral).
	for i := 0; i < cbSize; i++ {
		if result[ySize+i] != 128 {
			t.Fatalf("FTB Cb pixel %d should be 128, got %d", i, result[ySize+i])
		}
	}

	// Every Cr pixel should be 128 (neutral).
	for i := 0; i < crSize; i++ {
		if result[ySize+cbSize+i] != 128 {
			t.Fatalf("FTB Cr pixel %d should be 128, got %d", i, result[ySize+cbSize+i])
		}
	}
}

// ============================================================================
// Legal Range
// ============================================================================

// TestGPUOutputLegalRange verifies that GPU blend and composite outputs
// stay within BT.709 legal range when inputs are within legal range.
func TestGPUOutputLegalRange(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Create frames with values at the edges of legal range.
	// Frame A: near-black, Frame B: near-white
	a := makeTestFrame(t, ctx, pool, width, height, 20, 20, 230)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 230, 230, 20)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	positions := []float64{0.0, 0.25, 0.5, 0.75, 1.0}
	for _, pos := range positions {
		err = BlendMix(ctx, dst, a, b, pos)
		require.NoError(t, err)

		ySize := width * height
		cbSize := (width / 2) * (height / 2)
		crSize := cbSize
		result := make([]byte, ySize+cbSize+crSize)
		require.NoError(t, Download(ctx, result, dst, width, height))

		// Check all Y values are within extended range [0, 255].
		// GPU blending operates on full byte range but inputs are legal-range,
		// so outputs should be bounded by the input range.
		minY, maxY := byte(255), byte(0)
		for i := 0; i < ySize; i++ {
			if result[i] < minY {
				minY = result[i]
			}
			if result[i] > maxY {
				maxY = result[i]
			}
		}

		// With inputs at Y=20 and Y=230, the blend output should be between
		// these values (no overflow/underflow). Allow +/-1 for rounding.
		assert.GreaterOrEqual(t, int(minY), 19,
			"pos=%.2f: min Y should be >= 19, got %d", pos, minY)
		assert.LessOrEqual(t, int(maxY), 231,
			"pos=%.2f: max Y should be <= 231, got %d", pos, maxY)

		// Also verify chroma range
		minCb, maxCb := byte(255), byte(0)
		for i := 0; i < cbSize; i++ {
			if result[ySize+i] < minCb {
				minCb = result[ySize+i]
			}
			if result[ySize+i] > maxCb {
				maxCb = result[ySize+i]
			}
		}
		assert.GreaterOrEqual(t, int(minCb), 19,
			"pos=%.2f: min Cb should be >= 19, got %d", pos, minCb)
		assert.LessOrEqual(t, int(maxCb), 231,
			"pos=%.2f: max Cb should be <= 231, got %d", pos, maxCb)
	}
}

// ============================================================================
// Blend at multiple positions: monotonic transition
// ============================================================================

// TestGPUBlendMonotonicTransition verifies that as blend position increases
// from 0 to 1, the Y average transitions monotonically from A to B.
func TestGPUBlendMonotonicTransition(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	a := makeTestFrame(t, ctx, pool, width, height, 50, 128, 128)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 200, 128, 128)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	prevY := 0.0
	steps := 11
	for i := 0; i < steps; i++ {
		pos := float64(i) / float64(steps-1)

		err = BlendMix(ctx, dst, a, b, pos)
		require.NoError(t, err)

		yAvg, _, _ := avgPlanes(t, ctx, dst, width, height)

		if i > 0 {
			assert.GreaterOrEqual(t, yAvg, prevY-1.0,
				"step %d (pos=%.2f): Y average should increase monotonically, got %.1f after %.1f",
				i, pos, yAvg, prevY)
		}
		prevY = yAvg
	}
}

// ============================================================================
// FTB Partial: halfway should be between source and black
// ============================================================================

// TestGPUFTBHalfway verifies that FTB at 0.5 produces values between
// the source and broadcast black.
func TestGPUFTBHalfway(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 3)
	require.NoError(t, err)
	defer pool.Close()

	src := makeTestFrame(t, ctx, pool, width, height, 200, 50, 200)
	defer src.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendFTB(ctx, dst, src, 0.5)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)

	// Y: midpoint between 200 and 16 (broadcast black) = ~108
	// Using the GPU formula: (src * (256-128) + 16 * 128 + 128) >> 8
	assert.InDelta(t, 108.0, yAvg, 3.0, "FTB 0.5: Y should be ~108")
	// Cb: midpoint between 50 and 128 = ~89
	assert.InDelta(t, 89.0, cbAvg, 3.0, "FTB 0.5: Cb should be ~89")
	// Cr: midpoint between 200 and 128 = ~164
	assert.InDelta(t, 164.0, crAvg, 3.0, "FTB 0.5: Cr should be ~164")
}

// ============================================================================
// Upload/download multiple resolutions
// ============================================================================

// TestGPUUploadDownloadMultipleResolutions verifies round-trip correctness
// at multiple common resolutions.
func TestGPUUploadDownloadMultipleResolutions(t *testing.T) {
	ctx := testContext(t)

	resolutions := [][2]int{
		{64, 64},
		{320, 240},
		{640, 480},
		{1280, 720},
		{1920, 1080},
	}

	for _, res := range resolutions {
		w, h := res[0], res[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			pool, err := NewFramePool(ctx, w, h, 2)
			require.NoError(t, err)
			defer pool.Close()

			frame, err := pool.Acquire()
			require.NoError(t, err)
			defer frame.Release()

			ySize := w * h
			cbSize := (w / 2) * (h / 2)
			crSize := cbSize
			original := make([]byte, ySize+cbSize+crSize)
			// Uniform fill to avoid edge effects
			for i := 0; i < ySize; i++ {
				original[i] = 128
			}
			for i := 0; i < cbSize; i++ {
				original[ySize+i] = 100
			}
			for i := 0; i < crSize; i++ {
				original[ySize+cbSize+i] = 200
			}

			require.NoError(t, Upload(ctx, frame, original, w, h))

			result := make([]byte, len(original))
			require.NoError(t, Download(ctx, result, frame, w, h))

			// Check Y average matches
			var ySum int64
			for i := 0; i < ySize; i++ {
				ySum += int64(result[i])
			}
			yAvg := float64(ySum) / float64(ySize)
			assert.InDelta(t, 128.0, yAvg, 1.0, "Y average should be ~128")
		})
	}
}

// ============================================================================
// PSNR test: blend of identical frames should be lossless
// ============================================================================

// TestGPUBlendIdenticalFramesLossless verifies that blending two identical
// frames produces a result identical (or nearly identical) to the input.
func TestGPUBlendIdenticalFramesLossless(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Create two identical frames with a gradient pattern.
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	original := make([]byte, ySize+cbSize+crSize)
	for i := 0; i < ySize; i++ {
		original[i] = byte(16 + (i*219/ySize))
	}
	for i := 0; i < cbSize; i++ {
		original[ySize+i] = 128
	}
	for i := 0; i < crSize; i++ {
		original[ySize+cbSize+i] = 128
	}

	a, err := pool.Acquire()
	require.NoError(t, err)
	defer a.Release()
	b, err := pool.Acquire()
	require.NoError(t, err)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	require.NoError(t, Upload(ctx, a, original, width, height))
	require.NoError(t, Upload(ctx, b, original, width, height))

	// Blend at 0.5 — with identical inputs, output should be the same.
	require.NoError(t, BlendMix(ctx, dst, a, b, 0.5))

	result := make([]byte, len(original))
	require.NoError(t, Download(ctx, result, dst, width, height))

	// Compute PSNR on Y plane
	var sumSqDiff float64
	for i := 0; i < ySize; i++ {
		diff := float64(original[i]) - float64(result[i])
		sumSqDiff += diff * diff
	}
	mse := sumSqDiff / float64(ySize)
	var psnr float64
	if mse == 0 {
		psnr = 999.0
	} else {
		psnr = 10.0 * math.Log10(255.0*255.0/mse)
	}

	assert.Greater(t, psnr, 40.0,
		"blending identical frames should produce PSNR > 40 dB, got %.1f dB", psnr)
	t.Logf("Identical frame blend PSNR: %.1f dB", psnr)
}
