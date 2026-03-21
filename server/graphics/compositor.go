package graphics

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
)

// Errors returned by Compositor methods.
var (
	ErrAlreadyActive    = errors.New("graphics: overlay already active")
	ErrNotActive        = errors.New("graphics: overlay not active")
	ErrNoOverlay        = errors.New("graphics: no overlay frame uploaded")
	ErrFadeActive       = errors.New("graphics: fade transition in progress")
	ErrCompositorClosed = errors.New("compositor: closed")
	ErrLayerNotFound    = errors.New("graphics: layer not found")
	ErrTooManyLayers    = errors.New("graphics: maximum layers reached")
)

// DefaultMaxLayers is the maximum number of graphics layers.
const DefaultMaxLayers = 8

// AnimationConfig describes a graphics overlay animation.
type AnimationConfig struct {
	Mode     string  `json:"mode"`     // "pulse", "transition"
	MinAlpha float64 `json:"minAlpha"` // 0.0-1.0
	MaxAlpha float64 `json:"maxAlpha"` // 0.0-1.0
	SpeedHz  float64 `json:"speedHz"`  // oscillation frequency

	// Transition mode fields.
	ToRect     *RectState `json:"toRect,omitempty"`
	ToAlpha    *float64   `json:"toAlpha,omitempty"`
	DurationMs int        `json:"durationMs,omitempty"`
	Easing     string     `json:"easing,omitempty"` // "linear", "ease-in-out", "smoothstep"

	// DeactivateOnComplete sets layer.active = false when the animation finishes.
	// Used by FlyOut so the layer doesn't remain active at an off-screen position.
	DeactivateOnComplete bool `json:"deactivateOnComplete,omitempty"`
}

// LayerState describes the state of a single graphics layer for serialization.
type LayerState struct {
	ID            int       `json:"id"`
	Template      string    `json:"template,omitempty"`
	Active        bool      `json:"active"`
	FadePosition  float64   `json:"fadePosition,omitempty"`
	AnimationMode string    `json:"animationMode,omitempty"`
	AnimationHz   float64   `json:"animationHz,omitempty"`
	ZOrder        int       `json:"zOrder"`
	Rect          RectState `json:"rect"`
	ImageName     string    `json:"imageName,omitempty"`
	ImageWidth    int       `json:"imageWidth,omitempty"`
	ImageHeight   int       `json:"imageHeight,omitempty"`
}

// RectState describes a layer's position and size for serialization.
type RectState struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// State represents the current graphics compositor state.
type State struct {
	Layers        []LayerState `json:"layers"`
	ProgramWidth  int          `json:"programWidth,omitempty"`
	ProgramHeight int          `json:"programHeight,omitempty"`
}

// graphicsLayer holds per-layer overlay state.
type graphicsLayer struct {
	id            int
	overlay       []byte
	overlayWidth  int
	overlayHeight int
	template      string

	// Position within program frame (full-frame default: {0,0,progW,progH}).
	rect image.Rectangle

	active       bool
	fadePosition float64
	fadeDone     chan struct{}
	fadeCancel   chan struct{}

	animConfig *AnimationConfig
	animDone   chan struct{}
	animCancel chan struct{}

	zOrder int

	// overlayGen is incremented each time the overlay data changes.
	// Used by the GPU compositor node for cache invalidation.
	overlayGen uint64

	// Per-layer image storage. imageData retains original PNG bytes for
	// GET /api/graphics/layers/{id}/image retrieval; imageRGBA holds decoded
	// RGBA pixels used by SetOverlay for overlay compositing.
	imageName   string // original filename
	imageData   []byte // original PNG bytes (for GET retrieval)
	imageRGBA   []byte // decoded RGBA pixels (for overlay compositing)
	imageWidth  int
	imageHeight int

	// Frame-locked ticker state. Set by ticker engine, advanced by ProcessYUV.
	ticker *tickerState
}

// tickerState holds the pre-rendered strip and scroll state for a
// frame-locked ticker. The xOffset is advanced by exactly
// (speed / pipelineFPS) pixels on each ProcessYUV call, producing
// perfectly smooth scrolling at any pipeline frame rate.
type tickerState struct {
	strip     *image.RGBA   // pre-rendered text strip
	viewport  []byte        // reusable viewport buffer (progW * barH * 4)
	xOffset   float64       // current scroll position (sub-pixel precision)
	speed     float64       // pixels per second
	loopPoint int           // wrap position for loop mode (0 = no loop)
	viewW     int           // viewport width
	viewH     int           // viewport height
	done      bool          // true when non-loop ticker has scrolled off the end
	doneCh    chan struct{} // closed when non-loop ticker completes (signals goroutine to exit)
}

