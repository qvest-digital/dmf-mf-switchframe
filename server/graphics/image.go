package graphics

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

// ErrNoImage is returned when no image is stored on a layer.
var ErrNoImage = errors.New("graphics: no image on this layer")

// ErrImageTooLarge is returned for a PNG wider or taller than
// MaxImageDimension.
var ErrImageTooLarge = fmt.Errorf("graphics: image larger than %dx%d", MaxImageDimension, MaxImageDimension)

// MaxImageDimension bounds a layer image's width and height.
const MaxImageDimension = 8192

// decodePNG decodes a PNG to RGBA. The dimensions are read from the header
// first: png.Decode allocates the full image from them, so a few hundred
// compressed bytes could otherwise ask for gigabytes.
func decodePNG(data []byte) (*image.RGBA, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	if cfg.Width > MaxImageDimension || cfg.Height > MaxImageDimension {
		return nil, ErrImageTooLarge
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	return rgba, nil
}

// SetImage stores a PNG image on a layer. The PNG is decoded to RGBA for
// compositing, and the original PNG bytes are kept for retrieval via GetImage.
func (c *Compositor) SetImage(id int, name string, pngData []byte) error {
	// Decode PNG outside the lock
	rgba, err := decodePNG(pngData)
	if err != nil {
		return err
	}
	w := rgba.Bounds().Dx()
	h := rgba.Bounds().Dy()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		c.mu.Unlock()
		return ErrLayerNotFound
	}

	layer.imageName = name
	layer.imageData = make([]byte, len(pngData))
	copy(layer.imageData, pngData)
	layer.imageRGBA = rgba.Pix
	layer.imageWidth = w
	layer.imageHeight = h
	layer.template = "" // clear any previous template name

	// Also set as the active overlay so the image actually renders on program output.
	expected := w * h * 4
	if len(layer.overlay) != expected {
		layer.overlay = make([]byte, expected)
	}
	copy(layer.overlay, rgba.Pix)
	layer.overlayWidth = w
	layer.overlayHeight = h

	// Set rect to image native dimensions (capped to program resolution).
	// The overlay renders at its actual pixel size — no upscaling.
	// Users can drag/resize via the program monitor overlay.
	rectW, rectH := w, h
	if c.resolutionProvider != nil {
		if pw, ph := c.resolutionProvider(); pw > 0 && ph > 0 {
			if rectW > pw {
				rectW = pw
			}
			if rectH > ph {
				rectH = ph
			}
		}
	}
	// Even-align for YUV420 chroma compatibility (matches fast-control validation).
	rectW = rectW &^ 1
	rectH = rectH &^ 1
	if rectW < 2 {
		rectW = 2
	}
	if rectH < 2 {
		rectH = 2
	}
	layer.rect = image.Rect(0, 0, rectW, rectH)

	// Activate the layer so ProcessYUV renders it.
	layer.active = true
	layer.fadePosition = 1.0
	layer.overlayGen++

	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// GetImage returns the stored PNG image data for a layer.
func (c *Compositor) GetImage(id int) (name string, data []byte, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return "", nil, ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		return "", nil, ErrLayerNotFound
	}
	if layer.imageData == nil {
		return "", nil, ErrNoImage
	}
	// Return a copy so the caller can use it after the lock is released
	// without racing with concurrent SetImage/DeleteImage.
	dataCopy := make([]byte, len(layer.imageData))
	copy(dataCopy, layer.imageData)
	return layer.imageName, dataCopy, nil
}

// DeleteImage removes the stored image from a layer.
func (c *Compositor) DeleteImage(id int) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		c.mu.Unlock()
		return ErrLayerNotFound
	}
	if layer.imageData == nil {
		c.mu.Unlock()
		return ErrNoImage
	}

	layer.imageName = ""
	layer.imageData = nil
	layer.imageRGBA = nil
	layer.imageWidth = 0
	layer.imageHeight = 0

	// Deactivate and clear the overlay so the image stops rendering.
	layer.active = false
	layer.fadePosition = 0.0
	layer.rect = image.Rectangle{}
	layer.overlay = nil
	layer.overlayWidth = 0
	layer.overlayHeight = 0
	layer.overlayGen++

	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}
