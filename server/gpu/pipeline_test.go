//go:build cgo && cuda

package gpu

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUPipelineFullChain(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 640, 480
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Create encoder for the pipeline
	encoder, err := NewGPUEncoder(ctx, w, h, 30, 1, 2_000_000)
	require.NoError(t, err)
	defer encoder.Close()

	// Collect encoded output
	var encodedFrames [][]byte
	forceIDR := &atomic.Bool{}

	onEncoded := func(data []byte, isIDR bool, pts int64) {
		cp := make([]byte, len(data))
		copy(cp, data)
		encodedFrames = append(encodedFrames, cp)
	}

	// Build pipeline: passthrough nodes + encode
	pipe := NewGPUPipeline(ctx, pool)
	err = pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUPassthroughNode("gpu_key", false),
		NewGPUPassthroughNode("gpu_layout", false),
		NewGPUPassthroughNode("gpu_dsk", false),
		NewGPUPassthroughNode("gpu_stmap", false),
		NewGPUEncodeNode(ctx, encoder, forceIDR, onEncoded),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Create test pattern
	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = byte(i%220) + 16
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}

	// Run 30 frames through the pipeline
	forceIDR.Store(true)
	for i := 0; i < 30; i++ {
		frame, err := pipe.RunWithUpload(yuv, w, h, int64(i*3000))
		require.NoError(t, err)
		frame.Release()
	}

	assert.Greater(t, len(encodedFrames), 0, "pipeline should produce encoded H.264 output")
	t.Logf("GPU pipeline: %d/30 frames encoded, first frame %d bytes",
		len(encodedFrames), len(encodedFrames[0]))

	// Verify snapshot
	snap := pipe.Snapshot()
	assert.True(t, snap["gpu"].(bool))
	assert.Greater(t, snap["run_count"].(int64), int64(0))
}

func TestGPUPipelineWithRawSink(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Raw sink that captures downloaded frames
	var capturedYUV []byte
	var capturedW, capturedH int
	sinkFn := RawSinkFunc(func(yuv []byte, width, height int) {
		capturedYUV = make([]byte, len(yuv))
		copy(capturedYUV, yuv)
		capturedW = width
		capturedH = height
	})
	sinkPtr := &atomic.Pointer[RawSinkFunc]{}
	sinkPtr.Store(&sinkFn)

	encoder, err := NewGPUEncoder(ctx, w, h, 30, 1, 1_000_000)
	require.NoError(t, err)
	defer encoder.Close()

	forceIDR := &atomic.Bool{}

	pipe := NewGPUPipeline(ctx, pool)
	err = pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPURawSinkNode(ctx, sinkPtr),
		NewGPUEncodeNode(ctx, encoder, forceIDR, func([]byte, bool, int64) {}),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// Upload a frame with Y=200
	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = 200
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}

	forceIDR.Store(true)
	frame, err := pipe.RunWithUpload(yuv, w, h, 0)
	require.NoError(t, err)
	frame.Release()

	// Raw sink should have captured the frame
	require.NotNil(t, capturedYUV, "raw sink should capture frame")
	assert.Equal(t, w, capturedW)
	assert.Equal(t, h, capturedH)
	assert.Equal(t, byte(200), capturedYUV[h/2*w+w/2], "captured Y should match uploaded value")
}

func TestGPUPipelineRawSinkInactive(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	// No sink registered — raw sink node should be inactive
	sinkPtr := &atomic.Pointer[RawSinkFunc]{}

	encoder, err := NewGPUEncoder(ctx, w, h, 30, 1, 1_000_000)
	require.NoError(t, err)
	defer encoder.Close()

	pipe := NewGPUPipeline(ctx, pool)
	err = pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPURawSinkNode(ctx, sinkPtr),
		NewGPUEncodeNode(ctx, encoder, &atomic.Bool{}, func([]byte, bool, int64) {}),
	})
	require.NoError(t, err)
	defer pipe.Close()

	// All nodes are included in the active list (nodes check Active()
	// dynamically in ProcessGPU). Verify the pipeline runs successfully
	// even with an inactive raw sink — it skips download when no sink is set.
	frame, err := pool.Acquire()
	require.NoError(t, err)
	require.NoError(t, pipe.Run(frame))
	frame.Release()
}

func TestGPUPipelineSnapshot(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	pipe := NewGPUPipeline(ctx, pool)
	err = pipe.Build(w, h, pool.Pitch(), []GPUPipelineNode{
		NewGPUPassthroughNode("test_node", true),
	})
	require.NoError(t, err)
	defer pipe.Close()

	snap := pipe.Snapshot()
	assert.True(t, snap["gpu"].(bool))
	assert.Equal(t, 1, len(snap["active_nodes"].([]map[string]any)))
	assert.Equal(t, int64(0), snap["run_count"].(int64))
}
