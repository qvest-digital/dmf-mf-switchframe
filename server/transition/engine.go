package transition

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// Default encoder parameters used when Bitrate/FPS are not set.
const (
	DefaultBitrate = 4_000_000 // 4 Mbps
	DefaultFPS     = 30.0
)

// EngineConfig configures the TransitionEngine.
type EngineConfig struct {
	DecoderFactory DecoderFactory
	EncoderFactory EncoderFactory
	Output         func(data []byte, isKeyframe bool, pts int64)
	OnComplete     func(aborted bool)

	// Bitrate for the transition encoder in bits/sec. If zero, defaults
	// to DefaultBitrate (4 Mbps). Derived from the program source's
	// recent frame statistics by the switcher.
	Bitrate int

	// FPS for the transition encoder. If zero, defaults to DefaultFPS (30).
	// Derived from the program source's recent PTS deltas by the switcher.
	FPS float64
}

// TransitionEngine manages the dissolve pipeline lifecycle.
// Created when a transition starts, destroyed when it completes or aborts.
type TransitionEngine struct {
	mu             sync.RWMutex
	state          TransitionState
	transitionType TransitionType
	fromSource     string
	toSource       string // empty for FTB
	durationMs     int
	startTime      time.Time
	position       float64

	// Manual T-bar overrides automatic position
	manualControl  bool
	manualPosition float64

	// Codec pipeline
	decoderA VideoDecoder
	decoderB VideoDecoder // nil for FTB
	encoder  VideoEncoder
	blender  *FrameBlender

	// Latest decoded YUV420 frames (stored directly, no RGB conversion)
	latestYUVA   []byte
	latestYUVB   []byte
	firstEncoded bool // true after the first frame has been encoded

	// Frame dimensions (set on first decode)
	width  int
	height int

	// Reusable buffer for scaling mismatched-resolution frames
	scaleBuf []byte

	config EngineConfig
}

// NewTransitionEngine creates a new engine with the given configuration.
func NewTransitionEngine(config EngineConfig) *TransitionEngine {
	return &TransitionEngine{
		state:  StateIdle,
		config: config,
	}
}

// State returns the current engine state.
func (e *TransitionEngine) State() TransitionState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// TransitionType returns the current transition type.
func (e *TransitionEngine) TransitionType() TransitionType {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.transitionType
}

// FromSource returns the outgoing source key.
func (e *TransitionEngine) FromSource() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.fromSource
}

// ToSource returns the incoming source key.
func (e *TransitionEngine) ToSource() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.toSource
}

// Start initializes the transition pipeline. Creates decoders, encoder,
// and blender. Returns error if already active.
func (e *TransitionEngine) Start(from, to string, ttype TransitionType, durationMs int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state == StateActive {
		return ErrTransitionActive
	}

	// Create decoders
	decA, err := e.config.DecoderFactory()
	if err != nil {
		return fmt.Errorf("create decoder A: %w", err)
	}

	var decB VideoDecoder
	if ttype != TransitionFTB && ttype != TransitionFTBReverse {
		decB, err = e.config.DecoderFactory()
		if err != nil {
			decA.Close()
			return fmt.Errorf("create decoder B: %w", err)
		}
	}

	e.state = StateActive
	e.transitionType = ttype
	e.fromSource = from
	e.toSource = to
	e.durationMs = durationMs
	e.startTime = time.Now()
	e.position = 0
	e.manualControl = false
	e.manualPosition = 0
	e.decoderA = decA
	e.decoderB = decB
	e.encoder = nil  // lazy-init on first frame (need dimensions)
	e.blender = nil  // lazy-init on first frame (need dimensions)
	e.latestYUVA = nil
	e.latestYUVB = nil
	e.width = 0
	e.height = 0

	slog.Info("transition: engine started", "type", ttype, "from", from, "to", to, "durationMs", durationMs)
	return nil
}

// Position returns the current transition position (0.0 to 1.0).
func (e *TransitionEngine) Position() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentPosition()
}

// currentPosition calculates position. Caller must hold e.mu.
func (e *TransitionEngine) currentPosition() float64 {
	if e.state != StateActive {
		return 0
	}
	if e.manualControl {
		return e.manualPosition
	}
	elapsed := time.Since(e.startTime).Milliseconds()
	t := math.Min(float64(elapsed)/float64(e.durationMs), 1.0)
	// Smoothstep easing: zero-derivative at endpoints for perceptually smooth
	// transitions. Eliminates the abrupt start/stop of linear interpolation.
	return t * t * (3.0 - 2.0*t)
}

