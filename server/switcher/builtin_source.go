package switcher

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/graphics/textrender"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// BuiltinFrameTap is an external observer for built-in source frames. The
// app layer installs this to route raw YUV into a per-source MoQ-published
// preview encoder so the browser multiview tile has content. Tap runs on
// the producer goroutine — callers must not block.
type BuiltinFrameTap func(key string, yuv []byte, width, height int, pts int64, format video.Format)

// Canonical keys for the always-present virtual sources every engine exposes.
// Operators can CUT or PREVIEW to these without any external feed configured.
const (
	BuiltinSourceKeyBlack     = "source:black"
	BuiltinSourceKeyColorbars = "source:colorbars"
)

// BuiltinSourceCount is the number of virtual sources Switcher.New
// auto-registers. Exposed so downstream tests across packages can
// `len(userSources) + switcher.BuiltinSourceCount` without drifting when
// the set grows.
const BuiltinSourceCount = 2

// ErrBuiltinSourceUnknown is returned from BuiltinSourceFrame for a key
// that isn't a registered built-in virtual source.
var ErrBuiltinSourceUnknown = errors.New("switcher: unknown built-in source")

// builtinSource is an always-available reference-signal generator. The bars
// pattern is cached per (width, height, color format); the annex band (logo
// / clock / sprite) redraws every frame so operators visually confirm signal
// motion even when the pattern itself is a nominally static reference.
type builtinSource struct {
	key      string
	renderer *textrender.Renderer // shared across all built-ins; nil-safe

	mu         sync.Mutex
	width      int
	height     int
	format     video.Format // color format the static buffer was built for
	static     []byte       // cached YUV pattern at (width,height,format) before annex overlay
	annexY0    int          // y where annex band starts (height if annex disabled)
	frameIndex int64        // monotonic counter incremented each Frame() call
}

// Frame returns a YUV frame at (width, height) in the requested pixel format
// for this built-in source. Each call allocates and returns a fresh slice
// so the caller owns the buffer for its full lifetime — callers may hand
// the slice to downstream consumers (frame sync ring, pipeline work chan)
// without an additional copy. The cached static pattern is regenerated if
// any of (width, height, format) differ from the previous call; the annex
// (sprite + clock) is redrawn per call.
//
// Sprite motion is driven by an internal monotonic frame counter that
// advances one step per call — ties motion to the number of Frame() calls
// rather than wall clock, so consecutive rendered frames cannot regress
// the sprite under any pipeline scheduling pattern (frame-sync phase-lock,
// GPU cache lookup, FRC interpolation, or encoder reorder). The clock
// still reads wall time so it progresses with real-world time.
func (b *builtinSource) Frame(width, height int, f video.Format) []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if width <= 0 || height <= 0 {
		return nil
	}
	// Even dimensions required for YUV chroma alignment.
	width &^= 1
	height &^= 1
	if width != b.width || height != b.height || f != b.format || b.static == nil {
		b.regenerateStaticLocked(width, height, f)
		b.width = width
		b.height = height
		b.format = f
	}
	out := make([]byte, f.FrameSize(width, height))
	copy(out, b.static)
	if b.annexY0 < height {
		drawAnnexClock(out, f, width, height, b.annexY0, b.renderer, time.Now().UTC())
		drawAnnexSprite(out, f, width, height, b.annexY0, b.frameIndex)
		drawAnnexFrameNumber(out, f, width, height, b.annexY0, b.renderer, b.frameIndex)
	}
	b.frameIndex++
	return out
}

