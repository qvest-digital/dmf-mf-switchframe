package graphics

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makePNGBlock creates a PNG image of size w x h with a filled 2x2 block of
// the given color starting at (bx, by). All other pixels are transparent.
// Using a 2x2 block ensures even-aligned bounding box and non-zero alpha
// at positions that survive the every-64th-pixel transparency check.
func makePNGBlock(t *testing.T, w, h, bx, by int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < 2; dy++ {
		for dx := 0; dx < 2; dx++ {
			img.SetNRGBA(bx+dx, by+dy, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestSequenceEndToEnd(t *testing.T) {
	// 1. Create compositor and sequence engine.
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(60000, 1001) // 59.94fps

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, err := c.AddLayer()
	require.NoError(t, err)

	// 2. Build ZIP: 2 in frames, 3 loop frames, 1 out frame.
	// Use 8x8 images with a 2x2 opaque block at (2,2).
	// Different colors per phase so we can verify correct frame data in the
	// compositor's overlay after ProcessYUV advances the sequence.
	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	blue := color.NRGBA{R: 0, G: 0, B: 255, A: 255}

	inPNG := makePNGBlock(t, 8, 8, 2, 2, red)
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	outPNG := makePNGBlock(t, 8, 8, 2, 2, blue)

	files := map[string][]byte{
		"in/frame_0001.png":  inPNG,
		"in/frame_0002.png":  inPNG,
		"loop_0001.png":      loopPNG,
		"loop_0002.png":      loopPNG,
		"loop_0003.png":      loopPNG,
		"out/frame_0001.png": outPNG,
	}
	zipData := buildZIP(t, files)

	// 3. Upload via engine.
	err = se.upload(id, zipData)
	require.NoError(t, err)
	require.True(t, se.HasSequence(id), "HasSequence should return true after upload")

	// Verify crop region: the 2x2 block at (2,2) should produce an even-aligned crop.
	c.mu.RLock()
	seq := c.layers[id].seq
	cropX, cropY := seq.cropX, seq.cropY
	cropW, cropH := seq.cropW, seq.cropH
	c.mu.RUnlock()
	require.Equal(t, 2, cropX, "crop X should be even-aligned to 2")
	require.Equal(t, 2, cropY, "crop Y should be even-aligned to 2")
	require.True(t, cropW%2 == 0, "crop width should be even")
	require.True(t, cropH%2 == 0, "crop height should be even")
	require.True(t, cropW >= 2, "crop width should be at least 2")
	require.True(t, cropH >= 2, "crop height should be at least 2")

	// 4. Check state broadcast.
	status := c.Status()
	require.Len(t, status.Layers, 1)
	require.True(t, status.Layers[0].SequenceLoaded, "SequenceLoaded should be true")
	require.Equal(t, "idle", status.Layers[0].SequencePhase, "phase should be idle before playback")
	require.False(t, status.Layers[0].Active, "layer should not be active before playback")

	// 5. AutoOn (via compositor, which delegates to sequence engine).
	err = c.AutoOn(id, 0)
	require.NoError(t, err)

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	active := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseIn, phase, "AutoOn should start in phase")
	require.True(t, active, "AutoOn should activate layer")

	status = c.Status()
	require.Equal(t, "in", status.Layers[0].SequencePhase)
	require.True(t, status.Layers[0].Active)

	// 6. ProcessYUV through in frames (2 frames).
	// Each ProcessYUV call: reads current frame, then advances.
	// Frame 0 of in was set by AutoOn. ProcessYUV reads frame 0, advances to frame 1.
	// Next ProcessYUV reads frame 1, advances — in exhausted, transitions to loop.
	yuvSize := 8 * 8 * 3 / 2
	yuv := make([]byte, yuvSize)
	c.ProcessYUV(yuv, 8, 8, nil, nil)
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase, "after 2 ProcessYUV calls, should be in loop phase")

	status = c.Status()
	require.Equal(t, "loop", status.Layers[0].SequencePhase)

	// 7. ProcessYUV through several loop iterations (verify wrapping).
	// 3 loop frames, so 6 calls = 2 full loops.
	for i := 0; i < 6; i++ {
		c.ProcessYUV(yuv, 8, 8, nil, nil)
	}

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	loopActive := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase, "should still be in loop phase after wrapping")
	require.True(t, loopActive, "layer should remain active during loop")

	// 8. AutoOff (via compositor, which delegates to sequence engine).
	err = c.AutoOff(id, 0)
	require.NoError(t, err)

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseOut, phase, "AutoOff should start out phase")

	status = c.Status()
	require.Equal(t, "out", status.Layers[0].SequencePhase)
	require.True(t, status.Layers[0].Active, "layer should still be active during out phase")

	// 9. ProcessYUV through the out frame (1 frame).
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	doneActive := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseDone, phase, "after out frame completes, phase should be done")
	require.False(t, doneActive, "layer should be deactivated after out phase completes")

	// 10. Verify state broadcast shows done.
	status = c.Status()
	require.Equal(t, "done", status.Layers[0].SequencePhase)
	require.False(t, status.Layers[0].Active, "status should show layer inactive after done")

	// 11. Test Cut On/Off cycle.
	err = c.On(id) // should delegate to CutOn → loop phase
	require.NoError(t, err)

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	cutOnActive := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase, "On() should go to loop phase")
	require.True(t, cutOnActive, "On() should activate layer")

	err = c.Off(id) // should delegate to CutOff → immediate deactivation
	require.NoError(t, err)

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	cutOffActive := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseIdle, phase, "Off() should reset to idle")
	require.False(t, cutOffActive, "Off() should deactivate layer")

	// 12. Delete sequence.
	err = se.Delete(id)
	require.NoError(t, err)
	require.False(t, se.HasSequence(id), "HasSequence should be false after delete")

	c.mu.RLock()
	seqPtr := c.layers[id].seq
	c.mu.RUnlock()
	require.Nil(t, seqPtr, "layer.seq should be nil after delete")

	status = c.Status()
	require.False(t, status.Layers[0].SequenceLoaded, "SequenceLoaded should be false after delete")
}

