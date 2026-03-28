//go:build correctness && ((cgo && cuda) || (cgo && darwin))

package gpu

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Local mock state implementations (prefixed to avoid collision with integration test mocks) ---

type corrKeyBridge struct {
	hasKeys bool
	keys    []EnabledKeySnapshot
}

func (m *corrKeyBridge) HasEnabledKeysWithFills() bool             { return m.hasKeys }
func (m *corrKeyBridge) HasEnabledKeys() bool                      { return m.hasKeys }
func (m *corrKeyBridge) SnapshotEnabledKeys() []EnabledKeySnapshot { return m.keys }
func (m *corrKeyBridge) GPUFill(sourceKey string) *GPUFrame        { return nil }

type corrCompositorState struct {
	active bool
	layers []VisibleLayerSnapshot
}

func (m *corrCompositorState) HasActiveLayers() bool                         { return m.active }
func (m *corrCompositorState) SnapshotVisibleLayers() []VisibleLayerSnapshot { return m.layers }

type corrLayoutState struct {
	active bool
	slots  []SlotSnapshot
}

func (m *corrLayoutState) Active() bool                       { return m.active }
func (m *corrLayoutState) SnapshotSlots() []SlotSnapshot      { return m.slots }
func (m *corrLayoutState) GPUFill(sourceKey string) *GPUFrame { return nil }

type corrSTMapState struct {
	hasMap   bool
	name     string
	s, t     []float32
	animated bool
	animIdx  int
}

func (m *corrSTMapState) HasProgramMap() bool                               { return m.hasMap }
func (m *corrSTMapState) ProgramMapName() string                            { return m.name }
func (m *corrSTMapState) ProgramSTArrays() ([]float32, []float32)           { return m.s, m.t }
func (m *corrSTMapState) IsAnimated() bool                                  { return m.animated }
func (m *corrSTMapState) AdvanceAnimatedIndex() int                         { i := m.animIdx; m.animIdx++; return i }
func (m *corrSTMapState) AnimatedSTArraysAt(idx int) ([]float32, []float32) { return m.s, m.t }
func (m *corrSTMapState) ProgramGPUSnapshot() (string, bool, int, []float32, []float32, func()) {
	if !m.hasMap {
		return "", false, 0, nil, nil, func() {}
	}
	idx := 0
	if m.animated {
		idx = m.animIdx
		m.animIdx++
	}
	return m.name, m.animated, idx, m.s, m.t, func() {}
}

// ============================================================================
// TestGPUFullPipelineChain — Upload A (Y=80), blend with B (Y=160) at 0.5,
// download, verify avg Y≈120±2. Run 30 times, verify consistency.
// ============================================================================

func TestGPUFullPipelineChain(t *testing.T) {
	ctx := testContext(t)

	const width, height = 1920, 1080

	// Need at least 4 frames: A, B, dst, download scratch
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	const iterations = 30
	yResults := make([]float64, 0, iterations)

	for i := 0; i < iterations; i++ {
		a := makeTestFrame(t, ctx, pool, width, height, 80, 128, 128)
		b := makeTestFrame(t, ctx, pool, width, height, 160, 128, 128)
		dst, err := pool.Acquire()
		require.NoError(t, err, "iteration %d: acquire dst", i)

		// Blend at 0.5: expect (80+160)/2 = 120
		err = BlendMix(ctx, dst, a, b, 0.5)
		require.NoError(t, err, "iteration %d: blend", i)

		yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
		t.Logf("iteration %d: Y=%.2f, Cb=%.2f, Cr=%.2f", i, yAvg, cbAvg, crAvg)

		yResults = append(yResults, yAvg)

		// Assert per-iteration
		assert.InDelta(t, 120.0, yAvg, 2.0,
			"iteration %d: avg Y should be ~120 (blend of 80 and 160 at 0.5)", i)
		assert.InDelta(t, 128.0, cbAvg, 2.0,
			"iteration %d: avg Cb should be ~128", i)
		assert.InDelta(t, 128.0, crAvg, 2.0,
			"iteration %d: avg Cr should be ~128", i)

		dst.Release()
		b.Release()
		a.Release()
	}

	// Verify consistency across all 30 runs: all Y values should be within ±1 of each other.
	if len(yResults) > 1 {
		minY, maxY := yResults[0], yResults[0]
		for _, v := range yResults[1:] {
			if v < minY {
				minY = v
			}
			if v > maxY {
				maxY = v
			}
		}
		spread := maxY - minY
		t.Logf("Y consistency across %d runs: min=%.4f, max=%.4f, spread=%.4f", iterations, minY, maxY, spread)
		assert.LessOrEqual(t, spread, 1.0,
			"Y values should be consistent across iterations: spread=%.4f", spread)
	}
}