// Compositor manages multiple downstream keyer (DSK) graphics overlay layers.
// Each layer has independent position, fade, and animation state.
// When active, program frames are composited with all active layers
// in z-order using the AlphaBlendRGBA function.
//
// The compositor is safe for concurrent use from multiple goroutines.
type Compositor struct {
	log *slog.Logger
	mu  sync.RWMutex

	layers    map[int]*graphicsLayer
	nextID    int
	maxLayers int
	sortedIDs []int // z-order sorted, recomputed on change

	closed bool

	// Callback invoked on state change.
	// Receives a snapshot of the current state so callers don't need
	// to call Status() (which would deadlock under the compositor's lock).
	onStateChange func(State)

	// Returns program video resolution. Set via SetResolutionProvider.
	resolutionProvider func() (width, height int)

	// Pipeline frame rate for frame-locked animations (tickers).
	// Stored as rational num/den for drift-free advancement.
	pipelineFPSNum int
	pipelineFPSDen int

	// Cache of overlay generation counters from the last SnapshotVisibleLayers
	// call. When a layer's gen matches its cached value, the deep-copy is
	// skipped (Overlay=nil in the snapshot). The GPU compositor node reuses
	// its cached GPU overlay for unchanged layers.
	lastSnapshotGens map[int]uint64
}

// NewCompositor creates a new multi-layer graphics compositor.
func NewCompositor() *Compositor {
	return &Compositor{
		log:       slog.With("component", "graphics"),
		layers:    make(map[int]*graphicsLayer),
		maxLayers: DefaultMaxLayers,
	}
}

// AddLayer creates a new graphics layer with default full-frame positioning.
// Returns the layer ID.
func (c *Compositor) AddLayer() (int, error) {
	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()
		return 0, ErrCompositorClosed
	}
	if len(c.layers) >= c.maxLayers {
		c.mu.Unlock()
		return 0, ErrTooManyLayers
	}

	id := c.nextID
	c.nextID++

	layer := &graphicsLayer{
		id:     id,
		zOrder: len(c.layers),
	}
	c.layers[id] = layer
	c.recomputeSortedIDsLocked()

	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return id, nil
}

// RemoveLayer removes a layer by ID, cancelling any active fade/animation.
func (c *Compositor) RemoveLayer(id int) error {
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

	c.cancelLayerFadeLocked(layer)
	c.cancelLayerAnimationLocked(layer)
	delete(c.layers, id)
	c.recomputeSortedIDsLocked()

	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// SetLayerZOrder updates a layer's z-order and recomputes the sorted order.
func (c *Compositor) SetLayerZOrder(id, zOrder int) error {
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

	layer.zOrder = zOrder
	c.recomputeSortedIDsLocked()

	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// SetOverlay stores the RGBA overlay frame data for a specific layer.
func (c *Compositor) SetOverlay(id int, rgba []byte, width, height int, template string) error {
	expected := width * height * 4
	if len(rgba) != expected {
		return fmt.Errorf("rgba data size mismatch: got %d bytes, want %d (%dx%dx4)", len(rgba), expected, width, height)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		return ErrLayerNotFound
	}

	if len(layer.overlay) != expected {
		layer.overlay = make([]byte, expected)
	}
	copy(layer.overlay, rgba)
	layer.overlayWidth = width
	layer.overlayHeight = height
	layer.template = template
	layer.overlayGen++
	return nil
}

// SetLayerRect updates a layer's position rectangle. This triggers a state broadcast.
func (c *Compositor) SetLayerRect(id int, rect image.Rectangle) error {
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

	layer.rect = rect

	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// UpdateLayerRect is a fast-path position update that does NOT trigger a
// state broadcast. Used by fast-control datagrams during drag operations.
func (c *Compositor) UpdateLayerRect(id int, rect image.Rectangle) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCompositorClosed
	}
	layer, ok := c.layers[id]
	if !ok {
		return ErrLayerNotFound
	}

	layer.rect = rect
	return nil
}

// On activates a layer immediately (CUT ON).
func (c *Compositor) On(id int) error {
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
	if layer.overlay == nil {
		c.mu.Unlock()
		return ErrNoOverlay
	}

	c.cancelLayerFadeLocked(layer)
	c.cancelLayerAnimationLocked(layer)

	layer.active = true
	layer.fadePosition = 1.0
	c.log.Debug("layer CUT ON", "layer", id)
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// activateLayer sets a layer to active with full opacity. Used by ticker/text-anim
// engines that manage their own overlay lifecycle. Unlike On(), this does not
// require an existing overlay (the engine will set it shortly after).
func (c *Compositor) activateLayer(id int) {
	c.mu.Lock()
	layer, ok := c.layers[id]
	if !ok || c.closed {
		c.mu.Unlock()
		return
	}
	layer.active = true
	layer.fadePosition = 1.0
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
}

// deactivateAndClearLayer deactivates a layer and clears its overlay data.
// Used by ticker/text-anim engines on stop or natural completion.
func (c *Compositor) deactivateAndClearLayer(id int) {
	c.mu.Lock()
	layer, ok := c.layers[id]
	if !ok || c.closed {
		c.mu.Unlock()
		return
	}
	layer.active = false
	layer.fadePosition = 0.0
	layer.overlay = nil
	layer.overlayWidth = 0
	layer.overlayHeight = 0
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
}

// Off deactivates a layer immediately (CUT OFF).
func (c *Compositor) Off(id int) error {
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

	c.cancelLayerFadeLocked(layer)
	c.cancelLayerAnimationLocked(layer)

	layer.active = false
	layer.fadePosition = 0.0
	c.log.Debug("layer CUT OFF", "layer", id)
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	return nil
}

// AutoOn starts a fade-in transition (AUTO ON) for a specific layer.
func (c *Compositor) AutoOn(id int, duration time.Duration) error {
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
	if layer.overlay == nil {
		c.mu.Unlock()
		return ErrNoOverlay
	}
	if layer.fadeDone != nil || layer.animDone != nil {
		c.mu.Unlock()
		return ErrFadeActive
	}
	if layer.active && layer.fadePosition >= 1.0 {
		c.mu.Unlock()
		return nil
	}

	layer.active = true
	layer.fadePosition = 0.0
	layer.fadeDone = make(chan struct{})
	layer.fadeCancel = make(chan struct{})
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	go c.runFade(id, 0.0, 1.0, duration)
	return nil
}

// AutoOff starts a fade-out transition (AUTO OFF) for a specific layer.
func (c *Compositor) AutoOff(id int, duration time.Duration) error {
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
	if !layer.active {
		c.mu.Unlock()
		return ErrNotActive
	}
	if layer.fadeDone != nil || layer.animDone != nil {
		c.mu.Unlock()
		return ErrFadeActive
	}

	layer.fadeDone = make(chan struct{})
	layer.fadeCancel = make(chan struct{})
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	go c.runFade(id, 1.0, 0.0, duration)
	return nil
}

// Animate starts a looping animation on a specific layer.
func (c *Compositor) Animate(id int, cfg AnimationConfig) error {
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
	if !layer.active {
		c.mu.Unlock()
		return ErrNotActive
	}
	if layer.fadeDone != nil || layer.animDone != nil {
		c.mu.Unlock()
		return ErrFadeActive
	}

	layer.animConfig = &cfg
	layer.animCancel = make(chan struct{})
	layer.animDone = make(chan struct{})
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}

	switch cfg.Mode {
	case "transition":
		go c.runTransitionAnimation(id)
	default:
		go c.runPulseAnimation(id)
	}
	return nil
}

// StopAnimation stops any running animation on a layer and restores fadePosition to 1.0.
func (c *Compositor) StopAnimation(id int) error {
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
	if layer.animDone == nil {
		c.mu.Unlock()
		return nil
	}
	c.cancelLayerAnimationLocked(layer)
	layer.fadePosition = 1.0
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()
	if cb != nil {
		cb(state)
	}
	return nil
}

// Status returns the current graphics compositor state.
func (c *Compositor) Status() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.buildStateLocked()
	if c.resolutionProvider != nil {
		s.ProgramWidth, s.ProgramHeight = c.resolutionProvider()
	}
	return s
}

