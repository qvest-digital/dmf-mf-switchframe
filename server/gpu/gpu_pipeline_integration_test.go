//go:build darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock state implementations ---

type mockKeyBridge struct {
	hasKeys bool
	keys    []EnabledKeySnapshot
}

func (m *mockKeyBridge) HasEnabledKeysWithFills() bool             { return m.hasKeys }
func (m *mockKeyBridge) HasEnabledKeys() bool                      { return m.hasKeys }
func (m *mockKeyBridge) SnapshotEnabledKeys() []EnabledKeySnapshot { return m.keys }
func (m *mockKeyBridge) GPUFill(sourceKey string) *GPUFrame        { return nil }

type mockCompositorState struct {
	active bool
	layers []VisibleLayerSnapshot
}

func (m *mockCompositorState) HasActiveLayers() bool                   { return m.active }
func (m *mockCompositorState) SnapshotVisibleLayers() []VisibleLayerSnapshot { return m.layers }

type mockLayoutState struct {
	active bool
	slots  []SlotSnapshot
}

func (m *mockLayoutState) Active() bool                  { return m.active }
func (m *mockLayoutState) SnapshotSlots() []SlotSnapshot { return m.slots }
func (m *mockLayoutState) GPUFill(sourceKey string) *GPUFrame { return nil }

type mockSTMapState struct {
	hasMap   bool
	name     string
	s, t     []float32
	animated bool
	animIdx  int
}

func (m *mockSTMapState) HasProgramMap() bool                            { return m.hasMap }
func (m *mockSTMapState) ProgramMapName() string                         { return m.name }
func (m *mockSTMapState) ProgramSTArrays() ([]float32, []float32)        { return m.s, m.t }
func (m *mockSTMapState) IsAnimated() bool                               { return m.animated }
func (m *mockSTMapState) AdvanceAnimatedIndex() int                      { i := m.animIdx; m.animIdx++; return i }
func (m *mockSTMapState) AnimatedSTArraysAt(idx int) ([]float32, []float32) { return m.s, m.t }

// --- Helper functions ---

// createTestYUV420p creates a YUV420p frame filled with a solid color.
func createTestYUV420p(w, h int, yVal, cbVal, crVal byte) []byte {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	for i := 0; i < ySize; i++ {
		yuv[i] = yVal
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = cbVal
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = crVal
	}
	return yuv
}

// setupGPU creates a GPU context and frame pool, skipping the test if GPU is not available.
func setupGPU(t *testing.T, w, h, poolSize int) (*Context, *FramePool) {
	t.Helper()
	ctx, err := NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}

	pool, err := NewFramePool(ctx, w, h, poolSize)
	if err != nil {
		ctx.Close()
		t.Skip("GPU frame pool creation failed:", err)
	}

	return ctx, pool
}

// --- Integration Tests ---

func TestGPUPipelineIntegration_PassthroughAllInactive(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 4)
	defer ctx.Close()
	defer pool.Close()

	// All mocks return inactive state.
	keyBridge := &mockKeyBridge{hasKeys: false}
	compositor := &mockCompositorState{active: false}
	layout := &mockLayoutState{active: false}
	stmapState := &mockSTMapState{hasMap: false}

	// Build pipeline with all 4 processing nodes.
	pipe := NewGPUPipeline(ctx, pool)
	err := pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUKeyNode(ctx, pool, keyBridge),
		NewGPULayoutNode(ctx, pool, layout),
		NewGPUCompositorNode(ctx, compositor),
		NewGPUSTMapNode(ctx, pool, stmapState),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Create a test frame: Y=180, Cb=100, Cr=200
	inputYUV := createTestYUV420p(w, h, 180, 100, 200)

	// Run through the pipeline.
	frame, err := pipe.RunWithUpload(inputYUV, w, h, 90000)
	require.NoError(t, err)
	require.NotNil(t, frame)
	defer frame.Release()

	// Download result.
	outputYUV := make([]byte, w*h*3/2)
	err = Download(ctx, outputYUV, frame, w, h)
	require.NoError(t, err)

	// Frame should be unchanged since all nodes are inactive.
	ySize := w * h
	cbSize := (w / 2) * (h / 2)

	// Check Y plane center pixel.
	assert.Equal(t, byte(180), outputYUV[ySize/2+w/2],
		"Y center pixel should be unchanged")
	// Check Cb plane center pixel.
	assert.Equal(t, byte(100), outputYUV[ySize+(cbSize/2)],
		"Cb center pixel should be unchanged")
	// Check Cr plane center pixel.
	assert.Equal(t, byte(200), outputYUV[ySize+cbSize+(cbSize/2)],
		"Cr center pixel should be unchanged")

	// Verify the full Y plane is uniform.
	for i := 0; i < ySize; i++ {
		if outputYUV[i] != 180 {
			t.Fatalf("Y[%d] = %d, expected 180", i, outputYUV[i])
		}
	}

	// Verify pipeline metrics.
	snap := pipe.Snapshot()
	assert.True(t, snap["gpu"].(bool))
	assert.Equal(t, int64(1), snap["run_count"].(int64))

	t.Logf("Passthrough pipeline: run_count=%d, last_run_ns=%d",
		snap["run_count"], snap["last_run_ns"])
}