// ============================================================================
// TestGPUPipelineSustained1080p — 300 frames through upload→blend→download
// at 1920x1080. Monitor pool misses. Assert avg frame time < 33ms.
// ============================================================================

func TestGPUPipelineSustained1080p(t *testing.T) {
	ctx := testContext(t)

	const (
		width      = 1920
		height     = 1080
		frameCount = 300
	)

	// Pool of 6 frames: 2 sources + 1 dst + 3 headroom to avoid constant misses.
	pool, err := NewFramePool(ctx, width, height, 6)
	require.NoError(t, err)
	defer pool.Close()

	// Pre-allocate download buffer.
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	downloadBuf := make([]byte, ySize+cbSize+crSize)

	// Record initial pool stats.
	hitsStart, missesStart := pool.Stats()

	frameTimes := make([]time.Duration, 0, frameCount)
	var totalDuration time.Duration

	for i := 0; i < frameCount; i++ {
		start := time.Now()

		// Alternate Y values to exercise different blend results.
		yA := byte(60 + (i % 40))
		yB := byte(180 - (i % 30))

		a := makeTestFrame(t, ctx, pool, width, height, yA, 128, 128)
		b := makeTestFrame(t, ctx, pool, width, height, yB, 128, 128)
		dst, err := pool.Acquire()
		require.NoError(t, err, "frame %d: acquire dst", i)

		err = BlendMix(ctx, dst, a, b, 0.5)
		require.NoError(t, err, "frame %d: blend", i)

		err = Download(ctx, downloadBuf, dst, width, height)
		require.NoError(t, err, "frame %d: download", i)

		elapsed := time.Since(start)
		frameTimes = append(frameTimes, elapsed)
		totalDuration += elapsed

		// Release in correct order.
		dst.Release()
		b.Release()
		a.Release()
	}

	// Compute timing statistics.
	avgFrameTime := totalDuration / time.Duration(frameCount)
	var minFrame, maxFrame time.Duration
	minFrame = frameTimes[0]
	maxFrame = frameTimes[0]
	for _, ft := range frameTimes[1:] {
		if ft < minFrame {
			minFrame = ft
		}
		if ft > maxFrame {
			maxFrame = ft
		}
	}

	// Compute p99.
	sorted := make([]time.Duration, len(frameTimes))
	copy(sorted, frameTimes)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	p50 := sorted[len(sorted)/2]
	p99 := sorted[int(float64(len(sorted))*0.99)]

	hitsEnd, missesEnd := pool.Stats()
	poolMisses := missesEnd - missesStart
	poolHits := hitsEnd - hitsStart

	t.Logf("sustained 1080p stats (%d frames):", frameCount)
	t.Logf("  avg frame time:  %v", avgFrameTime)
	t.Logf("  min frame time:  %v", minFrame)
	t.Logf("  max frame time:  %v", maxFrame)
	t.Logf("  p50 frame time:  %v", p50)
	t.Logf("  p99 frame time:  %v", p99)
	t.Logf("  pool hits:       %d", poolHits)
	t.Logf("  pool misses:     %d", poolMisses)

	// Assert: average frame time must be under 33ms (30fps budget).
	assert.Less(t, avgFrameTime, 33*time.Millisecond,
		"average frame time should be under 33ms for 30fps; got %v", avgFrameTime)

	// Pool misses after initial warm-up should be low.
	// The first few frames may miss, but sustained should be all hits.
	// With pool size 6 and 3 frames per iteration, we expect some misses only
	// at the start. After warm-up, misses should stop.
	// We check that total misses are less than 10% of total acquisitions.
	totalAcquisitions := poolHits + poolMisses
	if totalAcquisitions > 0 {
		missRate := float64(poolMisses) / float64(totalAcquisitions)
		t.Logf("  pool miss rate:  %.2f%%", missRate*100)
		assert.Less(t, missRate, 0.10,
			"pool miss rate should be under 10%%; got %.2f%% (%d misses / %d total)",
			missRate*100, poolMisses, totalAcquisitions)
	}
}

