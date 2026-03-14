package graphics

import (
	"errors"
	"image"
	"image/color"

	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zsiec/switchframe/server/graphics/textrender"
)

var (
	ErrTextAnimActive   = errors.New("graphics: text animation already running on this layer")
	ErrTextAnimNotFound = errors.New("graphics: no text animation running on this layer")
)

// TextAnimationConfig describes a text animation.
type TextAnimationConfig struct {
	Mode           string  `json:"mode"` // "typewriter" or "fade-word"
	Text           string  `json:"text"`
	FontSize       float64 `json:"fontSize"`
	Bold           bool    `json:"bold"`
	CharsPerSec    float64 `json:"charsPerSec"`    // typewriter mode
	WordDelayMs    int     `json:"wordDelayMs"`    // fade-word mode: delay between words
	FadeDurationMs int     `json:"fadeDurationMs"` // fade-word mode: per-word fade duration
	Width          int     `json:"width"`          // render width (0 = program width)
	Height         int     `json:"height"`         // render height (0 = auto)
}

// textAnimInstance holds state for one running text animation.
type textAnimInstance struct {
	layerID int
	config  TextAnimationConfig
	cancel  chan struct{}
	done    chan struct{}
}

// TextAnimationEngine manages text animations across graphics layers.
type TextAnimationEngine struct {
	compositor *Compositor
	renderer   *textrender.Renderer
	log        *slog.Logger

	mu        sync.Mutex
	anims     map[int]*textAnimInstance
	closeOnce sync.Once
}

// NewTextAnimationEngine creates a text animation engine.
func NewTextAnimationEngine(c *Compositor, r *textrender.Renderer) *TextAnimationEngine {
	return &TextAnimationEngine{
		compositor: c,
		renderer:   r,
		log:        slog.With("component", "text-animation"),
		anims:      make(map[int]*textAnimInstance),
	}
}

// IsRunning returns true if a text animation is active on the given layer.
func (tae *TextAnimationEngine) IsRunning(layerID int) bool {
	tae.mu.Lock()
	defer tae.mu.Unlock()
	_, ok := tae.anims[layerID]
	return ok
}

// Start begins a text animation on the specified layer.
func (tae *TextAnimationEngine) Start(layerID int, cfg TextAnimationConfig) error {
	tae.mu.Lock()
	if _, ok := tae.anims[layerID]; ok {
		tae.mu.Unlock()
		return ErrTextAnimActive
	}

	inst := &textAnimInstance{
		layerID: layerID,
		config:  cfg,
		cancel:  make(chan struct{}),
		done:    make(chan struct{}),
	}
	tae.anims[layerID] = inst
	tae.mu.Unlock()

	// Stop any compositor animation on this layer
	_ = tae.compositor.StopAnimation(layerID)

	switch cfg.Mode {
	case "typewriter":
		go tae.runTypewriter(inst)
	case "fade-word":
		go tae.runFadeWord(inst)
	default:
		tae.mu.Lock()
		delete(tae.anims, layerID)
		tae.mu.Unlock()
		close(inst.done)
		return errors.New("graphics: unknown text animation mode: " + cfg.Mode)
	}
	return nil
}

// Stop halts the text animation on the specified layer.
// Returns nil if no animation is running (already finished or never started).
func (tae *TextAnimationEngine) Stop(layerID int) error {
	tae.mu.Lock()
	inst, ok := tae.anims[layerID]
	if !ok {
		tae.mu.Unlock()
		// Animation already finished or never started — no-op.
		return nil
	}
	delete(tae.anims, layerID)
	tae.mu.Unlock()

	close(inst.cancel)
	<-inst.done
	return nil
}

// Close stops all running text animations. It is safe to call multiple times.
func (tae *TextAnimationEngine) Close() {
	tae.closeOnce.Do(func() {
		tae.mu.Lock()
		anims := make([]*textAnimInstance, 0, len(tae.anims))
		for _, inst := range tae.anims {
			anims = append(anims, inst)
		}
		tae.anims = make(map[int]*textAnimInstance)
		tae.mu.Unlock()

		for _, inst := range anims {
			close(inst.cancel)
			<-inst.done
		}
	})
}

