package graphics

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Errors returned by Compositor methods.
var (
	ErrAlreadyActive = errors.New("graphics: overlay already active")
	ErrNotActive     = errors.New("graphics: overlay not active")
	ErrNoOverlay     = errors.New("graphics: no overlay frame uploaded")
	ErrFadeActive    = errors.New("graphics: fade transition in progress")
)

// State represents the current graphics overlay state.
type State struct {
	Active       bool    `json:"active"`
	Template     string  `json:"template,omitempty"`
	FadePosition float64 `json:"fadePosition,omitempty"` // 0.0 = invisible, 1.0 = fully visible
	ProgramWidth  int    `json:"programWidth,omitempty"`
	ProgramHeight int    `json:"programHeight,omitempty"`
}

// Compositor manages the downstream keyer (DSK) graphics overlay state.
// It stores the RGBA overlay data uploaded from the browser and tracks
// the active/fade state. When active, program frames can be composited
// with the overlay using the AlphaBlendRGBA function.
//
// The compositor is safe for concurrent use from multiple goroutines.
type Compositor struct {
	log *slog.Logger
	mu  sync.RWMutex

	// Overlay RGBA pixel data (width * height * 4 bytes).
	overlay       []byte
	overlayWidth  int
	overlayHeight int
	template      string

	// Active state and fade.
	active       bool
	fadePosition float64 // 0.0 = invisible, 1.0 = fully visible
	fadeDone     chan struct{}
	fadeCancel   chan struct{}
	closed       bool

	// Callback invoked on state change (active/inactive/fade).
	// Receives a snapshot of the current state so callers don't need
	// to call Status() (which would deadlock under the compositor's lock).
	onStateChange func(State)

	// Returns program video resolution. Set via SetResolutionProvider.
	resolutionProvider func() (width, height int)
}

// NewCompositor creates a new graphics overlay compositor.
func NewCompositor() *Compositor {
	return &Compositor{
		log: slog.With("component", "graphics"),
	}
}

// SetOverlay stores the RGBA overlay frame data. The overlay must be
// the given width and height (width*height*4 bytes). This can be called
// while the overlay is active to update the graphics in real-time.
func (c *Compositor) SetOverlay(rgba []byte, width, height int, template string) error {
	expected := width * height * 4
	if len(rgba) != expected {
		return fmt.Errorf("rgba data size mismatch: got %d bytes, want %d (%dx%dx4)", len(rgba), expected, width, height)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Copy the RGBA data to avoid retaining the caller's buffer.
	if len(c.overlay) != expected {
		c.overlay = make([]byte, expected)
	}
	copy(c.overlay, rgba)
	c.overlayWidth = width
	c.overlayHeight = height
	c.template = template
	return nil
}

// On activates the overlay immediately (CUT ON).
func (c *Compositor) On() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("compositor closed")
	}
	if c.overlay == nil {
		return ErrNoOverlay
	}

	// Cancel any in-progress fade.
	c.cancelFadeLocked()

	c.active = true
	c.fadePosition = 1.0
	c.log.Debug("overlay CUT ON")
	c.notifyStateChange()
	return nil
}

// Off deactivates the overlay immediately (CUT OFF).
func (c *Compositor) Off() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("compositor closed")
	}

	// Cancel any in-progress fade.
	c.cancelFadeLocked()

	c.active = false
	c.fadePosition = 0.0
	c.log.Debug("overlay CUT OFF")
	c.notifyStateChange()
	return nil
}

// AutoOn starts a fade-in transition (AUTO ON) over the given duration.
func (c *Compositor) AutoOn(duration time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("compositor closed")
	}
	if c.overlay == nil {
		return ErrNoOverlay
	}
	if c.fadeDone != nil {
		return ErrFadeActive
	}

	c.active = true
	c.fadePosition = 0.0
	c.fadeDone = make(chan struct{})
	c.fadeCancel = make(chan struct{})
	c.notifyStateChange()

	go c.runFade(0.0, 1.0, duration)
	return nil
}

// AutoOff starts a fade-out transition (AUTO OFF) over the given duration.
func (c *Compositor) AutoOff(duration time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("compositor closed")
	}
	if !c.active {
		return ErrNotActive
	}
	if c.fadeDone != nil {
		return ErrFadeActive
	}

	c.fadeDone = make(chan struct{})
	c.fadeCancel = make(chan struct{})
	c.notifyStateChange()

	go c.runFade(1.0, 0.0, duration)
	return nil
}