// ============================================================================
// TestGPUPipelineAllNodesConfigured — Create and configure all available
// GPU nodes (key, layout, compositor) at 1920x1080. Verify Configure()
// succeeds for each. Process one frame through each node individually.
// ============================================================================

func TestGPUPipelineAllNodesConfigured(t *testing.T) {
	ctx := testContext(t)

	const width, height = 1920, 1080

	// Extra pool capacity for node internal allocations (e.g., key mask buffer).
	pool, err := NewFramePool(ctx, width, height, 10)
	require.NoError(t, err)
	defer pool.Close()

	pitch := pool.Pitch()

	// Create mock state providers (all inactive — tests pure configure path).
	keyBridge := &corrKeyBridge{hasKeys: false}
	compositor := &corrCompositorState{active: false}
	layout := &corrLayoutState{active: false}
	stmapState := &corrSTMapState{hasMap: false}

	// Define all nodes.
	type nodeCase struct {
		name string
		node GPUPipelineNode
	}
	nodes := []nodeCase{
		{"key", NewGPUKeyNode(ctx, pool, keyBridge)},
		{"layout", NewGPULayoutNode(ctx, pool, layout)},
		{"compositor", NewGPUCompositorNode(ctx, compositor)},
		{"stmap", NewGPUSTMapNode(ctx, pool, stmapState)},
	}

	// Test Configure on each node.
	for _, nc := range nodes {
		t.Run(fmt.Sprintf("Configure_%s", nc.name), func(t *testing.T) {
			if nc.node == nil {
				t.Skipf("node %s returned nil (stub build?)", nc.name)
			}

			t.Logf("configuring node %q at %dx%d, pitch=%d", nc.node.Name(), width, height, pitch)
			err := nc.node.Configure(width, height, pitch)
			t.Logf("configure result: err=%v", err)
			assert.NoError(t, err, "Configure() should succeed for node %q", nc.name)
		})
	}

	// Test ProcessGPU on each node individually with a real frame.
	frame := makeTestFrame(t, ctx, pool, width, height, 128, 128, 128)
	defer frame.Release()

	for _, nc := range nodes {
		t.Run(fmt.Sprintf("ProcessGPU_%s", nc.name), func(t *testing.T) {
			if nc.node == nil {
				t.Skipf("node %s returned nil (stub build?)", nc.name)
			}

			t.Logf("processing frame through node %q (active=%v)", nc.node.Name(), nc.node.Active())

			err := nc.node.ProcessGPU(frame)
			t.Logf("processGPU result: err=%v", err)
			assert.NoError(t, err, "ProcessGPU() should not error for node %q", nc.name)

			nodeErr := nc.node.Err()
			t.Logf("node err after process: %v", nodeErr)
			assert.NoError(t, nodeErr, "Err() should be nil after successful ProcessGPU for node %q", nc.name)
		})
	}

	// Test building a full pipeline with all nodes.
	t.Run("FullPipelineBuild", func(t *testing.T) {
		pipe := NewGPUPipeline(ctx, pool)

		allNodes := make([]GPUPipelineNode, 0, len(nodes))
		for _, nc := range nodes {
			if nc.node != nil {
				allNodes = append(allNodes, nc.node)
			}
		}

		err := pipe.Build(width, height, pitch, allNodes)
		t.Logf("pipeline build: err=%v, node count=%d", err, len(allNodes))
		assert.NoError(t, err, "Build() should succeed with all nodes at 1080p")

		if err == nil {
			// Run a frame through the full pipeline.
			pipeFrame := makeTestFrame(t, ctx, pool, width, height, 100, 128, 128)
			defer pipeFrame.Release()

			runErr := pipe.Run(pipeFrame)
			t.Logf("pipeline run: err=%v", runErr)
			assert.NoError(t, runErr, "Run() should succeed through full pipeline")

			// Verify frame survives pipeline (all nodes inactive -> passthrough).
			yAvg, cbAvg, crAvg := avgPlanes(t, ctx, pipeFrame, width, height)
			t.Logf("post-pipeline: Y=%.2f, Cb=%.2f, Cr=%.2f", yAvg, cbAvg, crAvg)
			assert.InDelta(t, 100.0, yAvg, 2.0, "Y should survive inactive pipeline")
			assert.InDelta(t, 128.0, cbAvg, 2.0, "Cb should survive inactive pipeline")
			assert.InDelta(t, 128.0, crAvg, 2.0, "Cr should survive inactive pipeline")

			_ = pipe.Close()
		}
	})

	// Clean up nodes.
	for _, nc := range nodes {
		if nc.node != nil {
			_ = nc.node.Close()
		}
	}
}