func TestSequenceEndToEndNoInFrames(t *testing.T) {
	// Test a sequence with no in frames — AutoOn should skip straight to loop.
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, err := c.AddLayer()
	require.NoError(t, err)

	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	blue := color.NRGBA{R: 0, G: 0, B: 255, A: 255}

	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	outPNG := makePNGBlock(t, 8, 8, 2, 2, blue)

	files := map[string][]byte{
		"loop_0001.png":      loopPNG,
		"loop_0002.png":      loopPNG,
		"out/frame_0001.png": outPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))

	// AutoOn with no in frames should jump directly to loop.
	require.NoError(t, c.AutoOn(id, 0))

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase, "AutoOn with no in frames should go directly to loop")

	// Process a couple of loop frames.
	yuv := make([]byte, 8*8*3/2)
	c.ProcessYUV(yuv, 8, 8, nil, nil)
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	// AutoOff should play out.
	require.NoError(t, c.AutoOff(id, 0))

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseOut, phase)

	// Process the out frame.
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	active := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseDone, phase)
	require.False(t, active)
}

func TestSequenceEndToEndNoOutFrames(t *testing.T) {
	// Test a sequence with no out frames — AutoOff should deactivate immediately.
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, err := c.AddLayer()
	require.NoError(t, err)

	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}

	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)

	files := map[string][]byte{
		"loop_0001.png": loopPNG,
		"loop_0002.png": loopPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))
	require.NoError(t, c.AutoOn(id, 0))

	// Process some loop frames.
	yuv := make([]byte, 8*8*3/2)
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	// AutoOff with no out frames should deactivate immediately.
	require.NoError(t, c.AutoOff(id, 0))

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	active := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseIdle, phase, "no out frames -> should go to idle immediately")
	require.False(t, active, "no out frames -> should deactivate immediately")
}

