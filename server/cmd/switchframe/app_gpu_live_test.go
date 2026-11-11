package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/graphics"
)

// The GPU DSK node gets a live layer's pictures and the CPU's key LUT,
// and no layer whose fill and key could not be keyed together.
func TestCompositorStateAdapterLiveLayer(t *testing.T) {
	comp := graphics.NewCompositor()
	id, err := comp.AddLayer()
	require.NoError(t, err)
	require.NoError(t, comp.SetLive(id, "fill", "key", graphics.LiveOptions{}))
	require.NoError(t, comp.On(id))
	pic := func(w, h int) graphics.LivePicture {
		return graphics.LivePicture{Data: make([]byte, w*h*3/2), Width: w, Height: h}
	}
	a := &compositorStateAdapter{compositor: comp}
	src := graphics.LiveSource{ID: id, Fill: "fill", Key: "key"}

	comp.SetLiveFrames(src, pic(4, 4), pic(4, 4))
	snaps := a.SnapshotVisibleLayers()
	require.Len(t, snaps, 1)
	require.Len(t, snaps[0].LiveFill, 24)
	require.Len(t, snaps[0].LiveKey, 24)
	require.Equal(t, 4, snaps[0].LiveW)
	require.False(t, snaps[0].LiveTenBit)
	require.Equal(t, byte(255), snaps[0].LiveLUT[235])
	require.Equal(t, byte(0), snaps[0].LiveLUT[16])

	comp.SetLiveFrames(src, pic(4, 4), pic(8, 8))
	require.Empty(t, a.SnapshotVisibleLayers())
}

// With the GPU pipeline on, a shaped fill is refused rather than keyed
// wrong; the CPU pipeline takes it.
func TestLiveLayerGuard(t *testing.T) {
	app := &App{}
	require.NoError(t, app.liveLayerGuard(graphics.LiveOptions{Premultiplied: true}), "CPU pipeline")

	app.setGPUState(&gpuState{ctx: &gpu.Context{}, encoder: &gpu.GPUEncoder{}})
	err := app.liveLayerGuard(graphics.LiveOptions{Premultiplied: true})
	require.True(t, errors.Is(err, graphics.ErrLiveUnsupported), "%v", err)
	require.NoError(t, app.liveLayerGuard(graphics.LiveOptions{}))
}