// SetPosition sets the T-bar manual position (0.0-1.0).
// Switches to manual control mode. pos>=1.0 triggers completion.
// pos<=0.0 triggers abort (only if previously moved past 0).
func (e *TransitionEngine) SetPosition(pos float64) {
	e.mu.Lock()
	if e.state != StateActive {
		e.mu.Unlock()
		return
	}

	e.manualControl = true
	wasPastZero := e.manualPosition > 0
	e.manualPosition = math.Max(0, math.Min(1.0, pos))
	e.position = e.manualPosition

	var complete, abort bool
	if pos >= 1.0 {
		complete = true
	} else if pos <= 0.0 && wasPastZero {
		abort = true
	}

	if complete || abort {
		e.cleanup()
	}
	e.mu.Unlock()

	// Call callbacks outside lock
	if complete {
		if e.config.OnComplete != nil {
			e.config.OnComplete(false) // not aborted
		}
	} else if abort {
		if e.config.OnComplete != nil {
			e.config.OnComplete(true) // aborted
		}
	}
}

// decodeAndStore decodes a video frame and stores the result as the latest
// YUV420 data for the given source side. Returns true if the decode and store
// succeeded. Caller must hold e.mu.
func (e *TransitionEngine) decodeAndStore(sourceKey string, wireData []byte, isFrom bool) bool {
	var decoder VideoDecoder
	if isFrom {
		decoder = e.decoderA
	} else {
		decoder = e.decoderB
	}
	if decoder == nil {
		return false
	}

	yuv, w, h, err := decoder.Decode(wireData)
	if err != nil {
		slog.Debug("transition: decode failed", "source", sourceKey, "err", err, "dataLen", len(wireData))
		return false
	}
	slog.Debug("transition: decoded frame", "source", sourceKey, "isFrom", isFrom, "w", w, "h", h)

	// Lazy-init encoder and blender on first successful decode
	if e.width == 0 {
		e.width = w
		e.height = h
		e.blender = NewFrameBlender(w, h)

		bitrate := e.config.Bitrate
		if bitrate <= 0 {
			bitrate = DefaultBitrate
		}
		fps := e.config.FPS
		if fps <= 0 {
			fps = DefaultFPS
		}

		enc, encErr := e.config.EncoderFactory(w, h, bitrate, float32(fps))
		if encErr != nil {
			slog.Error("transition: encoder init failed", "err", encErr, "w", w, "h", h)
			return false
		}
		e.encoder = enc
		slog.Info("transition: encoder initialized", "w", w, "h", h, "bitrate", bitrate, "fps", fps)
	}

	// Scale if resolution doesn't match the target (set from first decoded frame).
	// Common in mixed-resolution setups (e.g. 1080p cameras + 720p ProPresenter).
	if w != e.width || h != e.height {
		slog.Debug("transition: scaling frame", "source", sourceKey,
			"from_w", w, "from_h", h, "to_w", e.width, "to_h", e.height)
		targetSize := e.width * e.height * 3 / 2
		if e.scaleBuf == nil || len(e.scaleBuf) < targetSize {
			e.scaleBuf = make([]byte, targetSize)
		}
		ScaleYUV420(yuv, w, h, e.scaleBuf, e.width, e.height)
		yuv = e.scaleBuf[:targetSize]
		w = e.width
		h = e.height
	}

	// Store YUV directly — no colorspace conversion needed.
	// Deep-copy because the decoder may reuse its internal buffer.
	yuvSize := w*h*3/2
	if isFrom {
		if len(e.latestYUVA) != yuvSize {
			e.latestYUVA = make([]byte, yuvSize)
		}
		copy(e.latestYUVA, yuv)
	} else {
		if len(e.latestYUVB) != yuvSize {
			e.latestYUVB = make([]byte, yuvSize)
		}
		copy(e.latestYUVB, yuv)
	}

	return true
}