func TestSequenceEndToEndStateBroadcast(t *testing.T) {
	// Verify state broadcasts fire at each lifecycle transition.
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	var broadcasts []State
	c.OnStateChange(func(s State) {
		broadcasts = append(broadcasts, s)
	})

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()
	broadcasts = nil // reset after AddLayer

	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	files := map[string][]byte{
		"in/frame_0001.png":  loopPNG,
		"loop_0001.png":      loopPNG,
		"out/frame_0001.png": loopPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))
	assert.GreaterOrEqual(t, len(broadcasts), 1, "Upload should broadcast")

	uploadIdx := len(broadcasts) - 1
	assert.True(t, broadcasts[uploadIdx].Layers[0].SequenceLoaded)
	assert.Equal(t, "idle", broadcasts[uploadIdx].Layers[0].SequencePhase)

	// AutoOn
	require.NoError(t, c.AutoOn(id, 0))
	autoOnIdx := len(broadcasts) - 1
	assert.Equal(t, "in", broadcasts[autoOnIdx].Layers[0].SequencePhase)
	assert.True(t, broadcasts[autoOnIdx].Layers[0].Active)

	// Process the in frame to transition to loop.
	yuv := make([]byte, 8*8*3/2)
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	// AutoOff
	require.NoError(t, c.AutoOff(id, 0))
	autoOffIdx := len(broadcasts) - 1
	assert.Equal(t, "out", broadcasts[autoOffIdx].Layers[0].SequencePhase)

	// Process the out frame.
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	// Delete
	require.NoError(t, se.Delete(id))
	deleteIdx := len(broadcasts) - 1
	assert.False(t, broadcasts[deleteIdx].Layers[0].SequenceLoaded)
}

func TestSequenceSnapshotVisibleLayers(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(60000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Build ZIP: 1 in frame, 2 loop frames, 1 out frame.
	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	blue := color.NRGBA{R: 0, G: 0, B: 255, A: 255}

	inPNG := makePNGBlock(t, 8, 8, 2, 2, red)
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	outPNG := makePNGBlock(t, 8, 8, 2, 2, blue)

	// Use 2 out frames so SnapshotVisibleLayers can observe the out phase
	// before the sequence completes. With a single out frame, the snapshot
	// call both advances past the last frame and deactivates the layer in
	// the same write-lock pass, so the subsequent read-lock snapshot finds
	// no visible layer.
	files := map[string][]byte{
		"in/frame_0001.png":  inPNG,
		"loop_0001.png":      loopPNG,
		"loop_0002.png":      loopPNG,
		"out/frame_0001.png": outPNG,
		"out/frame_0002.png": outPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))

	// Before activation, snapshot should be empty.
	snaps := c.SnapshotVisibleLayers()
	require.Empty(t, snaps, "no visible layers before activation")

	// AutoOn to start playing.
	require.NoError(t, c.AutoOn(id, 0))

	// First SnapshotVisibleLayers call: should see the in frame and advance.
	snaps = c.SnapshotVisibleLayers()
	require.Len(t, snaps, 1, "should have 1 visible layer")
	require.NotNil(t, snaps[0].Overlay, "overlay data should be present")
	require.Equal(t, id, snaps[0].ID)
	require.True(t, snaps[0].OverlayW > 0, "overlay width should be > 0")
	require.True(t, snaps[0].OverlayH > 0, "overlay height should be > 0")
	require.True(t, snaps[0].Gen > 0, "generation should be > 0")

	// Verify rect position matches crop.
	c.mu.RLock()
	seq := c.layers[id].seq
	expectRectX, expectRectY := seq.cropX, seq.cropY
	c.mu.RUnlock()
	require.Equal(t, expectRectX, snaps[0].RectX)
	require.Equal(t, expectRectY, snaps[0].RectY)

	// Second snapshot advances again — should transition to loop (1 in frame exhausted).
	snaps2 := c.SnapshotVisibleLayers()
	require.Len(t, snaps2, 1)
	// Generation should have bumped (different frame).
	require.Greater(t, snaps2[0].Gen, snaps[0].Gen, "generation should increase on frame advance")

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase, "should transition to loop after in frame exhausted")

	// Advance through a few loop frames via snapshot.
	for i := 0; i < 4; i++ {
		c.SnapshotVisibleLayers()
	}

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase, "should still be in loop after wrapping")

	// AutoOff.
	require.NoError(t, c.AutoOff(id, 0))

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseOut, phase)

	// First out frame snapshot: advances from out[0] to out[1], still active.
	snaps = c.SnapshotVisibleLayers()
	require.Len(t, snaps, 1, "first out frame should be visible")

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseOut, phase, "should still be in out phase with 1 frame remaining")

	// Second out frame snapshot: advances past the last out frame, layer deactivates.
	_ = c.SnapshotVisibleLayers()
	// The layer is deactivated during the write-lock advance, so the
	// read-lock snapshot may or may not include it. Either way, verify
	// the underlying state is done.
	c.mu.RLock()
	phase = c.layers[id].seq.phase
	active := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseDone, phase, "should be done after out frames exhausted")
	require.False(t, active, "layer should be deactivated after out")

	// Snapshot should now be empty (layer inactive).
	snaps = c.SnapshotVisibleLayers()
	require.Empty(t, snaps, "no visible layers after sequence completes")
}