// SetResolutionProvider sets a callback that returns the current program
// video resolution. Used by Status() to inform clients what resolution
// to render graphics at.
func (c *Compositor) SetResolutionProvider(fn func() (width, height int)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolutionProvider = fn
}

// SetPipelineFPS sets the pipeline frame rate for frame-locked animations.
// Uses rational num/den for drift-free ticker advancement (e.g., 30000/1001
// for 29.97fps). Must be called when the pipeline format changes.
func (c *Compositor) SetPipelineFPS(fpsNum, fpsDen int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pipelineFPSNum = fpsNum
	c.pipelineFPSDen = fpsDen
}

// OnStateChange registers a callback invoked when the overlay state changes.
func (c *Compositor) OnStateChange(fn func(State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStateChange = fn
}

// Close shuts down the compositor, cancelling all layer fades/animations.
func (c *Compositor) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, layer := range c.layers {
		c.cancelLayerFadeLocked(layer)
		c.cancelLayerAnimationLocked(layer)
	}
}

// ProcessYUV applies all active overlay layers to a raw YUV420 buffer in-place.
// Layers are composited sequentially in z-order (lowest first). Each layer's
// blend modifies the buffer before the next layer reads it, producing correct
// layered compositing. All blending runs single-threaded under RLock.
//
// blendScratch and colLUTScratch are optional caller-owned scratch buffers for
// sub-frame blending. If non-nil, they will be reused across calls to avoid
// per-frame allocation. Ownership stays with the caller (typically the pipeline node).
func (c *Compositor) ProcessYUV(yuv []byte, width, height int, blendScratch *[]byte, colLUTScratch *[]int) []byte {
	if width%2 != 0 || height%2 != 0 || width <= 0 || height <= 0 {
		return yuv
	}
	if len(yuv) < width*height*3/2 {
		return yuv
	}

	// Fast path: skip all processing when no layers are visible and no
	// tickers need advancement. Avoids write lock (ticker) + read lock
	// (compositing) + layer iteration on every frame.
	c.mu.RLock()
	hasWork := c.hasVisibleLayersLocked() || c.hasActiveTickersLocked()
	c.mu.RUnlock()
	if !hasWork {
		return yuv
	}

	c.mu.Lock()

	// Advance frame-locked tickers before compositing.
	if c.pipelineFPSNum > 0 && c.pipelineFPSDen > 0 {
		for _, id := range c.sortedIDs {
			layer := c.layers[id]
			if layer.ticker == nil || !layer.active || layer.ticker.done {
				continue
			}
			ts := layer.ticker
			// pixelsPerFrame = speed * fpsDen / fpsNum (rational, drift-free)
			pixelsPerFrame := ts.speed * float64(c.pipelineFPSDen) / float64(c.pipelineFPSNum)
			ts.xOffset += pixelsPerFrame

			// Loop wrap.
			if ts.loopPoint > 0 {
				for ts.xOffset >= float64(ts.loopPoint) {
					ts.xOffset -= float64(ts.loopPoint)
				}
			} else {
				// Non-loop: check if scrolled past the end of the strip.
				stripW := ts.strip.Bounds().Dx()
				if int(ts.xOffset)+ts.viewW > stripW {
					ts.done = true
					layer.active = false
					if ts.doneCh != nil {
						close(ts.doneCh)
						ts.doneCh = nil
					}
					continue
				}
			}

			// Extract viewport from strip at current offset.
			xInt := int(ts.xOffset)
			if ts.strip != nil && ts.viewport != nil {
				extractTickerViewport(ts.strip, xInt, ts.viewW, ts.viewH, ts.viewport)
				// Update overlay in-place (no copy needed, we hold the lock).
				layer.overlay = ts.viewport
				layer.overlayWidth = ts.viewW
				layer.overlayHeight = ts.viewH
				layer.overlayGen++ // GPU cache invalidation
			}
		}
	}

	c.mu.Unlock()

	// Compositing pass under read lock (no ticker writes).
	// Note: there is a brief window between Unlock and RLock where fade/animation
	// goroutines can modify layer state (fade position, rect, active flag). This is
	// benign — at most a one-frame delay (~16-41ms) in observing animation changes,
	// which is visually imperceptible.
	c.mu.RLock()
	defer c.mu.RUnlock()

	fullFrame := fullFrameRect(width, height)
	for _, id := range c.sortedIDs {
		layer := c.layers[id]
		if !layer.active || layer.fadePosition < 1.0/255.0 || layer.overlay == nil {
			continue
		}
		// Skip fully transparent overlays (empty layers, cleared graphics).
		if isOverlayTransparent(layer.overlay, layer.overlayWidth*layer.overlayHeight) {
			continue
		}
		if (layer.rect == image.Rectangle{} || layer.rect == fullFrame) &&
			layer.overlayWidth == width && layer.overlayHeight == height {
			// Fast path: full-frame overlay (existing behavior).
			AlphaBlendRGBA(yuv, layer.overlay, width, height, layer.fadePosition)
		} else {
			// Sub-frame: blend overlay into rect region using scratch buffer.
			rect := layer.rect
			if (rect == image.Rectangle{}) {
				rect = fullFrame
			}
			var scratch []byte
			if blendScratch != nil {
				scratch = *blendScratch
			}
			var colLUT []int
			if colLUTScratch != nil {
				colLUT = *colLUTScratch
			}
			scratch, colLUT = AlphaBlendRGBARectInto(yuv, layer.overlay, width, height,
				layer.overlayWidth, layer.overlayHeight,
				rect, layer.fadePosition, scratch, colLUT)
			if blendScratch != nil {
				*blendScratch = scratch
			}
			if colLUTScratch != nil {
				*colLUTScratch = colLUT
			}
		}
	}
	return yuv
}