func TestGPUPipelineIntegration_CompositorOverlay(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 4)
	defer ctx.Close()
	defer pool.Close()

	// Create a 4x4 red RGBA overlay at full alpha.
	// Red in BT.709: R=255 G=0 B=0 => Y~81, Cb~90, Cr~240 (limited range)
	overlayW, overlayH := 4, 4
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 255 // A (full alpha)
	}

	compositor := &mockCompositorState{
		active: true,
		layers: []VisibleLayerSnapshot{
			{
				ID:       0,
				Rect:     Rect{X: 0, Y: 0, W: overlayW, H: overlayH},
				Alpha:    1.0,
				Overlay:  rgba,
				OverlayW: overlayW,
				OverlayH: overlayH,
				Gen:      1,
			},
		},
	}

	keyBridge := &mockKeyBridge{hasKeys: false}
	layout := &mockLayoutState{active: false}
	stmapState := &mockSTMapState{hasMap: false}

	pipe := NewGPUPipeline(ctx, pool)
	err := pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUKeyNode(ctx, pool, keyBridge),
		NewGPULayoutNode(ctx, pool, layout),
		NewGPUCompositorNode(ctx, compositor),
		NewGPUSTMapNode(ctx, pool, stmapState),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Create a uniform gray frame: Y=128, Cb=128, Cr=128
	inputYUV := createTestYUV420p(w, h, 128, 128, 128)

	frame, err := pipe.RunWithUpload(inputYUV, w, h, 90000)
	require.NoError(t, err)
	require.NotNil(t, frame)
	defer frame.Release()

	outputYUV := make([]byte, w*h*3/2)
	err = Download(ctx, outputYUV, frame, w, h)
	require.NoError(t, err)

	// The overlay region (top-left 4x4) should have changed from the gray input.
	// Check that the overlay area's Y values differ from the untouched region.
	overlayY := outputYUV[0] // top-left pixel Y
	outsideY := outputYUV[w*(h-1)+(w-1)] // bottom-right pixel Y (outside overlay)

	assert.Equal(t, byte(128), outsideY,
		"Y outside overlay region should remain 128")
	assert.NotEqual(t, byte(128), overlayY,
		"Y in overlay region should have changed from 128")

	t.Logf("Compositor overlay: overlay Y=%d, outside Y=%d", overlayY, outsideY)
}

