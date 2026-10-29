package graphics

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidLayerName   = errors.New("graphics: layer name must be at most 64 characters")
	ErrInvalidLayerConfig = errors.New("graphics: layer config must be a JSON object of at most 64 KiB")
	ErrInvalidLayerOrder  = errors.New("graphics: order must list every layer exactly once")
	ErrInvalidLayerExport = errors.New("graphics: invalid layer export")
	ErrLayersOnAir        = errors.New("graphics: layers are on air")
)

const (
	maxLayerNameLen   = 64
	maxLayerConfigLen = 64 << 10

	// ExportFormat and ExportVersion identify a layer export document.
	ExportFormat  = "switchframe-graphics"
	ExportVersion = 1
)

// SetLayerSettings stores a layer's name and its control-surface config.
// A nil argument leaves that field as it is. The config is opaque to the
// compositor: it is what the control surface needs to show the layer the
// way it was set up, kept here so every session and both engines see it.
func (c *Compositor) SetLayerSettings(id int, name *string, config json.RawMessage) error {
	if name != nil {
		n, err := validLayerName(*name)
		if err != nil {
			return err
		}
		name = &n
	}
	if config != nil {
		var err error
		if config, err = validLayerConfig(config); err != nil {
			return err
		}
	}

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
	if name != nil {
		layer.name = *name
	}
	if config != nil {
		layer.config = config
	}
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// SetLayerOrder restacks the layers bottom to top in the order given, which
// must name every layer exactly once. Z-orders become 0..n-1; layer IDs, and
// with them everything that addresses a layer, stay as they are.
func (c *Compositor) SetLayerOrder(ids []int) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCompositorClosed
	}
	if len(ids) != len(c.layers) {
		c.mu.Unlock()
		return ErrInvalidLayerOrder
	}
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if _, ok := c.layers[id]; !ok || seen[id] {
			c.mu.Unlock()
			return ErrInvalidLayerOrder
		}
		seen[id] = true
	}
	for z, id := range ids {
		c.layers[id].zOrder = z
	}
	c.recomputeSortedIDsLocked()
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// LayerExport is the export document for every graphics layer.
type LayerExport struct {
	Format  string        `json:"format"`
	Version int           `json:"version"`
	Layers  []LayerRecord `json:"layers"`
}

// LayerRecord is one layer in a LayerExport. Runtime state (on air, fades,
// animations, tickers, sequences) is not part of it.
type LayerRecord struct {
	ID     int             `json:"id"`
	Name   string          `json:"name,omitempty"`
	ZOrder int             `json:"zOrder"`
	Rect   RectState       `json:"rect"`
	Config json.RawMessage `json:"config,omitempty"`
	Image  *LayerImage     `json:"image,omitempty"`
}

// LayerImage is a layer's uploaded PNG.
type LayerImage struct {
	Name string `json:"name"`
	PNG  string `json:"png"` // base64
}

// ExportLayers returns every layer, bottom to top.
func (c *Compositor) ExportLayers() LayerExport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := LayerExport{Format: ExportFormat, Version: ExportVersion, Layers: []LayerRecord{}}
	for _, id := range c.sortedIDs {
		l := c.layers[id]
		rec := LayerRecord{
			ID:     l.id,
			Name:   l.name,
			ZOrder: l.zOrder,
			Rect: RectState{
				X: l.rect.Min.X, Y: l.rect.Min.Y,
				Width: l.rect.Dx(), Height: l.rect.Dy(),
			},
			Config: l.config,
		}
		if l.imageData != nil {
			rec.Image = &LayerImage{Name: l.imageName, PNG: base64.StdEncoding.EncodeToString(l.imageData)}
		}
		out.Layers = append(out.Layers, rec)
	}
	return out
}