// IsActive returns whether any layer is currently active.
func (c *Compositor) IsActive() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, layer := range c.layers {
		if layer.active {
			return true
		}
	}
	return false
}

// hasVisibleLayersLocked returns true if any layer is active with a visible
// fade position and a non-nil overlay. Caller must hold at least c.mu.RLock.
func (c *Compositor) hasVisibleLayersLocked() bool {
	for _, layer := range c.layers {
		if layer.active && layer.fadePosition >= 1.0/255.0 && layer.overlay != nil {
			return true
		}
	}
	return false
}

// HasActiveLayers returns true if any layer is currently active with visible
// content. Used by the GPU compositor node's Active() check.
func (c *Compositor) HasActiveLayers() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hasVisibleLayersLocked()
}

// VisibleLayerSnap is a deep-copied snapshot of one visible layer's state.
// Used by the GPU compositor node to avoid holding locks during GPU dispatch.
type VisibleLayerSnap struct {
	ID               int
	RectX, RectY     int
	RectW, RectH     int
	Alpha            float32 // 0.0-1.0
	Overlay          []byte  // RGBA pixel data (deep copy)
	OverlayW, OverlayH int
	Gen              uint64  // generation counter for cache invalidation
}

// SnapshotVisibleLayers returns a deep-copied snapshot of all visible layers
// sorted by z-order. The GPU compositor node calls this once per frame to get
// all RGBA overlays and positions, then dispatches GPU kernels without locks.
//
// Uses a write lock to advance frame-locked tickers before snapshotting.
// This ensures tickers scroll at the correct rate regardless of whether
// the CPU or GPU pipeline is active.
func (c *Compositor) SnapshotVisibleLayers() []VisibleLayerSnap {
	// Advance tickers under write lock (same as ProcessYUV does).
	c.mu.Lock()
	if c.pipelineFPSNum > 0 && c.pipelineFPSDen > 0 {
		for _, id := range c.sortedIDs {
			layer := c.layers[id]
			if layer.ticker == nil || !layer.active || layer.ticker.done {
				continue
			}
			ts := layer.ticker
			pixelsPerFrame := ts.speed * float64(c.pipelineFPSDen) / float64(c.pipelineFPSNum)
			ts.xOffset += pixelsPerFrame

			if ts.loopPoint > 0 {
				for ts.xOffset >= float64(ts.loopPoint) {
					ts.xOffset -= float64(ts.loopPoint)
				}
			} else {
				stripW := ts.strip.Bounds().Dx()
				if int(ts.xOffset)+ts.viewW > stripW {
					ts.done = true
					layer.active = false
					if ts.doneCh != nil {
						close(ts.doneCh)
						ts.doneCh = nil
					}
					continue
				}
			}

			xInt := int(ts.xOffset)
			if ts.strip != nil && ts.viewport != nil {
				extractTickerViewport(ts.strip, xInt, ts.viewW, ts.viewH, ts.viewport)
				layer.overlay = ts.viewport
				layer.overlayWidth = ts.viewW
				layer.overlayHeight = ts.viewH
				layer.overlayGen++ // GPU cache invalidation
			}
		}
	}
	c.mu.Unlock()

	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []VisibleLayerSnap
	for _, id := range c.sortedIDs {
		layer := c.layers[id]
		if !layer.active || layer.fadePosition < 1.0/255.0 || layer.overlay == nil {
			continue
		}
		if isOverlayTransparent(layer.overlay, layer.overlayWidth*layer.overlayHeight) {
			continue
		}

		rect := layer.rect
		if (rect == image.Rectangle{}) {
			rect = image.Rect(0, 0, layer.overlayWidth, layer.overlayHeight)
		}

		snap := VisibleLayerSnap{
			ID:       layer.id,
			RectX:    rect.Min.X,
			RectY:    rect.Min.Y,
			RectW:    rect.Dx(),
			RectH:    rect.Dy(),
			Alpha:    float32(layer.fadePosition),
			OverlayW: layer.overlayWidth,
			OverlayH: layer.overlayHeight,
			Gen:      layer.overlayGen,
		}

		// Only deep-copy overlay RGBA when generation changed since
		// the GPU node's last upload. When Overlay is nil but Gen is set,
		// the GPU node reuses its cached GPU overlay (same gen = same data).
		// This eliminates 8.3MB allocation + memcpy per frame per layer
		// for static overlays (~250MB/s savings at 1080p 30fps).
		if knownGen, ok := c.lastSnapshotGens[layer.id]; !ok || knownGen != layer.overlayGen {
			overlayCopy := make([]byte, len(layer.overlay))
			copy(overlayCopy, layer.overlay)
			snap.Overlay = overlayCopy
			if c.lastSnapshotGens == nil {
				c.lastSnapshotGens = make(map[int]uint64)
			}
			c.lastSnapshotGens[layer.id] = layer.overlayGen
		}
		// else: Overlay is nil, Gen is set — GPU node uses cached overlay.

		result = append(result, snap)
	}
	return result
}

