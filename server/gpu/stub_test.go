//go:build !cgo || !cuda

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
