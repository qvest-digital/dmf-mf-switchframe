package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/graphics"
)

func TestCompositorNode_InactiveWhenNil(t *testing.T) {
	n := &compositorNode{compositor: nil}
	require.False(t, n.Active())
	require.Equal(t, "compositor", n.Name())
	require.Nil(t, n.Err())
	require.True(t, n.Latency() > 0)
	require.NoError(t, n.Close())
}

func TestCompositorNode_InactiveWhenNoOverlay(t *testing.T) {
	c := graphics.NewCompositor()
	n := &compositorNode{compositor: c}
	require.False(t, n.Active(), "should be inactive when no overlay is set")
}

func TestCompositorNode_ProcessPassthrough(t *testing.T) {
	c := graphics.NewCompositor()
	n := &compositorNode{compositor: c}

	pf := &ProcessingFrame{
		YUV:    make([]byte, 8*8*3/2),
		Width:  8,
		Height: 8,
		PTS:    1000,
	}
	pf.YUV[0] = 0x42

	out := n.Process(nil, pf)
	require.Same(t, pf, out, "Process should return src for in-place node")
	require.Equal(t, byte(0x42), out.YUV[0])
}
