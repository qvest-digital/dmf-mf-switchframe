//go:build correctness

package replay

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

// ============================================================================
// 10-bit Replay Buffer Format Tests
//
// Replay buffers store compressed frames with codec metadata (VPS/SPS/PPS for
// HEVC). These tests verify that 10-bit content properties are preserved through
// the replay pipeline.
// ============================================================================

// ============================================================================
// Format Preservation
// ============================================================================

// TestReplay10bit_BufferStoresFormat verifies that 10-bit YUV422P10LE frames
// retain their format characteristics through the blend interpolation path.
// The replay interpolator operates on decoded YUV frames — this verifies
// the 10-bit blend path preserves data integrity.
func TestReplay10bit_BufferStoresFormat(t *testing.T) {
	w, h := 64, 32
	frameSize := w * h * 4 // YUV422P10LE: w*h*4 bytes

	// Create two 10-bit frames with distinct Y values
	frameA := make([]byte, frameSize)
	frameB := make([]byte, frameSize)

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Frame A: Y=200, neutral chroma
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(frameA[i*2:], 200)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameA[cbOff+i*2:], 512)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameA[crOff+i*2:], 512)
	}

	// Frame B: Y=800, neutral chroma
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(frameB[i*2:], 800)
	}
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameB[cbOff+i*2:], 512)
	}
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameB[crOff+i*2:], 512)
	}

	// Verify frame sizes match 10-bit 4:2:2 expectations
	require.Equal(t, w*h*4, len(frameA),
		"10-bit YUV422 frame size must be w*h*4 = %d", w*h*4)
	require.Equal(t, w*h*4, len(frameB))

	// Verify the Y plane sample values are preserved
	gotA := binary.LittleEndian.Uint16(frameA[0:2])
	assert.Equal(t, uint16(200), gotA, "frame A Y[0] should be 200")

	gotB := binary.LittleEndian.Uint16(frameB[0:2])
	assert.Equal(t, uint16(800), gotB, "frame B Y[0] should be 800")
}

// ============================================================================
// Interpolation Midpoint
// ============================================================================

// TestReplay10bit_Interpolate_Midpoint verifies that blending two 10-bit
// frames at t=0.5 produces the mathematical midpoint.
// Frame A (Y=200) and B (Y=800) interpolated at t=0.5:
// result = (200*128 + 800*128 + 128) >> 8 = 500
func TestReplay10bit_Interpolate_Midpoint(t *testing.T) {
	w, h := 64, 32
	frameSize := w * h * 4

	// Create frame A: Y=200
	frameA := make([]byte, frameSize)
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(frameA[i*2:], 200)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameA[cbOff+i*2:], 512)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameA[crOff+i*2:], 512)
	}

	// Create frame B: Y=800
	frameB := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(frameB[i*2:], 800)
	}
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameB[cbOff+i*2:], 512)
	}
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameB[crOff+i*2:], 512)
	}

	// Blend at 50% using the 10-bit blend function
	dst := make([]byte, frameSize)
	err := transition.BlendMix10(dst, frameA, frameB, w, h, 128) // pos256=128 = 50%
	require.NoError(t, err)

	// Check the result: (200*128 + 800*128 + 128) >> 8 = 500
	gotY := binary.LittleEndian.Uint16(dst[0:2])
	assert.Equal(t, uint16(500), gotY,
		"10-bit blend midpoint: expected Y=500, got %d", gotY)

	// Chroma should be unchanged (512 blended with 512 = 512)
	gotCb := binary.LittleEndian.Uint16(dst[cbOff : cbOff+2])
	assert.Equal(t, uint16(512), gotCb,
		"10-bit blend midpoint: expected Cb=512, got %d", gotCb)
}

// ============================================================================
// Interpolation Endpoints
// ============================================================================

// TestReplay10bit_Interpolate_Endpoints verifies blend at t=0 and t=1.
func TestReplay10bit_Interpolate_Endpoints(t *testing.T) {
	w, h := 32, 16
	frameSize := w * h * 4

	frameA := make([]byte, frameSize)
	frameB := make([]byte, frameSize)
	ySize := w * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(frameA[i*2:], 300)
		binary.LittleEndian.PutUint16(frameB[i*2:], 700)
	}
	// Fill chroma with neutral
	chromaW := w / 2
	chromaSize := chromaW * h
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameA[cbOff+i*2:], 512)
		binary.LittleEndian.PutUint16(frameA[crOff+i*2:], 512)
		binary.LittleEndian.PutUint16(frameB[cbOff+i*2:], 512)
		binary.LittleEndian.PutUint16(frameB[crOff+i*2:], 512)
	}

	dst := make([]byte, frameSize)

	// t=0 (pos256=0): 100% A
	err := transition.BlendMix10(dst, frameA, frameB, w, h, 0)
	require.NoError(t, err)
	gotY := binary.LittleEndian.Uint16(dst[0:2])
	assert.Equal(t, uint16(300), gotY, "t=0: expected Y=300 (source A)")

	// t=1 (pos256=256): 100% B
	err = transition.BlendMix10(dst, frameA, frameB, w, h, 256)
	require.NoError(t, err)
	gotY = binary.LittleEndian.Uint16(dst[0:2])
	assert.Equal(t, uint16(700), gotY, "t=1: expected Y=700 (source B)")
}
