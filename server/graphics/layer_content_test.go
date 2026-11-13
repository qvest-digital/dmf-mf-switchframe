package graphics

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

// overlayOf copies a layer's overlay out under the lock, so an assertion
// that fails does not leave the lock held.
func overlayOf(c *Compositor, id int) (overlay []byte, w, h int, template string, gen uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	l := c.layers[id]
	return append([]byte(nil), l.overlay...), l.overlayWidth, l.overlayHeight, l.template, l.overlayGen
}

// A frame rendered from a template reaches the other engine byte for byte,
// half-transparent edges included: the overlay holds straight RGBA, and a
// premultiplied round trip would darken every antialiased pixel.
func TestLayerContent_FrameRoundTripsExactly(t *testing.T) {
	src := NewCompositor()
	defer src.Close()
	id := addLayers(t, src, 1)[0]
	rgba := make([]byte, 4*3*2)
	for i := range rgba {
		rgba[i] = byte(17 * i)
	}
	rgba[3], rgba[7] = 1, 128 // nearly and half transparent pixels
	require.NoError(t, src.SetOverlay(id, rgba, 3, 2, "lower-third"))

	lc, err := src.LayerContent(id)
	require.NoError(t, err)
	require.NotNil(t, lc.Frame)
	require.Nil(t, lc.Image)
	require.Equal(t, "lower-third", lc.Template)

	dst := NewCompositor()
	defer dst.Close()
	require.NoError(t, dst.AddLayerWithID(id))
	require.NoError(t, dst.SetLayerContent(id, lc))

	overlay, w, h, template, _ := overlayOf(dst, id)
	require.Equal(t, rgba, overlay)
	require.Equal(t, 3, w)
	require.Equal(t, 2, h)
	require.Equal(t, "lower-third", template)

	// With content the layer can go on air, which is what failed on an
	// engine that had lost its layers.
	require.NoError(t, dst.On(id))
}

// Content from another engine neither puts a layer on air nor moves it,
// as an upload does: on-air state and position come with the layer's own
// settings, and a frame of a graphic the source holds off air must not
// flash on program here.
func TestSetLayerContent_ImageLeavesLayerOffAirInPlace(t *testing.T) {
	src := NewCompositor()
	defer src.Close()
	id := addLayers(t, src, 1)[0]
	png := testPNG(t, 8, 6)
	require.NoError(t, src.SetImage(id, "logo.png", png))

	lc, err := src.LayerContent(id)
	require.NoError(t, err)
	require.Equal(t, png, lc.Image)
	require.Equal(t, "logo.png", lc.ImageName)
	require.Nil(t, lc.Frame, "an image layer carries no rendered frame")

	dst := NewCompositor()
	defer dst.Close()
	require.NoError(t, dst.AddLayerWithID(id))
	rect := image.Rect(100, 50, 300, 150)
	require.NoError(t, dst.SetLayerRect(id, rect))
	require.NoError(t, dst.SetLayerContent(id, lc))

	l := layerByID(t, dst, id)
	require.False(t, l.Active)
	require.Equal(t, RectState{X: 100, Y: 50, Width: 200, Height: 100}, l.Rect)
	require.Equal(t, "logo.png", l.ImageName)
	name, data, err := dst.GetImage(id)
	require.NoError(t, err)
	require.Equal(t, "logo.png", name)
	require.Equal(t, png, data)
	require.NoError(t, dst.On(id))
}

// A target that holds the source's image but shows a template frame over
// it, as after the source deleted the frame by uploading the same image
// again, shows the image: the image bytes alone match.
func TestSetLayerContent_SameImageReplacesTemplateFrame(t *testing.T) {
	png := testPNG(t, 4, 2)
	dst := NewCompositor()
	defer dst.Close()
	id := addLayers(t, dst, 1)[0]
	require.NoError(t, dst.SetImage(id, "logo.png", png))
	require.NoError(t, dst.SetOverlay(id, make([]byte, 4*6*4), 6, 4, "lower-third"))

	require.NoError(t, dst.SetLayerContent(id, LayerContent{ImageName: "logo.png", Image: png}))
	overlay, w, h, template, _ := overlayOf(dst, id)
	require.Equal(t, "", template)
	require.Equal(t, 4, w)
	require.Equal(t, 2, h)
	dst.mu.RLock()
	want := append([]byte(nil), dst.layers[id].imageRGBA...)
	dst.mu.RUnlock()
	require.Equal(t, want, overlay)
}