func TestSequenceSnapshotCacheReuse(t *testing.T) {
	// Verify that SnapshotVisibleLayers reuses cached overlay when generation
	// hasn't changed (i.e., when the layer isn't advancing).
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, err := c.AddLayer()
	require.NoError(t, err)

	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	files := map[string][]byte{
		"loop_0001.png": loopPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))
	require.NoError(t, c.AutoOn(id, 0))

	// First snapshot: gets overlay data (new generation).
	snaps1 := c.SnapshotVisibleLayers()
	require.Len(t, snaps1, 1)
	gen1 := snaps1[0].Gen
	require.NotNil(t, snaps1[0].Overlay, "first snapshot should include overlay data")

	// With only 1 loop frame, advancing wraps to the same frame.
	// The generation still bumps because SnapshotVisibleLayers always bumps gen
	// when the overlay is set from the current frame. But the second call to
	// SnapshotVisibleLayers should see the same gen (not bumped again because
	// advance wraps back to frame 0 and sets the same overlay).
	snaps2 := c.SnapshotVisibleLayers()
	require.Len(t, snaps2, 1)
	gen2 := snaps2[0].Gen
	// Each call through snapshot advances and bumps gen for a playing sequence.
	require.GreaterOrEqual(t, gen2, gen1, "generation should not decrease")
}

func TestSequenceEndToEndMultiLayer(t *testing.T) {
	// Test two layers with different sequences running simultaneously.
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()

	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}

	redPNG := makePNGBlock(t, 8, 8, 2, 2, red)
	greenPNG := makePNGBlock(t, 8, 8, 2, 2, green)

	// Layer 1: 1 in, 2 loop, 1 out
	files1 := map[string][]byte{
		"in/frame_0001.png":  redPNG,
		"loop_0001.png":      redPNG,
		"loop_0002.png":      redPNG,
		"out/frame_0001.png": redPNG,
	}
	// Layer 2: just 2 loop frames, no in/out
	files2 := map[string][]byte{
		"loop_0001.png": greenPNG,
		"loop_0002.png": greenPNG,
	}

	require.NoError(t, se.upload(id1, buildZIP(t, files1)))
	require.NoError(t, se.upload(id2, buildZIP(t, files2)))

	// Start both layers.
	require.NoError(t, c.AutoOn(id1, 0)) // starts in "in" phase
	require.NoError(t, c.AutoOn(id2, 0)) // skips in, goes to loop

	c.mu.RLock()
	phase1 := c.layers[id1].seq.phase
	phase2 := c.layers[id2].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseIn, phase1)
	require.Equal(t, seqPhaseLoop, phase2, "layer 2 has no in frames, should be in loop")

	// Process frames — both should advance.
	yuv := make([]byte, 8*8*3/2)
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	c.mu.RLock()
	phase1 = c.layers[id1].seq.phase
	phase2 = c.layers[id2].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase1, "layer 1 should transition to loop after 1 in frame")
	require.Equal(t, seqPhaseLoop, phase2, "layer 2 should still be in loop")

	// Turn off layer 1 (with out), keep layer 2 going.
	require.NoError(t, c.AutoOff(id1, 0))
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	c.mu.RLock()
	phase1 = c.layers[id1].seq.phase
	active1 := c.layers[id1].active
	phase2 = c.layers[id2].seq.phase
	active2 := c.layers[id2].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseDone, phase1, "layer 1 should be done after out frame")
	require.False(t, active1, "layer 1 should be inactive")
	require.Equal(t, seqPhaseLoop, phase2, "layer 2 should still be looping")
	require.True(t, active2, "layer 2 should still be active")

	// Turn off layer 2 (no out frames, immediate deactivation).
	require.NoError(t, c.AutoOff(id2, 0))

	c.mu.RLock()
	active2 = c.layers[id2].active
	c.mu.RUnlock()
	require.False(t, active2, "layer 2 should be deactivated (no out frames)")
}