// cleanup removes the animation from the map and deactivates the layer.
// Called on all exit paths (cancel, natural completion).
func (tae *TextAnimationEngine) cleanup(layerID int) {
	tae.mu.Lock()
	delete(tae.anims, layerID)
	tae.mu.Unlock()

	tae.compositor.deactivateAndClearLayer(layerID)
}

// runTypewriter reveals characters one at a time.
func (tae *TextAnimationEngine) runTypewriter(inst *textAnimInstance) {
	defer close(inst.done)
	defer tae.cleanup(inst.layerID)

	cfg := inst.config
	if cfg.CharsPerSec <= 0 {
		cfg.CharsPerSec = 15
	}
	if cfg.FontSize <= 0 {
		cfg.FontSize = 32
	}

	text := cfg.Text
	totalChars := utf8.RuneCountInString(text)
	if totalChars == 0 {
		return
	}

	// Determine render dimensions
	renderW := cfg.Width
	renderH := cfg.Height
	if renderW <= 0 {
		if tae.compositor.resolutionProvider != nil {
			renderW, _ = tae.compositor.resolutionProvider()
		} else {
			renderW = 1920
		}
	}
	if renderH <= 0 {
		_, renderH = tae.renderer.MeasureText(textrender.TextOptions{
			Text: text, FontSize: cfg.FontSize, Bold: cfg.Bold,
		})
		renderH = int(float64(renderH) * 1.5) // add padding
	}
	// Even-align for YUV420 chroma compatibility.
	renderW = renderW &^ 1
	renderH = renderH &^ 1
	if renderW < 2 {
		renderW = 2
	}
	if renderH < 2 {
		renderH = 2
	}

	// Activate the layer so the overlay is rendered by ProcessYUV.
	tae.compositor.activateLayer(inst.layerID)

	textColor := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	charInterval := time.Duration(float64(time.Second) / cfg.CharsPerSec)
	ticker := time.NewTicker(charInterval)
	defer ticker.Stop()

	revealed := 0
	runes := []rune(text)

	for {
		select {
		case <-inst.cancel:
			return
		case <-ticker.C:
			revealed++
			if revealed > totalChars {
				return // animation complete
			}

			// Render the revealed portion
			partial := string(runes[:revealed])
			_, rgba, err := tae.renderer.RenderText(renderW, renderH, textrender.TextOptions{
				Text:     partial,
				FontSize: cfg.FontSize,
				Bold:     cfg.Bold,
				Color:    textColor,
			})
			if err != nil {
				tae.log.Error("typewriter render failed", "error", err)
				return
			}
			_ = tae.compositor.SetOverlay(inst.layerID, rgba, renderW, renderH, "text-anim")
		}
	}
}

// preRenderedWord holds a word image rendered once at full opacity.
type preRenderedWord struct {
	img  *image.RGBA
	x    int
	w, h int
}