// hasActiveTickersLocked returns true if any layer has an active, non-done
// ticker that needs frame-by-frame advancement. Caller must hold c.mu.RLock.
func (c *Compositor) hasActiveTickersLocked() bool {
	for _, layer := range c.layers {
		if layer.active && layer.ticker != nil && !layer.ticker.done {
			return true
		}
	}
	return false
}

// fullFrameRect returns the image.Rectangle for a full-frame overlay.
func fullFrameRect(w, h int) image.Rectangle {
	return image.Rect(0, 0, w, h)
}

// runFade drives a fade from startAlpha to endAlpha over the given duration
// for a specific layer.
func (c *Compositor) runFade(layerID int, startAlpha, endAlpha float64, duration time.Duration) {
	const tickRate = 60
	tickDur := duration / time.Duration(tickRate)
	if tickDur < time.Millisecond {
		tickDur = time.Millisecond
	}
	ticker := time.NewTicker(tickDur)
	defer ticker.Stop()

	// Grab the cancel channel under lock so we know which one to select on.
	c.mu.RLock()
	layer, ok := c.layers[layerID]
	if !ok {
		c.mu.RUnlock()
		return
	}
	cancelCh := layer.fadeCancel
	c.mu.RUnlock()

	start := time.Now()
	cancelled := false

	defer func() {
		c.mu.Lock()
		layer, ok := c.layers[layerID]
		if ok {
			done := layer.fadeDone
			layer.fadeDone = nil
			layer.fadeCancel = nil
			var state State
			var cb func(State)
			if !cancelled {
				state = c.buildStateLocked()
				cb = c.onStateChange
			}
			c.mu.Unlock()
			if cb != nil {
				cb(state)
			}
			if done != nil {
				close(done)
			}
		} else {
			c.mu.Unlock()
		}
	}()

	for {
		select {
		case <-cancelCh:
			cancelled = true
			return
		case <-ticker.C:
			elapsed := time.Since(start)
			progress := float64(elapsed) / float64(duration)
			if progress >= 1.0 {
				progress = 1.0
			}

			pos := startAlpha + (endAlpha-startAlpha)*progress

			c.mu.Lock()
			layer, ok := c.layers[layerID]
			if !ok {
				c.mu.Unlock()
				return
			}
			layer.fadePosition = pos
			if progress >= 1.0 {
				if endAlpha == 0.0 {
					layer.active = false
				}
				c.mu.Unlock()
				return
			}
			state := c.buildStateLocked()
			cb := c.onStateChange
			c.mu.Unlock()
			if cb != nil {
				cb(state)
			}
		}
	}
}

