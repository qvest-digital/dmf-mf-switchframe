//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"
	"unsafe"

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

	// Read back and verify Y=64, UV Cb=512, Cr=512
	data := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize())
	samplesPerRow := int(frame.Pitch / 2)

	// Check Y plane
	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			off := (row*samplesPerRow + col) * 2
			val := binary.LittleEndian.Uint16(data[off:])
			if val != 64 {
				t.Fatalf("Y[%d,%d] = %d, want 64", row, col, val)
			}
		}
	}

	// Check UV plane (starts at pitch * height bytes)
	uvOff := int(frame.Pitch) * 8
	for row := 0; row < 8; row++ { // full height for 4:2:2
		for col := 0; col < 4; col++ { // width/2 chroma pairs
			cbOff := uvOff + (row*samplesPerRow+col*2)*2
			crOff := uvOff + (row*samplesPerRow+col*2+1)*2
			cb := binary.LittleEndian.Uint16(data[cbOff:])
			cr := binary.LittleEndian.Uint16(data[crOff:])
			if cb != 512 {
				t.Fatalf("UV[%d,%d].Cb = %d, want 512", row, col, cb)
			}
			if cr != 512 {
				t.Fatalf("UV[%d,%d].Cr = %d, want 512", row, col, cr)
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
	data := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize())
	val := binary.LittleEndian.Uint16(data[0:])
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

	// Verify it produced NV12 black (Y=16)
	data := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize())
	assert.Equal(t, byte(16), data[0], "dispatch NV12 Y[0,0] = %d, want 16", data[0])
}

func TestFillBlackP210_NilContext(t *testing.T) {
	err := FillBlackP210(nil, &GPUFrame{})
	assert.Equal(t, ErrGPUNotAvailable, err)
}

func TestFillColorP210_NilContext(t *testing.T) {
	err := FillColorP210(nil, &GPUFrame{}, 64, 512, 512)
	assert.Equal(t, ErrGPUNotAvailable, err)
}
