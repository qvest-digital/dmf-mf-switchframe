// Package textrender provides server-side text rendering into RGBA byte slices.
// It uses the Inter font (embedded via go:embed) and golang.org/x/image/font/opentype.
// Output flows through the existing graphics compositor SetOverlay pipeline.
package textrender

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/Inter-Regular.ttf
var interRegularData []byte

//go:embed fonts/Inter-Bold.ttf
var interBoldData []byte

// TextOptions describes what and how to render.
type TextOptions struct {
	Text     string
	FontSize float64
	Bold     bool
	Color    color.RGBA // default: white
	MaxWidth int        // 0 = no wrapping
}

// Renderer rasterizes text into RGBA images using embedded Inter fonts.
type Renderer struct {
	regularFont *opentype.Font
	boldFont    *opentype.Font

	// Font face cache: size -> face (regular and bold cached separately).
	mu           sync.Mutex
	regularFaces map[float64]font.Face
	boldFaces    map[float64]font.Face
}

// NewRenderer parses the embedded Inter fonts and returns a ready-to-use renderer.
func NewRenderer() (*Renderer, error) {
	regular, err := opentype.Parse(interRegularData)
	if err != nil {
		return nil, fmt.Errorf("parse Inter-Regular: %w", err)
	}
	bold, err := opentype.Parse(interBoldData)
	if err != nil {
		return nil, fmt.Errorf("parse Inter-Bold: %w", err)
	}
	return &Renderer{
		regularFont:  regular,
		boldFont:     bold,
		regularFaces: make(map[float64]font.Face),
		boldFaces:    make(map[float64]font.Face),
	}, nil
}

// face returns a cached font.Face for the given size and weight.
func (r *Renderer) face(size float64, bold bool) (font.Face, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cache := r.regularFaces
	f := r.regularFont
	if bold {
		cache = r.boldFaces
		f = r.boldFont
	}

	if face, ok := cache[size]; ok {
		return face, nil
	}

	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	cache[size] = face
	return face, nil
}

// MeasureText returns the pixel width and height needed to render the text.
// If opts.MaxWidth > 0, text is word-wrapped and the height reflects all lines.
func (r *Renderer) MeasureText(opts TextOptions) (width, height int) {
	if opts.FontSize <= 0 {
		opts.FontSize = 24
	}

	face, err := r.face(opts.FontSize, opts.Bold)
	if err != nil {
		return 0, 0
	}

	metrics := face.Metrics()
	lineHeight := metrics.Height.Ceil()

	if opts.Text == "" {
		return 0, lineHeight
	}

	if opts.MaxWidth <= 0 {
		// Single line
		adv := font.MeasureString(face, opts.Text)
		return adv.Ceil(), lineHeight
	}

	// Word-wrap
	lines := wordWrap(face, opts.Text, opts.MaxWidth)
	maxW := 0
	for _, line := range lines {
		w := font.MeasureString(face, line).Ceil()
		if w > maxW {
			maxW = w
		}
	}
	return maxW, lineHeight * len(lines)
}

// RenderToRGBA renders text into a new RGBA image of exactly the needed size.
// Returns the image and its raw RGBA pixel bytes.
func (r *Renderer) RenderToRGBA(opts TextOptions) (*image.RGBA, []byte, error) {
	w, h := r.MeasureText(opts)
	if w == 0 {
		w = 1
	}
	return r.RenderText(w, h, opts)
}

// RenderText renders text into a new RGBA image of the given dimensions.
// Text is drawn starting from the top-left. Returns the image and raw RGBA bytes.
func (r *Renderer) RenderText(width, height int, opts TextOptions) (*image.RGBA, []byte, error) {
	if width <= 0 || height <= 0 {
		return nil, nil, fmt.Errorf("invalid dimensions: %dx%d", width, height)
	}

	face, err := r.face(opts.FontSize, opts.Bold)
	if err != nil {
		return nil, nil, err
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Text color (default white)
	col := opts.Color
	if col.A == 0 && col.R == 0 && col.G == 0 && col.B == 0 {
		col = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: face,
	}

	metrics := face.Metrics()
	ascent := metrics.Ascent.Ceil()
	lineHeight := metrics.Height.Ceil()

	var lines []string
	if opts.MaxWidth > 0 {
		lines = wordWrap(face, opts.Text, opts.MaxWidth)
	} else {
		lines = []string{opts.Text}
	}

	for i, line := range lines {
		d.Dot = fixed.Point26_6{
			X: fixed.I(0),
			Y: fixed.I(ascent + i*lineHeight),
		}
		d.DrawString(line)
	}

	return img, img.Pix, nil
}

// wordWrap splits text into lines that fit within maxWidth pixels.
func wordWrap(face font.Face, text string, maxWidth int) []string {
	if text == "" {
		return nil
	}

	words := splitWords(text)
	if len(words) == 0 {
		return nil
	}

	maxW := fixed.I(maxWidth)
	var lines []string
	currentLine := words[0]

	for _, word := range words[1:] {
		testLine := currentLine + " " + word
		if font.MeasureString(face, testLine) <= maxW {
			currentLine = testLine
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}
	lines = append(lines, currentLine)
	return lines
}

// splitWords splits text on whitespace boundaries.
func splitWords(text string) []string {
	var words []string
	word := ""
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' {
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