func TestGPUPipelineIntegration_STMapIdentity(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 6) // extra frames for stmap temp buffer
	defer ctx.Close()
	defer pool.Close()

	// Create an identity ST map: each pixel maps to itself.
	// s[y*w+x] = float32(x) / float32(w-1)  (normalized 0..1)
	// t[y*w+x] = float32(y) / float32(h-1)  (normalized 0..1)
	s := make([]float32, w*h)
	tArr := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s[y*w+x] = float32(x) / float32(w-1)
			tArr[y*w+x] = float32(y) / float32(h-1)
		}
	}

	stmapState := &mockSTMapState{
		hasMap: true,
		name:   "identity",
		s:      s,
		t:      tArr,
	}

	keyBridge := &mockKeyBridge{hasKeys: false}
	compositor := &mockCompositorState{active: false}
	layout := &mockLayoutState{active: false}

	pipe := NewGPUPipeline(ctx, pool)
	err := pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUKeyNode(ctx, pool, keyBridge),
		NewGPULayoutNode(ctx, pool, layout),
		NewGPUCompositorNode(ctx, compositor),
		NewGPUSTMapNode(ctx, pool, stmapState),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Create a gradient frame for easy verification.
	inputYUV := make([]byte, w*h*3/2)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			inputYUV[y*w+x] = byte((x + y) % 256)
		}
	}
	ySize := w * h
	for i := ySize; i < len(inputYUV); i++ {
		inputYUV[i] = 128
	}

	frame, err := pipe.RunWithUpload(inputYUV, w, h, 90000)
	require.NoError(t, err)
	require.NotNil(t, frame)
	defer frame.Release()

	outputYUV := make([]byte, w*h*3/2)
	err = Download(ctx, outputYUV, frame, w, h)
	require.NoError(t, err)

	// Identity ST map should preserve the frame. Allow tolerance of 1 for
	// bilinear interpolation rounding at edges.
	mismatches := 0
	tolerance := byte(1)
	for i := 0; i < ySize; i++ {
		diff := int(outputYUV[i]) - int(inputYUV[i])
		if diff < -int(tolerance) || diff > int(tolerance) {
			mismatches++
			if mismatches <= 5 {
				y := i / w
				x := i % w
				t.Logf("Y mismatch at (%d,%d): input=%d output=%d", x, y, inputYUV[i], outputYUV[i])
			}
		}
	}

	// Allow up to 5% mismatch for edge pixels where bilinear interpolation
	// may clamp differently.
	maxMismatches := ySize * 5 / 100
	assert.LessOrEqual(t, mismatches, maxMismatches,
		"identity ST map should preserve most pixels (got %d mismatches out of %d)", mismatches, ySize)

	t.Logf("STMap identity: %d/%d Y pixels matched within tolerance %d",
		ySize-mismatches, ySize, tolerance)
}

func TestGPUPipelineIntegration_LayoutSlotFill(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 4)
	defer ctx.Close()
	defer pool.Close()

	// Create a solid white fill for the layout slot.
	// White in BT.709 limited range: Y=235, Cb=128, Cr=128.
	slotW, slotH := 32, 32
	fillYUV := createTestYUV420p(slotW, slotH, 235, 128, 128)

	layout := &mockLayoutState{
		active: true,
		slots: []SlotSnapshot{
			{
				Index:     0,
				Enabled:   true,
				SourceKey: "test-source",
				Rect:      Rect{X: 0, Y: 0, W: slotW, H: slotH},
				FillYUV:   fillYUV,
				FillW:     slotW,
				FillH:     slotH,
				Alpha:     1.0,
			},
		},
	}

	keyBridge := &mockKeyBridge{hasKeys: false}
	compositor := &mockCompositorState{active: false}
	stmapState := &mockSTMapState{hasMap: false}

	pipe := NewGPUPipeline(ctx, pool)
	err := pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUKeyNode(ctx, pool, keyBridge),
		NewGPULayoutNode(ctx, pool, layout),
		NewGPUCompositorNode(ctx, compositor),
		NewGPUSTMapNode(ctx, pool, stmapState),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Create a black program frame.
	inputYUV := createTestYUV420p(w, h, 16, 128, 128)

	frame, err := pipe.RunWithUpload(inputYUV, w, h, 90000)
	require.NoError(t, err)
	require.NotNil(t, frame)
	defer frame.Release()

	outputYUV := make([]byte, w*h*3/2)
	err = Download(ctx, outputYUV, frame, w, h)
	require.NoError(t, err)

	// The slot region (top-left 32x32) should now contain the white fill.
	// The rest of the frame should remain black.
	//
	// Check a pixel inside the slot region.
	insideY := outputYUV[slotH/2*w+slotW/2] // center of slot
	// Check a pixel outside the slot region.
	outsideY := outputYUV[(h-1)*w+(w-1)] // bottom-right corner

	assert.Equal(t, byte(16), outsideY,
		"Y outside slot should remain black (16)")

	// The inside pixel should be close to 235 (the white fill).
	// PIPComposite scales the fill to the slot rect; since they match
	// dimensions the value should be very close.
	assert.InDelta(t, 235, int(insideY), 5,
		"Y inside slot should be close to 235 (white fill)")

	t.Logf("Layout slot: inside Y=%d (expected ~235), outside Y=%d (expected 16)",
		insideY, outsideY)
}

