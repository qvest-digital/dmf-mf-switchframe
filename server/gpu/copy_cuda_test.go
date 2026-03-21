//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyNV12FromDevice_NilDst(t *testing.T) {
	err := CopyNV12FromDevice(nil, 0xDEAD, 1920, 1920, 1080)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil destination")
}

func TestCopyNV12FromDevice_NilSrc(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 1)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = CopyNV12FromDevice(frame, 0, 1920, 1920, 1080)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil source")
}
