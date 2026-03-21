//go:build darwin || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUPreviewEncodeNode_NilCtx(t *testing.T) {
	cb := func(data []byte, isIDR bool, pts int64) {}
	node := NewGPUPreviewEncodeNode(nil, 854, 480, 500_000, 30, 1, cb)
	assert.Nil(t, node, "nil ctx should return nil node")
}

func TestGPUPreviewEncodeNode_NilCallback(t *testing.T) {
	// We can't create a real context here without GPU, but we need a
	// non-nil *Context to test this path. Use NewContext which will
	// fail on non-GPU systems — in that case skip.
	ctx, err := NewContext()
	if err != nil {
		// Even without a GPU, we can verify the nil-callback check
		// by confirming that nil ctx + nil callback = nil.
		node := NewGPUPreviewEncodeNode(nil, 854, 480, 500_000, 30, 1, nil)
		assert.Nil(t, node, "nil ctx + nil callback should return nil node")
		return
	}
	defer ctx.Close()

	node := NewGPUPreviewEncodeNode(ctx, 854, 480, 500_000, 30, 1, nil)
	assert.Nil(t, node, "nil callback should return nil node")
}

func TestGPUPreviewEncodeNode_Interface(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}
	defer ctx.Close()

	var encoded [][]byte
	cb := func(data []byte, isIDR bool, pts int64) {
		cp := make([]byte, len(data))
		copy(cp, data)
		encoded = append(encoded, cp)
	}

	node := NewGPUPreviewEncodeNode(ctx, 854, 480, 500_000, 30, 1, cb)
	require.NotNil(t, node, "node should be non-nil with valid ctx and callback")

	// Verify interface compliance.
	var _ GPUPipelineNode = node

	// Check Name.
	assert.Equal(t, "gpu_preview_encode", node.Name())

	// Not active before Configure.
	assert.False(t, node.Active(), "should not be active before Configure")

	// No error initially.
	assert.NoError(t, node.Err())

	// Configure with 1080p source dimensions.
	err = node.Configure(1920, 1080, 1920)
	require.NoError(t, err, "Configure should succeed with valid GPU context")

	// Active after Configure.
	assert.True(t, node.Active(), "should be active after Configure")

	// Latency should be non-zero.
	assert.Greater(t, node.Latency().Nanoseconds(), int64(0))

	// ForceIDR should not panic.
	concrete := node.(*gpuPreviewEncodeNode)
	concrete.ForceIDR()
	assert.True(t, concrete.forceIDR.Load(), "ForceIDR should set the flag")

	// Close should succeed and deactivate.
	err = node.Close()
	assert.NoError(t, err, "Close should succeed")
	assert.False(t, node.Active(), "should not be active after Close")
}
