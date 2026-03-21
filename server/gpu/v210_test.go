//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// packV210Group packs 6 Y + 3 Cb + 3 Cr values (8-bit) into 4 V210 words.
func packV210Group(y [6]byte, cb [3]byte, cr [3]byte) [4]uint32 {
	// Shift 8-bit to 10-bit
	Y := [6]uint32{}
	for i := range y {
		Y[i] = uint32(y[i]) << 2
	}
	Cb := [3]uint32{}
	Cr := [3]uint32{}
	for i := range cb {
		Cb[i] = uint32(cb[i]) << 2
		Cr[i] = uint32(cr[i]) << 2
	}

	return [4]uint32{
		(Cb[0] & 0x3FF) | ((Y[0] & 0x3FF) << 10) | ((Cr[0] & 0x3FF) << 20),
		(Y[1] & 0x3FF) | ((Cb[1] & 0x3FF) << 10) | ((Y[2] & 0x3FF) << 20),
		(Cr[1] & 0x3FF) | ((Y[3] & 0x3FF) << 10) | ((Cb[2] & 0x3FF) << 20),
		(Y[4] & 0x3FF) | ((Cr[2] & 0x3FF) << 10) | ((Y[5] & 0x3FF) << 20),
	}
}

func TestV210UploadDownloadRoundTrip(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	// 12 pixels wide (2 V210 groups), 4 rows tall (even)
	w, h := 12, 4
	v210Stride := V210LineStride(w)

	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Build V210 test data: uniform mid-gray (Y=128, Cb=128, Cr=128)
	v210In := make([]byte, v210Stride*h)
	words := packV210Group(
		[6]byte{128, 128, 128, 128, 128, 128},
		[3]byte{128, 128, 128},
		[3]byte{128, 128, 128},
	)
	for row := 0; row < h; row++ {
		for g := 0; g < w/6; g++ {
			off := row*v210Stride + g*16
			for i, w := range words {
				v210In[off+i*4] = byte(w)
				v210In[off+i*4+1] = byte(w >> 8)
				v210In[off+i*4+2] = byte(w >> 16)
				v210In[off+i*4+3] = byte(w >> 24)
			}
		}
	}

	// Upload V210 → NV12 on GPU
	err = UploadV210(ctx, frame, v210In, w, h)
	require.NoError(t, err)

	// Download NV12 → YUV420p to verify
	yuv := make([]byte, w*h*3/2)
	err = Download(ctx, yuv, frame, w, h)
	require.NoError(t, err)

	// Y should be ~128 (10-bit 512 >> 2 = 128)
	assert.InDelta(t, 128, int(yuv[0]), 2, "Y[0] should be ~128")
	assert.InDelta(t, 128, int(yuv[w+1]), 2, "Y[1,1] should be ~128")

	// Cb/Cr should be ~128
	cbOffset := w * h
	assert.InDelta(t, 128, int(yuv[cbOffset]), 2, "Cb should be ~128")

	t.Logf("V210 round-trip: Y[0]=%d, Cb[0]=%d", yuv[0], yuv[cbOffset])
}

func TestV210GradientRoundTrip(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	// 1920x1080 is a realistic size (must be divisible by 6)
	w, h := 1920, 1080
	v210Stride := V210LineStride(w)

	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Build V210 with horizontal Y gradient, neutral chroma
	v210In := make([]byte, v210Stride*h)
	for row := 0; row < h; row++ {
		for g := 0; g < w/6; g++ {
			px := g * 6
			y := [6]byte{}
			for i := 0; i < 6; i++ {
				y[i] = byte(((px + i) * 219 / (w - 1)) + 16)
			}
			words := packV210Group(y, [3]byte{128, 128, 128}, [3]byte{128, 128, 128})
			off := row*v210Stride + g*16
			for i, w := range words {
				v210In[off+i*4] = byte(w)
				v210In[off+i*4+1] = byte(w >> 8)
				v210In[off+i*4+2] = byte(w >> 16)
				v210In[off+i*4+3] = byte(w >> 24)
			}
		}
	}

	err = UploadV210(ctx, frame, v210In, w, h)
	require.NoError(t, err)

	// Download as YUV420p and check gradient
	yuv := make([]byte, w*h*3/2)
	err = Download(ctx, yuv, frame, w, h)
	require.NoError(t, err)

	// Check Y gradient: left should be dark, right should be bright
	leftY := int(yuv[h/2*w+10])
	rightY := int(yuv[h/2*w+w-11])
	assert.Greater(t, rightY, leftY, "right should be brighter than left")
	t.Logf("V210 gradient: left Y=%d, right Y=%d", leftY, rightY)
}

func TestV210NV12ToV210RoundTrip(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 12, 4
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Upload a known YUV420p pattern
	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = 180 // Y
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 100 // Cb/Cr
	}
	require.NoError(t, Upload(ctx, frame, yuv, w, h))

	// Download as V210
	v210Stride := V210LineStride(w)
	v210Out := make([]byte, v210Stride*h)
	err = DownloadV210(ctx, v210Out, frame, w, h)
	require.NoError(t, err)

	// Verify V210 is non-zero (has data)
	nonZero := 0
	for _, b := range v210Out[:64] {
		if b != 0 {
			nonZero++
		}
	}
	assert.Greater(t, nonZero, 10, "V210 output should contain data")
}

func TestV210NilArgs(t *testing.T) {
	require.ErrorIs(t, UploadV210(nil, nil, nil, 0, 0), ErrGPUNotAvailable)
	require.ErrorIs(t, DownloadV210(nil, nil, nil, 0, 0), ErrGPUNotAvailable)
}

func TestV210LineStrideCalc(t *testing.T) {
	// 1920 pixels: 320 groups * 16 = 5120 bytes, already 128-aligned
	assert.Equal(t, 5120, V210LineStride(1920))
	// 12 pixels: 2 groups * 16 = 32 → round to 128
	assert.Equal(t, 128, V210LineStride(12))
}
