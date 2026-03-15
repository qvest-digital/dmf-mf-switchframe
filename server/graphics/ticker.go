package graphics

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"sync"

	"github.com/zsiec/switchframe/server/graphics/textrender"
)

var (
	ErrTickerActive   = errors.New("graphics: ticker already running on this layer")
	ErrTickerNotFound = errors.New("graphics: no ticker running on this layer")
)

// TickerConfig describes a scrolling ticker.
type TickerConfig struct {
	Text     string  `json:"text"`
	FontSize float64 `json:"fontSize"`
	Speed    float64 `json:"speed"` // pixels per second
	Bold     bool    `json:"bold"`
	Loop     bool    `json:"loop"`   // wrap text for seamless looping
	Height   int     `json:"height"` // bar height in pixels (0 = auto from fontSize)
}

// tickerInstance holds the state for one running ticker.
type tickerInstance struct {
	layerID int
	config  TickerConfig
	cancel  chan struct{}
	done    chan struct{}
}

// TickerEngine manages scrolling tickers across graphics layers.
type TickerEngine struct {
	compositor *Compositor
	renderer   *textrender.Renderer
	log        *slog.Logger

	mu        sync.Mutex
	tickers   map[int]*tickerInstance
	closeOnce sync.Once
}

// NewTickerEngine creates a ticker engine bound to a compositor and text renderer.
func NewTickerEngine(c *Compositor, r *textrender.Renderer) *TickerEngine {
	return &TickerEngine{
		compositor: c,
		renderer:   r,
		log:        slog.With("component", "ticker"),
		tickers:    make(map[int]*tickerInstance),
	}
}

// IsRunning returns true if a ticker is active on the given layer.
func (te *TickerEngine) IsRunning(layerID int) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	_, ok := te.tickers[layerID]
	return ok
}

// Start begins a scrolling ticker on the specified layer.
func (te *TickerEngine) Start(layerID int, cfg TickerConfig) error {
	te.mu.Lock()
	if _, ok := te.tickers[layerID]; ok {
		te.mu.Unlock()
		return ErrTickerActive
	}

	inst := &tickerInstance{
		layerID: layerID,
		config:  cfg,
		cancel:  make(chan struct{}),
		done:    make(chan struct{}),
	}
	te.tickers[layerID] = inst
	te.mu.Unlock()

	go te.runTicker(inst)
	return nil
}

// Stop halts the ticker on the specified layer and resets the layer rect
// back to full-frame so subsequent overlays are not clipped.
func (te *TickerEngine) Stop(layerID int) error {
	te.mu.Lock()
	inst, ok := te.tickers[layerID]
	if !ok {
		te.mu.Unlock()
		return ErrTickerNotFound
	}
	delete(te.tickers, layerID)
	te.mu.Unlock()

	close(inst.cancel)
	<-inst.done

	// Reset layer rect to zero (full-frame) so other templates aren't clipped.
	_ = te.compositor.SetLayerRect(layerID, image.Rectangle{})
	return nil
}

// UpdateText changes the ticker text on a running ticker (re-renders the strip).
func (te *TickerEngine) UpdateText(layerID int, text string) error {
	te.mu.Lock()
	inst, ok := te.tickers[layerID]
	if !ok {
		te.mu.Unlock()
		return ErrTickerNotFound
	}
	delete(te.tickers, layerID) // atomic removal under lock prevents Stop() from finding it
	te.mu.Unlock()

	// Stop the old goroutine — only we hold inst now
	close(inst.cancel)
	<-inst.done

	cfg := inst.config
	cfg.Text = text

	return te.Start(layerID, cfg)
}

// cleanup removes the ticker from the map, clears ticker state, and deactivates the layer.
// Called on all exit paths (cancel, natural completion).
func (te *TickerEngine) cleanup(layerID int) {
	te.mu.Lock()
	delete(te.tickers, layerID)
	te.mu.Unlock()

	// Clear ticker state from the layer.
	te.compositor.mu.Lock()
	if layer, ok := te.compositor.layers[layerID]; ok {
		layer.ticker = nil
	}
	te.compositor.mu.Unlock()

	te.compositor.deactivateAndClearLayer(layerID)
}

// Close stops all running tickers. It is safe to call multiple times.
func (te *TickerEngine) Close() {
	te.closeOnce.Do(func() {
		te.mu.Lock()
		tickers := make([]*tickerInstance, 0, len(te.tickers))
		for _, inst := range te.tickers {
			tickers = append(tickers, inst)
		}
		te.tickers = make(map[int]*tickerInstance)
		te.mu.Unlock()

		for _, inst := range tickers {
			close(inst.cancel)
			<-inst.done
		}
	})
}

// maxStripWidth caps the pre-rendered ticker strip to prevent excessive memory
// allocation from very long text. 65536 pixels at 48px tall = ~12.5 MB RGBA.
const maxStripWidth = 65536

