//go:build correctness

package playout

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/transition"
)

// ============================================================================
// 10-bit Playout Correctness Tests
//
// These tests verify that the playout engine produces correct 10-bit
// YUV422P10LE output according to BT.709 color science specifications.
// ============================================================================

// ============================================================================
// Idle Frame Is BT.709 Black
// ============================================================================

// TestPlayout10bit_IdleFrameIsBlack verifies that NewIdleFrame10 produces a
// frame with BT.709 10-bit limited-range black: Y=64, Cb=512, Cr=512.
//
// Per ITU-R BT.709:
// - 10-bit limited range Y black = 64 (NOT 0, NOT 16)
// - 10-bit neutral chroma = 512 (NOT 128, NOT 0)
func TestPlayout10bit_IdleFrameIsBlack(t *testing.T) {
	w, h := 320, 240
	idle := playout.NewIdleFrame10(w, h)

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Check all Y samples = 64 (BT.709 10-bit limited range black)
	for i := 0; i < ySize; i++ {
		val := binary.LittleEndian.Uint16(idle[i*2:])
		if val != 64 {
			t.Errorf("Y[%d] = %d, want 64 (BT.709 10-bit black)", i, val)
			break
		}
	}

	// Check all Cb samples = 512 (10-bit neutral chroma)
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		val := binary.LittleEndian.Uint16(idle[cbOff+i*2:])
		if val != 512 {
			t.Errorf("Cb[%d] = %d, want 512 (10-bit neutral chroma)", i, val)
			break
		}
	}

	// Check all Cr samples = 512
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		val := binary.LittleEndian.Uint16(idle[crOff+i*2:])
		if val != 512 {
			t.Errorf("Cr[%d] = %d, want 512 (10-bit neutral chroma)", i, val)
			break
		}
	}

	// Verify common wrong values are NOT present
	firstY := binary.LittleEndian.Uint16(idle[0:2])
	assert.NotEqual(t, uint16(0), firstY, "Y must not be full-range black (0)")
	assert.NotEqual(t, uint16(16), firstY, "Y must not be 8-bit black (16)")
	assert.NotEqual(t, uint16(640), firstY, "Y must not be wrongly scaled 8-bit black (16*40)")

	firstCb := binary.LittleEndian.Uint16(idle[cbOff : cbOff+2])
	assert.NotEqual(t, uint16(128), firstCb, "Cb must not be 8-bit neutral (128)")
	assert.NotEqual(t, uint16(0), firstCb, "Cb must not be 0")
}

// ============================================================================
// Blend Transition
// ============================================================================

// TestPlayout10bit_BlendTransition verifies BlendPlayout10 at the midpoint
// between two frames.
//
// Frame A: Y=200, Frame B: Y=800
// At pos256=128 (50%): (200*128 + 800*128 + 128) >> 8 = 500
func TestPlayout10bit_BlendTransition(t *testing.T) {
	w, h := 64, 32
	frameSize := w * h * 4

	frameA := make10Frame(w, h, 200, 512, 512)
	frameB := make10Frame(w, h, 800, 512, 512)
	dst := make([]byte, frameSize)

	err := playout.BlendPlayout10(dst, frameA, frameB, w, h, 128)
	require.NoError(t, err)

	gotY := binary.LittleEndian.Uint16(dst[0:2])
	assert.Equal(t, uint16(500), gotY,
		"blend at 50%%: Y should be 500 (midpoint of 200 and 800), got %d", gotY)

	// Chroma should be unchanged
	cbOff := w * h * 2
	gotCb := binary.LittleEndian.Uint16(dst[cbOff : cbOff+2])
	assert.Equal(t, uint16(512), gotCb,
		"blend at 50%%: Cb should remain 512, got %d", gotCb)
}

// TestPlayout10bit_BlendEndpoints verifies blend endpoints.
func TestPlayout10bit_BlendEndpoints(t *testing.T) {
	w, h := 32, 16
	frameSize := w * h * 4

	frameA := make10Frame(w, h, 300, 400, 600)
	frameB := make10Frame(w, h, 700, 200, 800)
	dst := make([]byte, frameSize)

	// pos256=0: 100% A
	err := playout.BlendPlayout10(dst, frameA, frameB, w, h, 0)
	require.NoError(t, err)
	gotY := binary.LittleEndian.Uint16(dst[0:2])
	assert.Equal(t, uint16(300), gotY, "pos=0: Y should equal A=300")

	// pos256=256: 100% B
	err = playout.BlendPlayout10(dst, frameA, frameB, w, h, 256)
	require.NoError(t, err)
	gotY = binary.LittleEndian.Uint16(dst[0:2])
	assert.Equal(t, uint16(700), gotY, "pos=256: Y should equal B=700")
}

// ============================================================================
// Frame Size
// ============================================================================

// TestPlayout10bit_FrameSize verifies the idle frame size is exactly w*h*4 bytes.
// YUV422P10LE: Y(w*h*2) + Cb(w/2*h*2) + Cr(w/2*h*2) = w*h*4
// This must NOT be w*h*3/2 (8-bit 4:2:0).
func TestPlayout10bit_FrameSize(t *testing.T) {
	testCases := []struct {
		w, h int
	}{
		{320, 240},
		{1920, 1080},
		{64, 32},
	}

	for _, tc := range testCases {
		idle := playout.NewIdleFrame10(tc.w, tc.h)

		expectedSize := tc.w * tc.h * 4
		assert.Equal(t, expectedSize, len(idle),
			"%dx%d: idle frame size should be %d (w*h*4), got %d",
			tc.w, tc.h, expectedSize, len(idle))

		// Must NOT be 8-bit 4:2:0 size
		wrongSize8bit := tc.w*tc.h + (tc.w/2)*(tc.h/2)*2
		assert.NotEqual(t, wrongSize8bit, len(idle),
			"%dx%d: idle frame must not have 8-bit YUV420 size (%d)",
			tc.w, tc.h, wrongSize8bit)
	}
}

// ============================================================================
// FTB Produces Correct Black
// ============================================================================

// TestPlayout10bit_FTBProducesBlack verifies the FTB blend at full position
// produces BT.709 10-bit black.
func TestPlayout10bit_FTBProducesBlack(t *testing.T) {
	w, h := 64, 32
	frameSize := w * h * 4

	src := make10Frame(w, h, 940, 300, 700) // white source
	dst := make([]byte, frameSize)

	// FTB at pos256=256 (fully black, limited range)
	err := transition.BlendFTB10(dst, src, w, h, 256, true)
	require.NoError(t, err)

	gotY := binary.LittleEndian.Uint16(dst[0:2])
	assert.Equal(t, uint16(64), gotY,
		"FTB 10-bit limited range: Y should be 64, got %d", gotY)

	cbOff := w * h * 2
	gotCb := binary.LittleEndian.Uint16(dst[cbOff : cbOff+2])
	assert.Equal(t, uint16(512), gotCb,
		"FTB: Cb should be neutral 512, got %d", gotCb)
}

// ============================================================================
// Helpers
// ============================================================================

// make10Frame creates a uniform YUV422P10LE frame.
func make10Frame(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, w*h*4)
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cb)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}
	return buf
}