// runFadeWord reveals words sequentially with an alpha ramp.
// Word images are pre-rendered once; only alpha blitting happens per frame.
func (tae *TextAnimationEngine) runFadeWord(inst *textAnimInstance) {
	defer close(inst.done)
	defer tae.cleanup(inst.layerID)

	cfg := inst.config
	if cfg.FontSize <= 0 {
		cfg.FontSize = 32
	}
	if cfg.WordDelayMs <= 0 {
		cfg.WordDelayMs = 300
	}
	if cfg.FadeDurationMs <= 0 {
		cfg.FadeDurationMs = 200
	}

	words := splitWordsForAnim(cfg.Text)
	if len(words) == 0 {
		return
	}

	renderW := cfg.Width
	renderH := cfg.Height
	if renderW <= 0 {
		if tae.compositor.resolutionProvider != nil {
			renderW, _ = tae.compositor.resolutionProvider()
		} else {
			renderW = 1920
		}
	}
	if renderH <= 0 {
		_, renderH = tae.renderer.MeasureText(textrender.TextOptions{
			Text: cfg.Text, FontSize: cfg.FontSize, Bold: cfg.Bold,
		})
		renderH = int(float64(renderH) * 1.5)
	}
	// Even-align for YUV420 chroma compatibility.
	renderW = renderW &^ 1
	renderH = renderH &^ 1
	if renderW < 2 {
		renderW = 2
	}
	if renderH < 2 {
		renderH = 2
	}

	// Pre-render all word images at full opacity (one-time cost).
	var preRendered []preRenderedWord
	xOffset := 0
	for i, word := range words {
		wordText := word
		if i > 0 {
			wordText = " " + word
		}
		wordW, wordH := tae.renderer.MeasureText(textrender.TextOptions{
			Text: wordText, FontSize: cfg.FontSize, Bold: cfg.Bold,
		})
		wordImg, _, err := tae.renderer.RenderText(wordW, wordH, textrender.TextOptions{
			Text:     wordText,
			FontSize: cfg.FontSize,
			Bold:     cfg.Bold,
			Color:    color.RGBA{R: 255, G: 255, B: 255, A: 255},
		})
		if err != nil {
			continue
		}
		preRendered = append(preRendered, preRenderedWord{img: wordImg, x: xOffset, w: wordW, h: wordH})
		xOffset += wordW
	}
	if len(preRendered) == 0 {
		return
	}

	// Reusable compositing buffer — cleared each frame instead of allocating.
	compBuf := image.NewRGBA(image.Rect(0, 0, renderW, renderH))

	// Activate the layer so the overlay is rendered by ProcessYUV.
	tae.compositor.activateLayer(inst.layerID)

	const frameRate = 60
	frameTicker := time.NewTicker(time.Second / frameRate)
	defer frameTicker.Stop()

	wordDelay := time.Duration(cfg.WordDelayMs) * time.Millisecond
	fadeDuration := time.Duration(cfg.FadeDurationMs) * time.Millisecond
	start := time.Now()

	for {
		select {
		case <-inst.cancel:
			return
		case <-frameTicker.C:
			elapsed := time.Since(start)

			// Clear compositing buffer.
			for i := range compBuf.Pix {
				compBuf.Pix[i] = 0
			}

			allDone := true
			for i, pw := range preRendered {
				wordStart := time.Duration(i) * wordDelay
				wordElapsed := elapsed - wordStart

				if wordElapsed < 0 {
					allDone = false
					continue
				}

				alpha := float64(wordElapsed) / float64(fadeDuration)
				if alpha > 1 {
					alpha = 1
				} else {
					allDone = false
				}

				// Blit pre-rendered word with alpha scaling.
				blitWordAlpha(compBuf, pw.img, pw.x, 0, uint8(alpha*255))
			}

			_ = tae.compositor.SetOverlay(inst.layerID, compBuf.Pix, renderW, renderH, "text-anim")

			if allDone {
				return
			}
		}
	}
}

// blitWordAlpha copies a pre-rendered word image into dst at (dstX, dstY)
// with per-pixel alpha scaled by the given factor.
func blitWordAlpha(dst, src *image.RGBA, dstX, dstY int, alpha uint8) {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	dstBounds := dst.Bounds()
	dstW := dstBounds.Dx()
	dstH := dstBounds.Dy()

	for y := 0; y < srcH; y++ {
		dy := dstY + y
		if dy < 0 || dy >= dstH {
			continue
		}
		srcRowOff := y * src.Stride
		dstRowOff := dy * dst.Stride
		for x := 0; x < srcW; x++ {
			dx := dstX + x
			if dx < 0 || dx >= dstW {
				continue
			}
			srcOff := srcRowOff + x*4
			dstOff := dstRowOff + dx*4

			srcA := src.Pix[srcOff+3]
			if srcA == 0 {
				continue
			}
			a := uint16(srcA) * uint16(alpha) / 255

			dst.Pix[dstOff] = src.Pix[srcOff]
			dst.Pix[dstOff+1] = src.Pix[srcOff+1]
			dst.Pix[dstOff+2] = src.Pix[srcOff+2]
			dst.Pix[dstOff+3] = uint8(a)
		}
	}
}

// splitWordsForAnim splits text on spaces for word-by-word animation.
func splitWordsForAnim(text string) []string {
	return strings.Fields(text)
}
