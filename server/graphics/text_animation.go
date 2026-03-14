package graphics

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
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
	Mode           string  `json:"mode"`           // "typewriter" or "fade-word"
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

	mu    sync.Mutex
	anims map[int]*textAnimInstance
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
func (tae *TextAnimationEngine) Stop(layerID int) error {
	tae.mu.Lock()
	inst, ok := tae.anims[layerID]
	if !ok {
		tae.mu.Unlock()
		return ErrTextAnimNotFound
	}
	delete(tae.anims, layerID)
	tae.mu.Unlock()

	close(inst.cancel)
	<-inst.done
	return nil
}

// Close stops all running text animations.
func (tae *TextAnimationEngine) Close() {
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
}

// cleanup removes the animation from the map (called when animation finishes naturally).
func (tae *TextAnimationEngine) cleanup(layerID int) {
	tae.mu.Lock()
	delete(tae.anims, layerID)
	tae.mu.Unlock()
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

// runFadeWord reveals words sequentially with an alpha ramp.
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

			// Determine which words are visible and their alpha
			img := image.NewRGBA(image.Rect(0, 0, renderW, renderH))
			allDone := true
			xOffset := 0

			for i, word := range words {
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

				// Render this word
				wordText := word
				if i > 0 {
					wordText = " " + word
				}
				wordW, wordH := tae.renderer.MeasureText(textrender.TextOptions{
					Text: wordText, FontSize: cfg.FontSize, Bold: cfg.Bold,
				})

				alphaU8 := uint8(alpha * 255)
				wordImg, _, err := tae.renderer.RenderText(wordW, wordH, textrender.TextOptions{
					Text:     wordText,
					FontSize: cfg.FontSize,
					Bold:     cfg.Bold,
					Color:    color.RGBA{R: 255, G: 255, B: 255, A: alphaU8},
				})
				if err != nil {
					continue
				}

				draw.Draw(img, image.Rect(xOffset, 0, xOffset+wordW, wordH), wordImg, image.Point{}, draw.Over)
				xOffset += wordW
			}

			_ = tae.compositor.SetOverlay(inst.layerID, img.Pix, renderW, renderH, "text-anim")

			if allDone {
				return
			}
		}
	}
}

// splitWordsForAnim splits text on spaces for word-by-word animation.
func splitWordsForAnim(text string) []string {
	var words []string
	word := ""
	for _, r := range text {
		if r == ' ' {
			if word != "" {
				words = append(words, word)
				word = ""
			}
		} else {
			word += string(r)
		}
	}
	if word != "" {
		words = append(words, word)
	}
	return words
}
