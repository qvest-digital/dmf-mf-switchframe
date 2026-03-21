//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSTMapIdentityWarp(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	// Upload a gradient pattern
	yuv := make([]byte, w*h*3/2)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			yuv[y*w+x] = byte(x*219/(w-1) + 16) // horizontal gradient
		}
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, src, yuv, w, h))

	// Create identity ST map: S[x,y] = x/w, T[x,y] = y/h
	s := make([]float32, w*h)
	tCoord := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s[y*w+x] = float32(x) / float32(w-1)
			tCoord[y*w+x] = float32(y) / float32(h-1)
		}
	}

	stmap, err := UploadSTMap(ctx, s, tCoord, w, h)
	require.NoError(t, err)
	defer stmap.Free()

	// Warp with identity map — output should match input
	err = STMapWarp(ctx, dst, src, stmap)
	require.NoError(t, err)

	// Download both and compare
	srcResult := make([]byte, w*h*3/2)
	dstResult := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, srcResult, src, w, h))
	require.NoError(t, Download(ctx, dstResult, dst, w, h))

	// Check interior Y pixels match (within ±1 for floating-point rounding)
	mismatches := 0
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			diff := int(dstResult[y*w+x]) - int(srcResult[y*w+x])
			if diff < -1 || diff > 1 {
				mismatches++
			}
		}
	}
	assert.Zero(t, mismatches, "identity warp should preserve pixels (±1 rounding)")
	t.Logf("Identity warp: %d mismatches out of %d interior pixels", mismatches, (h-2)*(w-2))
}

func TestSTMapHorizontalFlip(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	// Upload left=dark, right=bright pattern
	yuv := make([]byte, w*h*3/2)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			yuv[y*w+x] = byte(x*219/(w-1) + 16)
		}
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, src, yuv, w, h))

	// Horizontal flip: S = 1-x/w, T = y/h
	s := make([]float32, w*h)
	tCoord := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s[y*w+x] = 1.0 - float32(x)/float32(w-1)
			tCoord[y*w+x] = float32(y) / float32(h-1)
		}
	}

	stmap, err := UploadSTMap(ctx, s, tCoord, w, h)
	require.NoError(t, err)
	defer stmap.Free()

	err = STMapWarp(ctx, dst, src, stmap)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// After horizontal flip: left should be bright, right should be dark
	row := h / 2
	leftY := int(result[row*w+10])
	rightY := int(result[row*w+w-11])
	assert.Greater(t, leftY, rightY, "flipped: left (%d) should be brighter than right (%d)", leftY, rightY)

	t.Logf("H-flip: left Y=%d, right Y=%d", leftY, rightY)
}

func TestSTMapAnimated(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 64, 64
	nFrames := 4

	// Create animated identity maps (each frame is identical for simplicity)
	sMaps := make([][]float32, nFrames)
	tMaps := make([][]float32, nFrames)
	for f := 0; f < nFrames; f++ {
		sMaps[f] = make([]float32, w*h)
		tMaps[f] = make([]float32, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sMaps[f][y*w+x] = float32(x) / float32(w-1)
				tMaps[f][y*w+x] = float32(y) / float32(h-1)
			}
		}
	}

	anim, err := NewGPUAnimatedSTMap(ctx, sMaps, tMaps, w, h, 30)
	require.NoError(t, err)
	defer anim.Free()

	assert.Equal(t, nFrames, anim.FrameCount())
	assert.Equal(t, w, anim.Width)
	assert.Equal(t, h, anim.Height)

	// Cycle through frames
	for i := 0; i < nFrames*2; i++ {
		frame := anim.CurrentFrame()
		require.NotNil(t, frame)
		assert.Equal(t, w, frame.Width)
		assert.Equal(t, h, frame.Height)
	}
}

func TestSTMapUploadFree(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 64, 64
	s := make([]float32, w*h)
	tCoord := make([]float32, w*h)
	for i := range s {
		s[i] = float32(i%w) / float32(w-1)
		tCoord[i] = float32(i/w) / float32(h-1)
	}

	stmap, err := UploadSTMap(ctx, s, tCoord, w, h)
	require.NoError(t, err)
	assert.NotNil(t, stmap.DevS)
	assert.NotNil(t, stmap.DevT)

	stmap.Free()
	assert.Nil(t, stmap.DevS)
	assert.Nil(t, stmap.DevT)

	// Double free should be safe
	stmap.Free()
}

func TestSTMapNilArgs(t *testing.T) {
	require.ErrorIs(t, STMapWarp(nil, nil, nil, nil), ErrGPUNotAvailable)

	_, err := UploadSTMap(nil, nil, nil, 0, 0)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestSTMapDimensionMismatch(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool320, err := NewFramePool(ctx, 320, 240, 2)
	require.NoError(t, err)
	defer pool320.Close()

	src, _ := pool320.Acquire()
	dst, _ := pool320.Acquire()
	defer src.Release()
	defer dst.Release()

	// ST map at different resolution
	s := make([]float32, 64*64)
	tCoord := make([]float32, 64*64)
	stmap, err := UploadSTMap(ctx, s, tCoord, 64, 64)
	require.NoError(t, err)
	defer stmap.Free()

	err = STMapWarp(ctx, dst, src, stmap)
	require.Error(t, err, "should reject dimension mismatch")
}
