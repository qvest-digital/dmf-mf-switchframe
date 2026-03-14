package graphics

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func createTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Fill with red
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestSetImage(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	pngData := createTestPNG(t, 100, 50)
	err = c.SetImage(id, "logo.png", pngData)
	require.NoError(t, err)

	// Verify image is stored
	c.mu.RLock()
	layer := c.layers[id]
	require.Equal(t, "logo.png", layer.imageName)
	require.Equal(t, 100, layer.imageWidth)
	require.Equal(t, 50, layer.imageHeight)
	require.Equal(t, 100*50*4, len(layer.imageRGBA))
	c.mu.RUnlock()
}

func TestGetImage(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	pngData := createTestPNG(t, 100, 50)
	require.NoError(t, c.SetImage(id, "logo.png", pngData))

	name, data, err := c.GetImage(id)
	require.NoError(t, err)
	require.Equal(t, "logo.png", name)
	require.Equal(t, pngData, data)
}

func TestGetImage_NoImage(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	_, _, err = c.GetImage(id)
	require.ErrorIs(t, err, ErrNoImage)
}

func TestDeleteImage(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	pngData := createTestPNG(t, 100, 50)
	require.NoError(t, c.SetImage(id, "logo.png", pngData))
	require.NoError(t, c.DeleteImage(id))

	_, _, err = c.GetImage(id)
	require.ErrorIs(t, err, ErrNoImage)
}

func TestDeleteImage_NoImage(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	err = c.DeleteImage(id)
	require.ErrorIs(t, err, ErrNoImage)
}

func TestSetImage_InvalidPNG(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	err = c.SetImage(id, "bad.png", []byte("not a png"))
	require.Error(t, err)
}

func TestSetImage_LayerNotFound(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	err := c.SetImage(999, "logo.png", createTestPNG(t, 10, 10))
	require.ErrorIs(t, err, ErrLayerNotFound)
}

func TestSetImage_StateBroadcast(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	var lastState State
	c.OnStateChange(func(s State) { lastState = s })

	id, err := c.AddLayer()
	require.NoError(t, err)

	pngData := createTestPNG(t, 100, 50)
	require.NoError(t, c.SetImage(id, "logo.png", pngData))

	require.Equal(t, "logo.png", lastState.Layers[0].ImageName)
	require.Equal(t, 100, lastState.Layers[0].ImageWidth)
	require.Equal(t, 50, lastState.Layers[0].ImageHeight)
}