// Status returns the current graphics overlay state.
func (c *Compositor) Status() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := State{
		Active:       c.active,
		Template:     c.template,
		FadePosition: c.fadePosition,
	}
	if c.resolutionProvider != nil {
		s.ProgramWidth, s.ProgramHeight = c.resolutionProvider()
	}
	return s
}

// Overlay returns the current RGBA overlay data, dimensions, and alpha
// scale (fade position). Returns nil if no overlay is set or not active.
func (c *Compositor) Overlay() (rgba []byte, width, height int, alphaScale float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.active || c.overlay == nil {
		return nil, 0, 0, 0
	}
	return c.overlay, c.overlayWidth, c.overlayHeight, c.fadePosition
}

// SetResolutionProvider sets a callback that returns the current program
// video resolution. Used by Status() to inform clients what resolution
// to render graphics at.
func (c *Compositor) SetResolutionProvider(fn func() (width, height int)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolutionProvider = fn
}

// OnStateChange registers a callback invoked when the overlay state changes.
// The callback receives a snapshot of the compositor's state so it doesn't
// need to call Status() (which would deadlock since the callback runs under
// the compositor's lock).
func (c *Compositor) OnStateChange(fn func(State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStateChange = fn
}

// Close shuts down the compositor, cancelling any active fade.
func (c *Compositor) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.cancelFadeLocked()
}

// runFade drives a fade from startAlpha to endAlpha over the given duration.
// It updates fadePosition at ~60fps and sets the final state on completion.
// The fadeDone channel is always closed when this function returns.
func (c *Compositor) runFade(startAlpha, endAlpha float64, duration time.Duration) {
	const tickRate = 60 // updates per second
	tickDur := duration / time.Duration(tickRate)
	if tickDur < time.Millisecond {
		tickDur = time.Millisecond
	}
	ticker := time.NewTicker(tickDur)
	defer ticker.Stop()

	start := time.Now()
	cancelled := false

	defer func() {
		c.mu.Lock()
		done := c.fadeDone
		c.fadeDone = nil
		c.fadeCancel = nil
		if !cancelled {
			c.notifyStateChange()
		}
		c.mu.Unlock()
		if done != nil {
			close(done)
		}
	}()

	for {
		select {
		case <-c.fadeCancel:
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
			c.fadePosition = pos
			if progress >= 1.0 {
				// Fade complete.
				if endAlpha == 0.0 {
					c.active = false
				}
				c.mu.Unlock()
				return
			}
			c.notifyStateChange()
			c.mu.Unlock()
		}
	}
}

// cancelFadeLocked cancels any in-progress fade. Must be called with mu held.
func (c *Compositor) cancelFadeLocked() {
	if c.fadeCancel != nil {
		close(c.fadeCancel)
		// Save fadeDone, release lock so the goroutine can finish,
		// then wait for it and re-acquire lock.
		done := c.fadeDone
		c.mu.Unlock()
		if done != nil {
			<-done
		}
		c.mu.Lock()
	}
}

// ProcessYUV applies the overlay to a raw YUV420 buffer in-place.
// This is the codec-free processor used by the pipeline coordinator.
// When inactive or when the overlay resolution doesn't match, returns yuv unchanged.
func (c *Compositor) ProcessYUV(yuv []byte, width, height int) []byte {
	c.mu.RLock()
	active := c.active
	alphaScale := c.fadePosition
	overlayW := c.overlayWidth
	overlayH := c.overlayHeight
	hasOverlay := c.overlay != nil
	c.mu.RUnlock()

	if !active || alphaScale < 1.0/255.0 || !hasOverlay {
		return yuv
	}

	if overlayW != width || overlayH != height {
		return yuv
	}

	c.mu.RLock()
	AlphaBlendRGBA(yuv, c.overlay, width, height, c.fadePosition)
	c.mu.RUnlock()

	return yuv
}

// IsActive returns whether the overlay is currently active.
func (c *Compositor) IsActive() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}

// notifyStateChange invokes the state change callback if set.
// Must be called with mu held (read or write). Builds a state snapshot
// under the lock and passes it to the callback so the callback never
// needs to call Status() (which would deadlock).
func (c *Compositor) notifyStateChange() {
	if c.onStateChange != nil {
		c.onStateChange(State{
			Active:       c.active,
			Template:     c.template,
			FadePosition: c.fadePosition,
		})
	}
}
