package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
)

func graphicsTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	img.Set(1, 1, color.NRGBA{R: 200, A: 90})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// An engine that comes back without its graphics, as one rescheduled onto
// another node does, takes every layer from the peer's snapshot: the layer
// itself under the peer's ID, what it shows, and its settings, so a layer
// the peer holds on air goes on air here too. A layer the peer does not
// hold goes. Settings alone, all a snapshot carried before, fail on a layer
// that does not exist, and the engines never converge.
func TestSyncGraphicsLayers_RecreatesThePeersLayers(t *testing.T) {
	peer := graphics.NewCompositor()
	defer peer.Close()
	require.NoError(t, peer.AddLayerWithID(3))
	require.NoError(t, peer.AddLayerWithID(5))
	require.NoError(t, peer.SetImage(3, "logo.png", graphicsTestPNG(t)))
	require.NoError(t, peer.Off(3))
	require.NoError(t, peer.SetLayerRect(3, image.Rect(10, 20, 14, 22)))
	frame := make([]byte, 4*6*4)
	for i := range frame {
		frame[i] = byte(i)
	}
	require.NoError(t, peer.SetOverlay(5, frame, 6, 4, "lower-third"))
	require.NoError(t, peer.On(5))
	name := "Name strap"
	require.NoError(t, peer.SetLayerSettings(5, &name, json.RawMessage(`{"fields":{"name":"Ada"}}`)))

	// Over the wire, as a browser or the startup sync carries it.
	var captured internal.FullStateSnapshot
	captureGraphics(&captured, peer)
	require.True(t, captured.GraphicsContent, "a capture carries the whole set of layers")
	raw, err := json.Marshal(captured)
	require.NoError(t, err)
	var snap internal.FullStateSnapshot
	require.NoError(t, json.Unmarshal(raw, &snap))

	app := &App{compositor: graphics.NewCompositor()}
	defer app.compositor.Close()
	require.NoError(t, app.compositor.AddLayerWithID(9))

	require.NoError(t, app.SyncGraphicsLayers(snap.Graphics))
	for _, l := range snap.Graphics {
		require.NoError(t, app.SetGraphicsLayerActive(l.ID, l.Active))
		require.NoError(t, app.SetGraphicsLayerZOrder(l.ID, l.ZOrder))
		require.NoError(t, app.SetGraphicsLayerRect(l.ID, l.X, l.Y, l.Width, l.Height))
		require.NoError(t, app.SetGraphicsLayerSettings(l.ID, l.Name, l.Config))
	}

	requireSameExport(t, peer, app.compositor)
	got := app.compositor.Status().Layers
	require.Len(t, got, 2)
	active := map[int]bool{}
	for _, l := range got {
		active[l.ID] = l.Active
	}
	require.Equal(t, map[int]bool{3: false, 5: true}, active)
	lc, err := app.compositor.LayerContent(5)
	require.NoError(t, err)
	want, err := peer.LayerContent(5)
	require.NoError(t, err)
	require.Equal(t, want, lc)

	// The next reconcile pushes the same snapshot: nothing changes.
	require.NoError(t, app.SyncGraphicsLayers(snap.Graphics))
	requireSameExport(t, peer, app.compositor)
}

// requireSameExport compares two compositors' layers as a graphics export
// document reads them.
func requireSameExport(t *testing.T, want, got *graphics.Compositor) {
	t.Helper()
	w, err := json.Marshal(want.ExportLayers())
	require.NoError(t, err)
	g, err := json.Marshal(got.ExportLayers())
	require.NoError(t, err)
	require.JSONEq(t, string(w), string(g))
}

// Layers are removed before any is added, so a target full of layers the
// peer does not hold still takes the peer's.
func TestSyncGraphicsLayers_RemovesBeforeAdding(t *testing.T) {
	app := &App{compositor: graphics.NewCompositor()}
	defer app.compositor.Close()
	for id := 0; id < graphics.DefaultMaxLayers; id++ {
		require.NoError(t, app.compositor.AddLayerWithID(100+id))
	}
	require.NoError(t, app.SyncGraphicsLayers([]internal.GraphicsLayerSnapshot{{ID: 1}}))
	got := app.compositor.Status().Layers
	require.Len(t, got, 1)
	require.Equal(t, 1, got[0].ID)
}

// graphicsOnlyApp is an App with only a graphics compositor; the apply
// steps that need the switcher or the mixer do nothing.
type graphicsOnlyApp struct{ *App }

func (graphicsOnlyApp) SetFTB(bool) error                 { return nil }
func (graphicsOnlyApp) SetMasterLevel(float64) error      { return nil }
func (graphicsOnlyApp) SetLimiterThreshold(float64) error { return nil }
func (graphicsOnlyApp) SetLimiterCeiling(float64) error   { return nil }

// Through the whole apply, a layer the engine lacks is created before its
// on-air state is set, so it goes on air with the peer's content and the
// apply warns of nothing.
func TestApplyFullSnapshot_RecreatesLayersOnTheApp(t *testing.T) {
	peer := graphics.NewCompositor()
	defer peer.Close()
	require.NoError(t, peer.AddLayerWithID(4))
	require.NoError(t, peer.SetOverlay(4, make([]byte, 4*2*2), 2, 2, "bug"))
	require.NoError(t, peer.On(4))
	var snap internal.FullStateSnapshot
	captureGraphics(&snap, peer)

	app := &App{compositor: graphics.NewCompositor()}
	defer app.compositor.Close()
	warnings := internal.ApplyFullSnapshot(context.Background(), snap, graphicsOnlyApp{app})
	require.Empty(t, warnings)
	got := app.compositor.Status().Layers
	require.Len(t, got, 1)
	require.Equal(t, 4, got[0].ID)
	require.True(t, got[0].Active)
}