// ImportLayers replaces every layer with the ones in doc, keeping their IDs
// so playlist events that address a layer find it again. It is refused while
// any layer is on air, and nothing changes when doc is invalid. Imported
// layers start off air.
func (c *Compositor) ImportLayers(doc LayerExport) error {
	type prepared struct {
		rec  LayerRecord
		png  []byte
		rgba *image.RGBA
	}
	if doc.Format != ExportFormat || doc.Version != ExportVersion {
		return fmt.Errorf("%w: format %q version %d", ErrInvalidLayerExport, doc.Format, doc.Version)
	}
	if len(doc.Layers) > c.maxLayersValue() {
		return fmt.Errorf("%w: %d layers, at most %d", ErrInvalidLayerExport, len(doc.Layers), c.maxLayersValue())
	}
	layers := make([]prepared, 0, len(doc.Layers))
	ids := make(map[int]bool, len(doc.Layers))
	for _, rec := range doc.Layers {
		if rec.ID < 0 || ids[rec.ID] {
			return fmt.Errorf("%w: layer id %d is negative or repeated", ErrInvalidLayerExport, rec.ID)
		}
		ids[rec.ID] = true
		name, err := validLayerName(rec.Name)
		if err != nil {
			return fmt.Errorf("%w: layer %d: %v", ErrInvalidLayerExport, rec.ID, err)
		}
		rec.Name = name
		if rec.Config != nil {
			if rec.Config, err = validLayerConfig(rec.Config); err != nil {
				return fmt.Errorf("%w: layer %d: %v", ErrInvalidLayerExport, rec.ID, err)
			}
		}
		r := rec.Rect
		if r.X < 0 || r.Y < 0 || r.Width < 0 || r.Height < 0 || r.Width > 8192 || r.Height > 8192 || r.X > 8192 || r.Y > 8192 {
			return fmt.Errorf("%w: layer %d: rect out of range", ErrInvalidLayerExport, rec.ID)
		}
		p := prepared{rec: rec}
		if rec.Image != nil {
			data, err := base64.StdEncoding.DecodeString(rec.Image.PNG)
			if err != nil {
				return fmt.Errorf("%w: layer %d: image: %v", ErrInvalidLayerExport, rec.ID, err)
			}
			rgba, err := decodePNG(data)
			if err != nil {
				return fmt.Errorf("%w: layer %d: image: %v", ErrInvalidLayerExport, rec.ID, err)
			}
			p.png, p.rgba = data, rgba
		}
		layers = append(layers, p)
	}
	// Stack in the exported order; ties keep the document order.
	sort.SliceStable(layers, func(i, j int) bool { return layers[i].rec.ZOrder < layers[j].rec.ZOrder })

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCompositorClosed
	}
	for _, l := range c.layers {
		if l.active || l.fadeDone != nil || l.ticker != nil || l.seq != nil || l.seqProcessing {
			c.mu.Unlock()
			return ErrLayersOnAir
		}
	}
	for id, l := range c.layers {
		c.cancelLayerFadeLocked(l)
		c.cancelLayerAnimationLocked(l)
		delete(c.layers, id)
		delete(c.lastSnapshotCache, id)
	}
	for z, p := range layers {
		r := p.rec.Rect
		l := &graphicsLayer{
			id:     p.rec.ID,
			name:   p.rec.Name,
			config: p.rec.Config,
			zOrder: z,
			rect:   image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height),
		}
		if p.rgba != nil {
			w, h := p.rgba.Bounds().Dx(), p.rgba.Bounds().Dy()
			l.imageName = p.rec.Image.Name
			l.imageData = p.png
			l.imageRGBA = p.rgba.Pix
			l.imageWidth, l.imageHeight = w, h
			l.overlay = append([]byte(nil), p.rgba.Pix...)
			l.overlayWidth, l.overlayHeight = w, h
			l.overlayGen++
		}
		c.layers[l.id] = l
		// Never hand out an imported ID again, nor one handed out before.
		if l.id >= c.nextID {
			c.nextID = l.id + 1
		}
	}
	c.recomputeSortedIDsLocked()
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

func (c *Compositor) maxLayersValue() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxLayers
}

func validLayerName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxLayerNameLen {
		return "", ErrInvalidLayerName
	}
	return name, nil
}

// validLayerConfig accepts a JSON object or null; null clears the config.
func validLayerConfig(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > maxLayerConfigLen {
		return nil, ErrInvalidLayerConfig
	}
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil || obj == nil {
		return nil, ErrInvalidLayerConfig
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return nil, ErrInvalidLayerConfig
	}
	return buf.Bytes(), nil
}
