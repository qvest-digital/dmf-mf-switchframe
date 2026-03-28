package switcher

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/graphics"
)

// --- Fix 5: DSK compositor 10-bit processing ---

// TestCompositorNode_10bit_ProcessesOverlay verifies that the DSK graphics
// compositor actually processes 10-bit frames when an overlay is active,
// rather than passing through unchanged.
func TestCompositorNode_10bit_ProcessesOverlay(t *testing.T) {
	c := graphics.NewCompositor()

	// Create a white overlay (fully opaque white).
	w, h := 16, 16
	rgba := make([]byte, w*h*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255   // R
		rgba[i+1] = 255 // G
		rgba[i+2] = 255 // B
		rgba[i+3] = 255 // A (fully opaque)
	}

	// Add a layer with the overlay.
	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id, rgba, w, h, "test"))
	require.NoError(t, c.On(id))

	n := &compositorNode{compositor: c}

	// Create a 10-bit frame with all-black Y values (64 = BT.709 limited black).
	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	data := make([]byte, frameSize)
	// Fill Y plane with 64 (10-bit black)
	yPlaneSize := w * h * 2
	for i := 0; i < yPlaneSize; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 64) // black
	}
	// Fill chroma with neutral (512)
	for i := yPlaneSize; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 512)
	}

	frame := &ProcessingFrame{
		Data:   data,
		Width:  w,
		Height: h,
		Format: ColorYUV422_10bit,
	}

	out := n.Process(nil, frame)
	require.Same(t, frame, out)

	// The overlay is white, so Y values should be significantly above black (64).
	// White in 10-bit BT.709: Y = ~940.
	firstY := binary.LittleEndian.Uint16(out.Data[:2])
	assert.Greater(t, firstY, uint16(500),
		"white overlay on black 10-bit frame should produce high Y values (got %d)", firstY)
}

// TestCompositorNode_10bit_NoOverlay_Passthrough verifies that 10-bit frames
// pass through unchanged when no overlay is active.
func TestCompositorNode_10bit_NoOverlay_Passthrough(t *testing.T) {
	c := graphics.NewCompositor()
	n := &compositorNode{compositor: c}

	w, h := 16, 16
	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	data := make([]byte, frameSize)
	for i := 0; i < w*h*2; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 300)
	}
	for i := w * h * 2; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 512)
	}

	frame := &ProcessingFrame{
		Data:   data,
		Width:  w,
		Height: h,
		Format: ColorYUV422_10bit,
	}

	out := n.Process(nil, frame)
	require.Same(t, frame, out)

	// Values should be unchanged.
	firstY := binary.LittleEndian.Uint16(out.Data[:2])
	assert.Equal(t, uint16(300), firstY)
}

// TestCompositorNode_10bit_PartialAlpha verifies that semi-transparent overlays
// produce blended values on 10-bit frames.
func TestCompositorNode_10bit_PartialAlpha(t *testing.T) {
	c := graphics.NewCompositor()

	w, h := 16, 16
	// Create overlay with 50% alpha white
	rgba := make([]byte, w*h*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255   // R
		rgba[i+1] = 255 // G
		rgba[i+2] = 255 // B
		rgba[i+3] = 128 // A (50%)
	}

	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id, rgba, w, h, "test"))
	require.NoError(t, c.On(id))

	n := &compositorNode{compositor: c}

	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	data := make([]byte, frameSize)
	// Fill Y with 64 (black)
	for i := 0; i < w*h*2; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 64)
	}
	for i := w * h * 2; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 512)
	}

	frame := &ProcessingFrame{
		Data:   data,
		Width:  w,
		Height: h,
		Format: ColorYUV422_10bit,
	}

	out := n.Process(nil, frame)

	// 50% blend of white(940) and black(64) ≈ 502
	firstY := binary.LittleEndian.Uint16(out.Data[:2])
	assert.Greater(t, firstY, uint16(300), "50%% alpha blend should be between black and white")
	assert.Less(t, firstY, uint16(700), "50%% alpha blend should be between black and white")
}
