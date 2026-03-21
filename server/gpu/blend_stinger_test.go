//go:build darwin || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBlendStingerUV verifies that BlendStinger correctly handles UV
// (chroma) blending with per-pixel alpha. This catches the magenta/green
// corruption bug where the NV12 UV plane was not composited correctly.
func TestBlendStingerUV(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 64, 64
	pool, err := NewFramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	base, _ := pool.Acquire()
	overlay, _ := pool.Acquire()
	alpha, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer base.Release()
	defer overlay.Release()
	defer alpha.Release()
	defer dst.Release()

	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2
	uvSize := chromaW * chromaH

	// Base frame: Y=50, Cb=100, Cr=200
	yuvBase := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuvBase[i] = 50
	}
	for i := 0; i < uvSize; i++ {
		yuvBase[ySize+i] = 100       // Cb
		yuvBase[ySize+uvSize+i] = 200 // Cr
	}

	// Overlay frame: Y=200, Cb=200, Cr=100
	yuvOverlay := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuvOverlay[i] = 200
	}
	for i := 0; i < uvSize; i++ {
		yuvOverlay[ySize+i] = 200       // Cb
		yuvOverlay[ySize+uvSize+i] = 100 // Cr
	}

	// Alpha: top half = 255 (opaque, show overlay), bottom half = 0 (transparent, show base).
	// Build as YUV420p where Y=alpha, Cb/Cr=128 (neutral).
	halfY := h / 2
	alphaYUV := make([]byte, ySize+2*uvSize)
	for row := 0; row < h; row++ {
		val := byte(0)
		if row < halfY {
			val = 255
		}
		for col := 0; col < w; col++ {
			alphaYUV[row*w+col] = val
		}
	}
	for i := ySize; i < len(alphaYUV); i++ {
		alphaYUV[i] = 128
	}

	require.NoError(t, Upload(ctx, base, yuvBase, w, h))
	require.NoError(t, Upload(ctx, overlay, yuvOverlay, w, h))
	require.NoError(t, Upload(ctx, alpha, alphaYUV, w, h))

	err = BlendStinger(ctx, dst, base, overlay, alpha)
	require.NoError(t, err)

	result := make([]byte, ySize+2*uvSize)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// Check Y plane
	topY := result[5*w+w/2]       // row 5, center — should be overlay Y=200
	bottomY := result[(h-5)*w+w/2] // row h-5, center — should be base Y=50
	assert.InDelta(t, 200, int(topY), 3, "Y top half (opaque) should be overlay=200")
	assert.InDelta(t, 50, int(bottomY), 3, "Y bottom half (transparent) should be base=50")

	// Check Cb plane
	// Top chroma row 2 (luma row 4-5) should be overlay Cb=200
	topCb := result[ySize+2*chromaW+chromaW/2]
	// Bottom chroma row (last quarter) should be base Cb=100
	bottomCb := result[ySize+(chromaH-2)*chromaW+chromaW/2]
	assert.InDelta(t, 200, int(topCb), 3, "Cb top half (opaque) should be overlay=200, got %d", topCb)
	assert.InDelta(t, 100, int(bottomCb), 3, "Cb bottom half (transparent) should be base=100, got %d", bottomCb)

	// Check Cr plane
	topCr := result[ySize+uvSize+2*chromaW+chromaW/2]
	bottomCr := result[ySize+uvSize+(chromaH-2)*chromaW+chromaW/2]
	assert.InDelta(t, 100, int(topCr), 3, "Cr top half (opaque) should be overlay=100, got %d", topCr)
	assert.InDelta(t, 200, int(bottomCr), 3, "Cr bottom half (transparent) should be base=200, got %d", bottomCr)

	t.Logf("BlendStinger UV test: topY=%d bottomY=%d topCb=%d bottomCb=%d topCr=%d bottomCr=%d",
		topY, bottomY, topCb, bottomCb, topCr, bottomCr)
}

