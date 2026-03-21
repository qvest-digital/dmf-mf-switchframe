//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCUDATimer(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	timer, err := NewTimer()
	require.NoError(t, err)
	defer timer.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Time a FillBlack operation
	timer.Start(ctx.stream)
	FillBlack(ctx, frame)
	ms := timer.Stop(ctx.stream)

	assert.GreaterOrEqual(t, ms, float32(0), "timer should return non-negative")
	t.Logf("CUDA timer: FillBlack 1080p = %.3f ms", ms)
}

func TestCUDATimerMultipleOps(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	timer, err := NewTimer()
	require.NoError(t, err)
	defer timer.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 4)
	require.NoError(t, err)
	defer pool.Close()

	a, _ := pool.Acquire()
	b, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer a.Release()
	defer b.Release()
	defer dst.Release()
	FillBlack(ctx, a)
	FillBlack(ctx, b)

	// Time a blend operation
	timer.Start(ctx.stream)
	BlendMix(ctx, dst, a, b, 0.5)
	blendMs := timer.Stop(ctx.stream)

	t.Logf("CUDA timer: BlendMix 1080p = %.3f ms", blendMs)
	assert.Less(t, blendMs, float32(100), "blend should complete in < 100ms")
}

func TestPipelineMetrics(t *testing.T) {
	m := &PipelineMetrics{}
	m.FramesProcessed.Add(100)
	m.UploadUs.Store(500)
	m.EncodeUs.Store(3000)
	m.TotalUs.Store(4500)

	snap := m.Snapshot()
	assert.Equal(t, int64(100), snap["frames"])
	assert.Equal(t, int64(500), snap["upload_us"])
	assert.Equal(t, int64(3000), snap["encode_us"])
	assert.Equal(t, int64(4500), snap["total_us"])
}

func TestMemoryStatsExtended(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	stats := MemoryStatsExtended(ctx)
	assert.True(t, stats["available"].(bool))
	assert.Greater(t, stats["total_mb"].(int), 0)
	assert.NotEmpty(t, stats["device"])
	assert.NotEmpty(t, stats["compute"])
	t.Logf("GPU memory: %v", stats)
}

func TestMemoryStatsExtendedNil(t *testing.T) {
	stats := MemoryStatsExtended(nil)
	assert.False(t, stats["available"].(bool))
}