// cancelLayerFadeLocked cancels any in-progress fade on a layer.
// Must be called with mu held.
func (c *Compositor) cancelLayerFadeLocked(layer *graphicsLayer) {
	if layer.fadeCancel != nil {
		close(layer.fadeCancel)
		done := layer.fadeDone
		c.mu.Unlock()
		if done != nil {
			<-done
		}
		c.mu.Lock()
	}
}

// cancelLayerAnimationLocked cancels any in-progress animation on a layer.
// Must be called with mu held.
func (c *Compositor) cancelLayerAnimationLocked(layer *graphicsLayer) {
	if layer.animCancel != nil {
		close(layer.animCancel)
		done := layer.animDone
		c.mu.Unlock()
		if done != nil {
			<-done
		}
		c.mu.Lock()
	}
}

// runPulseAnimation drives a sinusoidal alpha oscillation at ~60fps for a layer.
func (c *Compositor) runPulseAnimation(layerID int) {
	const tickRate = 60
	ticker := time.NewTicker(time.Second / tickRate)
	defer ticker.Stop()

	c.mu.RLock()
	layer, ok := c.layers[layerID]
	if !ok {
		c.mu.RUnlock()
		return
	}
	cfg := *layer.animConfig
	cancelCh := layer.animCancel
	c.mu.RUnlock()

	start := time.Now()
	var tickCount int

	defer func() {
		c.mu.Lock()
		layer, ok := c.layers[layerID]
		if ok {
			done := layer.animDone
			layer.animConfig = nil
			layer.animCancel = nil
			layer.animDone = nil
			c.mu.Unlock()
			if done != nil {
				close(done)
			}
		} else {
			c.mu.Unlock()
		}
	}()

	for {
		select {
		case <-cancelCh:
			return
		case <-ticker.C:
			t := time.Since(start).Seconds()
			alpha := cfg.MinAlpha + (math.Sin(2*math.Pi*cfg.SpeedHz*t)+1)/2*(cfg.MaxAlpha-cfg.MinAlpha)

			c.mu.Lock()
			layer, ok := c.layers[layerID]
			if !ok {
				c.mu.Unlock()
				return
			}
			layer.fadePosition = alpha
			tickCount++
			var state State
			var cb func(State)
			if tickCount%4 == 0 {
				state = c.buildStateLocked()
				cb = c.onStateChange
			}
			c.mu.Unlock()

			if cb != nil {
				cb(state)
			}
		}
	}
}

