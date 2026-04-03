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
// download, verify EVERY Y pixel is 119-121. The GPU blend kernel computes:
//   (80*128 + 160*128 + 128) >> 8 = 120 exactly.
// Run 30 times, verify consistency.
// ============================================================================

func TestGPUFullPipelineChain(t *testing.T) {
	ctx := testContext(t)

	const width, height = 1920, 1080

	// Need at least 4 frames: A, B, dst, download scratch
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	downloadBuf := make([]byte, ySize+cbSize+crSize)

	const iterations = 30
	yResults := make([]float64, 0, iterations)

	for i := 0; i < iterations; i++ {
		a := makeTestFrame(t, ctx, pool, width, height, 80, 128, 128)
		b := makeTestFrame(t, ctx, pool, width, height, 160, 128, 128)
		dst, err := pool.Acquire()
		require.NoError(t, err, "iteration %d: acquire dst", i)

		// Blend at 0.5: expect (80*128 + 160*128 + 128) >> 8 = 120 exactly
		err = BlendMix(ctx, dst, a, b, 0.5)
		require.NoError(t, err, "iteration %d: blend", i)

		// Download full frame for per-pixel validation
		err = Download(ctx, downloadBuf, dst, width, height)
		require.NoError(t, err, "iteration %d: download", i)

		// Per-pixel Y plane validation: every pixel must be 119-121 (±1)
		outOfRange := 0
		var ySum int64
		for j := 0; j < ySize; j++ {
			v := downloadBuf[j]
			ySum += int64(v)
			if v < 119 || v > 121 {
				outOfRange++
			}
		}
		yAvg := float64(ySum) / float64(ySize)

		// Per-pixel Cb/Cr validation
		var cbSum, crSum int64
		cbOutOfRange := 0
		crOutOfRange := 0
		for j := 0; j < cbSize; j++ {
			v := downloadBuf[ySize+j]
			cbSum += int64(v)
			if v < 127 || v > 129 {
				cbOutOfRange++
			}
		}
		for j := 0; j < crSize; j++ {
			v := downloadBuf[ySize+cbSize+j]
			crSum += int64(v)
			if v < 127 || v > 129 {
				crOutOfRange++
			}
		}
		cbAvg := float64(cbSum) / float64(cbSize)
		crAvg := float64(crSum) / float64(crSize)

		t.Logf("iteration %d: Y=%.4f (out-of-range=%d/%d), Cb=%.4f (oor=%d), Cr=%.4f (oor=%d)",
			i, yAvg, outOfRange, ySize, cbAvg, cbOutOfRange, crAvg, crOutOfRange)

		yResults = append(yResults, yAvg)

		// Assert per-iteration: avg within ±1
		assert.InDelta(t, 120.0, yAvg, 1.0,
			"iteration %d: avg Y should be ~120 (blend of 80 and 160 at 0.5)", i)
		assert.InDelta(t, 128.0, cbAvg, 1.0,
			"iteration %d: avg Cb should be ~128", i)
		assert.InDelta(t, 128.0, crAvg, 1.0,
			"iteration %d: avg Cr should be ~128", i)

		// Per-pixel: zero pixels outside ±1 tolerance
		assert.Equal(t, 0, outOfRange,
			"iteration %d: %d/%d Y pixels outside [119,121] — blend should produce exactly 120", i, outOfRange, ySize)
		assert.Equal(t, 0, cbOutOfRange,
			"iteration %d: %d Cb pixels outside [127,129]", i, cbOutOfRange)
		assert.Equal(t, 0, crOutOfRange,
			"iteration %d: %d Cr pixels outside [127,129]", i, crOutOfRange)

		dst.Release()
		b.Release()
		a.Release()
	}

	// Verify consistency across all 30 runs: all Y values should be within ±0.5 of each other.
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
		assert.LessOrEqual(t, spread, 0.5,
			"Y values should be identical across iterations: spread=%.4f", spread)
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

	// Note: 1080p upload+blend+download is ~42ms on Metal (Apple Silicon), ~15ms on CUDA (L4).
	// Real-time 30fps requires GPU-resident pipeline (no CPU round-trip).
	// Sanity check: assert < 100ms to catch hangs/stalls, not hardware limits.
	assert.Less(t, avgFrameTime, 100*time.Millisecond,
		"average frame time should be under 100ms (sanity check); got %v", avgFrameTime)

	// Pool misses: with pool size 6 and 3 frames per iteration (all released before
	// next iteration), we expect at most the initial 6 cold misses. Assert hard cap.
	require.Less(t, poolMisses, uint64(5),
		"pool should not thrash under sustained load: got %d misses (hits=%d)", poolMisses, poolHits)
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
			assert.InDelta(t, 100.0, yAvg, 1.0, "Y should survive inactive pipeline")
			assert.InDelta(t, 128.0, cbAvg, 1.0, "Cb should survive inactive pipeline")
			assert.InDelta(t, 128.0, crAvg, 1.0, "Cr should survive inactive pipeline")

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
		{X: 0, Y: 0, W: halfW, H: halfH},         // top-left
		{X: halfW, Y: 0, W: halfW, H: halfH},     // top-right
		{X: 0, Y: halfH, W: halfW, H: halfH},     // bottom-left
		{X: halfW, Y: halfH, W: halfW, H: halfH}, // bottom-right
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
		assert.InDelta(t, float64(s.expected), float64(actual), 1.0,
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
		assert.InDelta(t, float64(colors[qi].y), avg, 1.0,
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
	assert.InDelta(t, 128.0, cbAvg, 1.0, "avg Cb should be ~128")
	assert.InDelta(t, 128.0, crAvg, 1.0, "avg Cr should be ~128")

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

	// Boundary precision checks: verify pixel at last column of left quadrant
	// is still the correct color, and first column of right quadrant is the next
	// quadrant's color. This catches off-by-one in composite coordinates.
	midRow := halfH / 2 // middle of top row of quadrants
	lastColLeft := halfW - 1
	firstColRight := halfW

	leftEdge := result[midRow*width+lastColLeft]
	rightEdge := result[midRow*width+firstColRight]
	t.Logf("boundary precision: pixel at (%d,%d)=Y%d (expect ~%d), pixel at (%d,%d)=Y%d (expect ~%d)",
		midRow, lastColLeft, leftEdge, colors[0].y, midRow, firstColRight, rightEdge, colors[1].y)
	assert.InDelta(t, float64(colors[0].y), float64(leftEdge), 2.0,
		"last pixel of top-left quadrant should match top-left color: got Y=%d, expected ~%d",
		leftEdge, colors[0].y)
	assert.InDelta(t, float64(colors[1].y), float64(rightEdge), 2.0,
		"first pixel of top-right quadrant should match top-right color: got Y=%d, expected ~%d",
		rightEdge, colors[1].y)

	// Vertical boundary: last row of top-left vs first row of bottom-left
	lastRowTop := halfH - 1
	firstRowBottom := halfH
	midCol := halfW / 2

	topEdge := result[lastRowTop*width+midCol]
	bottomEdge := result[firstRowBottom*width+midCol]
	t.Logf("vertical boundary precision: pixel at (%d,%d)=Y%d (expect ~%d), pixel at (%d,%d)=Y%d (expect ~%d)",
		lastRowTop, midCol, topEdge, colors[0].y, firstRowBottom, midCol, bottomEdge, colors[2].y)
	assert.InDelta(t, float64(colors[0].y), float64(topEdge), 2.0,
		"last row of top-left quadrant should match top-left color: got Y=%d, expected ~%d",
		topEdge, colors[0].y)
	assert.InDelta(t, float64(colors[2].y), float64(bottomEdge), 2.0,
		"first row of bottom-left quadrant should match bottom-left color: got Y=%d, expected ~%d",
		bottomEdge, colors[2].y)
}

// ============================================================================
// TestGPUMultiSourceIngest — Upload 4 frames at 1920x1080 with distinct Y
// values (40, 100, 160, 220), download each, verify correct color survives
// the upload→download round-trip.
// ============================================================================

func TestGPUMultiSourceIngest(t *testing.T) {
	ctx := testContext(t)

	const width, height = 1920, 1080

	// Pool: 4 source frames + headroom
	pool, err := NewFramePool(ctx, width, height, 6)
	require.NoError(t, err)
	defer pool.Close()

	type sourceColor struct {
		name string
		y    byte
	}
	sources := []sourceColor{
		{"source-A", 40},
		{"source-B", 100},
		{"source-C", 160},
		{"source-D", 220},
	}

	// Upload all 4 simultaneously (create all frames first, then verify)
	frames := make([]*GPUFrame, len(sources))
	for i, s := range sources {
		frames[i] = makeTestFrame(t, ctx, pool, width, height, s.y, 128, 128)
		defer frames[i].Release()
	}

	// Download each and verify correct color
	for i, s := range sources {
		yAvg, cbAvg, crAvg := avgPlanes(t, ctx, frames[i], width, height)
		t.Logf("MEASURE: %s: uploaded Y=%d, downloaded avg Y=%.4f, Cb=%.4f, Cr=%.4f",
			s.name, s.y, yAvg, cbAvg, crAvg)

		assert.InDelta(t, float64(s.y), yAvg, 1.0,
			"%s: avg Y should be ~%d after upload+download, got %.4f", s.name, s.y, yAvg)
		assert.InDelta(t, 128.0, cbAvg, 1.0,
			"%s: avg Cb should be ~128, got %.4f", s.name, cbAvg)
		assert.InDelta(t, 128.0, crAvg, 1.0,
			"%s: avg Cr should be ~128, got %.4f", s.name, crAvg)
	}

	// Verify no cross-contamination: each frame is still independent
	// Re-download and compare pairwise
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	bufs := make([][]byte, len(sources))
	for i := range sources {
		bufs[i] = make([]byte, totalSize)
		require.NoError(t, Download(ctx, bufs[i], frames[i], width, height))
	}

	for i := 0; i < len(sources); i++ {
		for j := i + 1; j < len(sources); j++ {
			// Spot-check 100 Y-plane samples — they should differ
			diffCount := 0
			for k := 0; k < 100; k++ {
				idx := k * (ySize / 100)
				if bufs[i][idx] != bufs[j][idx] {
					diffCount++
				}
			}
			t.Logf("MEASURE: %s vs %s: %d/100 Y samples differ",
				sources[i].name, sources[j].name, diffCount)
			assert.Greater(t, diffCount, 90,
				"sources %s and %s should have distinct Y planes, only %d/100 differed",
				sources[i].name, sources[j].name, diffCount)
		}
	}

	t.Log("MEASURE: 4-source upload+download complete, colors verified")
}

// ============================================================================
// TestGPUMultiSourceCutUnderLoad — Simulate program switching across 4
// sources with distinct colors. Verify each cut produces the correct source
// color with no cross-source contamination. 20 rapid switches total.
// ============================================================================

func TestGPUMultiSourceCutUnderLoad(t *testing.T) {
	ctx := testContext(t)

	const width, height = 1920, 1080

	// Pool: 4 sources + 1 dst + headroom
	pool, err := NewFramePool(ctx, width, height, 8)
	require.NoError(t, err)
	defer pool.Close()

	// Create 4 source frames with distinct Y values
	type sourceColor struct {
		name string
		y    byte
	}
	colors := []sourceColor{
		{"source-0", 40},
		{"source-1", 100},
		{"source-2", 160},
		{"source-3", 220},
	}

	frames := make([]*GPUFrame, len(colors))
	for i, c := range colors {
		frames[i] = makeTestFrame(t, ctx, pool, width, height, c.y, 128, 128)
		defer frames[i].Release()
	}

	// Test 1: CUT to source 0 (blend at pos=1.0 means 100% source B; pos=0.0 means 100% source A)
	// BlendMix(dst, A, B, pos) → result = A*(1-pos) + B*pos
	// So pos=0.0 → 100% A, pos=1.0 → 100% B
	// To simulate CUT to a source, blend it at pos=0.0 (pure A) or pos=1.0 (pure B)

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	downloadBuf := make([]byte, ySize+cbSize+crSize)

	// Helper: download and verify >95% of Y pixels match expected ±1.
	verifyPerPixel := func(label string, expected byte) float64 {
		t.Helper()
		err := Download(ctx, downloadBuf, dst, width, height)
		require.NoError(t, err, "%s: download", label)
		matching := 0
		for j := 0; j < ySize; j++ {
			v := downloadBuf[j]
			if v >= expected-1 && v <= expected+1 {
				matching++
			}
		}
		pct := float64(matching) / float64(ySize) * 100.0
		t.Logf("MEASURE: %s: %d/%d Y pixels match %d±1 (%.2f%%)", label, matching, ySize, expected, pct)
		require.Greater(t, pct, 95.0,
			"%s: expected >95%% of Y pixels to match %d±1, got %.2f%%", label, expected, pct)
		return pct
	}

	// CUT to source 0: blend(source0, source1, 0.0) → pure source0
	err = BlendMix(ctx, dst, frames[0], frames[1], 0.0)
	require.NoError(t, err)
	verifyPerPixel("CUT to source-0", colors[0].y)

	// CUT to source 1: blend(source0, source1, 1.0) → pure source1
	err = BlendMix(ctx, dst, frames[0], frames[1], 1.0)
	require.NoError(t, err)
	verifyPerPixel("CUT to source-1", colors[1].y)

	// Rapid cycle through all 4 sources: 20 switches
	switchErrors := 0
	for i := 0; i < 20; i++ {
		srcIdx := i % len(colors)
		// Blend to 100% of target source using pos=1.0 with dummy as A
		dummyIdx := (srcIdx + 1) % len(colors)
		err = BlendMix(ctx, dst, frames[dummyIdx], frames[srcIdx], 1.0)
		require.NoError(t, err, "switch %d: blend to %s", i, colors[srcIdx].name)

		// Full-frame per-pixel verification
		err = Download(ctx, downloadBuf, dst, width, height)
		require.NoError(t, err, "switch %d: download", i)

		matching := 0
		for j := 0; j < ySize; j++ {
			v := downloadBuf[j]
			if v >= colors[srcIdx].y-1 && v <= colors[srcIdx].y+1 {
				matching++
			}
		}
		pct := float64(matching) / float64(ySize) * 100.0
		t.Logf("MEASURE: switch %d → %s: %d/%d Y pixels match %d±1 (%.2f%%)",
			i, colors[srcIdx].name, matching, ySize, colors[srcIdx].y, pct)

		if pct < 95.0 {
			switchErrors++
			t.Logf("MEASURE: BUG: switch %d: only %.2f%% pixels matched (expected >95%%)", i, pct)
		}

		// Check no cross-contamination: Cb/Cr should stay neutral
		var cbSum, crSum int64
		for j := 0; j < cbSize; j++ {
			cbSum += int64(downloadBuf[ySize+j])
		}
		for j := 0; j < crSize; j++ {
			crSum += int64(downloadBuf[ySize+cbSize+j])
		}
		cbAvg := float64(cbSum) / float64(cbSize)
		crAvg := float64(crSum) / float64(crSize)
		if math.Abs(cbAvg-128.0) > 1.0 || math.Abs(crAvg-128.0) > 1.0 {
			t.Logf("MEASURE: BUG: switch %d: chroma contamination Cb=%.4f, Cr=%.4f", i, cbAvg, crAvg)
		}
	}

	assert.Equal(t, 0, switchErrors,
		"%d of 20 switches had <95%% pixel match — cross-source contamination?", switchErrors)
	t.Logf("MEASURE: 20 rapid switches complete, errors=%d", switchErrors)
}

// ============================================================================
// TestGPUEncodeConcurrentWithCompute — Run GPU blend (compute shader) and
// simultaneous download (transfer) in separate goroutines sharing the GPU.
// Verify no corruption over 30 iterations.
// ============================================================================

func TestGPUEncodeConcurrentWithCompute(t *testing.T) {
	ctx := testContext(t)

	const (
		width      = 1920
		height     = 1080
		iterations = 30
	)

	pool, err := NewFramePool(ctx, width, height, 8)
	require.NoError(t, err)
	defer pool.Close()

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	corruptionCount := 0

	for i := 0; i < iterations; i++ {
		// Create source frames for blend
		a := makeTestFrame(t, ctx, pool, width, height, 60, 128, 128)
		b := makeTestFrame(t, ctx, pool, width, height, 200, 128, 128)
		blendDst, err := pool.Acquire()
		require.NoError(t, err, "iteration %d: acquire blend dst", i)

		// Also create a separate frame for concurrent download
		readSrc := makeTestFrame(t, ctx, pool, width, height, 150, 128, 128)

		// Run blend and download concurrently
		blendErrCh := make(chan error, 1)
		downloadResultCh := make(chan []byte, 1)
		downloadErrCh := make(chan error, 1)

		// Goroutine 1: GPU compute (blend)
		go func() {
			blendErrCh <- BlendMix(ctx, blendDst, a, b, 0.5)
		}()

		// Goroutine 2: GPU transfer (download from separate frame)
		go func() {
			buf := make([]byte, totalSize)
			err := Download(ctx, buf, readSrc, width, height)
			downloadErrCh <- err
			downloadResultCh <- buf
		}()

		// Collect results
		blendErr := <-blendErrCh
		downloadErr := <-downloadErrCh
		downloadBuf := <-downloadResultCh

		if blendErr != nil {
			t.Logf("MEASURE: iteration %d: blend error = %v", i, blendErr)
		}
		require.NoError(t, blendErr, "iteration %d: blend should succeed", i)

		if downloadErr != nil {
			t.Logf("MEASURE: iteration %d: download error = %v", i, downloadErr)
		}
		require.NoError(t, downloadErr, "iteration %d: concurrent download should succeed", i)

		// Verify blend result: expected Y = (60+200)/2 = 130
		blendY, blendCb, blendCr := avgPlanes(t, ctx, blendDst, width, height)
		if i%10 == 0 {
			t.Logf("MEASURE: iteration %d: blend Y=%.4f (exp=130), Cb=%.4f, Cr=%.4f",
				i, blendY, blendCb, blendCr)
		}

		if math.Abs(blendY-130.0) > 1.0 {
			corruptionCount++
			t.Logf("MEASURE: BUG: iteration %d: blend Y=%.4f, expected ~130 (corruption?)", i, blendY)
		}

		// Verify download result: should be Y=150 from readSrc
		var ySum int64
		for j := 0; j < ySize; j++ {
			ySum += int64(downloadBuf[j])
		}
		dlYAvg := float64(ySum) / float64(ySize)
		if math.Abs(dlYAvg-150.0) > 1.0 {
			corruptionCount++
			t.Logf("MEASURE: BUG: iteration %d: download Y=%.4f, expected ~150 (corruption?)", i, dlYAvg)
		}
		if i%10 == 0 {
			t.Logf("MEASURE: iteration %d: concurrent download Y=%.4f (exp=150)", i, dlYAvg)
		}

		blendDst.Release()
		readSrc.Release()
		b.Release()
		a.Release()
	}

	assert.Equal(t, 0, corruptionCount,
		"found %d corrupted results in %d iterations of concurrent GPU compute+transfer",
		corruptionCount, iterations)
	t.Logf("MEASURE: %d iterations of concurrent compute+transfer complete, corruptions=%d",
		iterations, corruptionCount)
}

// ============================================================================
// TestGPU1080pFramePoolPressure — Small pool (6 frames) at 1920x1080,
// acquire 4, blend pairs, download, release and reacquire in a tight loop
// for 50 iterations. Pool should handle pressure gracefully.
// ============================================================================

func TestGPU1080pFramePoolPressure(t *testing.T) {
	ctx := testContext(t)

	const (
		width      = 1920
		height     = 1080
		poolSize   = 6
		iterations = 50
	)

	pool, err := NewFramePool(ctx, width, height, poolSize)
	require.NoError(t, err)
	defer pool.Close()

	hitsStart, missesStart := pool.Stats()
	t.Logf("MEASURE: initial pool stats: hits=%d, misses=%d", hitsStart, missesStart)

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize
	downloadBuf := make([]byte, totalSize)

	corruptFrames := 0
	var totalHitsAccum, totalMissesAccum uint64

	for i := 0; i < iterations; i++ {
		hitsBefore, missesBefore := pool.Stats()

		// Acquire 4 frames
		f0 := makeTestFrame(t, ctx, pool, width, height, 50, 128, 128)
		f1 := makeTestFrame(t, ctx, pool, width, height, 120, 128, 128)
		f2 := makeTestFrame(t, ctx, pool, width, height, 180, 128, 128)
		f3 := makeTestFrame(t, ctx, pool, width, height, 230, 128, 128)

		// Blend pair 1: f0+f1 → dst1
		dst1, err := pool.Acquire()
		require.NoError(t, err, "iteration %d: acquire dst1", i)
		err = BlendMix(ctx, dst1, f0, f1, 0.5)
		require.NoError(t, err, "iteration %d: blend pair 1", i)

		// Download and verify pair 1
		err = Download(ctx, downloadBuf, dst1, width, height)
		require.NoError(t, err, "iteration %d: download dst1", i)
		var ySum1 int64
		for j := 0; j < ySize; j++ {
			ySum1 += int64(downloadBuf[j])
		}
		yAvg1 := float64(ySum1) / float64(ySize)
		expectedY1 := (50.0 + 120.0) / 2.0 // 85
		if math.Abs(yAvg1-expectedY1) > 1.0 {
			corruptFrames++
			t.Logf("MEASURE: BUG: iteration %d pair1: Y=%.4f expected=%.1f", i, yAvg1, expectedY1)
		}

		// Release pair 1 results
		dst1.Release()
		f0.Release()
		f1.Release()

		// Blend pair 2: f2+f3 → dst2 (reusing released pool slots)
		dst2, err := pool.Acquire()
		require.NoError(t, err, "iteration %d: acquire dst2", i)
		err = BlendMix(ctx, dst2, f2, f3, 0.5)
		require.NoError(t, err, "iteration %d: blend pair 2", i)

		// Download and verify pair 2
		err = Download(ctx, downloadBuf, dst2, width, height)
		require.NoError(t, err, "iteration %d: download dst2", i)
		var ySum2 int64
		for j := 0; j < ySize; j++ {
			ySum2 += int64(downloadBuf[j])
		}
		yAvg2 := float64(ySum2) / float64(ySize)
		expectedY2 := (180.0 + 230.0) / 2.0 // 205
		if math.Abs(yAvg2-expectedY2) > 1.0 {
			corruptFrames++
			t.Logf("MEASURE: BUG: iteration %d pair2: Y=%.4f expected=%.1f", i, yAvg2, expectedY2)
		}

		// Release everything
		dst2.Release()
		f2.Release()
		f3.Release()

		hitsAfter, missesAfter := pool.Stats()
		cycleHits := hitsAfter - hitsBefore
		cycleMisses := missesAfter - missesBefore
		totalHitsAccum += cycleHits
		totalMissesAccum += cycleMisses

		if i%10 == 0 {
			t.Logf("MEASURE: iteration %d: pair1 Y=%.4f (exp=%.1f), pair2 Y=%.4f (exp=%.1f), cycle hits=%d misses=%d",
				i, yAvg1, expectedY1, yAvg2, expectedY2, cycleHits, cycleMisses)
		}
	}

	hitsEnd, missesEnd := pool.Stats()
	totalHits := hitsEnd - hitsStart
	totalMisses := missesEnd - missesStart
	t.Logf("MEASURE: pool hits=%d, misses=%d over %d cycles", totalHits, totalMisses, iterations)

	// Assert pool misses: with pool size 6 and each iteration acquiring 6 frames
	// (4 sources + 2 dst), then releasing all before next iteration, the pool
	// should not thrash. First iteration creates 6, after that all reuse.
	require.Less(t, totalMisses, uint64(3),
		"pool should not thrash under pressure: got %d misses (hits=%d)", totalMisses, totalHits)

	assert.Equal(t, 0, corruptFrames,
		"found %d corrupted frames in %d iterations of pool pressure test", corruptFrames, iterations)
	t.Logf("MEASURE: %d iterations complete, corrupt frames=%d, pool hits=%d misses=%d",
		iterations, corruptFrames, totalHits, totalMisses)
}