func (b *builtinSource) regenerateStaticLocked(width, height int, f video.Format) {
	size := f.FrameSize(width, height)
	if cap(b.static) < size {
		b.static = make([]byte, size)
	} else {
		b.static = b.static[:size]
	}

	switch b.key {
	case BuiltinSourceKeyBlack:
		video.FillBlack(b.static, f, width, height)
		b.annexY0 = height // no annex for the pure-black fallback source
		return

	case BuiltinSourceKeyColorbars:
		// Annex occupies the bottom ~20% of the frame. Chroma subsampling
		// in YUV420 requires the boundary to be on an even row; use even
		// alignment for 4:2:2 too (harmless; keeps both paths simple).
		annex := (height * 4) / 5
		annex &^= 1
		if annex < 0 || annex > height {
			annex = height
		}
		b.annexY0 = annex
		drawSMPTE75Bars(b.static, f, width, height, annex)
		fillAnnexBlack(b.static, f, width, height, annex)
		bakeLogo(b.static, f, width, height, annex, b.renderer)
		return
	}
}

// BuiltinSourceFrame returns a frame from the named built-in source at the
// current pipeline format (dimensions + color format). Returns
// ErrBuiltinSourceUnknown for non-built-in keys. Frame regenerates on format
// change; professional (YUV422 10-bit) and standard (YUV420 8-bit) modes
// are both supported.
func (s *Switcher) BuiltinSourceFrame(key string) ([]byte, int, int, error) {
	bs, ok := s.builtinSources[key]
	if !ok {
		return nil, 0, 0, fmt.Errorf("%w: %q", ErrBuiltinSourceUnknown, key)
	}
	pf := s.PipelineFormat()
	yuv := bs.Frame(pf.Width, pf.Height, pf.Color.ColorFormat)
	return yuv, pf.Width & ^1, pf.Height & ^1, nil
}

// registerBuiltinSourcesLocked wires source:black and source:colorbars into
// the switcher's source map with isVirtual=true. Called from Switcher.New
// under s.configMu.Lock; no broadcast here — New's caller is responsible. The
// builtinSources map is initialized once and never mutated afterward, so
// subsequent lock-free reads (BuiltinSourceFrame, builtinProducerLoop) are
// safe.
func (s *Switcher) registerBuiltinSourcesLocked() {
	renderer, err := textrender.NewRenderer()
	if err != nil {
		// Text rendering failure is non-fatal: we can still render bars
		// and black, just without the logo/clock. Log and continue.
		slog.Warn("built-in source text renderer unavailable; logo/clock disabled",
			"error", err)
		renderer = nil
	}
	keys := []string{BuiltinSourceKeyBlack, BuiltinSourceKeyColorbars}
	s.builtinSources = make(map[string]*builtinSource, len(keys))
	for _, key := range keys {
		k := key
		s.builtinSources[k] = &builtinSource{
			key:      k,
			renderer: renderer,
		}
		s.withSourcesMut(func(m map[string]*sourceState) {
			ss := &sourceState{
				key:       k,
				label:     builtinLabel(k),
				position:  len(m) + 1,
				isVirtual: true,
			}
			ss.initAtomicMirrors()
			m[k] = ss
		})
		s.health.registerSource(k)
	}
}

// isBuiltinSource reports whether key is one of the always-available virtual
// sources. buildStateLocked uses this to overwrite the per-source health
// status with SourceHealthy — built-ins are reference sources backed by an
// internal generator, not an ingest feed that can go stale.
func isBuiltinSource(key string) bool {
	return key == BuiltinSourceKeyBlack || key == BuiltinSourceKeyColorbars
}

// invalidateBuiltinSources forces every built-in to regenerate its static
// layer on the next Frame call. Called from SetPipelineFormat after the
// new format is stored. Caller holds s.configMu.
func (s *Switcher) invalidateBuiltinSources() {
	for _, bs := range s.builtinSources {
		bs.mu.Lock()
		bs.static = nil
		bs.width = 0
		bs.height = 0
		bs.mu.Unlock()
	}
}

