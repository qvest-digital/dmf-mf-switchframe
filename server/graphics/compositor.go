package graphics

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// Errors returned by Compositor methods.
var (
	ErrAlreadyActive = errors.New("graphics overlay already active")
	ErrNotActive     = errors.New("graphics overlay not active")
	ErrNoOverlay     = errors.New("no overlay frame has been uploaded")
	ErrFadeActive    = errors.New("fade transition in progress")
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
	mu sync.RWMutex

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

	// Codec pipeline for server-side compositing (decode → blend → encode).
	// Lazy-initialized on first active frame, destroyed on deactivation.
	decoderFactory transition.DecoderFactory
	encoderFactory transition.EncoderFactory
	decoder        transition.VideoDecoder
	encoder        transition.VideoEncoder
	encWidth       int
	encHeight      int
	yuvBuf         []byte // reusable buffer for decoded YUV data
	groupID        uint32 // monotonic group ID for encoded frames
	needsIDR       bool   // force IDR on next encoded frame after activation

	// Callback invoked on state change (active/inactive/fade).
	// Receives a snapshot of the current state so callers don't need
	// to call Status() (which would deadlock under the compositor's lock).
	onStateChange func(State)

	// Returns program video resolution. Set via SetResolutionProvider.
	resolutionProvider func() (width, height int)

	// Callback invoked when the compositor produces its first keyframe
	// with new SPS/PPS. Used to update the program relay's VideoInfo
	// so new MoQ connections get the compositor's avcC in the catalog.
	onVideoInfoChange func(sps, pps []byte, width, height int)
}