// runTransitionAnimation interpolates rect and/or alpha from current values
// to target over duration with easing for a specific layer.
func (c *Compositor) runTransitionAnimation(layerID int) {
	const tickRate = 60
	ticker := time.NewTicker(time.Second / tickRate)
	defer ticker.Stop()

	c.mu.RLock()
	layer, ok := c.layers[layerID]
	if !ok {
		c.mu.RUnlock()
		return
	}
	cfg := *layer.animConfig
	cancelCh := layer.animCancel
	fromRect := layer.rect
	fromAlpha := layer.fadePosition
	c.mu.RUnlock()

	duration := time.Duration(cfg.DurationMs) * time.Millisecond
	if duration <= 0 {
		duration = 500 * time.Millisecond
	}
	start := time.Now()
	var tickCount int

	defer func() {
		c.mu.Lock()
		layer, ok := c.layers[layerID]
		if ok {
			deactivate := cfg.DeactivateOnComplete
			done := layer.animDone
			layer.animConfig = nil
			layer.animCancel = nil
			layer.animDone = nil
			if deactivate {
				layer.active = false
				layer.fadePosition = 0
			}
			state := c.buildStateLocked()
			cb := c.onStateChange
			c.mu.Unlock()
			if done != nil {
				close(done)
			}
			if cb != nil {
				cb(state)
			}
		} else {
			c.mu.Unlock()
		}
	}()

	for {
		select {
		case <-cancelCh:
			return
		case <-ticker.C:
			elapsed := time.Since(start)
			progress := float64(elapsed) / float64(duration)
			if progress > 1.0 {
				progress = 1.0
			}
			t := applyEasing(progress, cfg.Easing)

			c.mu.Lock()
			layer, ok := c.layers[layerID]
			if !ok {
				c.mu.Unlock()
				return
			}

			if cfg.ToRect != nil {
				toRect := image.Rect(cfg.ToRect.X, cfg.ToRect.Y,
					cfg.ToRect.X+cfg.ToRect.Width, cfg.ToRect.Y+cfg.ToRect.Height)
				layer.rect = interpolateRect(fromRect, toRect, t)
			}
			if cfg.ToAlpha != nil {
				layer.fadePosition = fromAlpha + (*cfg.ToAlpha-fromAlpha)*t
			}

			tickCount++
			var state State
			var cb func(State)
			if progress >= 1.0 || tickCount%4 == 0 {
				state = c.buildStateLocked()
				cb = c.onStateChange
			}
			c.mu.Unlock()

			if cb != nil {
				cb(state)
			}

			if progress >= 1.0 {
				return
			}
		}
	}
}

// FlyIn animates a layer from off-screen to its current rect position.
// The entire setup (read target rect, set start position, start animation)
// is performed under a single write lock to prevent TOCTOU races.
func (c *Compositor) FlyIn(id int, from string, durationMs int) error {
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
	if !layer.active {
		c.mu.Unlock()
		return ErrNotActive
	}
	if layer.fadeDone != nil || layer.animDone != nil {
		c.mu.Unlock()
		return ErrFadeActive
	}

	targetRect := layer.rect
	progW, progH := 1920, 1080
	if c.resolutionProvider != nil {
		progW, progH = c.resolutionProvider()
	} else {
		c.log.Warn("FlyIn using default 1920x1080 resolution; call SetResolutionProvider for accurate off-screen positioning")
	}

	startRect := offScreenRect(from, targetRect, progW, progH)
	layer.rect = startRect

	cfg := AnimationConfig{
		Mode:       "transition",
		ToRect:     &RectState{X: targetRect.Min.X, Y: targetRect.Min.Y, Width: targetRect.Dx(), Height: targetRect.Dy()},
		DurationMs: durationMs,
		Easing:     "smoothstep",
	}
	layer.animConfig = &cfg
	layer.animCancel = make(chan struct{})
	layer.animDone = make(chan struct{})
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	go c.runTransitionAnimation(id)
	return nil
}

// FlyOn atomically activates a layer and animates it from off-screen to its
// target rect. This avoids the visual glitch of separate On() + FlyIn() calls
// where the layer is briefly visible at its default position.
func (c *Compositor) FlyOn(id int, from string, durationMs int) error {
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
	if layer.active {
		c.mu.Unlock()
		return ErrAlreadyActive
	}
	if layer.overlay == nil {
		c.mu.Unlock()
		return ErrNoOverlay
	}
	if layer.fadeDone != nil || layer.animDone != nil {
		c.mu.Unlock()
		return ErrFadeActive
	}

	// Read target rect (where the layer should end up)
	targetRect := layer.rect
	progW, progH := 1920, 1080
	if c.resolutionProvider != nil {
		progW, progH = c.resolutionProvider()
	}

	// Move rect off-screen
	startRect := offScreenRect(from, targetRect, progW, progH)
	layer.rect = startRect

	// Activate layer at off-screen position (invisible until animation moves it in)
	layer.active = true
	layer.fadePosition = 1.0

	// Start transition animation to target rect
	cfg := AnimationConfig{
		Mode:       "transition",
		ToRect:     &RectState{X: targetRect.Min.X, Y: targetRect.Min.Y, Width: targetRect.Dx(), Height: targetRect.Dy()},
		DurationMs: durationMs,
		Easing:     "smoothstep",
	}
	layer.animConfig = &cfg
	layer.animCancel = make(chan struct{})
	layer.animDone = make(chan struct{})
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	go c.runTransitionAnimation(id)
	return nil
}

