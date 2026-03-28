//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBlendWipeP210 verifies that BlendWipeP210 produces a left-to-right wipe
// between two P210 frames. At 50% position, the left half should be frame B
// and the right half should be frame A.
func TestBlendWipeP210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 6)
	require.NoError(t, err)
	defer pool.Close()

	a, err := pool.Acquire()
	require.NoError(t, err)
	defer a.Release()

	b, err := pool.Acquire()
	require.NoError(t, err)
	defer b.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	mask, err := pool.Acquire()
	require.NoError(t, err)
	defer mask.Release()

	// Frame A: Y=200, Cb=512, Cr=512
	dataA := makeP210TestFrame(w, h, 200, 512, 512)
	require.NoError(t, Upload10(ctx, a, dataA, w, h))

	// Frame B: Y=800, Cb=512, Cr=512
	dataB := makeP210TestFrame(w, h, 800, 512, 512)
	require.NoError(t, Upload10(ctx, b, dataB, w, h))

	// H-left wipe at 50%
	err = BlendWipeP210(ctx, dst, a, b, mask, 0.5, WipeHLeft, 4)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	// Left quarter should be B (800), right quarter should be A (200)
	leftY := readP210YSample(result, 16*w+w/4)   // row 16, ~25% from left
	rightY := readP210YSample(result, 16*w+3*w/4) // row 16, ~75% from left
	assert.Greater(t, leftY, rightY,
		"left Y (%d) should be brighter (B=800) than right Y (%d, A=200) at 50%% h-left wipe",
		leftY, rightY)
	assert.InDelta(t, 800, int(leftY), 30, "left Y should be near B=800, got %d", leftY)
	assert.InDelta(t, 200, int(rightY), 30, "right Y should be near A=200, got %d", rightY)

	t.Logf("BlendWipeP210: leftY=%d rightY=%d", leftY, rightY)
}

// TestBlendWipeP210_Endpoints verifies 0% and 100% wipe positions produce pure A and pure B.
func TestBlendWipeP210_Endpoints(t *testing.T) {
	ctx := testContext(t)

	w, h := 32, 16
	pool, err := NewP210FramePool(ctx, w, h, 6)
	require.NoError(t, err)
	defer pool.Close()

	a := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 300, 512, 512), w, h)
	defer a.Release()
	b := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 700, 512, 512), w, h)
	defer b.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()
	mask, err := pool.Acquire()
	require.NoError(t, err)
	defer mask.Release()

	result := make([]byte, p210DownloadSize(w, h))
	// Sample at center of frame to avoid single-pixel edge effects
	centerIdx := (h/2)*w + w/2

	// 0% = all A (wipe hasn't started)
	require.NoError(t, BlendWipeP210(ctx, dst, a, b, mask, 0.0, WipeHLeft, 0))
	require.NoError(t, Download10(ctx, dst, result, w, h))
	y0 := readP210YSample(result, centerIdx)
	assert.InDelta(t, 300, int(y0), 2, "0%% wipe should be all A (300), got %d", y0)

	// 100% = all B (wipe complete)
	require.NoError(t, BlendWipeP210(ctx, dst, a, b, mask, 1.0, WipeHLeft, 0))
	require.NoError(t, Download10(ctx, dst, result, w, h))
	y100 := readP210YSample(result, centerIdx)
	assert.InDelta(t, 700, int(y100), 2, "100%% wipe should be all B (700), got %d", y100)
}