// TestBlendStingerFullAlphaUV verifies that 100% alpha produces pure overlay UV.
func TestBlendStingerFullAlphaUV(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 64, 64
	pool, err := NewFramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	base, _ := pool.Acquire()
	overlay, _ := pool.Acquire()
	alphaFrame, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer base.Release()
	defer overlay.Release()
	defer alphaFrame.Release()
	defer dst.Release()

	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2
	uvSize := chromaW * chromaH

	// Base: Y=50, Cb=60, Cr=70
	yuvBase := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuvBase[i] = 50
	}
	for i := 0; i < uvSize; i++ {
		yuvBase[ySize+i] = 60
		yuvBase[ySize+uvSize+i] = 70
	}

	// Overlay: Y=200, Cb=210, Cr=220
	yuvOverlay := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuvOverlay[i] = 200
	}
	for i := 0; i < uvSize; i++ {
		yuvOverlay[ySize+i] = 210
		yuvOverlay[ySize+uvSize+i] = 220
	}

	// Full alpha=255 everywhere
	alphaYUV := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		alphaYUV[i] = 255
	}
	for i := ySize; i < len(alphaYUV); i++ {
		alphaYUV[i] = 128
	}

	require.NoError(t, Upload(ctx, base, yuvBase, w, h))
	require.NoError(t, Upload(ctx, overlay, yuvOverlay, w, h))
	require.NoError(t, Upload(ctx, alphaFrame, alphaYUV, w, h))

	err = BlendStinger(ctx, dst, base, overlay, alphaFrame)
	require.NoError(t, err)

	result := make([]byte, ySize+2*uvSize)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// All pixels should be overlay
	assert.InDelta(t, 200, int(result[0]), 2, "Y should be overlay=200")
	assert.InDelta(t, 210, int(result[ySize]), 2, "Cb should be overlay=210, got %d", result[ySize])
	assert.InDelta(t, 220, int(result[ySize+uvSize]), 2, "Cr should be overlay=220, got %d", result[ySize+uvSize])

	t.Logf("Full alpha: Y=%d Cb=%d Cr=%d", result[0], result[ySize], result[ySize+uvSize])
}

// TestBlendStingerSpatialUV tests with spatially varying data to catch
// pitch/stride misalignment that wouldn't show with uniform fills.
// Left half of base has Cb=60, right half has Cb=80. Overlay is uniform Cb=200.
// Alpha: left half opaque (255), right half transparent (0).
// Expected: left half Cb=200 (overlay), right half Cb=80 (base right).
// If pitch is wrong, the right-half Cb would be wrong (shifted data).
func TestBlendStingerSpatialUV(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	// Use a realistic-ish size where pitch != width (320 → pitch=512)
	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	base, _ := pool.Acquire()
	overlay, _ := pool.Acquire()
	alphaFrame, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer base.Release()
	defer overlay.Release()
	defer alphaFrame.Release()
	defer dst.Release()

	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2
	uvSize := chromaW * chromaH

	// Base: left half Cb=60 Cr=180, right half Cb=80 Cr=160
	yuvBase := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuvBase[i] = 128 // uniform Y
	}
	for row := 0; row < chromaH; row++ {
		for col := 0; col < chromaW; col++ {
			idx := row*chromaW + col
			if col < chromaW/2 {
				yuvBase[ySize+idx] = 60        // left Cb
				yuvBase[ySize+uvSize+idx] = 180 // left Cr
			} else {
				yuvBase[ySize+idx] = 80        // right Cb
				yuvBase[ySize+uvSize+idx] = 160 // right Cr
			}
		}
	}

	// Overlay: uniform Cb=200, Cr=100
	yuvOverlay := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuvOverlay[i] = 200
	}
	for i := 0; i < uvSize; i++ {
		yuvOverlay[ySize+i] = 200
		yuvOverlay[ySize+uvSize+i] = 100
	}

	// Alpha: left half opaque (255), right half transparent (0)
	alphaYUV := make([]byte, ySize+2*uvSize)
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			if col < w/2 {
				alphaYUV[row*w+col] = 255
			} else {
				alphaYUV[row*w+col] = 0
			}
		}
	}
	for i := ySize; i < len(alphaYUV); i++ {
		alphaYUV[i] = 128
	}

	require.NoError(t, Upload(ctx, base, yuvBase, w, h))
	require.NoError(t, Upload(ctx, overlay, yuvOverlay, w, h))
	require.NoError(t, Upload(ctx, alphaFrame, alphaYUV, w, h))

	err = BlendStinger(ctx, dst, base, overlay, alphaFrame)
	require.NoError(t, err)

	result := make([]byte, ySize+2*uvSize)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// Check left side (opaque alpha → overlay Cb=200, Cr=100)
	leftCb := result[ySize+chromaH/2*chromaW+chromaW/4]     // center-left
	leftCr := result[ySize+uvSize+chromaH/2*chromaW+chromaW/4]
	assert.InDelta(t, 200, int(leftCb), 3, "left Cb should be overlay=200, got %d", leftCb)
	assert.InDelta(t, 100, int(leftCr), 3, "left Cr should be overlay=100, got %d", leftCr)

	// Check right side (transparent alpha → base right Cb=80, Cr=160)
	rightCb := result[ySize+chromaH/2*chromaW+3*chromaW/4]     // center-right
	rightCr := result[ySize+uvSize+chromaH/2*chromaW+3*chromaW/4]
	assert.InDelta(t, 80, int(rightCb), 3, "right Cb should be base=80, got %d", rightCb)
	assert.InDelta(t, 160, int(rightCr), 3, "right Cr should be base=160, got %d", rightCr)

	t.Logf("Spatial UV: leftCb=%d leftCr=%d rightCb=%d rightCr=%d", leftCb, leftCr, rightCb, rightCr)
}

