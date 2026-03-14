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

	// Verify image is stored and overlay is set
	c.mu.RLock()
	layer := c.layers[id]
	require.Equal(t, "logo.png", layer.imageName)
	require.Equal(t, 100, layer.imageWidth)
	require.Equal(t, 50, layer.imageHeight)
	require.Equal(t, 100*50*4, len(layer.imageRGBA))
	require.Equal(t, 100*50*4, len(layer.overlay), "overlay should be set from uploaded image")
	require.Equal(t, 100, layer.overlayWidth)
	require.Equal(t, 50, layer.overlayHeight)
	require.True(t, layer.active, "layer should be activated by SetImage")
	require.Equal(t, 1.0, layer.fadePosition, "fade should be fully on")
	// Without a resolution provider, rect should fall back to image dimensions
	require.Equal(t, image.Rect(0, 0, 100, 50), layer.rect, "rect should be set to image dimensions")
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

	// Verify overlay is also cleared
	c.mu.RLock()
	layer := c.layers[id]
	require.Nil(t, layer.overlay, "overlay should be cleared when image is deleted")
	require.False(t, layer.active, "layer should be deactivated when image is deleted")
	require.Equal(t, image.Rectangle{}, layer.rect, "rect should be reset on delete")
	c.mu.RUnlock()
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

func TestSetImage_RectUsesImageDimensions(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	id, err := c.AddLayer()
	require.NoError(t, err)

	pngData := createTestPNG(t, 100, 50)
	require.NoError(t, c.SetImage(id, "logo.png", pngData))

	c.mu.RLock()
	layer := c.layers[id]
	// Rect should match image dimensions (smaller than program)
	require.Equal(t, image.Rect(0, 0, 100, 50), layer.rect)
	require.Equal(t, 100, layer.overlayWidth)
	require.Equal(t, 50, layer.overlayHeight)
	c.mu.RUnlock()
}

func TestSetImage_RectCappedToProgram(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 320, 240 })

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Image larger than program resolution
	pngData := createTestPNG(t, 1920, 1080)
	require.NoError(t, c.SetImage(id, "big.png", pngData))

	c.mu.RLock()
	layer := c.layers[id]
	// Rect should be capped to program resolution
	require.Equal(t, image.Rect(0, 0, 320, 240), layer.rect)
	c.mu.RUnlock()
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
	// Without resolution provider, rect should be image size
	require.Equal(t, 100, lastState.Layers[0].Rect.Width)
	require.Equal(t, 50, lastState.Layers[0].Rect.Height)
}