// TestBlendWipeP210_UV verifies that UV planes are correctly blended during a wipe.
func TestBlendWipeP210_UV(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 6)
	require.NoError(t, err)
	defer pool.Close()

	// Frame A: Cb=300, Cr=700
	a := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 500, 300, 700), w, h)
	defer a.Release()

	// Frame B: Cb=700, Cr=300
	b := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 500, 700, 300), w, h)
	defer b.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()
	mask, err := pool.Acquire()
	require.NoError(t, err)
	defer mask.Release()

	// Sample chroma at center to avoid edge effects (chroma x=0 maps to luma x=0,1)
	chromaW := w / 2
	centerChromaIdx := (h/2)*chromaW + chromaW/2

	// Full wipe (100%) should produce all B
	require.NoError(t, BlendWipeP210(ctx, dst, a, b, mask, 1.0, WipeHLeft, 0))

	result := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	gotCb := readP210CbSample(result, w, h, centerChromaIdx)
	gotCr := readP210CrSample(result, w, h, centerChromaIdx)
	assert.InDelta(t, 700, int(gotCb), 3, "100%% wipe Cb should be B=700, got %d", gotCb)
	assert.InDelta(t, 300, int(gotCr), 3, "100%% wipe Cr should be B=300, got %d", gotCr)

	// 0% wipe should produce all A
	require.NoError(t, BlendWipeP210(ctx, dst, a, b, mask, 0.0, WipeHLeft, 0))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	gotCb = readP210CbSample(result, w, h, centerChromaIdx)
	gotCr = readP210CrSample(result, w, h, centerChromaIdx)
	assert.InDelta(t, 300, int(gotCb), 3, "0%% wipe Cb should be A=300, got %d", gotCb)
	assert.InDelta(t, 700, int(gotCr), 3, "0%% wipe Cr should be A=700, got %d", gotCr)
}

// TestBlendStingerP210 verifies per-pixel alpha compositing on P210 frames.
// Top half alpha=255 (show overlay), bottom half alpha=0 (show base).
func TestBlendStingerP210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	// Base: Y=200, Cb=300, Cr=700
	base := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 200, 300, 700), w, h)
	defer base.Release()

	// Overlay: Y=800, Cb=700, Cr=300
	overlay := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 800, 700, 300), w, h)
	defer overlay.Release()

	// Alpha: top half=255 (opaque, show overlay), bottom half=0 (transparent, show base).
	// Alpha is uploaded as NV12-sized frame (uint8 Y plane + UV padding).
	// But wait — the alpha frame for P210 stinger needs to be P210-sized so it
	// has enough room for the UV scratch region. Upload into a P210 frame
	// using Upload10 with YUV422P10LE format where Y values are the alpha.
	alphaPool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer alphaPool.Close()

	alphaFrame, err := alphaPool.Acquire()
	require.NoError(t, err)
	defer alphaFrame.Release()

	// The alpha is stored in the Y plane of a P210-sized buffer as uint8 values.
	// We need to write raw uint8 alpha values into the buffer directly.
	// The blend_alpha_p210 kernel reads alpha as uint8 at alphaPitch bytes per row.
	fillP210AlphaMask(t, ctx, alphaFrame, w, h)

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendStingerP210(ctx, dst, base, overlay, alphaFrame)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	// Top half (alpha=255): should be overlay Y=800
	topY := readP210YSample(result, 5*w+w/2)
	assert.InDelta(t, 800, int(topY), 10,
		"top half (opaque alpha) should show overlay Y=800, got %d", topY)

	// Bottom half (alpha=0): should be base Y=200
	bottomY := readP210YSample(result, (h-5)*w+w/2)
	assert.InDelta(t, 200, int(bottomY), 10,
		"bottom half (transparent alpha) should show base Y=200, got %d", bottomY)

	t.Logf("BlendStingerP210: topY=%d (want ~800), bottomY=%d (want ~200)", topY, bottomY)
}