// IngestFrame processes a video frame from one of the two transition sources.
// Decodes frame, stores as latest YUV420. If sourceKey matches the incoming
// source (toSource), triggers blend+encode+output with the source's PTS
// to maintain timestamp continuity on the program stream.
// For FTB, the fromSource triggers blend (no toSource).
func (e *TransitionEngine) IngestFrame(sourceKey string, wireData []byte, pts int64) {
	e.mu.Lock()
	if e.state != StateActive {
		e.mu.Unlock()
		return
	}

	// Determine which source this is
	isFrom := sourceKey == e.fromSource
	isTo := sourceKey == e.toSource
	if !isFrom && !isTo {
		e.mu.Unlock()
		return
	}

	if !e.decodeAndStore(sourceKey, wireData, isFrom) {
		e.mu.Unlock()
		return
	}

	// Determine if we should trigger blend+output
	// For Mix/Dip: triggered by incoming source (toSource) frame
	// For FTB/FTBReverse: triggered by fromSource frame (no toSource)
	shouldBlend := false
	if (e.transitionType == TransitionFTB || e.transitionType == TransitionFTBReverse) && isFrom {
		shouldBlend = true
	} else if e.transitionType != TransitionFTB && e.transitionType != TransitionFTBReverse && isTo && e.latestYUVA != nil {
		shouldBlend = true
	}

	if !shouldBlend {
		e.mu.Unlock()
		return
	}

	// Calculate position
	pos := e.currentPosition()

	// Blend directly in YUV420 space — no colorspace conversion needed.
	// The blender's output buffer is pre-allocated and reused across frames.
	var blended []byte
	switch e.transitionType {
	case TransitionMix:
		blended = e.blender.BlendMix(e.latestYUVA, e.latestYUVB, pos)
	case TransitionDip:
		blended = e.blender.BlendDip(e.latestYUVA, e.latestYUVB, pos)
	case TransitionFTB:
		blended = e.blender.BlendFTB(e.latestYUVA, pos)
	case TransitionFTBReverse:
		// Inverted: position 0→1 fades from black to fully visible
		blended = e.blender.BlendFTB(e.latestYUVA, 1.0-pos)
	}

	// Encode — force IDR on the very first encoded frame so downstream
	// decoders always get a clean start, regardless of elapsed time.
	forceIDR := !e.firstEncoded
	e.firstEncoded = true
	encoded, isKeyframe, encErr := e.encoder.Encode(blended, forceIDR)
	if encErr != nil {
		slog.Warn("transition: encode failed", "err", encErr, "pos", pos)
		e.mu.Unlock()
		return
	}
	slog.Debug("transition: blended+encoded", "pos", fmt.Sprintf("%.3f", pos), "isKeyframe", isKeyframe, "outBytes", len(encoded))

	// Check if auto-mode completed
	var autoComplete bool
	if !e.manualControl && pos >= 1.0 {
		autoComplete = true
		slog.Info("transition: auto-complete", "type", e.transitionType)
		e.cleanup()
	}

	e.mu.Unlock()

	// Output and completion callbacks outside lock
	if e.config.Output != nil {
		e.config.Output(encoded, isKeyframe, pts)
	}

	if autoComplete && e.config.OnComplete != nil {
		e.config.OnComplete(false)
	}
}

// WarmupDecode feeds a frame to the decoder for the given source side,
// populating latestYUVA/latestYUVB so the first live IngestFrame can
// produce blended output immediately. Produces no output callbacks.
// No-op if the engine is not active.
func (e *TransitionEngine) WarmupDecode(sourceKey string, wireData []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state != StateActive {
		return
	}

	isFrom := sourceKey == e.fromSource
	isTo := sourceKey == e.toSource
	if !isFrom && !isTo {
		return
	}

	e.decodeAndStore(sourceKey, wireData, isFrom)
}

// Stop tears down decoders/encoder and resets state.
func (e *TransitionEngine) Stop() {
	e.mu.Lock()
	e.cleanup()
	e.mu.Unlock()
}

// cleanup releases codec resources and resets state. Caller must hold e.mu.
func (e *TransitionEngine) cleanup() {
	if e.decoderA != nil {
		e.decoderA.Close()
		e.decoderA = nil
	}
	if e.decoderB != nil {
		e.decoderB.Close()
		e.decoderB = nil
	}
	if e.encoder != nil {
		e.encoder.Close()
		e.encoder = nil
	}
	e.state = StateIdle
	e.position = 0
	e.manualPosition = 0
	e.manualControl = false
	e.firstEncoded = false
	e.latestYUVA = nil
	e.latestYUVB = nil
	e.blender = nil
	e.scaleBuf = nil
}