func TestGPUPipelineIntegration_FullPipeline(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 8) // extra frames for stmap + key
	defer ctx.Close()
	defer pool.Close()

	// --- Key bridge: active with a green-screen chroma key ---
	fillW, fillH := w, h
	fillYUV := createTestYUV420p(fillW, fillH, 180, 128, 128) // gray fill

	keyBridge := &mockKeyBridge{
		hasKeys: true,
		keys: []EnabledKeySnapshot{
			{
				SourceKey:      "cam1",
				Type:           "chroma",
				KeyCb:          54,   // green Cb
				KeyCr:          34,   // green Cr
				Similarity:     0.15,
				Smoothness:     0.05,
				SpillSuppress:  0.5,
				SpillReplaceCb: 128,
				SpillReplaceCr: 128,
				FillYUV:        fillYUV,
				FillW:          fillW,
				FillH:          fillH,
			},
		},
	}

	// --- Compositor: one small overlay ---
	overlayW, overlayH := 8, 8
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 128 // A (half alpha)
	}
	compositor := &mockCompositorState{
		active: true,
		layers: []VisibleLayerSnapshot{
			{
				ID:       0,
				Rect:     Rect{X: w - overlayW, Y: h - overlayH, W: overlayW, H: overlayH},
				Alpha:    0.8,
				Overlay:  rgba,
				OverlayW: overlayW,
				OverlayH: overlayH,
				Gen:      1,
			},
		},
	}

	// --- Layout: one PIP slot ---
	slotW, slotH := 16, 16
	slotFill := createTestYUV420p(slotW, slotH, 200, 128, 128)
	layout := &mockLayoutState{
		active: true,
		slots: []SlotSnapshot{
			{
				Index:     0,
				Enabled:   true,
				SourceKey: "pip-source",
				Rect:      Rect{X: w - slotW, Y: 0, W: slotW, H: slotH},
				FillYUV:   slotFill,
				FillW:     slotW,
				FillH:     slotH,
				Alpha:     1.0,
			},
		},
	}

	// --- STMap: identity map ---
	s := make([]float32, w*h)
	tArr := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s[y*w+x] = float32(x) / float32(w-1)
			tArr[y*w+x] = float32(y) / float32(h-1)
		}
	}
	stmapState := &mockSTMapState{
		hasMap: true,
		name:   "identity",
		s:      s,
		t:      tArr,
	}

	// Build pipeline with all 4 nodes active.
	pipe := NewGPUPipeline(ctx, pool)
	err := pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUKeyNode(ctx, pool, keyBridge),
		NewGPULayoutNode(ctx, pool, layout),
		NewGPUCompositorNode(ctx, compositor),
		NewGPUSTMapNode(ctx, pool, stmapState),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Create a test input frame.
	inputYUV := createTestYUV420p(w, h, 100, 128, 128)

	// Run through the full pipeline. The main assertion is that this
	// completes without panics or errors.
	frame, err := pipe.RunWithUpload(inputYUV, w, h, 90000)
	require.NoError(t, err, "full pipeline should not return an error")
	require.NotNil(t, frame)
	defer frame.Release()

	// Download result to verify no corruption.
	outputYUV := make([]byte, w*h*3/2)
	err = Download(ctx, outputYUV, frame, w, h)
	require.NoError(t, err, "download after full pipeline should succeed")

	// Verify pipeline snapshot.
	snap := pipe.Snapshot()
	assert.True(t, snap["gpu"].(bool))
	assert.Equal(t, int64(1), snap["run_count"].(int64))

	activeNodes := snap["active_nodes"].([]map[string]any)
	assert.GreaterOrEqual(t, len(activeNodes), 3,
		"at least 3 nodes should be active (key, layout, compositor, stmap)")

	// Log active node names and timings.
	for _, n := range activeNodes {
		t.Logf("  node=%s last_ns=%d", n["name"], n["last_ns"])
	}

	t.Logf("Full pipeline: %d active nodes, run_count=%d, last_run_ns=%d",
		len(activeNodes), snap["run_count"], snap["last_run_ns"])
}

