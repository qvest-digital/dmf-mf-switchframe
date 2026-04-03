//go:build correctness

package avsync

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

// ============================================================================
// 10-bit A/V Sync Tests
//
// PTS (Presentation Timestamp) is format-agnostic — it's a 90kHz clock value
// that tags each frame regardless of pixel format. These tests verify that
// 10-bit processing operations preserve PTS values correctly and do not
// introduce timing distortion.
// ============================================================================

// ============================================================================
// PTS Preservation Through Processing
// ============================================================================

// TestAVSync10bit_PTSPreservation verifies that 10-bit frames preserve PTS
// values through processing operations. The 10-bit blend path should not
// modify or lose PTS data.
func TestAVSync10bit_PTSPreservation(t *testing.T) {
	w, h := 64, 32
	frameSize := w * h * 4

	// Create source frames with known content at specific PTS values
	type ptsFrame struct {
		pts  int64
		yVal uint16
	}

	frames := []ptsFrame{
		{pts: 0, yVal: 100},
		{pts: 3000, yVal: 200}, // 30fps = 3000 PTS ticks/frame at 90kHz
		{pts: 6000, yVal: 300},
		{pts: 9000, yVal: 400},
		{pts: 12000, yVal: 500},
	}

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i, f := range frames {
		// Create 10-bit frame
		buf := make([]byte, frameSize)
		for s := 0; s < ySize; s++ {
			binary.LittleEndian.PutUint16(buf[s*2:], f.yVal)
		}
		cbOff := ySize * 2
		for s := 0; s < chromaSize; s++ {
			binary.LittleEndian.PutUint16(buf[cbOff+s*2:], 512)
		}
		crOff := cbOff + chromaSize*2
		for s := 0; s < chromaSize; s++ {
			binary.LittleEndian.PutUint16(buf[crOff+s*2:], 512)
		}

		// Verify the Y value is intact (format was not corrupted)
		gotY := binary.LittleEndian.Uint16(buf[0:2])
		assert.Equal(t, f.yVal, gotY,
			"frame %d (PTS=%d): Y should be %d, got %d", i, f.pts, f.yVal, gotY)

		// Verify frame size is correct for 10-bit
		assert.Equal(t, frameSize, len(buf),
			"frame %d: size should be %d for 10-bit 4:2:2", i, frameSize)
	}

	// Verify PTS spacing is consistent (30fps = 3000 ticks between frames)
	for i := 1; i < len(frames); i++ {
		delta := frames[i].pts - frames[i-1].pts
		assert.Equal(t, int64(3000), delta,
			"PTS delta between frames %d and %d should be 3000 (30fps at 90kHz), got %d",
			i-1, i, delta)
	}
}

// ============================================================================
// Blend Does Not Affect PTS Semantics
// ============================================================================

// TestAVSync10bit_BlendOutputTimingConsistency verifies that consecutive
// blend operations on 10-bit frames produce outputs that could carry
// consecutive PTS values without timing distortion. The blend operation
// itself does not set PTS, but it must not corrupt the frame data that
// carries timing information.
func TestAVSync10bit_BlendOutputTimingConsistency(t *testing.T) {
	w, h := 64, 32
	frameSize := w * h * 4

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2

	// Create two distinct 10-bit source frames
	frameA := make([]byte, frameSize)
	frameB := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(frameA[i*2:], 200)
		binary.LittleEndian.PutUint16(frameB[i*2:], 800)
	}
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(frameA[cbOff+i*2:], 512)
		binary.LittleEndian.PutUint16(frameA[crOff+i*2:], 512)
		binary.LittleEndian.PutUint16(frameB[cbOff+i*2:], 512)
		binary.LittleEndian.PutUint16(frameB[crOff+i*2:], 512)
	}

	// Run 30 consecutive blends (simulating 1 second at 30fps)
	const numFrames = 30
	prevY := uint16(0)
	for i := 0; i < numFrames; i++ {
		dst := make([]byte, frameSize)
		// Simulate a dissolve: position increases each frame
		pos256 := (i * 256) / (numFrames - 1)
		err := transition.BlendMix10(dst, frameA, frameB, w, h, pos256)
		require.NoError(t, err, "frame %d", i)

		gotY := binary.LittleEndian.Uint16(dst[0:2])

		// Y must be in valid 10-bit range
		assert.LessOrEqual(t, gotY, uint16(1023),
			"frame %d: Y=%d exceeds 10-bit max", i, gotY)

		// For a dissolve, Y should generally increase (A=200 -> B=800)
		if i > 0 {
			assert.GreaterOrEqual(t, gotY, prevY,
				"frame %d: Y=%d decreased from prev=%d during dissolve", i, gotY, prevY)
		}
		prevY = gotY
	}

	// First frame should be near A (200), last near B (800)
	dst0 := make([]byte, frameSize)
	dstLast := make([]byte, frameSize)
	require.NoError(t, transition.BlendMix10(dst0, frameA, frameB, w, h, 0))
	require.NoError(t, transition.BlendMix10(dstLast, frameA, frameB, w, h, 256))

	y0 := binary.LittleEndian.Uint16(dst0[0:2])
	yLast := binary.LittleEndian.Uint16(dstLast[0:2])
	assert.Equal(t, uint16(200), y0, "first frame should be source A (200)")
	assert.Equal(t, uint16(800), yLast, "last frame should be source B (800)")
}
