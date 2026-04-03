//go:build correctness

package playout

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/playout"
)

// ============================================================================
// 10-bit Playout Media Frame Tests
//
// These tests verify that playout media frame helpers produce correct 10-bit
// YUV422P10LE output matching BT.709 specifications.
// ============================================================================

// ============================================================================
// Black Frame Values
// ============================================================================

// TestPlayout10bit_MediaFrameBlack verifies that the black frame generator
// produces correct BT.709 10-bit limited-range values.
//
// BT.709 10-bit limited range:
//
//	Y black  = 64  (not 0, not 16)
//	Cb/Cr neutral = 512 (not 128, not 0)
func TestPlayout10bit_MediaFrameBlack(t *testing.T) {
	w, h := 128, 64

	// NewIdleFrame10 is the black frame generator
	black := playout.NewIdleFrame10(w, h)
	require.NotNil(t, black)

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Sample multiple positions across the frame
	samplePositions := []int{0, 1, ySize / 4, ySize / 2, ySize - 1}
	for _, pos := range samplePositions {
		gotY := binary.LittleEndian.Uint16(black[pos*2:])
		assert.Equal(t, uint16(64), gotY,
			"black frame Y[%d] = %d, want 64 (BT.709 10-bit limited black)", pos, gotY)
	}

	// Sample Cb
	cbOff := ySize * 2
	cbPositions := []int{0, chromaSize / 2, chromaSize - 1}
	for _, pos := range cbPositions {
		gotCb := binary.LittleEndian.Uint16(black[cbOff+pos*2:])
		assert.Equal(t, uint16(512), gotCb,
			"black frame Cb[%d] = %d, want 512 (neutral chroma)", pos, gotCb)
	}

	// Sample Cr
	crOff := cbOff + chromaSize*2
	for _, pos := range cbPositions {
		gotCr := binary.LittleEndian.Uint16(black[crOff+pos*2:])
		assert.Equal(t, uint16(512), gotCr,
			"black frame Cr[%d] = %d, want 512 (neutral chroma)", pos, gotCr)
	}
}

// ============================================================================
// Black Frame is Distinct from 8-bit Black
// ============================================================================

// TestPlayout10bit_MediaFrameNotEightBitBlack verifies the black frame does
// not accidentally use 8-bit black values (Y=16, Cb=128, Cr=128) at 10-bit
// storage width.
func TestPlayout10bit_MediaFrameNotEightBitBlack(t *testing.T) {
	w, h := 64, 32
	black := playout.NewIdleFrame10(w, h)

	// Check Y is not 16 (8-bit black stored in uint16)
	gotY := binary.LittleEndian.Uint16(black[0:2])
	assert.NotEqual(t, uint16(16), gotY,
		"10-bit black Y must not be 16 (that's 8-bit black)")

	// Check Cb is not 128 (8-bit neutral stored in uint16)
	cbOff := w * h * 2
	gotCb := binary.LittleEndian.Uint16(black[cbOff : cbOff+2])
	assert.NotEqual(t, uint16(128), gotCb,
		"10-bit neutral Cb must not be 128 (that's 8-bit neutral)")
}

// ============================================================================
// Multiple Dimensions
// ============================================================================

// TestPlayout10bit_MediaFrameMultipleDimensions verifies black frame generation
// works at various standard broadcast resolutions.
func TestPlayout10bit_MediaFrameMultipleDimensions(t *testing.T) {
	dimensions := []struct{ w, h int }{
		{320, 240},   // QVGA
		{640, 480},   // VGA
		{1280, 720},  // 720p
		{1920, 1080}, // 1080i/p
	}

	for _, d := range dimensions {
		black := playout.NewIdleFrame10(d.w, d.h)

		// Size must be exactly w*h*4
		assert.Equal(t, d.w*d.h*4, len(black),
			"%dx%d: frame size should be %d", d.w, d.h, d.w*d.h*4)

		// First Y must be 64
		gotY := binary.LittleEndian.Uint16(black[0:2])
		assert.Equal(t, uint16(64), gotY,
			"%dx%d: Y[0] should be 64", d.w, d.h)
	}
}
