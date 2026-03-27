package graphics

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// makeTestSequenceZIP builds a ZIP with in/frame_0001.png, loop_0001.png,
// loop_0002.png, out/frame_0001.png. All 4x4 images with a non-transparent
// pixel at (1,1).
func makeTestSequenceZIP(t *testing.T) []byte {
	t.Helper()
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	pngData := makePNG(t, 4, 4, 1, 1, white)

	files := map[string][]byte{
		"in/frame_0001.png":  pngData,
		"loop_0001.png":      pngData,
		"loop_0002.png":      pngData,
		"out/frame_0001.png": pngData,
	}
	return buildZIP(t, files)
}

// makeTestSequenceZIPNoOut builds a ZIP with only loop frames (no in/out).
func makeTestSequenceZIPNoOut(t *testing.T) []byte {
	t.Helper()
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	pngData := makePNG(t, 4, 4, 1, 1, white)

	files := map[string][]byte{
		"loop_0001.png": pngData,
		"loop_0002.png": pngData,
	}
	return buildZIP(t, files)
}

func TestSequenceEngineUploadAndAutoOn(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	zipData := makeTestSequenceZIP(t)

	// Upload
	err = se.Upload(id, zipData)
	require.NoError(t, err)
	require.True(t, se.HasSequence(id))

	// Layer should have overlay set but NOT be active yet.
	c.mu.RLock()
	layer := c.layers[id]
	hasOverlay := layer.overlay != nil
	active := layer.active
	c.mu.RUnlock()
	require.True(t, hasOverlay, "upload should set initial overlay from first loop frame")
	require.False(t, active, "upload should not activate layer")

	// AutoOn should start the in phase and activate the layer.
	err = se.AutoOn(id)
	require.NoError(t, err)

	c.mu.RLock()
	seq := c.layers[id].seq
	phase := seq.phase
	layerActive := c.layers[id].active
	c.mu.RUnlock()

	require.Equal(t, seqPhaseIn, phase, "AutoOn should start in phase")
	require.True(t, layerActive, "AutoOn should activate layer")
}

func TestSequenceEngineCutOn(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id, zipData))

	// CutOn should jump directly to loop phase.
	err = se.CutOn(id)
	require.NoError(t, err)

	c.mu.RLock()
	seq := c.layers[id].seq
	phase := seq.phase
	layerActive := c.layers[id].active
	c.mu.RUnlock()

	require.Equal(t, seqPhaseLoop, phase, "CutOn should start loop phase")
	require.True(t, layerActive, "CutOn should activate layer")
}

func TestSequenceEngineAutoOff(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id, zipData))
	require.NoError(t, se.CutOn(id))

	// AutoOff should start the out phase.
	err = se.AutoOff(id)
	require.NoError(t, err)

	c.mu.RLock()
	seq := c.layers[id].seq
	phase := seq.phase
	c.mu.RUnlock()

	require.Equal(t, seqPhaseOut, phase, "AutoOff should start out phase")
}

func TestSequenceEngineAutoOffNoOutFrames(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	zipData := makeTestSequenceZIPNoOut(t)
	require.NoError(t, se.Upload(id, zipData))
	require.NoError(t, se.CutOn(id))

	// AutoOff with no out frames should deactivate immediately.
	err = se.AutoOff(id)
	require.NoError(t, err)

	c.mu.RLock()
	layerActive := c.layers[id].active
	seq := c.layers[id].seq
	phase := seq.phase
	c.mu.RUnlock()

	require.False(t, layerActive, "AutoOff with no out frames should deactivate layer")
	require.Equal(t, seqPhaseIdle, phase, "AutoOff with no out frames should reset to idle")
}

func TestSequenceEngineCutOff(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id, zipData))
	require.NoError(t, se.CutOn(id))

	// CutOff should immediately deactivate.
	err = se.CutOff(id)
	require.NoError(t, err)

	c.mu.RLock()
	seq := c.layers[id].seq
	phase := seq.phase
	layerActive := c.layers[id].active
	c.mu.RUnlock()

	require.Equal(t, seqPhaseIdle, phase, "CutOff should set phase to idle")
	require.False(t, layerActive, "CutOff should deactivate layer")
}

func TestSequenceEngineDelete(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id, zipData))
	require.True(t, se.HasSequence(id))

	// Delete should remove the sequence and deactivate.
	err = se.Delete(id)
	require.NoError(t, err)
	require.False(t, se.HasSequence(id))

	c.mu.RLock()
	seq := c.layers[id].seq
	layerActive := c.layers[id].active
	c.mu.RUnlock()

	require.Nil(t, seq, "Delete should clear layer.seq")
	require.False(t, layerActive, "Delete should deactivate layer")
}

