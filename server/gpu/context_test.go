//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContext(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	require.NotNil(t, ctx)
	defer ctx.Close()

	// Verify device properties are populated
	props := ctx.DeviceProperties()
	assert.NotEmpty(t, props.Name)
	assert.Greater(t, props.ComputeCapability[0], 0, "compute major should be > 0")
	assert.Greater(t, props.TotalMemory, int64(0), "total memory should be > 0")
	assert.Greater(t, props.MultiprocessorCount, 0, "SM count should be > 0")
	assert.Greater(t, props.MaxThreadsPerBlock, 0, "max threads should be > 0")

	t.Logf("GPU: %s (SM %d.%d), VRAM: %d MB, SMs: %d",
		props.Name,
		props.ComputeCapability[0], props.ComputeCapability[1],
		props.TotalMemory/(1024*1024),
		props.MultiprocessorCount)
}

func TestContextStreams(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	assert.NotNil(t, ctx.Stream(), "processing stream should be non-nil")
	assert.NotNil(t, ctx.EncStream(), "encode stream should be non-nil")
}

func TestContextSync(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	// Sync on empty stream should succeed immediately
	err = ctx.Sync()
	require.NoError(t, err)
}

func TestContextMemoryStats(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	stats := ctx.MemoryStats()
	assert.Greater(t, stats.TotalMB, 0, "total VRAM should be > 0")
	assert.Greater(t, stats.FreeMB, 0, "free VRAM should be > 0")
	assert.GreaterOrEqual(t, stats.UsedMB, 0, "used VRAM should be >= 0")
	// Integer division rounding means total may differ from free+used by ±1
	assert.InDelta(t, stats.TotalMB, stats.FreeMB+stats.UsedMB, 1)

	t.Logf("VRAM: %d MB total, %d MB free, %d MB used",
		stats.TotalMB, stats.FreeMB, stats.UsedMB)
}

func TestContextCloseNilSafe(t *testing.T) {
	var ctx *Context
	err := ctx.Close()
	assert.NoError(t, err, "Close on nil context should not panic")
}

func TestContextDoubleClose(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)

	err = ctx.Close()
	assert.NoError(t, err)

	// Second close should also be safe
	err = ctx.Close()
	assert.NoError(t, err)
}