// NewCompositor creates a new graphics overlay compositor.
func NewCompositor() *Compositor {
	return &Compositor{}
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
	c.needsIDR = true
	slog.Debug("graphics overlay CUT ON")
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
	c.destroyCodecs()
	slog.Debug("graphics overlay CUT OFF")
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
	c.needsIDR = true
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

// OnVideoInfoChange registers a callback invoked when the compositor produces
// its first keyframe with new SPS/PPS. The callback receives raw SPS/PPS NALUs
// and frame dimensions so the caller can update the program relay's VideoInfo.
func (c *Compositor) OnVideoInfoChange(fn func(sps, pps []byte, width, height int)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onVideoInfoChange = fn
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

// SetCodecFactories configures the decoder/encoder factories for server-side
// compositing. Without these, ProcessFrame will pass through frames unchanged.
func (c *Compositor) SetCodecFactories(dec transition.DecoderFactory, enc transition.EncoderFactory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.decoderFactory = dec
	c.encoderFactory = enc
}

// ProcessFrame is the video processor hook called by the switcher on every
// program frame. When the overlay is active and codec factories are configured,
// it decodes the H.264 frame, composites the RGBA overlay, and re-encodes.
// When inactive, it returns the frame unchanged (zero overhead).
//
// On activation, the first composited frame is forced to be an IDR (keyframe)
// so the browser decoder gets a clean start with the new codec parameters.
func (c *Compositor) ProcessFrame(frame *media.VideoFrame) *media.VideoFrame {
	c.mu.RLock()
	active := c.active
	alphaScale := c.fadePosition
	overlayW := c.overlayWidth
	overlayH := c.overlayHeight
	hasOverlay := c.overlay != nil
	hasCodecs := c.decoderFactory != nil && c.encoderFactory != nil
	c.mu.RUnlock()

	if !active || alphaScale < 1.0/255.0 || !hasOverlay || !hasCodecs {
		return frame // passthrough
	}

	// We can only composite if the overlay resolution matches the video.
	// We don't know the video resolution until we decode, but we can
	// skip non-keyframes when we haven't initialized codecs yet (need
	// a keyframe with SPS/PPS to start the decoder).
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check state under write lock (may have changed)
	if !c.active || c.overlay == nil {
		return frame
	}

	// Lazy-init decoder
	if c.decoder == nil {
		// Need a keyframe to initialize the decoder (requires SPS/PPS)
		if !frame.IsKeyframe {
			return frame
		}
		dec, err := c.decoderFactory()
		if err != nil {
			slog.Error("graphics: decoder init failed", "err", err)
			return frame
		}
		c.decoder = dec
	}

	// Decode H.264 → YUV420
	annexB := codec.AVC1ToAnnexB(frame.WireData)
	if frame.IsKeyframe && len(frame.SPS) > 0 {
		var buf []byte
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, frame.SPS...)
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, frame.PPS...)
		buf = append(buf, annexB...)
		annexB = buf
	}
	yuv, w, h, err := c.decoder.Decode(annexB)
	if err != nil {
		slog.Debug("graphics: decode failed", "err", err)
		// Once the encoder has produced frames, we must NOT pass through
		// source frames — they have different SPS/PPS and would break
		// the browser's decoder. Drop the frame instead (nil = skip).
		if c.encoder != nil {
			return nil
		}
		return frame
	}

	// If overlay resolution doesn't match video, pass through unchanged
	// (only safe before the encoder starts producing frames).
	if overlayW != w || overlayH != h {
		slog.Debug("graphics: overlay resolution mismatch, passthrough",
			"overlay", fmt.Sprintf("%dx%d", overlayW, overlayH),
			"frame", fmt.Sprintf("%dx%d", w, h))
		if c.encoder != nil {
			return nil
		}
		return frame
	}

	// Lazy-init encoder (need dimensions from first decode)
	if c.encoder == nil {
		enc, err := c.encoderFactory(w, h, transition.DefaultBitrate, transition.DefaultFPS)
		if err != nil {
			slog.Error("graphics: encoder init failed", "err", err, "w", w, "h", h)
			return frame
		}
		c.encoder = enc
		c.encWidth = w
		c.encHeight = h
		slog.Info("graphics: codec pipeline initialized", "w", w, "h", h)
	}

	// Deep-copy decoded YUV (decoder reuses internal buffer)
	yuvSize := w * h * 3 / 2
	if len(c.yuvBuf) != yuvSize {
		c.yuvBuf = make([]byte, yuvSize)
	}
	copy(c.yuvBuf, yuv[:yuvSize])

	// Composite the RGBA overlay onto the YUV frame
	AlphaBlendRGBA(c.yuvBuf, c.overlay, w, h, c.fadePosition)

	// Force IDR on the first composited frame after activation so the
	// browser decoder gets a clean start with new SPS/PPS. Also force
	// IDR whenever the source frame was a keyframe to maintain GOP
	// structure.
	forceIDR := frame.IsKeyframe || c.needsIDR
	c.needsIDR = false

	encoded, isKeyframe, err := c.encoder.Encode(c.yuvBuf, forceIDR)
	if err != nil {
		slog.Warn("graphics: encode failed", "err", err)
		return nil // don't pass through source frame — codec mismatch
	}

	// Ensure groupID stays monotonically increasing relative to the source
	// stream. Without this, the compositor's counter (starting at 0) would
	// produce GroupIDs lower than the source's current value, causing MoQ
	// subscribers to discard composited frames as stale.
	if frame.GroupID > c.groupID {
		c.groupID = frame.GroupID
	}
	if isKeyframe {
		c.groupID++
	}

	// Convert Annex B output to AVC1 VideoFrame
	avc1 := codec.AnnexBToAVC1(encoded)
	result := &media.VideoFrame{
		PTS:        frame.PTS,
		DTS:        frame.DTS,
		IsKeyframe: isKeyframe,
		WireData:   avc1,
		Codec:      frame.Codec,
		GroupID:    c.groupID,
	}
	if isKeyframe {
		for _, nalu := range codec.ExtractNALUs(avc1) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1F {
			case 7:
				result.SPS = nalu
			case 8:
				result.PPS = nalu
			}
		}
		// Notify the program relay about the new codec config so the MoQ
		// catalog serves the compositor's avcC to new connections.
		if result.SPS != nil && result.PPS != nil && c.onVideoInfoChange != nil {
			c.onVideoInfoChange(result.SPS, result.PPS, c.encWidth, c.encHeight)
		}
	}
	return result
}

// destroyCodecs releases decoder/encoder resources. Caller must hold c.mu.
func (c *Compositor) destroyCodecs() {
	if c.decoder != nil {
		c.decoder.Close()
		c.decoder = nil
	}
	if c.encoder != nil {
		c.encoder.Close()
		c.encoder = nil
	}
	c.yuvBuf = nil
	c.encWidth = 0
	c.encHeight = 0
}

// Close shuts down the compositor, cancelling any active fade and releasing codecs.
func (c *Compositor) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.cancelFadeLocked()
	c.destroyCodecs()
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
					c.destroyCodecs()
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