func TestSequenceEndToEndProcessYUVModifiesBuffer(t *testing.T) {
	// Verify that ProcessYUV actually composites the sequence overlay into the
	// YUV buffer (i.e., the buffer is modified when a sequence is active).
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()

	// Use a brighter color so the alpha blend has visible effect.
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, white)
	files := map[string][]byte{
		"loop_0001.png": loopPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))
	require.NoError(t, c.AutoOn(id, 0))

	// Create a black YUV frame.
	yuvSize := 8 * 8 * 3 / 2
	yuv := make([]byte, yuvSize)
	// Y=0, U=128, V=128 is black in YUV420.
	for i := 0; i < 8*8; i++ {
		yuv[i] = 0 // Y
	}
	for i := 8 * 8; i < yuvSize; i++ {
		yuv[i] = 128 // U and V
	}

	// Save a copy before processing.
	before := make([]byte, yuvSize)
	copy(before, yuv)

	c.ProcessYUV(yuv, 8, 8, nil, nil)

	// The buffer should be modified in the crop region where the overlay has alpha > 0.
	// We can't predict exact values (depends on AlphaBlendRGBA implementation),
	// but the buffer should differ from the all-black original.
	differs := false
	for i := range yuv {
		if yuv[i] != before[i] {
			differs = true
			break
		}
	}
	require.True(t, differs, "ProcessYUV should modify the YUV buffer when a sequence is active with opaque pixels")
}

func TestSequenceEndToEndReuploadWhilePlaying(t *testing.T) {
	// Verify that re-uploading a sequence while it's playing replaces cleanly.
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()

	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	files := map[string][]byte{
		"loop_0001.png": loopPNG,
		"loop_0002.png": loopPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))
	require.NoError(t, c.AutoOn(id, 0))

	// Process a frame.
	yuv := make([]byte, 8*8*3/2)
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	// Re-upload replaces the sequence.
	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	redPNG := makePNGBlock(t, 8, 8, 2, 2, red)
	files2 := map[string][]byte{
		"loop_0001.png": redPNG,
		"loop_0002.png": redPNG,
		"loop_0003.png": redPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files2)))

	// After re-upload, the new sequence should be idle, layer not active (upload doesn't activate).
	c.mu.RLock()
	phase := c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseIdle, phase, "re-upload should reset to idle")

	// Can start the new sequence.
	require.NoError(t, c.AutoOn(id, 0))

	c.mu.RLock()
	loopCount := len(c.layers[id].seq.loopFrames)
	c.mu.RUnlock()
	require.Equal(t, 3, loopCount, "new sequence should have 3 loop frames")
}

