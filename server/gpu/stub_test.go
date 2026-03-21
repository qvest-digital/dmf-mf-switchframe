//go:build (!cgo || !cuda) && !darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubNewContext(t *testing.T) {
	ctx, err := NewContext()
	require.ErrorIs(t, err, ErrGPUNotAvailable)
	assert.Nil(t, ctx)
}

func TestStubFramePool(t *testing.T) {
	pool, err := NewFramePool(nil, 1920, 1080, 4)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
	assert.Nil(t, pool)
}

func TestStubUploadDownload(t *testing.T) {
	err := Upload(nil, nil, nil, 1920, 1080)
	require.ErrorIs(t, err, ErrGPUNotAvailable)

	err = Download(nil, nil, nil, 1920, 1080)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestStubGPUFrameNilSafe(t *testing.T) {
	// Verify stubs are nil-safe
	var f GPUFrame
	f.Release()
	f.Ref()
	assert.Equal(t, 0, f.Width)
}

func TestStubGPUSourceManagerSnapshot(t *testing.T) {
	mgr := NewGPUSourceManager(nil, nil, nil)
	// Stub returns nil manager, but Snapshot should still work.
	if mgr == nil {
		return
	}
	snap := mgr.Snapshot()
	require.Equal(t, 0, snap["source_count"])
}

func TestStubGPUPipelineSnapshot(t *testing.T) {
	pipe := NewGPUPipeline(nil, nil)
	snap := pipe.Snapshot()
	require.False(t, snap["gpu"].(bool))
}