// FlyOut animates a layer from its current rect to off-screen.
// The entire setup is performed under a single write lock.
func (c *Compositor) FlyOut(id int, to string, durationMs int) error {
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
	if !layer.active {
		c.mu.Unlock()
		return ErrNotActive
	}
	if layer.fadeDone != nil || layer.animDone != nil {
		c.mu.Unlock()
		return ErrFadeActive
	}

	currentRect := layer.rect
	progW, progH := 1920, 1080
	if c.resolutionProvider != nil {
		progW, progH = c.resolutionProvider()
	} else {
		c.log.Warn("FlyOut using default 1920x1080 resolution; call SetResolutionProvider for accurate off-screen positioning")
	}

	endRect := offScreenRect(to, currentRect, progW, progH)

	cfg := AnimationConfig{
		Mode:                 "transition",
		ToRect:               &RectState{X: endRect.Min.X, Y: endRect.Min.Y, Width: endRect.Dx(), Height: endRect.Dy()},
		DurationMs:           durationMs,
		Easing:               "smoothstep",
		DeactivateOnComplete: true,
	}
	layer.animConfig = &cfg
	layer.animCancel = make(chan struct{})
	layer.animDone = make(chan struct{})
	state := c.buildStateLocked()
	cb := c.onStateChange
	c.mu.Unlock()

	if cb != nil {
		cb(state)
	}
	go c.runTransitionAnimation(id)
	return nil
}

// SlideLayer animates a layer from its current position to a new rect.
func (c *Compositor) SlideLayer(id int, toRect image.Rectangle, durationMs int) error {
	cfg := AnimationConfig{
		Mode:       "transition",
		ToRect:     &RectState{X: toRect.Min.X, Y: toRect.Min.Y, Width: toRect.Dx(), Height: toRect.Dy()},
		DurationMs: durationMs,
		Easing:     "smoothstep",
	}
	return c.Animate(id, cfg)
}

// recomputeSortedIDsLocked rebuilds sortedIDs from the layer map, sorted by z-order.
// Must be called with mu held.
func (c *Compositor) recomputeSortedIDsLocked() {
	c.sortedIDs = c.sortedIDs[:0]
	for id := range c.layers {
		c.sortedIDs = append(c.sortedIDs, id)
	}
	sort.Slice(c.sortedIDs, func(i, j int) bool {
		li := c.layers[c.sortedIDs[i]]
		lj := c.layers[c.sortedIDs[j]]
		if li.zOrder != lj.zOrder {
			return li.zOrder < lj.zOrder
		}
		return c.sortedIDs[i] < c.sortedIDs[j]
	})
}

// buildStateLocked returns a snapshot of the compositor's current state.
// Must be called with mu held (read or write).
func (c *Compositor) buildStateLocked() State {
	s := State{
		Layers: make([]LayerState, 0, len(c.sortedIDs)),
	}
	for _, id := range c.sortedIDs {
		layer := c.layers[id]
		ls := LayerState{
			ID:           layer.id,
			Template:     layer.template,
			Active:       layer.active,
			FadePosition: layer.fadePosition,
			ZOrder:       layer.zOrder,
			Rect: RectState{
				X:      layer.rect.Min.X,
				Y:      layer.rect.Min.Y,
				Width:  layer.rect.Dx(),
				Height: layer.rect.Dy(),
			},
			ImageName:   layer.imageName,
			ImageWidth:  layer.imageWidth,
			ImageHeight: layer.imageHeight,
		}
		if layer.animConfig != nil {
			ls.AnimationMode = layer.animConfig.Mode
			ls.AnimationHz = layer.animConfig.SpeedHz
		}
		s.Layers = append(s.Layers, ls)
	}
	return s
}

// applyEasing applies an easing function to a progress value [0,1].
func applyEasing(t float64, easing string) float64 {
	switch easing {
	case "smoothstep":
		return t * t * (3 - 2*t)
	case "ease-in-out":
		if t < 0.5 {
			return 2 * t * t
		}
		return 1 - (-2*t+2)*(-2*t+2)/2
	default: // "linear"
		return t
	}
}

// interpolateRect linearly interpolates between two rectangles.
// Output coordinates are even-aligned (&^1) for YUV420 chroma compatibility.
func interpolateRect(a, b image.Rectangle, t float64) image.Rectangle {
	return image.Rect(
		int(float64(a.Min.X)+float64(b.Min.X-a.Min.X)*t)&^1,
		int(float64(a.Min.Y)+float64(b.Min.Y-a.Min.Y)*t)&^1,
		int(float64(a.Max.X)+float64(b.Max.X-a.Max.X)*t)&^1,
		int(float64(a.Max.Y)+float64(b.Max.Y-a.Max.Y)*t)&^1,
	)
}

// offScreenRect computes a rect position off-screen in the given direction.
func offScreenRect(direction string, rect image.Rectangle, progW, progH int) image.Rectangle {
	w, h := rect.Dx(), rect.Dy()
	switch direction {
	case "left":
		return image.Rect(-w, rect.Min.Y, 0, rect.Min.Y+h)
	case "right":
		return image.Rect(progW, rect.Min.Y, progW+w, rect.Min.Y+h)
	case "top":
		return image.Rect(rect.Min.X, -h, rect.Min.X+w, 0)
	case "bottom":
		return image.Rect(rect.Min.X, progH, rect.Min.X+w, progH+h)
	default:
		return rect
	}
}