func TestGPUSourceManager_AutoRegister(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 8)
	defer ctx.Close()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Before IngestYUV, GetFrame should return nil.
	frame := mgr.GetFrame("cam1")
	assert.Nil(t, frame, "GetFrame before any ingest should return nil")

	// IngestYUV should auto-register the source.
	yuv := createTestYUV420p(w, h, 180, 100, 200)
	mgr.IngestYUV("cam1", yuv, w, h, 90000)

	// Now GetFrame should return a valid frame.
	frame = mgr.GetFrame("cam1")
	require.NotNil(t, frame, "GetFrame after auto-register should return a frame")
	assert.Equal(t, int64(90000), frame.PTS)
	frame.Release()

	// Subsequent IngestYUV calls should update the cached frame.
	mgr.IngestYUV("cam1", yuv, w, h, 180000)
	frame = mgr.GetFrame("cam1")
	require.NotNil(t, frame)
	assert.Equal(t, int64(180000), frame.PTS)
	frame.Release()
}

func TestGPUSourceManager_AutoRegisterMultipleSources(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 16)
	defer ctx.Close()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	yuv := createTestYUV420p(w, h, 128, 128, 128)

	// Auto-register 4 sources via IngestYUV.
	for i := 0; i < 4; i++ {
		key := "cam" + string(rune('1'+i))
		mgr.IngestYUV(key, yuv, w, h, int64(i*90000))
	}

	// All 4 should be accessible via GetFrame.
	for i := 0; i < 4; i++ {
		key := "cam" + string(rune('1'+i))
		frame := mgr.GetFrame(key)
		require.NotNil(t, frame, "GetFrame(%s) should return a frame", key)
		assert.Equal(t, int64(i*90000), frame.PTS)
		frame.Release()
	}
}

func TestGPUSourceManager_ExplicitRegisterThenIngest(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 8)
	defer ctx.Close()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Explicit registration.
	mgr.RegisterSource("cam1", w, h, nil)

	// IngestYUV should use the existing entry (not create a new one).
	yuv := createTestYUV420p(w, h, 180, 100, 200)
	mgr.IngestYUV("cam1", yuv, w, h, 90000)

	frame := mgr.GetFrame("cam1")
	require.NotNil(t, frame)
	assert.Equal(t, int64(90000), frame.PTS)
	frame.Release()
}

// mockKeyBridgeWithSourceMgr uses a real GPUSourceManager for GPUFill.
type mockKeyBridgeWithSourceMgr struct {
	hasKeys   bool
	keys      []EnabledKeySnapshot
	sourceMgr *GPUSourceManager
}

func (m *mockKeyBridgeWithSourceMgr) HasEnabledKeysWithFills() bool             { return m.hasKeys }
func (m *mockKeyBridgeWithSourceMgr) HasEnabledKeys() bool                      { return m.hasKeys }
func (m *mockKeyBridgeWithSourceMgr) SnapshotEnabledKeys() []EnabledKeySnapshot { return m.keys }
func (m *mockKeyBridgeWithSourceMgr) GPUFill(sourceKey string) *GPUFrame {
	if m.sourceMgr == nil {
		return nil
	}
	return m.sourceMgr.GetFrame(sourceKey)
}

// mockLayoutStateWithSourceMgr uses a real GPUSourceManager for GPUFill.
type mockLayoutStateWithSourceMgr struct {
	active    bool
	slots     []SlotSnapshot
	sourceMgr *GPUSourceManager
}

