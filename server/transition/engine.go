package transition

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// EngineConfig configures the TransitionEngine.
type EngineConfig struct {
	DecoderFactory DecoderFactory
	EncoderFactory EncoderFactory
	Output         func(data []byte, isKeyframe bool)
	OnComplete     func(aborted bool)
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

	// Latest decoded RGB frames
	latestRGBA []byte
	latestRGBB []byte
	rgbBufA    []byte // reusable conversion buffer
	rgbBufB    []byte // reusable conversion buffer

	// Frame dimensions (set on first decode)
	width  int
	height int

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
	e.latestRGBA = nil
	e.latestRGBB = nil
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
	return math.Min(float64(elapsed)/float64(e.durationMs), 1.0)
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

// IngestFrame processes a video frame from one of the two transition sources.
// Decodes frame, stores as latest RGB. If sourceKey matches the incoming
// source (toSource), triggers blend+encode+output.
// For FTB, the fromSource triggers blend (no toSource).
func (e *TransitionEngine) IngestFrame(sourceKey string, wireData []byte) {
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

	// Decode
	var decoder VideoDecoder
	if isFrom {
		decoder = e.decoderA
	} else {
		decoder = e.decoderB
	}
	if decoder == nil {
		e.mu.Unlock()
		return
	}

	yuv, w, h, err := decoder.Decode(wireData)
	if err != nil {
		slog.Debug("transition: decode failed", "source", sourceKey, "err", err, "dataLen", len(wireData))
		e.mu.Unlock()
		return
	}
	slog.Debug("transition: decoded frame", "source", sourceKey, "isFrom", isFrom, "w", w, "h", h)

	// Lazy-init encoder and blender on first successful decode
	if e.width == 0 {
		e.width = w
		e.height = h
		e.blender = NewFrameBlender(w, h)
		e.rgbBufA = make([]byte, w*h*3)
		e.rgbBufB = make([]byte, w*h*3)

		enc, encErr := e.config.EncoderFactory(w, h, 4000000, 30.0)
		if encErr != nil {
			slog.Error("transition: encoder init failed", "err", encErr, "w", w, "h", h)
			e.mu.Unlock()
			return
		}
		e.encoder = enc
		slog.Info("transition: encoder initialized", "w", w, "h", h)
	}

	// Resolution mismatch check
	if w != e.width || h != e.height {
		slog.Warn("transition: resolution mismatch, skipping frame", "source", sourceKey,
			"expected", fmt.Sprintf("%dx%d", e.width, e.height), "got", fmt.Sprintf("%dx%d", w, h))
		e.mu.Unlock()
		return
	}

	// Convert YUV->RGB and store
	if isFrom {
		YUV420ToRGB(yuv, w, h, e.rgbBufA)
		e.latestRGBA = e.rgbBufA
	} else {
		YUV420ToRGB(yuv, w, h, e.rgbBufB)
		e.latestRGBB = e.rgbBufB
	}

	// Determine if we should trigger blend+output
	// For Mix/Dip: triggered by incoming source (toSource) frame
	// For FTB/FTBReverse: triggered by fromSource frame (no toSource)
	shouldBlend := false
	if (e.transitionType == TransitionFTB || e.transitionType == TransitionFTBReverse) && isFrom {
		shouldBlend = true
	} else if e.transitionType != TransitionFTB && e.transitionType != TransitionFTBReverse && isTo && e.latestRGBA != nil {
		shouldBlend = true
	}

	if !shouldBlend {
		e.mu.Unlock()
		return
	}

	// Calculate position
	pos := e.currentPosition()

	// Blend
	var blended []byte
	switch e.transitionType {
	case TransitionMix:
		blended = e.blender.BlendMix(e.latestRGBA, e.latestRGBB, pos)
	case TransitionDip:
		blended = e.blender.BlendDip(e.latestRGBA, e.latestRGBB, pos)
	case TransitionFTB:
		blended = e.blender.BlendFTB(e.latestRGBA, pos)
	case TransitionFTBReverse:
		// Inverted: position 0→1 fades from black to fully visible
		blended = e.blender.BlendFTB(e.latestRGBA, 1.0-pos)
	}

	// Convert blended RGB -> YUV420 for encoding
	yuvOut := make([]byte, e.width*e.height*3/2)
	RGBToYUV420(blended, e.width, e.height, yuvOut)

	// Encode
	forceIDR := pos == 0.0 // force keyframe at start
	encoded, isKeyframe, encErr := e.encoder.Encode(yuvOut, forceIDR)
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
		e.config.Output(encoded, isKeyframe)
	}

	if autoComplete && e.config.OnComplete != nil {
		e.config.OnComplete(false)
	}
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
	e.latestRGBA = nil
	e.latestRGBB = nil
	e.blender = nil
}