// ============================================================================
// TestGPUMultiSourceQuadComposite — Ingest 4 sources (distinct colors),
// configure quad layout via PIPComposite. Download result, verify each
// quadrant has correct color ±2.
// ============================================================================

func TestGPUMultiSourceQuadComposite(t *testing.T) {
	ctx := testContext(t)

	const width, height = 1920, 1080

	// Pool: 1 dst + 4 sources + headroom.
	pool, err := NewFramePool(ctx, width, height, 8)
	require.NoError(t, err)
	defer pool.Close()

	// Create 4 source frames at source resolution (960x540 = quarter of 1080p).
	srcW, srcH := width/2, height/2
	srcPool, err := NewFramePool(ctx, srcW, srcH, 4)
	require.NoError(t, err)
	defer srcPool.Close()

	// Define 4 distinct colors (Y values). Cb/Cr stay at 128 (neutral).
	type quadColor struct {
		name string
		y    byte
	}
	colors := []quadColor{
		{"top-left", 40},
		{"top-right", 100},
		{"bottom-left", 160},
		{"bottom-right", 220},
	}

	// Create source frames.
	sources := make([]*GPUFrame, 4)
	for i, c := range colors {
		sources[i] = makeTestFrame(t, ctx, srcPool, srcW, srcH, c.y, 128, 128)
		defer sources[i].Release()
	}

	// Create destination frame, fill with black first.
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()
	require.NoError(t, FillBlack(ctx, dst))

	// Define quad layout rectangles. Even-align all coordinates.
	halfW := (width / 2) &^ 1
	halfH := (height / 2) &^ 1
	rects := []Rect{
		{X: 0, Y: 0, W: halfW, H: halfH},            // top-left
		{X: halfW, Y: 0, W: halfW, H: halfH},         // top-right
		{X: 0, Y: halfH, W: halfW, H: halfH},         // bottom-left
		{X: halfW, Y: halfH, W: halfW, H: halfH},     // bottom-right
	}

	// Composite each source into its quadrant.
	for i, rect := range rects {
		t.Logf("compositing %s (Y=%d) into rect(%d,%d,%d,%d)",
			colors[i].name, colors[i].y, rect.X, rect.Y, rect.W, rect.H)
		err := PIPComposite(ctx, dst, sources[i], rect, 1.0)
		require.NoError(t, err, "PIPComposite %s", colors[i].name)
	}

	// Download result.
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download(ctx, result, dst, width, height))

	// Sample each quadrant center and verify Y value.
	type sample struct {
		name     string
		row, col int
		expected byte
	}
	samples := []sample{
		{"top-left", halfH / 4, halfW / 4, colors[0].y},
		{"top-right", halfH / 4, halfW + halfW/4, colors[1].y},
		{"bottom-left", halfH + halfH/4, halfW / 4, colors[2].y},
		{"bottom-right", halfH + halfH/4, halfW + halfW/4, colors[3].y},
	}

	for _, s := range samples {
		idx := s.row*width + s.col
		actual := result[idx]
		t.Logf("quadrant %s: sample at (%d,%d) => Y=%d (expected=%d)",
			s.name, s.row, s.col, actual, s.expected)
		assert.InDelta(t, float64(s.expected), float64(actual), 2.0,
			"quadrant %s: Y at center (%d,%d) should be ~%d, got %d",
			s.name, s.row, s.col, s.expected, actual)
	}

	// Also compute average Y for each quadrant region (more robust).
	for qi, rect := range rects {
		var sum int64
		count := 0
		// Sample interior area (avoid edges where bilinear scaling may cause blending).
		margin := 10
		for row := rect.Y + margin; row < rect.Y+rect.H-margin; row++ {
			for col := rect.X + margin; col < rect.X+rect.W-margin; col++ {
				sum += int64(result[row*width+col])
				count++
			}
		}
		avg := float64(sum) / float64(count)
		t.Logf("quadrant %s: region avg Y=%.2f (expected=%d, count=%d)",
			colors[qi].name, avg, colors[qi].y, count)
		assert.InDelta(t, float64(colors[qi].y), avg, 2.0,
			"quadrant %s: avg Y should be ~%d, got %.2f",
			colors[qi].name, colors[qi].y, avg)
	}

	// Verify chroma planes are neutral (128) everywhere.
	var cbSum, crSum int64
	for i := 0; i < cbSize; i++ {
		cbSum += int64(result[ySize+i])
	}
	for i := 0; i < crSize; i++ {
		crSum += int64(result[ySize+cbSize+i])
	}
	cbAvg := float64(cbSum) / float64(cbSize)
	crAvg := float64(crSum) / float64(crSize)
	t.Logf("chroma: avg Cb=%.2f, avg Cr=%.2f", cbAvg, crAvg)
	assert.InDelta(t, 128.0, cbAvg, 2.0, "avg Cb should be ~128")
	assert.InDelta(t, 128.0, crAvg, 2.0, "avg Cr should be ~128")

	// Verify quadrant boundaries: adjacent quadrants should have distinct Y values.
	// Sample just inside each boundary.
	leftOfBoundary := result[(halfH/2)*width+(halfW-5)]
	rightOfBoundary := result[(halfH/2)*width+(halfW+5)]
	diff := math.Abs(float64(leftOfBoundary) - float64(rightOfBoundary))
	t.Logf("horizontal boundary: left Y=%d, right Y=%d, diff=%.0f", leftOfBoundary, rightOfBoundary, diff)
	assert.Greater(t, diff, 30.0,
		"quadrants across horizontal boundary should have distinct Y values: left=%d right=%d",
		leftOfBoundary, rightOfBoundary)

	topOfBoundary := result[(halfH-5)*width+(halfW/2)]
	bottomOfBoundary := result[(halfH+5)*width+(halfW/2)]
	vdiff := math.Abs(float64(topOfBoundary) - float64(bottomOfBoundary))
	t.Logf("vertical boundary: top Y=%d, bottom Y=%d, diff=%.0f", topOfBoundary, bottomOfBoundary, vdiff)
	assert.Greater(t, vdiff, 30.0,
		"quadrants across vertical boundary should have distinct Y values: top=%d bottom=%d",
		topOfBoundary, bottomOfBoundary)
}
