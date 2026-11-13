package graphics

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

// LayerContent is what a layer shows apart from its settings: the PNG
// uploaded to it, and the frame last rendered into it from a template. It
// lets another engine hold the same layer, on air or off.
type LayerContent struct {
	ImageName string
	Image     []byte // uploaded PNG, nil when none
	Template  string // what the overlay was rendered from, "" for the image
	Frame     []byte // PNG of the overlay, nil for "" and live templates
}

// liveTemplate reports whether an overlay is rendered continuously on this
// engine, by the HTML5 sidecar or the text animation engine: a copy of one
// frame of it would be stale at once.
func liveTemplate(template string) bool {
	return template == "html5" || template == "text-anim"
}

// LayerContent returns what layer id shows.
func (c *Compositor) LayerContent(id int) (LayerContent, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return LayerContent{}, ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		c.mu.RUnlock()
		return LayerContent{}, ErrLayerNotFound
	}
	lc := LayerContent{ImageName: layer.imageName, Template: layer.template}
	if layer.imageData != nil {
		lc.Image = append([]byte(nil), layer.imageData...)
	}
	var frame *image.NRGBA
	gen := layer.overlayGen
	if layer.template != "" && !liveTemplate(layer.template) && len(layer.overlay) > 0 {
		if layer.framePNG != nil && layer.framePNGGen == gen {
			lc.Frame = layer.framePNG
		} else {
			frame = &image.NRGBA{
				Pix:    append([]byte(nil), layer.overlay...),
				Stride: layer.overlayWidth * 4,
				Rect:   image.Rect(0, 0, layer.overlayWidth, layer.overlayHeight),
			}
		}
	}
	c.mu.RUnlock()

	if frame != nil {
		// The overlay holds the straight RGBA bytes a template renders;
		// NRGBA keeps them byte for byte through the PNG.
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&buf, frame); err != nil {
			return LayerContent{}, fmt.Errorf("encode frame: %w", err)
		}
		lc.Frame = buf.Bytes()
		c.mu.Lock()
		if l, ok := c.layers[id]; ok && l.overlayGen == gen {
			l.framePNG, l.framePNGGen = lc.Frame, gen
		}
		c.mu.Unlock()
	}
	return lc, nil
}

// SetLayerContent gives layer id the content another engine's layer
// shows. Unlike an upload it neither puts the layer on air nor moves it:
// those are settings of their own. A nil Image clears the layer's image.
// A live template's overlay, rendered here, is left as it is. Content the
// layer already shows is not decoded or set again.
func (c *Compositor) SetLayerContent(id int, lc LayerContent) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		c.mu.RUnlock()
		return ErrLayerNotFound
	}
	sameImage := layer.imageName == lc.ImageName && bytes.Equal(layer.imageData, lc.Image)
	sameFrame := lc.Frame == nil ||
		(layer.template == lc.Template && layer.framePNGGen == layer.overlayGen && bytes.Equal(layer.framePNG, lc.Frame))
	// With Template "" the overlay is the image, or nothing.
	sameOverlay := lc.Template != "" || layer.template == ""
	c.mu.RUnlock()
	if sameImage && sameFrame && sameOverlay {
		return nil
	}

	var img *image.RGBA
	if lc.Image != nil && !sameImage {
		var err error
		if img, err = decodePNG(lc.Image); err != nil {
			return err
		}
	}
	var frame *image.NRGBA
	if lc.Frame != nil && !sameFrame {
		var err error
		if frame, err = decodeFrame(lc.Frame); err != nil {
			return err
		}
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCompositorClosed
	}
	layer, ok = c.layers[id]
	if !ok {
		c.mu.Unlock()
		return ErrLayerNotFound
	}
	if !sameImage {
		layer.imageName = lc.ImageName
		layer.imageData, layer.imageRGBA = nil, nil
		layer.imageWidth, layer.imageHeight = 0, 0
		if img != nil {
			layer.imageData = append([]byte(nil), lc.Image...)
			layer.imageRGBA = img.Pix
			layer.imageWidth, layer.imageHeight = img.Bounds().Dx(), img.Bounds().Dy()
		}
	}
	switch {
	case lc.Template == "" && (!sameImage || layer.template != ""):
		// The source shows its image, or nothing.
		layer.overlay = nil
		layer.overlayWidth, layer.overlayHeight = 0, 0
		if layer.imageRGBA != nil {
			layer.overlay = append([]byte(nil), layer.imageRGBA...)
			layer.overlayWidth, layer.overlayHeight = layer.imageWidth, layer.imageHeight
		}
		layer.template = ""
		layer.overlayGen++
	case frame != nil:
		layer.overlay = append([]byte(nil), frame.Pix...)
		layer.overlayWidth, layer.overlayHeight = frame.Rect.Dx(), frame.Rect.Dy()
		layer.template = lc.Template
		layer.overlayGen++
		layer.framePNG, layer.framePNGGen = append([]byte(nil), lc.Frame...), layer.overlayGen
	}
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// decodeFrame decodes a frame PNG to straight RGBA, bounded as decodePNG
// bounds an image.
func decodeFrame(data []byte) (*image.NRGBA, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode frame: %w", err)
	}
	if cfg.Width > MaxImageDimension || cfg.Height > MaxImageDimension {
		return nil, ErrImageTooLarge
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode frame: %w", err)
	}
	b := img.Bounds()
	if n, ok := img.(*image.NRGBA); ok && b.Min == (image.Point{}) && n.Stride == b.Dx()*4 {
		return n, nil
	}
	// An opaque frame comes back as RGB, where straight and premultiplied
	// are the same bytes.
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
	return n, nil
}
