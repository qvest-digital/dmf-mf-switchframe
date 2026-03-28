//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfNoUnifiedMemory skips the test if UploadYUV420PToP210 is unavailable
// (CUDA — requires unified memory which is Metal-only).
func skipIfNoUnifiedMemory(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "not supported on CUDA") {
		t.Skip("UploadYUV420PToP210 requires unified memory (Metal only)")
	}
}

func TestUploadYUV420PToP210_UniformMidGray(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	// Fill Y=128, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		yuv[i] = 128
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = 128
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = 128
	}

	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = UploadYUV420PToP210(ctx, frame, yuv, w, h)
	skipIfNoUnifiedMemory(t, err)
	require.NoError(t, err)

	// Read back P210 data via Download10 (planar YUV422P10LE)
	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))

	// Check Y plane: every sample should be 128<<2 = 512
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			off := (row*w + col) * 2
			val := binary.LittleEndian.Uint16(buf[off:])
			if val != 512 {
				t.Fatalf("Y[%d][%d] = %d, want 512", row, col, val)
			}
		}
	}

	// Check chroma planes: Cb=512, Cr=512 at full height (4:2:2)
	chromaW := w / 2
	yPlaneSize := w * h * 2
	cbPlaneSize := chromaW * h * 2
	for row := 0; row < h; row++ {
		for col := 0; col < chromaW; col++ {
			cbOff := yPlaneSize + (row*chromaW+col)*2
			crOff := yPlaneSize + cbPlaneSize + (row*chromaW+col)*2
			cb := binary.LittleEndian.Uint16(buf[cbOff:])
			cr := binary.LittleEndian.Uint16(buf[crOff:])
			if cb != 512 {
				t.Fatalf("Cb[%d][%d] = %d, want 512", row, col, cb)
			}
			if cr != 512 {
				t.Fatalf("Cr[%d][%d] = %d, want 512", row, col, cr)
			}
		}
	}
}

func TestUploadYUV420PToP210_ChromaUpsample(t *testing.T) {
	ctx := testContext(t)

	w, h := 4, 4
	ySize := w * h
	cbSize := (w / 2) * (h / 2) // 2x2 = 4 bytes
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	// Y = 64 everywhere
	for i := 0; i < ySize; i++ {
		yuv[i] = 64
	}
	// Cb = 100, Cr = 200 (half-height: 2 rows of chroma)
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = 100
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = 200
	}

	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = UploadYUV420PToP210(ctx, frame, yuv, w, h)
	skipIfNoUnifiedMemory(t, err)
	require.NoError(t, err)

	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))

	// Y plane: 64<<2 = 256
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			off := (row*w + col) * 2
			val := binary.LittleEndian.Uint16(buf[off:])
			assert.Equal(t, uint16(256), val, "Y[%d][%d]", row, col)
		}
	}

	// Chroma planes: full height (4 rows), Cb=100<<2=400, Cr=200<<2=800
	chromaW := w / 2
	yPlaneSize := w * h * 2
	cbPlaneSize := chromaW * h * 2
	for row := 0; row < h; row++ {
		for col := 0; col < chromaW; col++ {
			cbOff := yPlaneSize + (row*chromaW+col)*2
			crOff := yPlaneSize + cbPlaneSize + (row*chromaW+col)*2
			cb := binary.LittleEndian.Uint16(buf[cbOff:])
			cr := binary.LittleEndian.Uint16(buf[crOff:])
			assert.Equal(t, uint16(400), cb, "Cb[%d][%d]", row, col)
			assert.Equal(t, uint16(800), cr, "Cr[%d][%d]", row, col)
		}
	}
}

func TestUploadYUV420PToP210_BlackFrame(t *testing.T) {
	ctx := testContext(t)

	w, h := 16, 16
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	// BT.709 limited-range black: Y=16, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		yuv[i] = 16
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = 128
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = 128
	}

	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = UploadYUV420PToP210(ctx, frame, yuv, w, h)
	skipIfNoUnifiedMemory(t, err)
	require.NoError(t, err)

	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))

	// Y: 16<<2 = 64
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			off := (i*w + j) * 2
			val := binary.LittleEndian.Uint16(buf[off:])
			assert.Equal(t, uint16(64), val, "Y[%d][%d]", i, j)
		}
	}

	// Chroma: Cb=512, Cr=512 at full height
	chromaW := w / 2
	yPlaneSize := w * h * 2
	cbPlaneSize := chromaW * h * 2
	for i := 0; i < h; i++ {
		for j := 0; j < chromaW; j++ {
			cbOff := yPlaneSize + (i*chromaW+j)*2
			crOff := yPlaneSize + cbPlaneSize + (i*chromaW+j)*2
			assert.Equal(t, uint16(512), binary.LittleEndian.Uint16(buf[cbOff:]), "Cb[%d][%d]", i, j)
			assert.Equal(t, uint16(512), binary.LittleEndian.Uint16(buf[crOff:]), "Cr[%d][%d]", i, j)
		}
	}
}

func TestUploadYUV420PToP210_MaxWhite(t *testing.T) {
	ctx := testContext(t)

	w, h := 16, 16
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	// BT.709 max white: Y=235, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		yuv[i] = 235
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = 128
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = 128
	}

	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = UploadYUV420PToP210(ctx, frame, yuv, w, h)
	skipIfNoUnifiedMemory(t, err)
	require.NoError(t, err)

	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))

	// Y: 235<<2 = 940
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			off := (i*w + j) * 2
			val := binary.LittleEndian.Uint16(buf[off:])
			assert.Equal(t, uint16(940), val, "Y[%d][%d]", i, j)
		}
	}

	// Chroma: Cb=512, Cr=512 at full height
	chromaW := w / 2
	yPlaneSize := w * h * 2
	cbPlaneSize := chromaW * h * 2
	for i := 0; i < h; i++ {
		for j := 0; j < chromaW; j++ {
			cbOff := yPlaneSize + (i*chromaW+j)*2
			crOff := yPlaneSize + cbPlaneSize + (i*chromaW+j)*2
			assert.Equal(t, uint16(512), binary.LittleEndian.Uint16(buf[cbOff:]), "Cb[%d][%d]", i, j)
			assert.Equal(t, uint16(512), binary.LittleEndian.Uint16(buf[crOff:]), "Cr[%d][%d]", i, j)
		}
	}
}

func TestUploadYUV420PToP210_BufferTooSmall(t *testing.T) {
	ctx := testContext(t)

	w, h := 16, 16
	pool, err := NewP210FramePool(ctx, w, h, 1)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	tooSmall := make([]byte, 100)
	err = UploadYUV420PToP210(ctx, frame, tooSmall, w, h)
	// On Metal: "too small" error. On CUDA: "not supported" error. Both are errors.
	assert.Error(t, err)
}

func TestUploadYUV420PToP210_NilContext(t *testing.T) {
	err := UploadYUV420PToP210(nil, &GPUFrame{}, make([]byte, 1024), 16, 16)
	// On Metal: ErrGPUNotAvailable. On CUDA: "not supported" error. Both must error.
	assert.Error(t, err)
}

func TestUploadYUV420PToP210_DimensionsTooSmall(t *testing.T) {
	ctx := testContext(t)
	pool, err := NewP210FramePool(ctx, 16, 16, 1)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = UploadYUV420PToP210(ctx, frame, make([]byte, 4096), 1, 1)
	// On Metal: ">= 2x2" error. On CUDA: "not supported" error. Both are errors.
	assert.Error(t, err)
}