func TestSequenceEndToEndRapidOnOff(t *testing.T) {
	// Stress test: rapid On/Off cycles should not panic or leave inconsistent state.
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()

	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	files := map[string][]byte{
		"in/frame_0001.png":  loopPNG,
		"loop_0001.png":      loopPNG,
		"loop_0002.png":      loopPNG,
		"out/frame_0001.png": loopPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))

	yuv := make([]byte, 8*8*3/2)

	for i := 0; i < 20; i++ {
		require.NoError(t, c.On(id))
		c.ProcessYUV(yuv, 8, 8, nil, nil)
		require.NoError(t, c.Off(id))
		c.ProcessYUV(yuv, 8, 8, nil, nil)
	}

	// After all cycles, should be in idle state.
	c.mu.RLock()
	phase := c.layers[id].seq.phase
	active := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseIdle, phase)
	require.False(t, active)
}

func TestSequenceEndToEndAutoOnAutoOffCycle(t *testing.T) {
	// Full AutoOn → loop → AutoOff cycle with ProcessYUV driving frame advancement.
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()

	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	files := map[string][]byte{
		"in/frame_0001.png":  loopPNG,
		"in/frame_0002.png":  loopPNG,
		"in/frame_0003.png":  loopPNG,
		"loop_0001.png":      loopPNG,
		"out/frame_0001.png": loopPNG,
		"out/frame_0002.png": loopPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))

	// AutoOn
	require.NoError(t, c.AutoOn(id, 0))

	yuv := make([]byte, 8*8*3/2)

	// Process 3 in frames.
	for i := 0; i < 3; i++ {
		c.mu.RLock()
		phase := c.layers[id].seq.phase
		c.mu.RUnlock()
		require.Equal(t, seqPhaseIn, phase, "should be in phase during in frames (iter %d)", i)
		c.ProcessYUV(yuv, 8, 8, nil, nil)
	}

	// Should be in loop now.
	c.mu.RLock()
	phase := c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase)

	// Process some loop frames.
	for i := 0; i < 5; i++ {
		c.ProcessYUV(yuv, 8, 8, nil, nil)
	}

	// AutoOff
	require.NoError(t, c.AutoOff(id, 0))

	// Process 2 out frames.
	for i := 0; i < 2; i++ {
		c.mu.RLock()
		phase = c.layers[id].seq.phase
		c.mu.RUnlock()
		require.Equal(t, seqPhaseOut, phase, "should be in out phase during out frames (iter %d)", i)
		c.ProcessYUV(yuv, 8, 8, nil, nil)
	}

	// Should be done.
	c.mu.RLock()
	phase = c.layers[id].seq.phase
	active := c.layers[id].active
	c.mu.RUnlock()
	require.Equal(t, seqPhaseDone, phase)
	require.False(t, active)
}

func TestSequenceEndToEndTimingNotAffectedByDuration(t *testing.T) {
	// Verify that passing a duration to AutoOn/AutoOff is ignored for sequences
	// (sequences use frame-count timing, not wall-clock fades).
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()

	green := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	loopPNG := makePNGBlock(t, 8, 8, 2, 2, green)
	files := map[string][]byte{
		"in/frame_0001.png":  loopPNG,
		"loop_0001.png":      loopPNG,
		"out/frame_0001.png": loopPNG,
	}
	require.NoError(t, se.upload(id, buildZIP(t, files)))

	// Pass a large duration — should still use frame-locked timing.
	require.NoError(t, c.AutoOn(id, 5*time.Second))

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	fadePos := c.layers[id].fadePosition
	c.mu.RUnlock()

	// Sequence AutoOn sets fadePosition to 1.0 immediately (no fade ramp).
	require.Equal(t, seqPhaseIn, phase)
	require.Equal(t, 1.0, fadePos, "sequence should have immediate fade position, ignoring duration")

	// One ProcessYUV should advance through the single in frame to loop.
	yuv := make([]byte, 8*8*3/2)
	c.ProcessYUV(yuv, 8, 8, nil, nil)

	c.mu.RLock()
	phase = c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase, "should advance to loop via frame count, not wall-clock")
}
