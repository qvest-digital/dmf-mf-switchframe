package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/graphics"
)

func TestUpstreamKeyNode_InactiveWhenNilBridge(t *testing.T) {
	n := &upstreamKeyNode{bridge: nil}
	require.False(t, n.Active())
	require.Equal(t, "upstream-key", n.Name())
	require.Nil(t, n.Err())
	require.True(t, n.Latency() > 0)
	require.NoError(t, n.Close())
}

func TestUpstreamKeyNode_InactiveWhenNoKeys(t *testing.T) {
	kp := graphics.NewKeyProcessor()
	bridge := graphics.NewKeyProcessorBridge(kp)
	n := &upstreamKeyNode{bridge: bridge}
	require.False(t, n.Active(), "should be inactive when no keys are enabled")
}

func TestUpstreamKeyNode_ProcessPassthrough(t *testing.T) {
	// With no keys enabled, Process should still work (no-op from bridge)
	kp := graphics.NewKeyProcessor()
	bridge := graphics.NewKeyProcessorBridge(kp)
	n := &upstreamKeyNode{bridge: bridge}

	pf := &ProcessingFrame{
		YUV:    make([]byte, 8*8*3/2),
		Width:  8,
		Height: 8,
		PTS:    1000,
	}
	pf.YUV[0] = 0x42

	out := n.Process(nil, pf)
	require.Same(t, pf, out, "Process should return src for in-place node")
}