// runTicker pre-renders the text strip and installs a tickerState on the layer.
// The actual scroll advancement is driven by ProcessYUV (frame-locked), not by
// a wall-clock ticker. This goroutine just renders, installs state, and waits
// for cancellation.
func (te *TickerEngine) runTicker(inst *tickerInstance) {
	defer close(inst.done)
	defer te.cleanup(inst.layerID)

	cfg := inst.config
	if cfg.Speed <= 0 {
		cfg.Speed = 100
	}
	if cfg.FontSize <= 0 {
		cfg.FontSize = 24
	}

	// Get program resolution for viewport width.
	progW, progH := 1920, 1080
	if te.compositor.resolutionProvider != nil {
		progW, progH = te.compositor.resolutionProvider()
	}

	// Bar height (even-aligned for YUV420).
	barH := cfg.Height
	if barH <= 0 {
		barH = int(cfg.FontSize * 2.0)
	}
	barH = barH &^ 1 // even-align
	if barH < 2 {
		barH = 2
	}

	// Position ticker bar at the bottom of the screen instead of full-frame.
	tickerY := (progH - barH) &^ 1 // even-align Y position
	tickerRect := image.Rect(0, tickerY, progW, tickerY+barH)
	_ = te.compositor.SetLayerRect(inst.layerID, tickerRect)

	// Pre-render the full text strip
	textW, textH := te.renderer.MeasureText(textrender.TextOptions{
		Text:     cfg.Text,
		FontSize: cfg.FontSize,
		Bold:     cfg.Bold,
	})
	if textW == 0 {
		return
	}

	// For loop mode: render [gap][text][gap][text] so viewport can wrap seamlessly
	gapW := progW // one screen-width gap
	var stripW int
	if cfg.Loop {
		stripW = gapW + textW + gapW + textW
	} else {
		stripW = gapW + textW + gapW
	}

	// Cap strip width to prevent excessive memory allocation.
	if stripW > maxStripWidth {
		te.log.Warn("ticker strip width capped", "original", stripW, "max", maxStripWidth)
		stripW = maxStripWidth
	}

	// Create strip image
	strip := image.NewRGBA(image.Rect(0, 0, stripW, barH))

	// Fill with background color
	bgCol := color.RGBA{R: 10, G: 10, B: 25, A: 240}
	draw.Draw(strip, strip.Bounds(), image.NewUniform(bgCol), image.Point{}, draw.Src)

	// Render text onto strip
	textColor := color.RGBA{R: 225, G: 232, B: 240, A: 255}
	textImg, _, err := te.renderer.RenderText(textW, barH, textrender.TextOptions{
		Text:     cfg.Text,
		FontSize: cfg.FontSize,
		Bold:     cfg.Bold,
		Color:    textColor,
	})
	if err != nil {
		te.log.Error("ticker render failed", "error", err)
		return
	}

	// Draw text at first position
	textY := (barH - textH) / 2
	if textY < 0 {
		textY = 0
	}
	draw.Draw(strip, image.Rect(gapW, textY, gapW+textW, textY+textH), textImg, image.Point{}, draw.Over)

	// For loop mode, draw second copy
	if cfg.Loop {
		offset := gapW + textW + gapW
		draw.Draw(strip, image.Rect(offset, textY, offset+textW, textY+textH), textImg, image.Point{}, draw.Over)
	}

	// Add top accent line (blue glow)
	accentCol := color.RGBA{R: 59, G: 130, B: 246, A: 255}
	for x := 0; x < stripW; x++ {
		strip.SetRGBA(x, 0, accentCol)
		strip.SetRGBA(x, 1, accentCol)
	}

	loopPoint := 0
	if cfg.Loop {
		loopPoint = gapW + textW + gapW
	}

	// Install frame-locked ticker state on the layer.
	// ProcessYUV will advance xOffset by (speed / pipelineFPS) each frame.
	viewport := make([]byte, progW*barH*4)
	// Render initial viewport at offset 0.
	extractTickerViewport(strip, 0, progW, barH, viewport)

	// For non-loop tickers, create a done channel so ProcessYUV can signal completion.
	var doneCh chan struct{}
	if !cfg.Loop {
		doneCh = make(chan struct{})
	}

	te.compositor.mu.Lock()
	layer, ok := te.compositor.layers[inst.layerID]
	if ok {
		layer.ticker = &tickerState{
			strip:     strip,
			viewport:  viewport,
			xOffset:   0,
			speed:     cfg.Speed,
			loopPoint: loopPoint,
			viewW:     progW,
			viewH:     barH,
			doneCh:    doneCh,
		}
		layer.overlay = viewport
		layer.overlayWidth = progW
		layer.overlayHeight = barH
	}
	te.compositor.mu.Unlock()

	// Activate the layer so the overlay is rendered by ProcessYUV.
	te.compositor.activateLayer(inst.layerID)

	// Wait for cancellation or natural completion (non-loop).
	// All scrolling is driven by ProcessYUV.
	if doneCh != nil {
		select {
		case <-inst.cancel:
		case <-doneCh:
		}
	} else {
		<-inst.cancel
	}
}

// extractTickerViewport copies a progW x barH viewport from the strip at the given x offset.
// Package-level function used by both the ticker engine and ProcessYUV's frame-locked path.
func extractTickerViewport(strip *image.RGBA, xOffset, viewW, viewH int, dst []byte) {
	stripStride := strip.Stride
	dstStride := viewW * 4

	for y := 0; y < viewH; y++ {
		srcStart := y*stripStride + xOffset*4
		srcEnd := srcStart + dstStride
		if srcEnd > len(strip.Pix) {
			// Wrap-around safety: fill remaining with background
			available := len(strip.Pix) - srcStart
			if available > 0 {
				copy(dst[y*dstStride:], strip.Pix[srcStart:srcStart+available])
			}
			continue
		}
		copy(dst[y*dstStride:y*dstStride+dstStride], strip.Pix[srcStart:srcEnd])
	}
}
