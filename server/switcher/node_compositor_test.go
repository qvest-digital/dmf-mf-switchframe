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

func TestCompositorNode_AlwaysActiveWhenAttached(t *testing.T) {
	// Compositor node is always active when attached to avoid pipeline
	// rebuilds on graphics toggle. ProcessYUV has its own fast path.
	c := graphics.NewCompositor()
	n := &compositorNode{compositor: c}
	require.True(t, n.Active(), "should be active even with no overlay (fast no-op path)")
}

func TestCompositorNode_ActiveWhenOverlayOn(t *testing.T) {
	c := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id, rgba, 4, 4, "test"))
	require.NoError(t, c.On(id))
	n := &compositorNode{compositor: c}
	require.True(t, n.Active(), "should be active when overlay is on")
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
