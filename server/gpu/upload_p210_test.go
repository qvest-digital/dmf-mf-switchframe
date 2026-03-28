//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// asUint16Slice reinterprets a byte slice as a little-endian uint16 slice.
func asUint16Slice(b []byte) []uint16 {
	return unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2)
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
	require.NoError(t, err)

	// Read back P210 data from unified memory
	raw := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize())
	dstStrideU16 := frame.Pitch / 2

	// Check Y plane: every sample should be 128<<2 = 512
	yPlane := asUint16Slice(raw[:frame.Pitch*h])
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			val := yPlane[row*dstStrideU16+col]
			if val != 512 {
				t.Fatalf("Y[%d][%d] = %d, want 512", row, col, val)
			}
		}
	}

	// Check UV plane: Cb=512, Cr=512 at full height (4:2:2)
	uvStart := frame.Pitch * h
	uvPlane := asUint16Slice(raw[uvStart : uvStart+frame.Pitch*h])
	chromaW := w / 2
	for row := 0; row < h; row++ {
		for col := 0; col < chromaW; col++ {
			cb := uvPlane[row*dstStrideU16+col*2]
			cr := uvPlane[row*dstStrideU16+col*2+1]
			if cb != 512 {
				t.Fatalf("UV Cb[%d][%d] = %d, want 512", row, col, cb)
			}
			if cr != 512 {
				t.Fatalf("UV Cr[%d][%d] = %d, want 512", row, col, cr)
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
	require.NoError(t, err)

	raw := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize())
	dstStrideU16 := frame.Pitch / 2

	// Y plane: 64<<2 = 256
	yPlane := asUint16Slice(raw[:frame.Pitch*h])
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			val := yPlane[row*dstStrideU16+col]
			assert.Equal(t, uint16(256), val, "Y[%d][%d]", row, col)
		}
	}

	// UV plane: full height (4 rows), Cb=100<<2=400, Cr=200<<2=800
	uvStart := frame.Pitch * h
	uvPlane := asUint16Slice(raw[uvStart : uvStart+frame.Pitch*h])
	chromaW := w / 2
	for row := 0; row < h; row++ {
		for col := 0; col < chromaW; col++ {
			cb := uvPlane[row*dstStrideU16+col*2]
			cr := uvPlane[row*dstStrideU16+col*2+1]
			assert.Equal(t, uint16(400), cb, "UV Cb[%d][%d]", row, col)
			assert.Equal(t, uint16(800), cr, "UV Cr[%d][%d]", row, col)
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
	require.NoError(t, err)

	raw := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize())
	dstStrideU16 := frame.Pitch / 2

	// Y: 16<<2 = 64
	yPlane := asUint16Slice(raw[:frame.Pitch*h])
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			assert.Equal(t, uint16(64), yPlane[i*dstStrideU16+j], "Y[%d][%d]", i, j)
		}
	}

	// UV: Cb=512, Cr=512 at full height
	uvStart := frame.Pitch * h
	uvPlane := asUint16Slice(raw[uvStart : uvStart+frame.Pitch*h])
	chromaW := w / 2
	for i := 0; i < h; i++ {
		for j := 0; j < chromaW; j++ {
			assert.Equal(t, uint16(512), uvPlane[i*dstStrideU16+j*2], "Cb[%d][%d]", i, j)
			assert.Equal(t, uint16(512), uvPlane[i*dstStrideU16+j*2+1], "Cr[%d][%d]", i, j)
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
	require.NoError(t, err)

	raw := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize())
	dstStrideU16 := frame.Pitch / 2

	// Y: 235<<2 = 940
	yPlane := asUint16Slice(raw[:frame.Pitch*h])
	for i := 0; i < h; i++ {
		for j := 0; j < w; j++ {
			assert.Equal(t, uint16(940), yPlane[i*dstStrideU16+j], "Y[%d][%d]", i, j)
		}
	}

	// UV: Cb=512, Cr=512 at full height
	uvStart := frame.Pitch * h
	uvPlane := asUint16Slice(raw[uvStart : uvStart+frame.Pitch*h])
	chromaW := w / 2
	for i := 0; i < h; i++ {
		for j := 0; j < chromaW; j++ {
			assert.Equal(t, uint16(512), uvPlane[i*dstStrideU16+j*2], "Cb[%d][%d]", i, j)
			assert.Equal(t, uint16(512), uvPlane[i*dstStrideU16+j*2+1], "Cr[%d][%d]", i, j)
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
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too small")
}

func TestUploadYUV420PToP210_NilContext(t *testing.T) {
	err := UploadYUV420PToP210(nil, &GPUFrame{}, make([]byte, 1024), 16, 16)
	assert.Equal(t, ErrGPUNotAvailable, err)
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
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ">= 2x2")
}

// Suppress unused import warning for binary package.
var _ = binary.LittleEndian