// SetBuiltinFrameTap installs a callback that fires for every built-in
// frame the producer generates. Pass nil to remove the tap. Safe to call
// from any goroutine. The app layer uses this to feed per-source MoQ
// preview encoders so browser multiview tiles render thumbnails for
// BLACK / BARS. The tap runs on the producer goroutine; handlers must
// not block.
func (s *Switcher) SetBuiltinFrameTap(fn BuiltinFrameTap) {
	if fn == nil {
		s.builtinFrameTap.Store(nil)
		return
	}
	s.builtinFrameTap.Store(&fn)
}

// startBuiltinProducer spawns the goroutine that pushes built-in frames at
// the pipeline rate. One-shot — do not call twice on the same switcher.
func (s *Switcher) startBuiltinProducer() {
	s.builtinProducerStop = make(chan struct{})
	s.builtinProducerDone = make(chan struct{})
	go s.builtinProducerLoop()
}

// stopBuiltinProducer signals the producer loop to exit and waits for it.
// Safe to call twice: the select drains an already-closed stop channel via
// the receive arm and skips the close. Relied on by Switcher.Close's
// sync.Once wrap so the same-Switcher double-close path is race-clean, and
// by tests that explicitly stop the producer mid-test before the Cleanup
// Close fires.
func (s *Switcher) stopBuiltinProducer() {
	if s.builtinProducerStop == nil {
		return
	}
	select {
	case <-s.builtinProducerStop:
	default:
		close(s.builtinProducerStop)
	}
	<-s.builtinProducerDone
}

func (s *Switcher) builtinProducerLoop() {
	defer close(s.builtinProducerDone)

	// Initial tick at the current pipeline format's FrameDuration. Only
	// Reset the ticker if the format changes mid-loop, to avoid cancelling
	// in-flight ticks and the attendant coalescing behavior every tick.
	lastDur := s.PipelineFormat().FrameDuration()
	if lastDur <= 0 {
		lastDur = 33333 * time.Microsecond
	}
	tick := time.NewTicker(lastDur)
	defer tick.Stop()

	// PTS is frame-count-anchored at the pipeline FPS — exactly CFR, no
	// scheduling jitter. The encoder and downstream frame-sync + GPU cache
	// paths are sensitive to PTS irregularities (the program path applies
	// motion-compensated interpolation when configured, a phase-lock
	// early-release signal, and GPU frame caching keyed by PTS), any of
	// which can turn tiny PTS variance into visible sprite oscillation on
	// the program output. Frame count also drives the sprite position so
	// motion advances by exact integer increments per frame — under any
	// pipeline scheduling pattern, consecutive rendered frames cannot
	// regress the sprite.
	//
	// Push frames unconditionally at pipeline FPS, independent of whether
	// a built-in is currently on program or preview. The multiview tiles
	// in the UI subscribe to each source's per-source preview MoQ track,
	// and those tracks need a frame stream or the operator sees a blank
	// thumbnail for BLACK/BARS and can't confirm what they're about to
	// cut to. CPU cost at 1080p30 for both built-ins is ~20% of one core —
	// on par with a single real ingest source — and matches how every
	// broadcast mixer (ATEM, vMix, TriCaster) runs its color generators.
	var frameIndex int64
	for {
		select {
		case <-s.builtinProducerStop:
			return
		case <-tick.C:
		}

		pf := s.PipelineFormat()
		if d := pf.FrameDuration(); d > 0 && d != lastDur {
			tick.Reset(d)
			lastDur = d
		}

		// ptsPerFrame = 90000 * fpsDen / fpsNum. For 30fps: 3000. For
		// 59.94fps (30000/1001): 1501 with truncation — close enough
		// that downstream handles it without drift visible on a
		// multi-hour run.
		var ptsPerFrame int64 = 3000
		if pf.FPSNum > 0 {
			ptsPerFrame = int64(90000) * int64(pf.FPSDen) / int64(pf.FPSNum)
		}
		pts := frameIndex * ptsPerFrame

		var tap BuiltinFrameTap
		if p := s.builtinFrameTap.Load(); p != nil {
			tap = *p
		}
		for key, bs := range s.builtinSources {
			frame := bs.Frame(pf.Width, pf.Height, pf.Color.ColorFormat)
			if frame == nil {
				continue
			}
			w := pf.Width & ^1
			h := pf.Height & ^1
			pframe := &ProcessingFrame{
				Data:   frame,
				Format: pf.Color.ColorFormat,
				Width:  w,
				Height: h,
				PTS:    pts,
				DTS:    pts,
			}
			s.IngestRawVideo(key, pframe)
			if tap != nil {
				// Tap sees the same bytes Frame() returned. Frame()
				// allocates a fresh slice per call, so tap consumers
				// that retain the buffer are safe against the next
				// tick's overwrite.
				tap(key, frame, w, h, pts, pf.Color.ColorFormat)
			}
		}
		frameIndex++
	}
}