// TestBlendStingerZeroAlphaUV verifies that 0% alpha produces pure base UV.
func TestBlendStingerZeroAlphaUV(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 64, 64
	pool, err := NewFramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	base, _ := pool.Acquire()
	overlay, _ := pool.Acquire()
	alphaFrame, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer base.Release()
	defer overlay.Release()
	defer alphaFrame.Release()
	defer dst.Release()

	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2
	uvSize := chromaW * chromaH

	// Base: Y=50, Cb=60, Cr=70
	yuvBase := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuvBase[i] = 50
	}
	for i := 0; i < uvSize; i++ {
		yuvBase[ySize+i] = 60
		yuvBase[ySize+uvSize+i] = 70
	}

	// Overlay: Y=200, Cb=210, Cr=220
	yuvOverlay := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuvOverlay[i] = 200
	}
	for i := 0; i < uvSize; i++ {
		yuvOverlay[ySize+i] = 210
		yuvOverlay[ySize+uvSize+i] = 220
	}

	// Zero alpha everywhere
	alphaYUV := make([]byte, ySize+2*uvSize)
	// Y plane all 0 (alpha=0)
	for i := ySize; i < len(alphaYUV); i++ {
		alphaYUV[i] = 128
	}

	require.NoError(t, Upload(ctx, base, yuvBase, w, h))
	require.NoError(t, Upload(ctx, overlay, yuvOverlay, w, h))
	require.NoError(t, Upload(ctx, alphaFrame, alphaYUV, w, h))

	err = BlendStinger(ctx, dst, base, overlay, alphaFrame)
	require.NoError(t, err)

	result := make([]byte, ySize+2*uvSize)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// All pixels should be base
	assert.InDelta(t, 50, int(result[0]), 2, "Y should be base=50")
	assert.InDelta(t, 60, int(result[ySize]), 2, "Cb should be base=60, got %d", result[ySize])
	assert.InDelta(t, 70, int(result[ySize+uvSize]), 2, "Cr should be base=70, got %d", result[ySize+uvSize])

	t.Logf("Zero alpha: Y=%d Cb=%d Cr=%d", result[0], result[ySize], result[ySize+uvSize])
}
