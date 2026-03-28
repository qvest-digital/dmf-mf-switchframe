//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFillBlackP210(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, 8, 8, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = FillBlackP210(ctx, frame)
	require.NoError(t, err)

	// Read back via Download10 (planar YUV422P10LE) and verify Y=64, Cb=512, Cr=512
	w, h := 8, 8
	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))

	// Check Y plane
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			off := (row*w + col) * 2
			val := binary.LittleEndian.Uint16(buf[off:])
			if val != 64 {
				t.Fatalf("Y[%d,%d] = %d, want 64", row, col, val)
			}
		}
	}

	// Check Cb and Cr planes (separate in planar layout)
	chromaW := w / 2
	yPlaneSize := w * h * 2
	cbPlaneSize := chromaW * h * 2
	for row := 0; row < h; row++ { // full height for 4:2:2
		for col := 0; col < chromaW; col++ {
			cbOff := yPlaneSize + (row*chromaW+col)*2
			crOff := yPlaneSize + cbPlaneSize + (row*chromaW+col)*2
			cb := binary.LittleEndian.Uint16(buf[cbOff:])
			cr := binary.LittleEndian.Uint16(buf[crOff:])
			if cb != 512 {
				t.Fatalf("Cb[%d,%d] = %d, want 512", row, col, cb)
			}
			if cr != 512 {
				t.Fatalf("Cr[%d,%d] = %d, want 512", row, col, cr)
			}
		}
	}
}

func TestFillColorP210(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, 16, 8, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Fill with custom color: Y=300, Cb=400, Cr=600
	err = FillColorP210(ctx, frame, 300, 400, 600)
	require.NoError(t, err)

	// Download via planar API and verify
	dst := make([]byte, p210DownloadSize(16, 8))
	err = Download10(ctx, frame, dst, 16, 8)
	require.NoError(t, err)

	gotY := readP210YSample(dst, 0)
	assert.Equal(t, uint16(300), gotY, "FillColorP210 Y: got %d, want 300", gotY)

	gotCb := readP210CbSample(dst, 16, 8, 0)
	assert.Equal(t, uint16(400), gotCb, "FillColorP210 Cb: got %d, want 400", gotCb)

	gotCr := readP210CrSample(dst, 16, 8, 0)
	assert.Equal(t, uint16(600), gotCr, "FillColorP210 Cr: got %d, want 600", gotCr)
}

func TestGPUFillBlackDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, 8, 8, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Dispatch should route to P210 variant
	err = GPUFillBlackDispatch(ctx, frame)
	require.NoError(t, err)

	// Verify it produced P210 black (Y=64), not NV12 black (Y=16)
	w, h := 8, 8
	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))

	val := binary.LittleEndian.Uint16(buf[0:])
	assert.Equal(t, uint16(64), val, "dispatch Y[0,0] = %d, want 64 (P210 black)", val)
}

func TestGPUFillBlackDispatch_NV12(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewFramePool(ctx, 8, 8, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Dispatch should route to NV12 variant
	err = GPUFillBlackDispatch(ctx, frame)
	require.NoError(t, err)

	// Verify it produced NV12 black (Y=16) via Download (NV12 -> YUV420p)
	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)
	require.NoError(t, Download(ctx, yuv, frame, w, h))

	assert.Equal(t, byte(16), yuv[0], "dispatch NV12 Y[0,0] = %d, want 16", yuv[0])
}

func TestFillBlackP210_NilContext(t *testing.T) {
	err := FillBlackP210(nil, &GPUFrame{})
	assert.Equal(t, ErrGPUNotAvailable, err)
}

func TestFillColorP210_NilContext(t *testing.T) {
	err := FillColorP210(nil, &GPUFrame{}, 64, 512, 512)
	assert.Equal(t, ErrGPUNotAvailable, err)
}