// A source whose image was deleted carries none; a target that missed the
// delete drops its own, and with it the overlay the image was.
func TestSetLayerContent_NoImageClearsImage(t *testing.T) {
	dst := NewCompositor()
	defer dst.Close()
	id := addLayers(t, dst, 1)[0]
	require.NoError(t, dst.SetImage(id, "logo.png", testPNG(t, 4, 2)))

	require.NoError(t, dst.SetLayerContent(id, LayerContent{}))
	_, _, err := dst.GetImage(id)
	require.ErrorIs(t, err, ErrNoImage)
	overlay, _, _, _, _ := overlayOf(dst, id)
	require.Empty(t, overlay)
}

// A reconcile applies the same snapshot again and again. Content already
// shown is not decoded, set, or broadcast again, so the compositor lock is
// not held for a decode and the GPU path keeps its cached overlay.
func TestSetLayerContent_SameContentIsNoChange(t *testing.T) {
	for name, set := range map[string]func(c *Compositor, id int){
		"frame": func(c *Compositor, id int) {
			require.NoError(t, c.SetOverlay(id, make([]byte, 4*2*2), 2, 2, "bug"))
		},
		"image": func(c *Compositor, id int) {
			require.NoError(t, c.SetImage(id, "logo.png", testPNG(t, 4, 2)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := NewCompositor()
			defer src.Close()
			id := addLayers(t, src, 1)[0]
			set(src, id)
			lc, err := src.LayerContent(id)
			require.NoError(t, err)

			dst := NewCompositor()
			defer dst.Close()
			require.NoError(t, dst.AddLayerWithID(id))
			require.NoError(t, dst.SetLayerContent(id, lc))
			_, _, _, _, gen := overlayOf(dst, id)
			broadcasts := 0
			dst.OnStateChange(func(State) { broadcasts++ })

			require.NoError(t, dst.SetLayerContent(id, lc))
			_, _, _, _, again := overlayOf(dst, id)
			require.Equal(t, gen, again)
			require.Zero(t, broadcasts, "an unchanged push broadcasts state")
		})
	}
}

// A frame captured once is encoded once: the next capture of the same
// overlay hands back the same PNG.
func TestLayerContent_ReusesEncodedFrame(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	id := addLayers(t, c, 1)[0]
	require.NoError(t, c.SetOverlay(id, make([]byte, 4*2*2), 2, 2, "bug"))
	first, err := c.LayerContent(id)
	require.NoError(t, err)
	second, err := c.LayerContent(id)
	require.NoError(t, err)
	require.Same(t, &first.Frame[0], &second.Frame[0])

	require.NoError(t, c.SetOverlay(id, make([]byte, 4*2*2), 2, 2, "bug"))
	third, err := c.LayerContent(id)
	require.NoError(t, err)
	require.NotSame(t, &first.Frame[0], &third.Frame[0], "a new overlay is encoded again")
}

// HTML5 and text animation overlays are rendered here continuously; a copy
// of one frame would be stale at once, so none is carried.
func TestLayerContent_NoFrameForLiveTemplates(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	id := addLayers(t, c, 1)[0]
	for _, tmpl := range []string{"html5", "text-anim"} {
		require.NoError(t, c.SetOverlay(id, make([]byte, 4*2*2), 2, 2, tmpl))
		lc, err := c.LayerContent(id)
		require.NoError(t, err)
		require.Nil(t, lc.Frame, tmpl)
		require.Equal(t, tmpl, lc.Template)
	}
}

func TestSetLayerContent_Errors(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	require.ErrorIs(t, c.SetLayerContent(7, LayerContent{Image: testPNG(t, 2, 2)}), ErrLayerNotFound)
	id := addLayers(t, c, 1)[0]
	require.Error(t, c.SetLayerContent(id, LayerContent{Template: "bug", Frame: []byte("not a png")}))
	require.ErrorIs(t, c.SetLayerContent(id, LayerContent{Template: "bug", Frame: hugePNG(t, MaxImageDimension+1, 1)}), ErrImageTooLarge)
}