func (m *mockLayoutStateWithSourceMgr) Active() bool                  { return m.active }
func (m *mockLayoutStateWithSourceMgr) SnapshotSlots() []SlotSnapshot { return m.slots }
func (m *mockLayoutStateWithSourceMgr) GPUFill(sourceKey string) *GPUFrame {
	if m.sourceMgr == nil {
		return nil
	}
	return m.sourceMgr.GetFrame(sourceKey)
}

func TestGPUPipelineIntegration_SourceManagerWithPipeline(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 16) // extra frames for source mgr + pipeline
	defer ctx.Close()
	defer pool.Close()

	// Create source manager and auto-register via IngestYUV.
	sourceMgr := NewGPUSourceManager(ctx, pool, nil)
	defer sourceMgr.Close()

	// Ingest a white fill for the PIP source.
	pipFill := createTestYUV420p(w, h, 235, 128, 128) // white
	sourceMgr.IngestYUV("pip-cam", pipFill, w, h, 90000)

	// Layout adapter with GPUFill backed by source manager.
	layoutAdapter := &mockLayoutStateWithSourceMgr{
		active:    true,
		sourceMgr: sourceMgr,
		slots: []SlotSnapshot{
			{
				Index:     0,
				Enabled:   true,
				SourceKey: "pip-cam",
				Rect:      Rect{X: 0, Y: 0, W: 32, H: 32},
				// FillYUV is empty — GPUFill should be used instead.
				Alpha: 1.0,
			},
		},
	}

	keyBridge := &mockKeyBridge{hasKeys: false}
	compositor := &mockCompositorState{active: false}
	stmapState := &mockSTMapState{hasMap: false}

	pipe := NewGPUPipeline(ctx, pool)
	err := pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUKeyNode(ctx, pool, keyBridge),
		NewGPULayoutNode(ctx, pool, layoutAdapter),
		NewGPUCompositorNode(ctx, compositor),
		NewGPUSTMapNode(ctx, pool, stmapState),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Create a black program frame.
	inputYUV := createTestYUV420p(w, h, 16, 128, 128) // black

	frame, err := pipe.RunWithUpload(inputYUV, w, h, 90000)
	require.NoError(t, err, "pipeline with GPUFill-backed layout should succeed")
	require.NotNil(t, frame)
	defer frame.Release()

	// Download and verify the slot region changed.
	outputYUV := make([]byte, w*h*3/2)
	err = Download(ctx, outputYUV, frame, w, h)
	require.NoError(t, err)

	// The slot region (top-left 32x32) should have the white fill from GPU cache.
	insideY := outputYUV[16*w+16] // center of slot
	outsideY := outputYUV[(h-1)*w+(w-1)] // bottom-right corner

	assert.Equal(t, byte(16), outsideY,
		"Y outside slot should remain black")

	// Inside should be close to 235 (white fill from GPU cache).
	assert.InDelta(t, 235, int(insideY), 5,
		"Y inside slot should be close to 235 (GPU cached fill)")

	t.Logf("Source manager + pipeline: inside Y=%d (expected ~235), outside Y=%d (expected 16)",
		insideY, outsideY)
}

func TestGPUPipelineIntegration_MultipleFrames(t *testing.T) {
	const w, h = 64, 64
	ctx, pool := setupGPU(t, w, h, 4)
	defer ctx.Close()
	defer pool.Close()

	// Simple pipeline with no active processing.
	pipe := NewGPUPipeline(ctx, pool)
	err := pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUPassthroughNode("test", false),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Run 10 frames through the pipeline to verify stability.
	for i := 0; i < 10; i++ {
		yuv := createTestYUV420p(w, h, byte(50+i*20), 128, 128)
		frame, err := pipe.RunWithUpload(yuv, w, h, int64(i*3000))
		require.NoError(t, err, "frame %d should succeed", i)
		require.NotNil(t, frame)
		frame.Release()
	}

	snap := pipe.Snapshot()
	assert.Equal(t, int64(10), snap["run_count"].(int64))
	t.Logf("Multiple frames: run_count=%d", snap["run_count"])
}