func builtinLabel(key string) string {
	switch key {
	case BuiltinSourceKeyBlack:
		return "BLACK"
	case BuiltinSourceKeyColorbars:
		return "BARS"
	}
	return key
}

// ---- YUV fills (format-agnostic) ---------------------------------------

// fillYUVRect paints a rectangular region with a uniform YCbCr triple. For
// 10-bit formats, callers pass pre-scaled values (e.g. Y=180<<2=720). The
// rectangle is clamped to the frame and quantized to even coordinates so
// chroma subsampling stays consistent across 4:2:0 and 4:2:2.
func fillYUVRect(buf []byte, f video.Format, width, height, x0, y0, x1, y1 int, y, cb, cr uint16) {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > width {
		x1 = width
	}
	if y1 > height {
		y1 = height
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	x0 &^= 1
	y0 &^= 1
	x1 &^= 1
	y1 &^= 1

	yOff := 0
	cbOff := f.CbOffset(width, height)
	crOff := f.CrOffset(width, height)
	chromaW := f.ChromaWidth(width)  // w/2 for both 420 and 422
	chromaStep := 2                  // 4:2:0: chroma row every 2 luma rows
	if f == video.YUV422_10bit {
		chromaStep = 1 // 4:2:2: chroma row == luma row
	}

	if f == video.YUV422_10bit {
		// 10-bit LE uint16 samples; Y plane stride in bytes = width*2.
		for row := y0; row < y1; row++ {
			rowOff := yOff + row*width*2
			for col := x0; col < x1; col++ {
				binary.LittleEndian.PutUint16(buf[rowOff+col*2:], y)
			}
		}
		for row := y0; row < y1; row += chromaStep {
			chromaRow := row
			rowCbOff := cbOff + chromaRow*chromaW*2
			rowCrOff := crOff + chromaRow*chromaW*2
			for col := x0; col < x1; col += 2 {
				uvCol := col / 2
				binary.LittleEndian.PutUint16(buf[rowCbOff+uvCol*2:], cb)
				binary.LittleEndian.PutUint16(buf[rowCrOff+uvCol*2:], cr)
			}
		}
		return
	}

	// 8-bit 4:2:0.
	y8, cb8, cr8 := byte(y), byte(cb), byte(cr)
	for row := y0; row < y1; row++ {
		off := yOff + row*width
		for col := x0; col < x1; col++ {
			buf[off+col] = y8
		}
	}
	for row := y0; row < y1; row += chromaStep {
		chromaRow := row / 2
		rowCbOff := cbOff + chromaRow*chromaW
		rowCrOff := crOff + chromaRow*chromaW
		for col := x0; col < x1; col += 2 {
			uvCol := col / 2
			buf[rowCbOff+uvCol] = cb8
			buf[rowCrOff+uvCol] = cr8
		}
	}
}

// drawSMPTE75Bars fills rows [0, barsBottom) with 8 vertical 75% SMPTE RP 219
// bars: gray / yellow / cyan / green / magenta / red / blue / black. Column
// widths round to even for chroma alignment; any width not divisible by 8
// leaves a few extra pixels on the last bar.
//
// NOTE on interpretation: we treat "75% bars" as 75% of the RGB primary
// (R=G=B=191), then run through BT.709 limited-range conversion. For 10-bit
// output, the 8-bit YCbCr is left-shifted by 2 — the canonical 8-bit legal
// range [16,235] maps cleanly to 10-bit [64,940].
func drawSMPTE75Bars(buf []byte, f video.Format, width, height, barsBottom int) {
	if barsBottom <= 0 {
		return
	}
	const v = 191 // 75% × 255, rounded
	rgbs := [8][3]uint8{
		{v, v, v}, // gray
		{v, v, 0}, // yellow
		{0, v, v}, // cyan
		{0, v, 0}, // green
		{v, 0, v}, // magenta
		{v, 0, 0}, // red
		{0, 0, v}, // blue
		{0, 0, 0}, // black
	}
	barW := width / 8
	barW &^= 1
	if barW < 2 {
		barW = 2
	}
	for i := 0; i < 8; i++ {
		x0 := i * barW
		x1 := x0 + barW
		if i == 7 {
			x1 = width // final bar sweeps to the right edge
		}
		y8, cb8, cr8 := transition.RGBToYUV_BT709Limited(rgbs[i][0], rgbs[i][1], rgbs[i][2])
		y, cb, cr := scaleYUVToFormat(f, y8, cb8, cr8)
		fillYUVRect(buf, f, width, height, x0, 0, x1, barsBottom, y, cb, cr)
	}
}

// scaleYUVToFormat scales 8-bit BT.709 legal-range YCbCr to the requested
// pipeline format. For 10-bit output, left-shift by 2 maps [16,235]→[64,940]
// and [16,240]→[64,960] with no quantization change beyond the implicit
// 2-bit zero-fill at the LSB.
func scaleYUVToFormat(f video.Format, y8, cb8, cr8 uint8) (y, cb, cr uint16) {
	if f == video.YUV422_10bit {
		return uint16(y8) << 2, uint16(cb8) << 2, uint16(cr8) << 2
	}
	return uint16(y8), uint16(cb8), uint16(cr8)
}

// fillAnnexBlack paints the annex band with legal-range black so sprite /
// logo / clock stand out against a neutral backdrop.
func fillAnnexBlack(buf []byte, f video.Format, width, height, annexY0 int) {
	if annexY0 >= height {
		return
	}
	y := uint16(f.BlackY())
	c := uint16(f.NeutralChroma())
	fillYUVRect(buf, f, width, height, 0, annexY0, width, height, y, c, c)
}

// bakeLogo renders SWITCHFRAME into the left side of the annex band, once
// per format. Renderer may be nil on systems where the embedded Inter font
// fails to parse; in that case the annex just shows clock + sprite.
func bakeLogo(buf []byte, f video.Format, width, height, annexY0 int, r *textrender.Renderer) {
	if r == nil || annexY0 >= height {
		return
	}
	annexH := height - annexY0
	fontSize := float64(annexH) * 0.55
	if fontSize < 14 {
		fontSize = 14
	}
	opts := textrender.TextOptions{
		Text:     "SWITCHFRAME",
		FontSize: fontSize,
		Bold:     true,
		Color:    color.RGBA{R: 230, G: 230, B: 230, A: 255},
	}
	w, h := r.MeasureText(opts)
	if w <= 0 || h <= 0 {
		return
	}
	img, _, err := r.RenderText(w, h, opts)
	if err != nil {
		slog.Warn("builtin source: logo render failed", "error", err)
		return
	}
	margin := annexH / 4
	blitRGBAToYUV(buf, f, width, height, margin, annexY0+(annexH-h)/2, img)
}

// drawAnnexClock burns a UTC HH:MM:SS digital clock on the right side of
// the annex. Drawn every frame; the text string changes once per second so
// the rendered glyph image is stable for ~30 successive frames at 30fps.
func drawAnnexClock(buf []byte, f video.Format, width, height, annexY0 int, r *textrender.Renderer, now time.Time) {
	if r == nil || annexY0 >= height {
		return
	}
	annexH := height - annexY0
	fontSize := float64(annexH) * 0.55
	if fontSize < 14 {
		fontSize = 14
	}
	opts := textrender.TextOptions{
		Text:      now.Format("15:04:05 UTC"),
		FontSize:  fontSize,
		Bold:      true,
		Monospace: true, // fixed-width digits so the clock text doesn't shift each second
		Color:     color.RGBA{R: 255, G: 255, B: 255, A: 255},
	}
	w, h := r.MeasureText(opts)
	if w <= 0 || h <= 0 {
		return
	}
	img, _, err := r.RenderText(w, h, opts)
	if err != nil {
		return
	}
	margin := annexH / 4
	x := width - margin - w
	y := annexY0 + (annexH-h)/2
	blitRGBAToYUV(buf, f, width, height, x, y, img)
}

// drawAnnexFrameNumber burns the source-side monotonic frame counter into
// the top-left corner of the frame (above the bars region, outside the
// annex band, so it can't overlap the logo/clock layout). Intended as a
// diagnostic: a decoder that reorders frames, repeats old content, or
// races with newer content will show a non-monotonic number sequence on
// playback; strictly increasing integers rule reorder in or out.
//
// Drawn on top of the leftmost gray bar (that region tolerates white
// text overlay since gray is already near-white luma). Small font with a
// black letterbox drawn behind it for legibility.
func drawAnnexFrameNumber(buf []byte, f video.Format, width, height, annexY0 int, r *textrender.Renderer, frameIndex int64) {
	if r == nil {
		return
	}
	// Place in the top-left of the bars region (NOT in the annex band).
	// That keeps it off the logo + clock layout.
	fontSize := float64(height) * 0.04
	if fontSize < 14 {
		fontSize = 14
	}
	opts := textrender.TextOptions{
		Text:      fmt.Sprintf("F:%07d", frameIndex),
		FontSize:  fontSize,
		Bold:      true,
		Monospace: true,
		Color:     color.RGBA{R: 255, G: 255, B: 255, A: 255},
	}
	w, h := r.MeasureText(opts)
	if w <= 0 || h <= 0 {
		return
	}
	img, _, err := r.RenderText(w, h, opts)
	if err != nil {
		return
	}
	pad := 8
	x := pad
	x &^= 1
	y := pad
	y &^= 1
	// Black rectangle behind the text for legibility against the bars.
	fillYUVRect(buf, f, width, height, x-pad/2, y-pad/2, x+w+pad/2, y+h+pad/2,
		uint16(f.BlackY()), uint16(f.NeutralChroma()), uint16(f.NeutralChroma()))
	blitRGBAToYUV(buf, f, width, height, x, y, img)
}

// spriteCyclePeriodFrames is the number of frames per full sprite sweep
// across the annex. Motion is driven by (frameIndex % period) so the
// position at frame N is strictly determined by N — no wall-clock read,
// no jitter-sensitive derivation. At 30fps, 60 frames = a 2-second sweep.
const spriteCyclePeriodFrames int64 = 60

// drawAnnexSprite draws a short horizontal colored bar in the middle of the
// annex that moves left-to-right on a fixed-frame-count cycle, proving to
// a downstream operator that the signal is live even when the reference
// pattern itself is static.
//
// The sprite's travel range runs from fully off-screen on the left
// (x=-spriteW) to fully off-screen on the right (x=width). This makes
// the wrap a natural "exit-then-reenter" rather than a right-to-left
// teleport — the sprite is off-screen for one frame's worth of travel
// before reappearing on the left, so the viewer sees continuous forward
// motion with no apparent backward jump at cycle boundaries.
//
// Position is a pure function of frameIndex: at frame N the sprite is at
// x = -spriteW + (N mod period) * travel / period. Exact integer math,
// strictly monotonic within a cycle, cannot regress between frames under
// any pipeline scheduling pattern.
func drawAnnexSprite(buf []byte, f video.Format, width, height, annexY0 int, frameIndex int64) {
	if annexY0 >= height {
		return
	}
	annexH := height - annexY0
	spriteH := annexH / 3
	if spriteH < 2 {
		spriteH = 2
	}
	spriteH &^= 1
	spriteW := width / 20
	if spriteW < 16 {
		spriteW = 16
	}
	spriteW &^= 1

	trackY0 := annexY0 + (annexH-spriteH)/2
	trackY0 &^= 1
	trackY1 := trackY0 + spriteH

	// Travel range goes from fully off-screen left to fully off-screen
	// right, so the sprite enters the visible region from the left, exits
	// on the right, and wraps cleanly with no on-screen teleport.
	travelRange := int64(width + spriteW)
	phase := frameIndex % spriteCyclePeriodFrames
	if phase < 0 {
		phase += spriteCyclePeriodFrames
	}
	x0 := -spriteW + int(phase*travelRange/spriteCyclePeriodFrames)
	x0 &^= 1
	x1 := x0 + spriteW

	// 75% yellow sprite — bright against the black annex, matches one of
	// the reference bars.
	const v = 191
	y8, cb8, cr8 := transition.RGBToYUV_BT709Limited(v, v, 0)
	y, cb, cr := scaleYUVToFormat(f, y8, cb8, cr8)
	fillYUVRect(buf, f, width, height, x0, trackY0, x1, trackY1, y, cb, cr)
}

// blitRGBAToYUV rasterizes an RGBA image into a YUV buffer using BT.709
// legal-range conversion, respecting alpha. Pixels with alpha below 8 are
// skipped (preserves the annex background for sub-pixel anti-aliased glyph
// edges). Supports both YUV420 8-bit and YUV422 10-bit destinations.
func blitRGBAToYUV(buf []byte, f video.Format, width, height, dstX, dstY int, src *image.RGBA) {
	if src == nil {
		return
	}
	sr := src.Bounds()
	cbOff := f.CbOffset(width, height)
	crOff := f.CrOffset(width, height)
	chromaW := f.ChromaWidth(width)
	tenBit := f == video.YUV422_10bit

	for row := 0; row < sr.Dy(); row++ {
		dstRow := dstY + row
		if dstRow < 0 || dstRow >= height {
			continue
		}
		srcLine := src.Pix[row*src.Stride:]
		for col := 0; col < sr.Dx(); col++ {
			dstCol := dstX + col
			if dstCol < 0 || dstCol >= width {
				continue
			}
			r := srcLine[col*4+0]
			g := srcLine[col*4+1]
			b := srcLine[col*4+2]
			a := srcLine[col*4+3]
			if a < 8 {
				continue
			}
			y8, cb8, cr8 := transition.RGBToYUV_BT709Limited(r, g, b)

			if tenBit {
				yVal := uint16(y8) << 2
				cbVal := uint16(cb8) << 2
				crVal := uint16(cr8) << 2
				binary.LittleEndian.PutUint16(buf[(dstRow*width+dstCol)*2:], yVal)
				// 4:2:2 — chroma row tracks luma row exactly.
				uvRow := dstRow
				uvCol := dstCol / 2
				binary.LittleEndian.PutUint16(buf[cbOff+(uvRow*chromaW+uvCol)*2:], cbVal)
				binary.LittleEndian.PutUint16(buf[crOff+(uvRow*chromaW+uvCol)*2:], crVal)
				continue
			}

			// 8-bit 4:2:0.
			buf[dstRow*width+dstCol] = y8
			uvRow := dstRow / 2
			uvCol := dstCol / 2
			buf[cbOff+uvRow*chromaW+uvCol] = cb8
			buf[crOff+uvRow*chromaW+uvCol] = cr8
		}
	}
}
