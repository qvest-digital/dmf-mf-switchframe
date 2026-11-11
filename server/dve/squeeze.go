package dve

import (
	"cmp"
	"errors"
	"fmt"
	"image"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// SqueezeConfig is a squeeze-back: where the program goes and what shows
// around it. DurationMs and Easing are how On and Off animate when a move
// names none.
type SqueezeConfig struct {
	Rect       Rect   `json:"rect"`
	Background string `json:"background,omitempty"` // source key; "" is black
	DurationMs int    `json:"durationMs,omitempty"`
	Easing     string `json:"easing,omitempty"`
}

// SqueezeMove is how one On or Off animates. Nil DurationMs and empty
// Easing take the configured ones; a DurationMs of 0 cuts.
type SqueezeMove struct {
	DurationMs *int   `json:"durationMs,omitempty"`
	Easing     string `json:"easing,omitempty"`
}

// SqueezeState is a squeeze-back as state reports it. On is the state an
// On or Off moves to; Animating is true until the move has arrived.
type SqueezeState struct {
	SqueezeConfig
	Configured bool `json:"configured"`
	On         bool `json:"on"`
	Animating  bool `json:"animating"`
}

// ErrSqueezeNotConfigured is returned by On before any Set.
var ErrSqueezeNotConfigured = errors.New("squeezeback is not configured")

// Squeeze shrinks the whole composited program into a rect over a
// background source, for L-frames. A DVE slot shrinks one source; this
// runs after the DVE compositor so upstream keys and PiPs shrink with the
// program, and before downstream graphics, which stay full frame.
type Squeeze struct {
	mu   sync.Mutex
	cfg  atomic.Pointer[SqueezeConfig] // nil until Set; read lock-free by NeedsSource
	on   bool
	anim *slotAnimation

	// active is on or animating; ProcessFrame returns at once without it.
	active atomic.Bool

	bg       squeezeFill
	bgSnap   []byte // background copy taken under mu for scaling outside it
	scaleBuf []byte // the scaled program; pipeline goroutine only

	frameW, frameH int

	now func() time.Time // test hook; nil means time.Now

	onArrive func() // under mu; see SetOnArrive
}

// squeezeFill is the latest background frame.
type squeezeFill struct {
	yuv    []byte
	w, h   int
	format video.Format
	valid  bool
}

// NewSqueeze returns a squeeze-back stage for frameW x frameH, off and
// unconfigured.
func NewSqueeze(frameW, frameH int) *Squeeze {
	return &Squeeze{frameW: frameW, frameH: frameH}
}

// SetOnArrive sets fn to be called, on the pipeline goroutine and outside
// the lock, when a move arrives: state stops reporting it as animating,
// and after an Off the background leaves the air and tally.
func (s *Squeeze) SetOnArrive(fn func()) {
	s.mu.Lock()
	s.onArrive = fn
	s.mu.Unlock()
}

// UpdateFormat sets the frame size the configured rect is in. On a size
// change the rect and a move in flight scale with the frame, so the
// L-frame keeps its shape and Set validates against the new size.
func (s *Squeeze) UpdateFormat(frameW, frameH int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldW, oldH := s.frameW, s.frameH
	s.frameW, s.frameH = frameW, frameH
	if oldW <= 0 || oldH <= 0 || (oldW == frameW && oldH == frameH) {
		return
	}
	scale := func(r image.Rectangle) image.Rectangle {
		return scaleRect(r, oldW, oldH, frameW, frameH)
	}
	if c := s.cfg.Load(); c != nil {
		cfg := *c
		r := scale(squeezeTarget(cfg.Rect))
		cfg.Rect = Rect{X: float64(r.Min.X), Y: float64(r.Min.Y), W: float64(r.Dx()), H: float64(r.Dy())}
		s.cfg.Store(&cfg)
	}
	if a := s.anim; a != nil {
		moved := *a
		moved.FromRect, moved.ToRect = scale(a.FromRect), scale(a.ToRect)
		s.anim = &moved
	}
}

// scaleRect maps r from a fromW x fromH frame to a toW x toH one, corners
// on even pixels.
func scaleRect(r image.Rectangle, fromW, fromH, toW, toH int) image.Rectangle {
	x := func(v int) int { return EvenAlign(v * toW / fromW) }
	y := func(v int) int { return EvenAlign(v * toH / fromH) }
	return image.Rect(x(r.Min.X), y(r.Min.Y), x(r.Max.X), y(r.Max.Y))
}

// Set replaces the configuration. While on, the program moves to the new
// rect over the new configuration's duration.
func (s *Squeeze) Set(cfg SqueezeConfig) error {
	if _, err := transition.ParseEasing(cfg.Easing); err != nil {
		return err
	}
	if err := validateSqueezeDuration(cfg.DurationMs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateSqueezeRect(cfg.Rect, s.frameW, s.frameH); err != nil {
		return err
	}
	from, _ := s.rectLocked()
	if old := s.cfg.Load(); old == nil || old.Background != cfg.Background {
		s.bg.valid = false
	}
	s.cfg.Store(&cfg)
	if s.on {
		ease, _ := transition.ParseEasing(cfg.Easing)
		s.startLocked(from, squeezeTarget(cfg.Rect), cfg.DurationMs, ease.Ease)
	}
	return nil
}

// validateSqueezeDuration caps a move as a layout morph is capped: an
// unbounded value overflows time.Duration and the move never arrives.
func validateSqueezeDuration(ms int) error {
	if ms < 0 || ms > MaxMorphDurationMs {
		return fmt.Errorf("durationMs must be between 0 and %d, got %d", MaxMorphDurationMs, ms)
	}
	return nil
}

// validateSqueezeRect requires an even, non-empty rect inside the frame:
// chroma is subsampled horizontally, and vertically in 4:2:0.
// Bounds are checked as floats before any int conversion: an out-of-range
// float converts to an arbitrary int that can pass every int check.
func validateSqueezeRect(r Rect, frameW, frameH int) error {
	for _, v := range [...]struct {
		name string
		v    float64
	}{{"x", r.X}, {"y", r.Y}, {"w", r.W}, {"h", r.H}} {
		if math.Abs(v.v) > math.MaxInt32 || v.v != math.Trunc(v.v) || int(v.v)%2 != 0 {
			return fmt.Errorf("rect %s must be an even whole number, got %v", v.name, v.v)
		}
	}
	if r.W <= 0 || r.H <= 0 {
		return fmt.Errorf("rect must have positive size, got %vx%v", r.W, r.H)
	}
	if r.X < 0 || r.Y < 0 || r.X+r.W > float64(frameW) || r.Y+r.H > float64(frameH) {
		return fmt.Errorf("rect %v,%v %vx%v is outside the %dx%d frame", r.X, r.Y, r.W, r.H, frameW, frameH)
	}
	return nil
}

func squeezeTarget(r Rect) image.Rectangle {
	return image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
}

// On squeezes the program into the configured rect, from wherever it is.
func (s *Squeeze) On(m SqueezeMove) error {
	if s.cfg.Load() == nil {
		return ErrSqueezeNotConfigured
	}
	return s.move(true, m)
}

// Off returns the program to full frame, from wherever it is.
func (s *Squeeze) Off(m SqueezeMove) error {
	return s.move(false, m)
}

func (s *Squeeze) move(on bool, m SqueezeMove) error {
	var cfg SqueezeConfig
	if c := s.cfg.Load(); c != nil {
		cfg = *c
	}
	durMs := cfg.DurationMs
	if m.DurationMs != nil {
		durMs = *m.DurationMs
	}
	if err := validateSqueezeDuration(durMs); err != nil {
		return err
	}
	ease, err := transition.ParseEasing(cmp.Or(m.Easing, cfg.Easing))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	from, _ := s.rectLocked()
	to := image.Rect(0, 0, s.frameW, s.frameH)
	if on {
		to = squeezeTarget(cfg.Rect)
	}
	s.on = on
	s.startLocked(from, to, durMs, ease.Ease)
	return nil
}

// startLocked animates from -> to, or cuts when durMs is 0 or there is no
// distance to cover. Must be called with s.mu held.
func (s *Squeeze) startLocked(from, to image.Rectangle, durMs int, ease func(float64) float64) {
	s.anim = nil
	if durMs > 0 && from != to {
		s.anim = &slotAnimation{
			StartTime: s.clock(),
			Duration:  time.Duration(durMs) * time.Millisecond,
			FromRect:  from,
			ToRect:    to,
			Easing:    ease,
			now:       s.now,
		}
	}
	s.active.Store(s.on || s.anim != nil)
}

// rectLocked returns the program rect now in frame coordinates, ending an
// animation that has arrived; arrived is true when that happened.
// Must be called with s.mu held.
func (s *Squeeze) rectLocked() (r image.Rectangle, arrived bool) {
	if a := s.anim; a != nil {
		// Decided on elapsed time, not eased progress: an overshooting
		// curve passes 1 before the end.
		if s.clock().Sub(a.StartTime) < a.Duration {
			return a.InterpolateRect(a.Progress()), false
		}
		s.anim = nil
		s.active.Store(s.on)
		arrived = true
		if !s.on {
			// Not fed while off: a frame kept now would be stale at
			// the next On.
			s.bg.valid = false
		}
	}
	if c := s.cfg.Load(); s.on && c != nil {
		return squeezeTarget(c.Rect), arrived
	}
	return image.Rect(0, 0, s.frameW, s.frameH), arrived
}

// Frame returns where the program goes in a width x height frame, and
// false when it fills the frame and nothing is to be drawn. It advances
// the animation, so the pipeline calls it once per frame.
func (s *Squeeze) Frame(width, height int) (image.Rectangle, bool) {
	if !s.active.Load() {
		return image.Rectangle{}, false
	}
	s.mu.Lock()
	r, arrived := s.rectLocked()
	fw, fh := s.frameW, s.frameH
	cb := s.onArrive
	s.mu.Unlock()
	if arrived && cb != nil {
		cb()
	}
	// A frame of another size than the format, as the GPU pipeline may
	// pass before the CPU one reconfigures, scales as UpdateFormat does.
	if fw > 0 && fh > 0 && (width != fw || height != fh) {
		r = scaleRect(r, fw, fh, width, height)
	}
	// An overshooting curve can leave the frame.
	r = r.Intersect(image.Rect(0, 0, width, height))
	r = image.Rect(EvenAlign(r.Min.X), EvenAlign(r.Min.Y), EvenAlign(r.Max.X), EvenAlign(r.Max.Y))
	if r.Empty() || r == image.Rect(0, 0, width, height) {
		return image.Rectangle{}, false
	}
	return r, true
}

// Active reports whether the program is squeezed or moving.
func (s *Squeeze) Active() bool { return s.active.Load() }

// BackgroundOnAir returns the background source while it shows, else "".
func (s *Squeeze) BackgroundOnAir() string {
	if !s.active.Load() {
		return ""
	}
	if c := s.cfg.Load(); c != nil {
		return c.Background
	}
	return ""
}

// Background returns the configured background source, "" for black.
func (s *Squeeze) Background() string {
	if c := s.cfg.Load(); c != nil {
		return c.Background
	}
	return ""
}

// NeedsSource reports whether sourceKey is the background and is shown,
// on or moving. A squeezeback configured but off costs no frame copy. An
// On starts at full frame, so the background is fed before it is seen;
// until its first frame, black shows.
func (s *Squeeze) NeedsSource(sourceKey string) bool {
	if !s.active.Load() {
		return false
	}
	c := s.cfg.Load()
	return c != nil && c.Background != "" && c.Background == sourceKey
}

// IngestSourceFrame keeps an 8-bit 4:2:0 background frame.
func (s *Squeeze) IngestSourceFrame(sourceKey string, yuv []byte, width, height int) {
	s.ingest(sourceKey, yuv, width, height, video.YUV420_8bit)
}

// IngestSourceFrame10 keeps a 10-bit 4:2:2 background frame.
func (s *Squeeze) IngestSourceFrame10(sourceKey string, yuv []byte, width, height int) {
	s.ingest(sourceKey, yuv, width, height, video.YUV422_10bit)
}

func (s *Squeeze) ingest(sourceKey string, yuv []byte, width, height int, f video.Format) {
	size := f.FrameSize(width, height)
	if len(yuv) < size || !s.NeedsSource(sourceKey) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cap(s.bg.yuv) < size {
		s.bg.yuv = make([]byte, size)
	}
	s.bg.yuv = s.bg.yuv[:size]
	copy(s.bg.yuv, yuv[:size])
	s.bg.w, s.bg.h, s.bg.format, s.bg.valid = width, height, f, true
}

// ProcessFrame squeezes an 8-bit 4:2:0 program frame in place.
func (s *Squeeze) ProcessFrame(yuv []byte, width, height int) []byte {
	return s.process(yuv, width, height, video.YUV420_8bit)
}

// ProcessFrame10 squeezes a 10-bit 4:2:2 program frame in place.
func (s *Squeeze) ProcessFrame10(yuv []byte, width, height int) []byte {
	return s.process(yuv, width, height, video.YUV422_10bit)
}

func (s *Squeeze) process(yuv []byte, width, height int, f video.Format) []byte {
	if len(yuv) < f.FrameSize(width, height) {
		return yuv
	}
	rect, ok := s.Frame(width, height)
	if !ok {
		return yuv
	}
	w, h := rect.Dx(), rect.Dy()
	s.scaleBuf = growBuf(s.scaleBuf, f.FrameSize(w, h))
	if err := scaleFrame(f, yuv, width, height, s.scaleBuf, w, h); err != nil {
		return yuv
	}
	s.drawBackground(yuv, width, height, f)
	if f == video.YUV422_10bit {
		composePIP10(yuv, width, height, s.scaleBuf, w, h, rect)
	} else {
		ComposePIPOpaque(yuv, width, height, s.scaleBuf, w, h, rect)
	}
	return yuv
}

// drawBackground fills dst with the background, scaled to fit, or black
// when there is none or it is in another format.
func (s *Squeeze) drawBackground(dst []byte, width, height int, f video.Format) {
	s.mu.Lock()
	bg := s.bg
	if !bg.valid || bg.format != f {
		s.mu.Unlock()
		video.FillBlack(dst, f, width, height)
		return
	}
	if bg.w == width && bg.h == height {
		copy(dst[:f.FrameSize(width, height)], bg.yuv)
		s.mu.Unlock()
		return
	}
	// Scaled outside the lock, so ingest does not wait on it.
	s.bgSnap = growBuf(s.bgSnap, len(bg.yuv))
	copy(s.bgSnap, bg.yuv)
	s.mu.Unlock()
	if err := scaleFrame(f, s.bgSnap, bg.w, bg.h, dst, width, height); err != nil {
		video.FillBlack(dst, f, width, height)
	}
}

// Snapshot returns the squeeze-back as state reports it.
func (s *Squeeze) Snapshot() SqueezeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := SqueezeState{On: s.on}
	if c := s.cfg.Load(); c != nil {
		st.SqueezeConfig = *c
		st.Configured = true
	}
	st.Animating = s.anim != nil && s.clock().Sub(s.anim.StartTime) < s.anim.Duration
	return st
}

func (s *Squeeze) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func growBuf(b []byte, n int) []byte {
	if cap(b) < n {
		return make([]byte, n)
	}
	return b[:n]
}

// scaleFrame scales bilinear, as DVE slots do.
func scaleFrame(f video.Format, src []byte, srcW, srcH int, dst []byte, dstW, dstH int) error {
	if f == video.YUV422_10bit {
		return transition.ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH)
	}
	transition.ScaleYUV420WithQuality(src, srcW, srcH, dst, dstW, dstH, transition.ScaleQualityFast)
	return nil
}

// composePIP10 is ComposePIPOpaque for 10-bit 4:2:2: two bytes a sample,
// chroma at half width and full height.
func composePIP10(dst []byte, dstW, dstH int, src []byte, srcW, srcH int, rect image.Rectangle) {
	copyW := min(srcW, dstW-rect.Min.X)
	copyH := min(srcH, dstH-rect.Min.Y)
	if copyW <= 0 || copyH <= 0 || rect.Min.X < 0 || rect.Min.Y < 0 {
		return
	}
	f := video.YUV422_10bit
	dstPlanes := [3]int{0, f.CbOffset(dstW, dstH), f.CrOffset(dstW, dstH)}
	srcPlanes := [3]int{0, f.CbOffset(srcW, srcH), f.CrOffset(srcW, srcH)}
	for p := range 3 {
		dw, sw, x, cw := dstW, srcW, rect.Min.X, copyW
		if p > 0 {
			dw, sw, x, cw = dstW/2, srcW/2, rect.Min.X/2, copyW/2
		}
		for y := range copyH {
			d := dstPlanes[p] + ((rect.Min.Y+y)*dw+x)*2
			o := srcPlanes[p] + y*sw*2
			copy(dst[d:d+cw*2], src[o:o+cw*2])
		}
	}
}