// TestBlendStingerP210_UV verifies that UV planes are correctly composited in stinger mode.
func TestBlendStingerP210_UV(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	// Base: Cb=300, Cr=700
	base := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 500, 300, 700), w, h)
	defer base.Release()

	// Overlay: Cb=700, Cr=300
	overlay := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 500, 700, 300), w, h)
	defer overlay.Release()

	// Full alpha (255 everywhere)
	alphaPool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer alphaPool.Close()

	alphaFrame, err := alphaPool.Acquire()
	require.NoError(t, err)
	defer alphaFrame.Release()

	fillP210AlphaUniform(t, ctx, alphaFrame, w, h, 255)

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendStingerP210(ctx, dst, base, overlay, alphaFrame)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	gotCb := readP210CbSample(result, w, h, 0)
	gotCr := readP210CrSample(result, w, h, 0)
	assert.InDelta(t, 700, int(gotCb), 5, "full alpha stinger Cb should be overlay=700, got %d", gotCb)
	assert.InDelta(t, 300, int(gotCr), 5, "full alpha stinger Cr should be overlay=300, got %d", gotCr)

	// Zero alpha should produce base
	fillP210AlphaUniform(t, ctx, alphaFrame, w, h, 0)

	err = BlendStingerP210(ctx, dst, base, overlay, alphaFrame)
	require.NoError(t, err)

	require.NoError(t, Download10(ctx, dst, result, w, h))
	gotCb = readP210CbSample(result, w, h, 0)
	gotCr = readP210CrSample(result, w, h, 0)
	assert.InDelta(t, 300, int(gotCb), 5, "zero alpha stinger Cb should be base=300, got %d", gotCb)
	assert.InDelta(t, 700, int(gotCr), 5, "zero alpha stinger Cr should be base=700, got %d", gotCr)
}

// TestBlendWipeP210_Dispatch verifies that GPUBlendWipeDispatch routes P210 frames correctly.
func TestBlendWipeP210_Dispatch(t *testing.T) {
	ctx := testContext(t)

	w, h := 32, 16
	pool, err := NewP210FramePool(ctx, w, h, 6)
	require.NoError(t, err)
	defer pool.Close()

	a := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 200, 512, 512), w, h)
	defer a.Release()
	b := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 800, 512, 512), w, h)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()
	mask, err := pool.Acquire()
	require.NoError(t, err)
	defer mask.Release()

	// Dispatch should route to P210 variant
	err = GPUBlendWipeDispatch(ctx, dst, a, b, mask, 1.0, WipeHLeft, 0)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	y := readP210YSample(result, 0)
	assert.InDelta(t, 800, int(y), 3, "dispatch 100%% wipe should be all B (800), got %d", y)
}

// TestBlendStingerP210_Dispatch verifies that GPUBlendStingerDispatch routes P210 frames correctly.
func TestBlendStingerP210_Dispatch(t *testing.T) {
	ctx := testContext(t)

	w, h := 32, 16
	pool, err := NewP210FramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	base := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 200, 512, 512), w, h)
	defer base.Release()
	overlay := uploadP210(t, ctx, pool, makeP210TestFrame(w, h, 800, 512, 512), w, h)
	defer overlay.Release()

	alphaPool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer alphaPool.Close()

	alphaFrame, err := alphaPool.Acquire()
	require.NoError(t, err)
	defer alphaFrame.Release()
	fillP210AlphaUniform(t, ctx, alphaFrame, w, h, 255)

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Dispatch should route to P210 variant
	err = GPUBlendStingerDispatch(ctx, dst, base, overlay, alphaFrame)
	require.NoError(t, err)

	result := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	y := readP210YSample(result, 0)
	assert.InDelta(t, 800, int(y), 5, "dispatch full-alpha stinger should be overlay (800), got %d", y)
}

// TestBlendWipeP210_NilArgs verifies nil argument handling.
func TestBlendWipeP210_NilArgs(t *testing.T) {
	assert.ErrorIs(t, BlendWipeP210(nil, nil, nil, nil, nil, 0.5, WipeHLeft, 0), ErrGPUNotAvailable)
}

// TestBlendStingerP210_NilArgs verifies nil argument handling.
func TestBlendStingerP210_NilArgs(t *testing.T) {
	assert.ErrorIs(t, BlendStingerP210(nil, nil, nil, nil, nil), ErrGPUNotAvailable)
}
