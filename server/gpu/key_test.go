//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChromaKeyGreenScreen(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	maskBuf, _ := pool.Acquire()
	defer frame.Release()
	defer maskBuf.Release()

	// Create a "green screen" frame: Y=128, Cb=44 (green), Cr=21 (green)
	// BT.709 green: approximately Cb~44, Cr~21
	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = 128 // Y
	}
	cbOffset := w * h
	crOffset := cbOffset + w/2*h/2
	for i := 0; i < w/2*h/2; i++ {
		yuv[cbOffset+i] = 44 // Cb (green)
		yuv[crOffset+i] = 21 // Cr (green)
	}
	require.NoError(t, Upload(ctx, frame, yuv, w, h))

	// Key out green (Cb=44, Cr=21)
	cfg := ChromaKeyConfig{
		KeyCb:      44,
		KeyCr:      21,
		Similarity: 0.3,
		Smoothness: 0.1,
	}
	err = ChromaKey(ctx, frame, maskBuf, cfg)
	require.NoError(t, err)

	// Download mask and verify it's mostly transparent (green keyed out)
	maskYUV := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, maskYUV, maskBuf, w, h))

	// Check that most of the Y plane of the mask is near 0 (transparent)
	transparent := 0
	for i := 0; i < w*h; i++ {
		if maskYUV[i] < 64 {
			transparent++
		}
	}
	ratio := float64(transparent) / float64(w*h)
	assert.Greater(t, ratio, 0.8, "most pixels should be keyed (transparent): %.2f", ratio)
	t.Logf("Chroma key: %.1f%% transparent", ratio*100)
}

func TestChromaKeyNonGreen(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	maskBuf, _ := pool.Acquire()
	defer frame.Release()
	defer maskBuf.Release()

	// Create a non-green frame (neutral gray: Cb=128, Cr=128)
	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = 128
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128 // neutral chroma
	}
	require.NoError(t, Upload(ctx, frame, yuv, w, h))

	cfg := ChromaKeyConfig{
		KeyCb:      44,
		KeyCr:      21,
		Similarity: 0.3,
		Smoothness: 0.1,
	}
	err = ChromaKey(ctx, frame, maskBuf, cfg)
	require.NoError(t, err)

	maskYUV := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, maskYUV, maskBuf, w, h))

	// Non-green frame should be mostly opaque (alpha=255)
	opaque := 0
	for i := 0; i < w*h; i++ {
		if maskYUV[i] > 192 {
			opaque++
		}
	}
	ratio := float64(opaque) / float64(w*h)
	assert.Greater(t, ratio, 0.8, "non-green pixels should be opaque: %.2f", ratio)
	t.Logf("Chroma key non-green: %.1f%% opaque", ratio*100)
}

func TestLumaKey(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	maskBuf, _ := pool.Acquire()
	defer frame.Release()
	defer maskBuf.Release()

	// Create frame with dark left (Y=20) and bright right (Y=200)
	yuv := make([]byte, w*h*3/2)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				yuv[y*w+x] = 20 // dark
			} else {
				yuv[y*w+x] = 200 // bright
			}
		}
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, frame, yuv, w, h))

	// Luma key: transparent below 50, opaque above 150
	lut := BuildLumaKeyLUT(50, 150, 10)
	err = LumaKey(ctx, frame, maskBuf, lut)
	require.NoError(t, err)

	maskYUV := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, maskYUV, maskBuf, w, h))

	// Dark side should be transparent, bright side opaque
	darkAlpha := int(maskYUV[h/2*w+w/4])   // center of dark side
	brightAlpha := int(maskYUV[h/2*w+3*w/4]) // center of bright side

	assert.Less(t, darkAlpha, 64, "dark pixels should be transparent")
	assert.Greater(t, brightAlpha, 192, "bright pixels should be opaque")
	t.Logf("Luma key: dark alpha=%d, bright alpha=%d", darkAlpha, brightAlpha)
}

func TestChromaKeyNilArgs(t *testing.T) {
	err := ChromaKey(nil, nil, nil, ChromaKeyConfig{})
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestLumaKeyNilArgs(t *testing.T) {
	err := LumaKey(nil, nil, nil, [256]byte{})
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}