func TestSequenceEngineUploadSetsRect(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id, zipData))

	// Rect should be set to the crop region.
	c.mu.RLock()
	rect := c.layers[id].rect
	seq := c.layers[id].seq
	c.mu.RUnlock()

	expected := image.Rect(seq.cropX, seq.cropY, seq.cropX+seq.cropW, seq.cropY+seq.cropH)
	require.Equal(t, expected, rect, "Upload should set layer rect to crop region")
}

func TestSequenceEngineClose(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()

	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id1, zipData))
	require.NoError(t, se.Upload(id2, zipData))
	require.NoError(t, se.CutOn(id1))
	require.NoError(t, se.CutOn(id2))

	// Close should clean up all sequences.
	se.Close()

	require.False(t, se.HasSequence(id1))
	require.False(t, se.HasSequence(id2))

	c.mu.RLock()
	layer1Active := c.layers[id1].active
	layer2Active := c.layers[id2].active
	c.mu.RUnlock()

	require.False(t, layer1Active, "Close should deactivate layer 1")
	require.False(t, layer2Active, "Close should deactivate layer 2")
}

func TestSequenceEngineCloseIdempotent(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)

	id, _ := c.AddLayer()
	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id, zipData))

	// Calling Close multiple times must not panic.
	se.Close()
	se.Close()
	se.Close()
}

func TestSequenceEngineUploadLayerNotFound(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	zipData := makeTestSequenceZIP(t)
	err := se.Upload(999, zipData)
	require.ErrorIs(t, err, ErrLayerNotFound)
}

func TestSequenceEngineAutoOnNoSequence(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, _ := c.AddLayer()

	err := se.AutoOn(id)
	require.Error(t, err, "AutoOn without upload should fail")
}

func TestSequenceEngineStateCallback(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	callCount := 0
	c.OnStateChange(func(s State) {
		callCount++
	})

	se := NewSequenceEngine(c)
	defer se.Close()

	id, _ := c.AddLayer()
	callCount = 0 // reset after AddLayer

	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id, zipData))

	// Upload should have triggered at least one state broadcast.
	// (It sets rect, overlay, etc. under compositor lock and broadcasts.)
	require.Greater(t, callCount, 0, "Upload should trigger state broadcast")
}

func TestSequenceEngineReupload(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	se := NewSequenceEngine(c)
	defer se.Close()

	id, _ := c.AddLayer()

	zipData := makeTestSequenceZIP(t)
	require.NoError(t, se.Upload(id, zipData))

	// Re-uploading should replace the sequence.
	require.NoError(t, se.Upload(id, zipData))
	require.True(t, se.HasSequence(id))

	// Should still work after re-upload.
	require.NoError(t, se.CutOn(id))

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	c.mu.RUnlock()
	require.Equal(t, seqPhaseLoop, phase)
}

// --- Compositor delegation tests (On/Off/AutoOn/AutoOff → SequenceEngine) ---

func TestCompositorOnDelegatesToSequence(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()
	require.NoError(t, se.Upload(id, makeTestSequenceZIP(t)))

	// On() should delegate to CutOn → loop phase.
	require.NoError(t, c.On(id))

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	active := c.layers[id].active
	c.mu.RUnlock()

	require.Equal(t, seqPhaseLoop, phase, "On() should delegate to CutOn → loop phase")
	require.True(t, active, "On() should activate the layer")
}

func TestCompositorOffDelegatesToSequence(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()
	require.NoError(t, se.Upload(id, makeTestSequenceZIP(t)))
	require.NoError(t, se.CutOn(id)) // activate first

	require.NoError(t, c.Off(id))

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	active := c.layers[id].active
	c.mu.RUnlock()

	require.Equal(t, seqPhaseIdle, phase, "Off() should delegate to CutOff → idle phase")
	require.False(t, active, "Off() should deactivate the layer")
}

func TestCompositorAutoOnDelegatesToSequence(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()
	require.NoError(t, se.Upload(id, makeTestSequenceZIP(t)))

	require.NoError(t, c.AutoOn(id, 500*time.Millisecond))

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	c.mu.RUnlock()

	require.Equal(t, seqPhaseIn, phase, "AutoOn() should delegate to SequenceEngine.AutoOn → in phase")
}

func TestCompositorAutoOffDelegatesToSequence(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	se := NewSequenceEngine(c)
	defer se.Close()
	c.SetSequenceEngine(se)

	id, _ := c.AddLayer()
	require.NoError(t, se.Upload(id, makeTestSequenceZIP(t)))
	require.NoError(t, se.CutOn(id)) // activate

	require.NoError(t, c.AutoOff(id, 500*time.Millisecond))

	c.mu.RLock()
	phase := c.layers[id].seq.phase
	c.mu.RUnlock()

	require.Equal(t, seqPhaseOut, phase, "AutoOff() should delegate to SequenceEngine.AutoOff → out phase")
}
