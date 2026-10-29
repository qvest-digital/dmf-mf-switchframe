package transition

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// Default encoder parameters for the pipeline codec pool.
const (
	DefaultBitrate = 4_000_000 // 4 Mbps
	DefaultFPS     = 30.0
	DefaultGOPSecs = 2 // IDR interval in seconds (program output)

	// PreviewGOPSecs is a shorter IDR interval for preview/relay encodes.
	// Shorter GOPs mean faster recovery after transport stalls — the browser
	// waits at most 1s for the next keyframe instead of 2s.
	PreviewGOPSecs = 1

	// PreviewGOPFrames is the IDR interval in frames for preview encodes.
	// ~15 frames at 30fps = 0.5s GOPs. Halving the GOP reduces the
	// damaged-group cascade impact on jittery networks: each drop loses
	// at most ~14 frames instead of ~29.
	PreviewGOPFrames = 15
)

// DefaultTimeout is the default watchdog timeout for transition frame starvation.
// If no frames arrive from either source for this duration, the transition is aborted.
const DefaultTimeout = 10 * time.Second

// StingerData holds pre-decoded stinger overlay frames for use during a
// stinger transition. Populated by the switcher from a stinger.Clip.
type StingerData struct {
	// Frames holds YUV420 + alpha data for each stinger frame.
	Frames []StingerFrameData
	// Width and Height of the stinger frames.
	Width, Height int
	// CutPoint is the position [0.0-1.0] where the underlying source switches from A to B.
	CutPoint float64
	// Audio is optional stinger audio (interleaved float32 PCM).
	Audio           []float32
	AudioSampleRate int
	AudioChannels   int
}

// StingerFrameData is a single stinger overlay frame.
type StingerFrameData struct {
	YUV   []byte // YUV420 planar
	Alpha []byte // per-luma-pixel alpha [0-255]
}

// EngineConfig configures the Engine.
type EngineConfig struct {
	DecoderFactory DecoderFactory
	Output         func(yuv []byte, width, height int, pts int64, isKeyframe bool)
	OnComplete     func(aborted bool)

	// WipeDirection specifies the wipe direction when Type is "wipe".
	// Ignored for other transition types. Used only for legacy directional wipes;
	// gradient-map wipes use WipeConfig instead.
	WipeDirection WipeDirection

	// WipeConfig specifies gradient-map-based wipe parameters. When set and
	// Type is "wipe", the engine resolves the gradient map from WipeMapStore
	// and uses BlendWipeMap instead of BlendWipe. Takes precedence over WipeDirection.
	WipeConfig *wipemap.WipeConfig

	// WipeMapStore resolves gradient maps by pattern ID. Required when
	// WipeConfig is set; ignored otherwise.
	WipeMapStore *wipemap.WipeMapStore

	// Stinger holds the pre-decoded stinger overlay data. Required when
	// Type is "stinger", ignored for other types.
	Stinger *StingerData

	// Easing sets the easing curve for the transition. If nil, the engine
	// falls back to legacy smoothstep for backward compatibility.
	Easing *EasingCurve

	// HintWidth/HintHeight pre-initialize the blender at Start() time.
	// When set, the engine can produce output (via black frame fallback)
	// even before any decode succeeds. Set from the pipeline's known
	// resolution to eliminate output gaps during B-frame reorder warmup.
	HintWidth  int
	HintHeight int

	// SkipBlend skips the CPU pixel blend in blendAndOutput but still
	// tracks position and handles auto-complete timing. Used when GPU
	// transitions are active — the GPU pipeline performs the blend, and
	// the CPU engine only manages transition state.
	SkipBlend bool

	// Render, when set, draws each transition frame in place of the blend
	// for the engine's type: dst, from and to are w x h frames in format f,
	// and to is nil when there is no incoming frame. It reports false for a
	// frame it leaves to the blend. DVE transitions run on a mix engine and
	// draw through this when no GPU renders them.
	Render RenderFunc

	// FramePeriod is the time one output frame may take. The first Render
	// call slower than that is logged. Zero turns the check off.
	FramePeriod time.Duration
}

// RenderFunc draws one transition frame; see EngineConfig.Render.
type RenderFunc func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool

// Engine manages the dissolve pipeline lifecycle.
// Created when a transition starts, destroyed when it completes or aborts.
type Engine struct {
	log            *slog.Logger
	mu             sync.RWMutex
	state          State
	transitionType Type
	wipeDirection  WipeDirection
	wipeConfig     *wipemap.WipeConfig // gradient-map wipe config (nil = legacy directional)
	wipeGradient   []byte              // resolved gradient map for current dimensions
	fromSource     string
	toSource       string // empty for FTB
	durationMs     int
	startTime      time.Time
	position       float64

	// Manual T-bar overrides automatic position
	manualControl  bool
	manualPosition float64

	// Easing curve for auto-position calculation (nil = legacy smoothstep)
	easing *EasingCurve

	// Codec pipeline
	decoderA VideoDecoder
	decoderB VideoDecoder // nil for FTB
	blender  *FrameBlender

	// Latest decoded YUV420 frames (stored directly, no RGB conversion)
	latestYUVA  []byte
	latestYUVB  []byte
	firstOutput bool // true after the first frame has been output

	// Frame dimensions (set on first decode)
	width  int
	height int

	// Set by WarmupComplete(), cleared on first live IngestFrame.
	// When set and a keyframe arrives, the decoder is flushed to reset
	// stale warmup references before decoding the fresh IDR.
	needsFlushA bool
	needsFlushB bool

	// Reusable black frame for blend fallback when a source hasn't decoded yet
	// (B-frame reorder EAGAIN during warmup). BT.709 limited-range black.
	blackBuf []byte

	// Reusable buffer for scaling mismatched-resolution frames
	scaleBuf []byte

	// Pre-scaled stinger frames (scaled to match video dimensions on first use)
	stingerScaled []StingerFrameData

	// Reusable buffer for upconverting 8-bit stinger frames to 10-bit (avoids per-frame alloc).
	stinger10bitBuf []byte

	// In-flight decode tracking: cleanup waits for active decodes to finish
	// before closing decoders, preventing use-after-free.
	decodeWG sync.WaitGroup

	// cleaning is true while cleanup() has released the lock to wait for
	// in-flight decodes. Prevents Start() from succeeding during the window.
	cleaning bool

	// Watchdog: aborts transition if no frames arrive within timeout
	timeout      time.Duration // default 10s, configurable via SetTimeout()
	lastFrameAt  time.Time     // updated in IngestFrame()
	watchdogStop chan struct{} // closed in cleanup() to stop watchdog goroutine
	watchdogOnce sync.Once     // prevents double-close of watchdogStop

	// Timing instrumentation (atomic, lock-free — safe to read from any goroutine)
	decodeLastNano atomic.Int64
	decodeMaxNano  atomic.Int64
	blendLastNano  atomic.Int64
	blendMaxNano   atomic.Int64
	ingestLastNano atomic.Int64
	ingestMaxNano  atomic.Int64
	framesIngested atomic.Int64
	framesBlended  atomic.Int64

	// rawBlender is cached for BlendRaw to avoid per-frame allocation (Issue 7).
	// Recreated only when resolution changes.
	rawBlender *FrameBlender

	render       *asyncRender // draws config.Render frames, nil until the first; under mu
	overrunNoted atomic.Bool  // a Render overrun has been logged for this engine

	// Reusable scratch buffers for BlendRawInto mismatched-resolution paths.
	// blendRawMu serializes concurrent BlendRawInto callers so the scratch
	// stays consistent even if a future caller lands on a second goroutine
	// (today the transition state machine serializes ticks, making this
	// lock uncontended — cost is ~20 ns per frame).
	blendRawMu       sync.Mutex
	rawScaleToBuf    []byte  // scaled toYUV when toW/toH != fromW/fromH
	rawScaleSBuf     []byte  // scaled stinger YUV when stinger dims != output dims
	rawAlphaScaleBuf []byte  // scaled stinger alpha ditto
	rawXCoordsFullW  []int64 // full-width scaler coords (Y plane and alpha)
	rawXCoordsHalfW  []int64 // half-width scaler coords (UV planes)

	config EngineConfig

	// colorFormat specifies the pipeline pixel format (8-bit or 10-bit).
	// Determines which blend functions are used and frame sizing.
	colorFormat video.Format
}

// NewEngine creates a new engine with the given configuration.
func NewEngine(config EngineConfig) *Engine {
	return &Engine{
		log:     slog.With("component", "transition"),
		state:   StateIdle,
		timeout: DefaultTimeout,
		config:  config,
	}
}

// Timing returns a snapshot of the engine's timing instrumentation.
// Safe to call from any goroutine (all fields are atomic).
func (e *Engine) Timing() map[string]any {
	return map[string]any{
		"decode_last_ms":  float64(e.decodeLastNano.Load()) / 1e6,
		"decode_max_ms":   float64(e.decodeMaxNano.Load()) / 1e6,
		"blend_last_ms":   float64(e.blendLastNano.Load()) / 1e6,
		"blend_max_ms":    float64(e.blendMaxNano.Load()) / 1e6,
		"ingest_last_ms":  float64(e.ingestLastNano.Load()) / 1e6,
		"ingest_max_ms":   float64(e.ingestMaxNano.Load()) / 1e6,
		"frames_ingested": e.framesIngested.Load(),
		"frames_blended":  e.framesBlended.Load(),
	}
}

// SetTimeout configures the watchdog timeout. If no frames arrive from
// either source for this duration during an active transition, the
// transition is aborted. Must be called before Start().
func (e *Engine) SetTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.timeout = d
}

// SetColorFormat sets the pipeline color format for blend operations.
// Must be called before Start(). When set to video.YUV422_10bit, the engine
// uses 10-bit blend functions and 10-bit frame sizing.
func (e *Engine) SetColorFormat(f video.Format) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.colorFormat = f
}

// Timeout returns the current watchdog timeout.
func (e *Engine) Timeout() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.timeout
}

// State returns the current engine state.
func (e *Engine) State() State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// TransitionType returns the current transition type.
func (e *Engine) TransitionType() Type {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.transitionType
}

// FromSource returns the outgoing source key.
func (e *Engine) FromSource() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.fromSource
}

// ToSource returns the incoming source key.
func (e *Engine) ToSource() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.toSource
}

// StingerFrameAt returns the stinger overlay YUV, alpha, and cut point for
// the given transition position. Returns nil slices if no stinger is configured.
// Used by the GPU transition path to upload stinger data to GPU.
func (e *Engine) StingerFrameAt(pos float64) (yuv, alpha []byte, width, height int, cutPoint float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sd := e.config.Stinger
	if sd == nil || len(sd.Frames) == 0 {
		return nil, nil, 0, 0, 0.5
	}

	// Lazy-scale stinger frames to engine dimensions on first access.
	// In SkipBlend mode (GPU transitions), blendStinger is never called,
	// so the lazy scale in blendStinger never runs. We do it here instead.
	if e.stingerScaled == nil && e.width > 0 && e.height > 0 {
		e.stingerScaled = e.scaleStingerFrames(sd)
	}

	frames := sd.Frames
	w, h := sd.Width, sd.Height
	if e.stingerScaled != nil {
		frames = e.stingerScaled
		w = e.width
		h = e.height
	}

	frameIdx := int(pos * float64(len(frames)))
	if frameIdx >= len(frames) {
		frameIdx = len(frames) - 1
	}
	if frameIdx < 0 {
		frameIdx = 0
	}

	sf := &frames[frameIdx]
	return sf.YUV, sf.Alpha, w, h, sd.CutPoint
}

// WipeDirection returns the wipe direction for the current transition.
func (e *Engine) WipeDirection() WipeDirection {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.wipeDirection
}

// WipeMapConfig returns the current wipe map config, or nil if using legacy directional wipe.
func (e *Engine) WipeMapConfig() *wipemap.WipeConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.wipeConfig
}

// WipeGradient returns the resolved gradient map (single-channel, w*h bytes),
// or nil if no gradient-map wipe is active.
func (e *Engine) WipeGradient() []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.wipeGradient
}

// resolveWipeGradient pre-resolves the gradient map from the WipeMapStore at the
// current engine dimensions. Caller must hold e.mu. Safe to call when wipeConfig
// or WipeMapStore is nil (no-op).
func (e *Engine) resolveWipeGradient() {
	if e.wipeConfig == nil || e.config.WipeMapStore == nil || e.width == 0 || e.height == 0 {
		return
	}
	gm, err := e.config.WipeMapStore.GetMap(e.wipeConfig.PatternID, e.width, e.height)
	if err != nil {
		e.log.Warn("failed to resolve wipe gradient map, falling back to directional",
			"pattern", e.wipeConfig.PatternID, "err", err)
		e.wipeConfig = nil // fall back to legacy
		return
	}
	e.wipeGradient = gm
}

// Easing returns the current easing type, or "smoothstep" if nil.
func (e *Engine) Easing() EasingType {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.easing != nil {
		return e.easing.Type
	}
	return EasingSmoothstep
}

// Start initializes the transition pipeline. Creates decoders and blender.
// Returns error if already active.
func (e *Engine) Start(from, to string, ttype Type, durationMs int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state == StateActive || e.cleaning {
		return ErrActive
	}

	// Create decoders (optional — nil factory means raw YUV only via IngestRawFrame).
	var decA, decB VideoDecoder
	if e.config.DecoderFactory != nil {
		var err error
		decA, err = e.config.DecoderFactory()
		if err != nil {
			return fmt.Errorf("create decoder A: %w", err)
		}

		if ttype != FTB && ttype != FTBReverse {
			decB, err = e.config.DecoderFactory()
			if err != nil {
				decA.Close()
				return fmt.Errorf("create decoder B: %w", err)
			}
		}
	}

	e.state = StateActive
	e.transitionType = ttype
	e.wipeDirection = e.config.WipeDirection
	e.wipeConfig = e.config.WipeConfig
	e.wipeGradient = nil
	e.fromSource = from
	e.toSource = to
	e.durationMs = durationMs
	e.startTime = time.Now()
	e.position = 0
	e.manualControl = false
	e.manualPosition = 0
	e.easing = e.config.Easing
	e.decoderA = decA
	e.decoderB = decB
	e.latestYUVA = nil
	e.latestYUVB = nil

	// Pre-initialize blender from hint dimensions if available.
	// This eliminates output gaps when ALL warmup decodes return EAGAIN
	// (B-frame reorder). Without hints, blender stays nil until first
	// successful decode.
	if e.config.HintWidth > 0 && e.config.HintHeight > 0 {
		e.width = e.config.HintWidth
		e.height = e.config.HintHeight
		blender, err := NewFrameBlender(e.width, e.height)
		if err != nil {
			return fmt.Errorf("create blender: %w", err)
		}
		e.blender = blender

		// Pre-resolve gradient map for wipe-map transitions at known dimensions.
		e.resolveWipeGradient()
	} else {
		e.blender = nil
		e.width = 0
		e.height = 0
	}

	// Initialize watchdog state
	e.lastFrameAt = time.Now()
	e.watchdogStop = make(chan struct{})
	e.watchdogOnce = sync.Once{}

	// Pre-scale stinger frames eagerly so the first blend frame doesn't stall.
	// Without this, lazy scaling on the first blendStinger() call can block
	// for 15-30ms (scaling 30 frames at 1080p), causing a visible stutter.
	e.stingerScaled = nil
	if ttype == Stinger && e.config.Stinger != nil && len(e.config.Stinger.Frames) > 0 && e.width > 0 {
		e.stingerScaled = e.scaleStingerFrames(e.config.Stinger)
	}

	e.log.Info("engine started", "type", ttype, "from", from, "to", to, "durationMs", durationMs)

	// Start watchdog goroutine
	go e.runWatchdog(e.watchdogStop, e.timeout)

	return nil
}

// Position returns the current transition position (0.0 to 1.0).
func (e *Engine) Position() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentPosition()
}

// currentPosition calculates position. Caller must hold e.mu.
func (e *Engine) currentPosition() float64 {
	if e.state != StateActive {
		return 0
	}
	if e.manualControl {
		return e.manualPosition
	}
	elapsed := time.Since(e.startTime).Milliseconds()
	t := math.Min(float64(elapsed)/float64(e.durationMs), 1.0)
	// Apply easing curve. Nil defaults to smoothstep for backward compatibility.
	var pos float64
	if e.easing != nil {
		pos = e.easing.Ease(t)
	} else {
		pos = t * t * (3.0 - 2.0*t) // Legacy smoothstep
	}
	return math.Max(0.0, math.Min(1.0, pos))
}

// SetPosition sets the T-bar manual position (0.0-1.0).
// Switches to manual control mode. pos>=1.0 triggers completion.
// pos<=0.0 triggers abort (only if previously moved past 0).
func (e *Engine) SetPosition(pos float64) {
	e.mu.Lock()
	if e.state != StateActive {
		e.mu.Unlock()
		return
	}

	e.manualControl = true
	wasPastZero := e.manualPosition > 0
	clamped := math.Max(0, math.Min(1.0, pos))
	e.manualPosition = clamped
	e.position = clamped

	var complete, abort bool
	if clamped >= 0.999 {
		complete = true
	} else if clamped <= 0.0 && wasPastZero {
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
func (e *Engine) decodeAndStore(sourceKey string, wireData []byte, isFrom bool) bool {
	var decoder VideoDecoder
	if isFrom {
		decoder = e.decoderA
	} else {
		decoder = e.decoderB
	}
	if decoder == nil {
		return false
	}

	decStart := time.Now()
	yuv, w, h, err := decoder.Decode(wireData)
	decDur := time.Since(decStart).Nanoseconds()
	e.decodeLastNano.Store(decDur)
	atomicutil.UpdateMax(&e.decodeMaxNano, decDur)
	if err != nil {
		e.log.Debug("decode failed", "source", sourceKey, "err", err, "dataLen", len(wireData))
		return false
	}
	e.log.Debug("decoded frame", "source", sourceKey, "isFrom", isFrom, "w", w, "h", h)

	// Lazy-init blender on first successful decode
	if e.width == 0 {
		e.width = w
		e.height = h
		blender, err := NewFrameBlender(w, h)
		if err != nil {
			e.log.Warn("skipping frame: failed to create blender", "err", err, "w", w, "h", h)
			return false
		}
		e.blender = blender
		e.resolveWipeGradient()
	}

	// Scale if resolution doesn't match the target (set from first decoded frame).
	// This should no longer trigger now that per-source normalization scales at
	// the decoder output. Kept as a defensive fallback.
	if w != e.width || h != e.height {
		slog.Debug("transition: resolution mismatch, scaling",
			"source_w", w, "source_h", h, "target_w", e.width, "target_h", e.height)
		targetSize := e.colorFormat.FrameSize(e.width, e.height)
		if e.scaleBuf == nil || len(e.scaleBuf) < targetSize {
			e.scaleBuf = make([]byte, targetSize)
		}
		if e.colorFormat == video.YUV422_10bit {
			_ = ScaleYUV422P10(yuv, w, h, e.scaleBuf, e.width, e.height)
		} else {
			ScaleYUV420WithQuality(yuv, w, h, e.scaleBuf, e.width, e.height, ScaleQualityFast)
		}
		yuv = e.scaleBuf[:targetSize]
		w = e.width
		h = e.height
	}

	// Store YUV directly — no colorspace conversion needed.
	// Deep-copy because the decoder may reuse its internal buffer.
	yuvSize := e.colorFormat.FrameSize(w, h)
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
//
// Lock scope is minimized: decode happens outside the lock so that two
// sources sending frames near-simultaneously don't block each other during
// the 3-16ms decode step.
func (e *Engine) IngestFrame(sourceKey string, wireData []byte, pts int64, isKeyframe bool) {
	ingestStart := time.Now()
	defer func() {
		dur := time.Since(ingestStart).Nanoseconds()
		e.ingestLastNano.Store(dur)
		atomicutil.UpdateMax(&e.ingestMaxNano, dur)
	}()
	e.framesIngested.Add(1)

	// --- Phase 1 (lock): snapshot state, determine source, flush if needed ---
	e.mu.Lock()
	if e.state != StateActive {
		e.mu.Unlock()
		return
	}

	isFrom := sourceKey == e.fromSource
	isTo := sourceKey == e.toSource
	if !isFrom && !isTo {
		e.mu.Unlock()
		return
	}

	e.lastFrameAt = time.Now()

	// Snapshot the decoder pointer for use outside the lock.
	// Each source (A/B) has its own decoder so concurrent decodes don't conflict.
	var decoder VideoDecoder
	if isFrom {
		decoder = e.decoderA
		if e.needsFlushA {
			if isKeyframe {
				type flusher interface{ Flush() }
				if f, ok := decoder.(flusher); ok {
					f.Flush()
				}
			}
			e.needsFlushA = false
		}
	} else {
		decoder = e.decoderB
		if e.needsFlushB {
			if isKeyframe {
				type flusher interface{ Flush() }
				if f, ok := decoder.(flusher); ok {
					f.Flush()
				}
			}
			e.needsFlushB = false
		}
	}
	// Track in-flight decode BEFORE releasing the lock so cleanup()'s
	// decodeWG.Wait() will see it and wait for the decode to finish.
	if decoder != nil {
		e.decodeWG.Add(1)
	}
	e.mu.Unlock()

	// --- Phase 2 (no lock): decode using snapshotted decoder ---
	var decodedYUV []byte
	var decW, decH int
	decodeOK := false
	if decoder != nil {
		decStart := time.Now()
		// Run the decode inside a closure so decodeWG.Done() is ALWAYS paired
		// with the Add(1) above, even if the cgo decoder (FFmpeg/OpenH264)
		// panics on a malformed bitstream. Without this, a panic would skip
		// Done() and deadlock cleanup()'s decodeWG.Wait(), leaking the engine.
		// recover() also converts a cgo fault into a decode error rather than
		// killing the frame-delivery goroutine.
		yuv, w, h, err := func() (yuv []byte, w, h int, err error) {
			defer e.decodeWG.Done() // paired with Add(1) above
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("decoder panic: %v", r)
					e.log.Error("decoder panicked", "source", sourceKey, "panic", r, "dataLen", len(wireData))
				}
			}()
			return decoder.Decode(wireData)
		}()
		decDur := time.Since(decStart).Nanoseconds()
		e.decodeLastNano.Store(decDur)
		atomicutil.UpdateMax(&e.decodeMaxNano, decDur)
		if err == nil {
			decodedYUV = yuv
			decW = w
			decH = h
			decodeOK = true
			e.log.Debug("decoded frame", "source", sourceKey, "isFrom", isFrom, "w", w, "h", h)
		} else {
			e.log.Debug("decode failed", "source", sourceKey, "err", err, "dataLen", len(wireData))
		}
	}

	// --- Phase 3 (lock): store decoded YUV, blend, check auto-complete ---
	e.mu.Lock()
	if e.state != StateActive {
		e.mu.Unlock()
		return
	}

	if decodeOK {
		if e.width == 0 {
			e.width = decW
			e.height = decH
			blender, blenderErr := NewFrameBlender(decW, decH)
			if blenderErr != nil {
				e.log.Warn("skipping frame: failed to create blender", "err", blenderErr, "w", decW, "h", decH)
				e.width = 0
				e.height = 0
				e.mu.Unlock()
				return
			}
			e.blender = blender

			// Resolve gradient map now that dimensions are known.
			e.resolveWipeGradient()
		}

		// Scale if resolution doesn't match the target.
		// This should no longer trigger now that per-source normalization scales at
		// the decoder output. Kept as a defensive fallback.
		if decW != e.width || decH != e.height {
			slog.Debug("transition: resolution mismatch, scaling",
				"source_w", decW, "source_h", decH, "target_w", e.width, "target_h", e.height)
			targetSize := e.colorFormat.FrameSize(e.width, e.height)
			if e.scaleBuf == nil || len(e.scaleBuf) < targetSize {
				e.scaleBuf = make([]byte, targetSize)
			}
			if e.colorFormat == video.YUV422_10bit {
				_ = ScaleYUV422P10(decodedYUV, decW, decH, e.scaleBuf, e.width, e.height)
			} else {
				ScaleYUV420WithQuality(decodedYUV, decW, decH, e.scaleBuf, e.width, e.height, ScaleQualityFast)
			}
			decodedYUV = e.scaleBuf[:targetSize]
			decW = e.width
			decH = e.height
		}

		// Deep-copy because the decoder may reuse its internal buffer.
		yuvSize := e.colorFormat.FrameSize(decW, decH)
		if isFrom {
			if len(e.latestYUVA) != yuvSize {
				e.latestYUVA = make([]byte, yuvSize)
			}
			copy(e.latestYUVA, decodedYUV)
		} else {
			if len(e.latestYUVB) != yuvSize {
				e.latestYUVB = make([]byte, yuvSize)
			}
			copy(e.latestYUVB, decodedYUV)
		}
	}

	// Blend, output, and check for auto-completion (shared with IngestRawFrame).
	e.blendAndOutput(isFrom, pts)
}

// IngestRawFrame accepts a pre-decoded YUV420 frame (e.g., from MXL sources).
// Skips H.264 decode — stores YUV directly and triggers blend. The frame is
// scaled to the engine's resolution if dimensions don't match.
func (e *Engine) IngestRawFrame(sourceKey string, yuv []byte, width, height int, pts int64) {
	ingestStart := time.Now()
	defer func() {
		dur := time.Since(ingestStart).Nanoseconds()
		e.ingestLastNano.Store(dur)
		atomicutil.UpdateMax(&e.ingestMaxNano, dur)
	}()
	e.framesIngested.Add(1)

	e.mu.Lock()
	if e.state != StateActive {
		e.mu.Unlock()
		return
	}

	isFrom := sourceKey == e.fromSource
	isTo := sourceKey == e.toSource
	if !isFrom && !isTo {
		e.mu.Unlock()
		return
	}

	e.lastFrameAt = time.Now()

	// Lazy-init blender from this frame's dimensions if not yet set.
	if e.width == 0 {
		e.width = width
		e.height = height
		blender, blenderErr := NewFrameBlender(width, height)
		if blenderErr != nil {
			e.log.Warn("skipping frame: failed to create blender", "err", blenderErr, "w", width, "h", height)
			e.width = 0
			e.height = 0
			e.mu.Unlock()
			return
		}
		e.blender = blender
	}

	// Scale to engine resolution if needed. This should no longer trigger now that
	// per-source normalization scales at the decoder output. Kept as a defensive fallback.
	src := yuv
	if width != e.width || height != e.height {
		slog.Debug("transition: resolution mismatch, scaling",
			"source_w", width, "source_h", height, "target_w", e.width, "target_h", e.height)
		targetSize := e.colorFormat.FrameSize(e.width, e.height)
		if e.scaleBuf == nil || len(e.scaleBuf) < targetSize {
			e.scaleBuf = make([]byte, targetSize)
		}
		if e.colorFormat == video.YUV422_10bit {
			_ = ScaleYUV422P10(yuv, width, height, e.scaleBuf, e.width, e.height)
		} else {
			ScaleYUV420WithQuality(yuv, width, height, e.scaleBuf, e.width, e.height, ScaleQualityFast)
		}
		src = e.scaleBuf[:targetSize]
	}

	// Store directly — no decode needed. Deep-copy since caller may reuse buffer.
	yuvSize := e.colorFormat.FrameSize(e.width, e.height)
	if isFrom {
		if len(e.latestYUVA) != yuvSize {
			e.latestYUVA = make([]byte, yuvSize)
		}
		copy(e.latestYUVA, src)
		e.needsFlushA = false // Raw YUV — no decoder to flush
	} else {
		if len(e.latestYUVB) != yuvSize {
			e.latestYUVB = make([]byte, yuvSize)
		}
		copy(e.latestYUVB, src)
		e.needsFlushB = false
	}

	// Blend, output, and check for auto-completion (shared with IngestFrame).
	e.blendAndOutput(isFrom, pts)
}

// blendAndOutput performs blend triggering, blending, auto-completion, and
// output. Shared by IngestFrame and IngestRawFrame. Caller must hold e.mu.
// Unlocks e.mu before returning (output callbacks run outside the lock).
func (e *Engine) blendAndOutput(isFrom bool, pts int64) {
	// Determine if we should trigger blend+output.
	// For Mix/Dip/Wipe/Stinger: triggered by incoming TO source frame.
	// For FTB/FTBReverse: triggered by FROM source frame (no toSource).
	// FROM source frames still store YUV in latestYUVA (above) so the blend
	// function has access to both sources. Triggering only on TO keeps output
	// rate at 1x (matching source frame rate) and prevents 2x PTS advancement
	// that causes persistent A/V desync.
	shouldBlend := false
	if (e.transitionType == FTB || e.transitionType == FTBReverse) && isFrom {
		shouldBlend = true
	} else if !isFrom { // isTo
		shouldBlend = true
	}

	if !shouldBlend {
		e.mu.Unlock()
		return
	}

	// SkipBlend mode: GPU handles the pixel blend, we only track state.
	// Position is calculated but auto-complete is NOT triggered here —
	// the GPU transition path in handleRawVideoFrame calls ForceComplete()
	// AFTER the GPU blend frame is produced, ensuring the last blended
	// frame reaches the encoder before the transition completes.
	if e.config.SkipBlend {
		e.framesBlended.Add(1)
		e.mu.Unlock()
		return
	}

	if e.blender == nil {
		e.mu.Unlock()
		return
	}

	// Resolve source frames. For non-FTB transitions, skip this blend if
	// source A (from/program) hasn't arrived yet rather than substituting a
	// black frame which causes a visible flash. The viewer sees the last
	// pipeline frame for at most one frame period until source A arrives.
	yuvA := e.latestYUVA
	yuvB := e.latestYUVB
	if yuvA == nil {
		if e.transitionType != FTB && e.transitionType != FTBReverse {
			e.mu.Unlock()
			return
		}
		yuvA = e.getBlackFrame()
	}
	if yuvB == nil {
		yuvB = e.getBlackFrame()
	}

	pos := e.currentPosition()

	if e.config.Render != nil {
		e.submitRender(yuvA, yuvB, pos, pts)
		return
	}

	blendStart := time.Now()
	var blended []byte
	if e.colorFormat == video.YUV422_10bit {
		blended = e.blend10bit(yuvA, yuvB, pos)
	} else if blended == nil {
		switch e.transitionType {
		case Mix:
			blended = e.blender.BlendMix(yuvA, yuvB, pos)
		case Dip:
			blended = e.blender.BlendDip(yuvA, yuvB, pos)
		case FTB:
			blended = e.blender.BlendFTB(yuvA, pos)
		case FTBReverse:
			blended = e.blender.BlendFTB(yuvA, 1.0-pos)
		case Wipe:
			if e.wipeConfig != nil && e.wipeGradient != nil {
				blended = e.blender.BlendWipeMap(yuvA, yuvB, pos, e.wipeGradient, *e.wipeConfig)
			} else {
				blended = e.blender.BlendWipe(yuvA, yuvB, pos, e.wipeDirection)
			}
		case Stinger:
			blended = e.blendStinger(pos)
		default:
			blended = yuvA
		}
	}
	blendDur := time.Since(blendStart).Nanoseconds()
	e.blendLastNano.Store(blendDur)
	atomicutil.UpdateMax(&e.blendMaxNano, blendDur)
	e.framesBlended.Add(1)

	isKF := !e.firstOutput
	e.firstOutput = true
	e.log.Debug("blended", "pos", fmt.Sprintf("%.3f", pos), "w", e.width, "h", e.height)

	var autoComplete bool
	if !e.manualControl && pos >= 1.0 {
		autoComplete = true
		e.log.Info("auto-complete", "type", e.transitionType)
		e.cleanup()
	}

	w, h := e.width, e.height
	e.mu.Unlock()

	// Output callback and completion run outside the lock.
	if e.config.Output != nil && blended != nil {
		e.config.Output(blended, w, h, pts, isKF)
	}

	if autoComplete && e.config.OnComplete != nil {
		e.config.OnComplete(false)
	}
}

// submitRender hands a frame to the render goroutine, starting it on the
// first frame, and completes the transition when the position reaches the
// end: the goroutine then draws that frame, outputs it and calls
// OnComplete. Caller must hold e.mu; submitRender releases it.
func (e *Engine) submitRender(yuvA, yuvB []byte, pos float64, pts int64) {
	f := video.YUV420_8bit
	if e.colorFormat == video.YUV422_10bit {
		f = video.YUV422_10bit
	}
	if e.render == nil {
		e.render = newAsyncRender(e, e.transitionType)
	}
	isKF := !e.firstOutput
	e.firstOutput = true
	final := !e.manualControl && pos >= 1.0
	e.render.submit(yuvA, yuvB, e.width, e.height, f, pos, pts, isKF, final)
	if final {
		e.log.Info("auto-complete", "type", e.transitionType)
		// The goroutine finishes the transition; cleanup must not stop it.
		e.render = nil
		e.cleanup()
	}
	e.mu.Unlock()
}

// timedRender calls config.Render and logs the first call of the engine
// that takes longer than a frame period.
func (e *Engine) timedRender(ttype Type, dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
	start := time.Now()
	ok := e.config.Render(dst, from, to, w, h, f, pos)
	period := e.config.FramePeriod
	if took := time.Since(start); ok && period > 0 && took > period && !e.overrunNoted.Swap(true) {
		e.log.Warn("transition render slower than the frame rate",
			"type", ttype, "took", took, "frame_period", period)
	}
	return ok
}

// WarmupDecode feeds a frame to the decoder for the given source side,
// populating latestYUVA/latestYUVB so the first live IngestFrame can
// produce blended output immediately. Produces no output callbacks.
// No-op if the engine is not active.
func (e *Engine) WarmupDecode(sourceKey string, wireData []byte) {
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

// WarmupComplete marks the end of warmup. Sets flush flags so that if the
// first live frame from either source is a keyframe, the decoder is flushed
// to discard stale warmup references before decoding the fresh IDR.
// No-op when decoders are nil (raw-only mode).
// ForceComplete triggers transition completion from the caller (GPU transition
// path). Used in SkipBlend mode where auto-complete is deferred so the GPU
// blend frame is produced BEFORE the transition state changes.
func (e *Engine) ForceComplete() {
	e.mu.Lock()
	if e.state != StateActive {
		e.mu.Unlock()
		return
	}
	e.log.Info("force-complete (GPU)", "type", e.transitionType)
	e.cleanup()
	e.mu.Unlock()

	if e.config.OnComplete != nil {
		e.config.OnComplete(false)
	}
}

func (e *Engine) WarmupComplete() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.needsFlushA = true
	e.needsFlushB = true
}

// getBlackFrame returns a reusable black frame matching the engine's
// dimensions and color format. Used as a placeholder when a source hasn't
// produced decoded output yet. BT.709 limited range. Caller must hold e.mu.
func (e *Engine) getBlackFrame() []byte {
	if e.colorFormat == video.YUV422_10bit {
		return e.getBlackFrame10()
	}
	total := video.YUV420_8bit.FrameSize(e.width, e.height)
	if len(e.blackBuf) != total {
		e.blackBuf = make([]byte, total)
		video.FillBlack(e.blackBuf, video.YUV420_8bit, e.width, e.height)
	}
	return e.blackBuf
}

// getBlackFrame10 returns a reusable YUV422P10LE black frame.
// BT.709 limited range: Y=64, Cb=Cr=512 (10-bit values). Caller must hold e.mu.
func (e *Engine) getBlackFrame10() []byte {
	total := video.YUV422_10bit.FrameSize(e.width, e.height)
	if len(e.blackBuf) != total {
		e.blackBuf = make([]byte, total)
		video.FillBlack(e.blackBuf, video.YUV422_10bit, e.width, e.height)
	}
	return e.blackBuf
}

// blend10bit performs format-aware blending for 10-bit YUV422P10LE frames.
// Uses the 10-bit blend functions (BlendMix10, BlendDip10, etc.).
// Caller must hold e.mu.
func (e *Engine) blend10bit(yuvA, yuvB []byte, pos float64) []byte {
	w, h := e.width, e.height
	frameSize := video.YUV422_10bit.FrameSize(w, h)

	// Ensure output buffer exists. Reuse the blender's yuvBufOut for 10-bit output.
	if e.blender == nil {
		e.blender = &FrameBlender{width: w, height: h}
	}
	if len(e.blender.yuvBufOut) != frameSize {
		e.blender.yuvBufOut = make([]byte, frameSize)
	}
	dst := e.blender.yuvBufOut

	pos256 := int(pos * 256)
	if pos256 < 0 {
		pos256 = 0
	}
	if pos256 > 256 {
		pos256 = 256
	}

	switch e.transitionType {
	case Mix:
		_ = BlendMix10(dst, yuvA, yuvB, w, h, pos256)
	case Dip:
		_ = BlendDip10(dst, yuvA, yuvB, w, h, pos256, true)
	case FTB:
		_ = BlendFTB10(dst, yuvA, w, h, pos256, true)
	case FTBReverse:
		inv256 := 256 - pos256
		_ = BlendFTB10(dst, yuvA, w, h, inv256, true)
	case Wipe:
		// Generate wipe alpha map through the blender, then use 10-bit wipe.
		e.blender.width = w
		e.blender.height = h
		if len(e.blender.wipeAlphaMap) != w*h {
			e.blender.wipeAlphaMap = make([]byte, w*h)
		}
		if e.wipeConfig != nil && e.wipeGradient != nil {
			generateWipeMapAlpha(e.blender.wipeAlphaMap, e.wipeGradient, pos, *e.wipeConfig)
		} else {
			e.blender.generateWipeAlpha(pos, e.wipeDirection)
		}
		_ = BlendWipe10(dst, yuvA, yuvB, w, h, e.blender.wipeAlphaMap)
	case Stinger:
		sd := e.config.Stinger
		if sd == nil || len(sd.Frames) == 0 {
			copy(dst, yuvA)
		} else {
			// Lazy-scale 8-bit stinger frames if needed.
			if e.stingerScaled == nil {
				e.stingerScaled = e.scaleStingerFrames(sd)
			}

			// Pick frame by position.
			frameIdx := int(pos * float64(len(e.stingerScaled)))
			if frameIdx >= len(e.stingerScaled) {
				frameIdx = len(e.stingerScaled) - 1
			}
			if frameIdx < 0 {
				frameIdx = 0
			}
			sf := &e.stingerScaled[frameIdx]

			// Pick the base source: A before cut point, B after.
			base := yuvA
			if pos >= sd.CutPoint && yuvB != nil {
				base = yuvB
			}

			// Upconvert the 8-bit YUV420 stinger frame to 10-bit YUV422P10LE.
			// Reuse a cached buffer to avoid per-frame allocation.
			if e.stinger10bitBuf == nil || len(e.stinger10bitBuf) < frameSize {
				e.stinger10bitBuf = make([]byte, frameSize)
			}
			if err := upconvert420to422_10(sf.YUV, e.stinger10bitBuf, w, h); err != nil {
				// Upconvert failed — fall back to base frame.
				copy(dst, base)
			} else {
				_ = BlendStinger10(dst, base, e.stinger10bitBuf, w, h, sf.Alpha)
			}
		}
	default:
		copy(dst, yuvA)
	}
	return dst
}

// Abort cancels the active transition and invokes OnComplete(aborted=true).
// Safe to call from any goroutine. Idempotent — calling on an idle engine is a no-op.
func (e *Engine) Abort() {
	e.mu.Lock()
	if e.state != StateActive {
		e.mu.Unlock()
		return
	}
	e.log.Warn("aborted", "type", e.transitionType, "from", e.fromSource, "to", e.toSource)
	e.cleanup()
	e.mu.Unlock()

	if e.config.OnComplete != nil {
		e.config.OnComplete(true)
	}
}

// Stop tears down decoders and resets state.
func (e *Engine) Stop() {
	e.mu.Lock()
	e.cleanup()
	e.mu.Unlock()
}

// runWatchdog periodically checks for frame starvation. If no frames have
// arrived within the configured timeout, it aborts the transition.
// Exits when the stop channel is closed.
func (e *Engine) runWatchdog(stop chan struct{}, timeout time.Duration) {
	interval := timeout / 4
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			e.mu.RLock()
			active := e.state == StateActive
			elapsed := time.Since(e.lastFrameAt)
			e.mu.RUnlock()

			if active && elapsed > timeout {
				e.log.Warn("watchdog timeout — no frames received",
					"timeout", timeout, "elapsed", elapsed)
				e.Abort()
				return
			}
		}
	}
}

// blendStinger composites the stinger overlay over source A (before cut point)
// or source B (after cut point). Caller must hold e.mu.
func (e *Engine) blendStinger(pos float64) []byte {
	sd := e.config.Stinger
	if sd == nil || len(sd.Frames) == 0 {
		// Fallback: if no stinger data, do a hard cut at the cut point.
		// Deep-copy because the caller releases the lock before using the result.
		if pos >= 0.5 {
			cp := make([]byte, len(e.latestYUVB))
			copy(cp, e.latestYUVB)
			return cp
		}
		cp := make([]byte, len(e.latestYUVA))
		copy(cp, e.latestYUVA)
		return cp
	}

	// Lazy-scale stinger frames to match video dimensions on first use
	if e.stingerScaled == nil {
		e.stingerScaled = e.scaleStingerFrames(sd)
	}

	// Pick the stinger frame based on position
	frameIdx := int(pos * float64(len(e.stingerScaled)))
	if frameIdx >= len(e.stingerScaled) {
		frameIdx = len(e.stingerScaled) - 1
	}
	if frameIdx < 0 {
		frameIdx = 0
	}
	sf := &e.stingerScaled[frameIdx]

	// Pick the base source: A before cut point, B after
	base := e.latestYUVA
	if pos >= sd.CutPoint && e.latestYUVB != nil {
		base = e.latestYUVB
	}

	return e.blender.BlendStinger(base, sf.YUV, sf.Alpha)
}

// scaleStingerFrames scales stinger frames to match the engine's video dimensions.
// If dimensions already match, returns the original frames. Caller must hold e.mu.
func (e *Engine) scaleStingerFrames(sd *StingerData) []StingerFrameData {
	if sd.Width == e.width && sd.Height == e.height {
		return sd.Frames
	}

	e.log.Info("scaling stinger frames",
		"from", fmt.Sprintf("%dx%d", sd.Width, sd.Height),
		"to", fmt.Sprintf("%dx%d", e.width, e.height))

	scaled := make([]StingerFrameData, len(sd.Frames))
	targetYSize := e.width * e.height
	targetUVSize := (e.width / 2) * (e.height / 2)
	targetYUVSize := targetYSize + 2*targetUVSize

	for i, f := range sd.Frames {
		// Scale YUV
		scaledYUV := make([]byte, targetYUVSize)
		ScaleYUV420WithQuality(f.YUV, sd.Width, sd.Height, scaledYUV, e.width, e.height, ScaleQualityFast)

		// Bilinear must match the YUV scaler — nearest zippers the alpha edge.
		scaledAlpha := make([]byte, targetYSize)
		scalePlane(f.Alpha, sd.Width, sd.Height, scaledAlpha, e.width, e.height)

		scaled[i] = StingerFrameData{YUV: scaledYUV, Alpha: scaledAlpha}
	}
	return scaled
}

// StartSync configures the engine for synchronous blending (no goroutines).
// Sets transition type, duration, and direction but does NOT spawn any
// goroutines — no watchdog, no decoders, no codec pipeline. This is the
// StartSync initializes a transition for the reference-clock path.
// Unlike Start(), it does NOT use wall-clock timing — the caller drives
// position via AutoAdvancePosition. No watchdog goroutine is started
// because the sync loop handles completion directly.
func (e *Engine) StartSync(from, to string, ttype Type, durationMs int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.transitionType = ttype
	e.wipeDirection = e.config.WipeDirection
	e.easing = e.config.Easing
	e.durationMs = durationMs
	e.position = 0
	e.state = StateActive
	e.manualControl = false
	e.manualPosition = 0
	e.fromSource = from
	e.toSource = to
	e.latestYUVA = nil
	e.latestYUVB = nil

	// Initialize blender from hint dimensions.
	if e.config.HintWidth > 0 && e.config.HintHeight > 0 {
		e.width = e.config.HintWidth
		e.height = e.config.HintHeight
		blender, err := NewFrameBlender(e.width, e.height)
		if err == nil {
			e.blender = blender
		}
	}

	// Pre-resolve gradient map for wipe-map transitions at known dimensions.
	e.wipeConfig = e.config.WipeConfig
	e.wipeGradient = nil
	e.resolveWipeGradient()

	// Pre-scale stinger frames eagerly.
	e.stingerScaled = nil
	if ttype == Stinger && e.config.Stinger != nil && len(e.config.Stinger.Frames) > 0 && e.width > 0 {
		e.stingerScaled = e.scaleStingerFrames(e.config.Stinger)
	}

	e.log.Info("engine started (sync)", "type", ttype, "from", from, "to", to, "durationMs", durationMs)
}

// AutoAdvancePosition advances the transition position by one tick based on
// frame duration. Returns the eased position and whether the transition is
// complete (linear t >= 1.0). On completion, the engine state is set to Idle.
// This is the reference-clock path — no wall-clock timing, purely tick-driven.
//
// The internal e.position tracks linear progress (0→1). The returned value
// has the easing curve applied, matching what currentPosition() produces
// in the wall-clock path.
func (e *Engine) AutoAdvancePosition(frameDurationMs float64) (float64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.durationMs <= 0 {
		e.state = StateIdle
		return 1.0, true
	}
	e.position += frameDurationMs / float64(e.durationMs)
	if e.position >= 1.0 {
		e.position = 1.0
		e.state = StateIdle
		return 1.0, true
	}
	// Apply easing curve to linear position, matching currentPosition().
	t := e.position
	var pos float64
	if e.easing != nil {
		pos = e.easing.Ease(t)
	} else {
		pos = t * t * (3.0 - 2.0*t) // smoothstep
	}
	return math.Max(0.0, math.Min(1.0, pos)), false
}

// BlendRaw performs a synchronous blend of two YUV420 frames at the given
// position. This is the reference-clock path — no goroutines, no internal
// timing, no codec pipeline.
//
// Returns the blended YUV420 data, output width, and output height.
// If frames have different dimensions, toYUV is scaled to match fromYUV.
// For FTB transitions, toYUV may be nil.
//
// The returned slice is freshly allocated per call; two successive calls
// yield independent slices. This contract is pinned by
// TestBlendRawReturnsCopy — a future refactor that pools the result into an
// engine field would break it. Callers on the per-frame hot path should use
// BlendRawInto with a reusable dst buffer to avoid the ~3 MB per-frame
// allocation for the result.
func (e *Engine) BlendRaw(
	fromYUV []byte, fromW, fromH int,
	toYUV []byte, toW, toH int,
	position float64,
) ([]byte, int, int) {
	return e.BlendRawInto(nil, fromYUV, fromW, fromH, toYUV, toW, toH, position)
}

// BlendRawInto is the scratch-reuse variant of BlendRaw. The blended frame
// is written into dst (grown if necessary, reslice-preserving any existing
// capacity) and the grown slice is returned. Pass nil for a freshly allocated
// result; pass a caller-owned buffer to reuse across frames and avoid the
// 3–9 MB/frame allocation pattern on resolution-mismatched stinger paths.
//
// Single-caller invariant: BlendRawInto uses engine-scoped scratch buffers
// for the intermediate scaled frames. The transition state machine
// serializes ticks per engine, so the scratch is safe without additional
// locking beyond what BlendRaw already takes for the blender cache.
func (e *Engine) BlendRawInto(
	dst []byte,
	fromYUV []byte, fromW, fromH int,
	toYUV []byte, toW, toH int,
	position float64,
) ([]byte, int, int) {
	// Serialize scratch access across concurrent callers. Today only the
	// transition state machine calls this (single-threaded per engine),
	// so the lock is uncontended.
	e.blendRawMu.Lock()
	defer e.blendRawMu.Unlock()

	e.mu.Lock()
	ttype := e.transitionType
	cf := video.YUV420_8bit
	if e.colorFormat == video.YUV422_10bit {
		cf = video.YUV422_10bit
	}
	wipeDir := e.wipeDirection
	stinger := e.config.Stinger

	outW, outH := fromW, fromH

	// Ensure even dimensions for YUV420
	if outW%2 != 0 || outH%2 != 0 {
		e.mu.Unlock()
		return nil, 0, 0
	}

	// Reuse cached blender if resolution matches, otherwise create new (Issue 7).
	blender := e.rawBlender
	if blender == nil || blender.width != outW || blender.height != outH {
		var err error
		blender, err = NewFrameBlender(outW, outH)
		if err != nil {
			e.mu.Unlock()
			return nil, 0, 0
		}
		e.rawBlender = blender
	}
	e.mu.Unlock()

	// Scale toYUV to match fromYUV dimensions if they differ. Scratch buffer
	// lives on the engine; grown on first use and reused across frames.
	if toYUV != nil && (toW != fromW || toH != fromH) {
		targetSize := fromW * fromH * 3 / 2
		e.rawScaleToBuf = growBuf(e.rawScaleToBuf, targetSize)
		e.rawXCoordsFullW = growCoords(e.rawXCoordsFullW, fromW)
		e.rawXCoordsHalfW = growCoords(e.rawXCoordsHalfW, fromW/2)
		scaleYUV420WithCoords(toYUV, toW, toH, e.rawScaleToBuf, fromW, fromH, e.rawXCoordsFullW, e.rawXCoordsHalfW)
		toYUV = e.rawScaleToBuf
	}

	// Generate broadcast-standard black frame for FTB / missing source B
	makeBlack := func() []byte {
		return video.NewBlackFrame(video.YUV420_8bit, outW, outH)
	}

	// Guard against nil/empty fromYUV — can happen during source flapping
	// when the freeze-repeat frame's buffer was recycled.
	if len(fromYUV) == 0 {
		fromYUV = makeBlack()
	}

	if e.config.Render != nil {
		size := cf.FrameSize(outW, outH)
		// No pacing here: the caller owns dst and processes it in place
		// after this returns, so a repeat would hand back a processed
		// frame. The sync loop paces its own ticks.
		dst = growBuf(dst, size)
		if e.timedRender(ttype, dst, fromYUV, toYUV, outW, outH, cf, position) {
			return dst, outW, outH
		}
	}

	var blended []byte
	switch ttype {
	case Mix:
		if toYUV == nil {
			toYUV = makeBlack()
		}
		blended = blender.BlendMix(fromYUV, toYUV, position)
	case Dip:
		if toYUV == nil {
			toYUV = makeBlack()
		}
		blended = blender.BlendDip(fromYUV, toYUV, position)
	case FTB:
		blended = blender.BlendFTB(fromYUV, position)
	case FTBReverse:
		blended = blender.BlendFTB(fromYUV, 1.0-position)
	case Wipe:
		if toYUV == nil {
			toYUV = makeBlack()
		}
		if e.wipeConfig != nil && e.wipeGradient != nil {
			blended = blender.BlendWipeMap(fromYUV, toYUV, position, e.wipeGradient, *e.wipeConfig)
		} else {
			blended = blender.BlendWipe(fromYUV, toYUV, position, wipeDir)
		}
	case Stinger:
		if stinger != nil && len(stinger.Frames) > 0 {
			frameIdx := int(position * float64(len(stinger.Frames)))
			if frameIdx >= len(stinger.Frames) {
				frameIdx = len(stinger.Frames) - 1
			}
			if frameIdx < 0 {
				frameIdx = 0
			}
			sf := &stinger.Frames[frameIdx]

			// Pick base source: A before cut point, B after
			base := fromYUV
			if position >= stinger.CutPoint && toYUV != nil {
				base = toYUV
			}

			// Scale stinger frame if needed — reuse engine scratch.
			sYUV := sf.YUV
			sAlpha := sf.Alpha
			if stinger.Width != outW || stinger.Height != outH {
				scaledYUVSize := outW * outH * 3 / 2
				scaledAlphaSize := outW * outH
				e.rawScaleSBuf = growBuf(e.rawScaleSBuf, scaledYUVSize)
				e.rawAlphaScaleBuf = growBuf(e.rawAlphaScaleBuf, scaledAlphaSize)
				e.rawXCoordsFullW = growCoords(e.rawXCoordsFullW, outW)
				e.rawXCoordsHalfW = growCoords(e.rawXCoordsHalfW, outW/2)
				scaleYUV420WithCoords(sf.YUV, stinger.Width, stinger.Height, e.rawScaleSBuf, outW, outH, e.rawXCoordsFullW, e.rawXCoordsHalfW)
				// Bilinear must match the YUV scaler — nearest zippers the alpha edge.
				// Reuse rawXCoordsFullW: both Y and alpha use dstW = outW, and
				// scaleYUV420WithCoords already finished above.
				scalePlaneWithCoords(sf.Alpha, stinger.Width, stinger.Height, e.rawAlphaScaleBuf, outW, outH, e.rawXCoordsFullW)
				sYUV = e.rawScaleSBuf
				sAlpha = e.rawAlphaScaleBuf
			}
			blended = blender.BlendStinger(base, sYUV, sAlpha)
		} else {
			// No stinger data: hard cut at 0.5
			if position >= 0.5 && toYUV != nil {
				blended = toYUV
			} else {
				blended = fromYUV
			}
		}
	default:
		blended = fromYUV
	}

	if blended == nil {
		return nil, 0, 0
	}

	// Copy the blender's internal-buffer result into the caller's dst so
	// the slice is stable across the next BlendRaw call. Grow dst if too
	// small; reuse its capacity otherwise.
	dst = growBuf(dst, len(blended))
	copy(dst, blended)
	return dst, outW, outH
}

// growBuf returns a slice of at least n bytes, reusing buf's capacity when
// possible. If cap(buf) >= n it reslices; otherwise allocates a fresh slice
// of length n. Zero-allocation in steady state once the caller's buffer has
// grown to the target size.
func growBuf(buf []byte, n int) []byte {
	if cap(buf) < n {
		return make([]byte, n)
	}
	return buf[:n]
}

// growCoords is growBuf for []int64 scaler-coord tables.
func growCoords(buf []int64, n int) []int64 {
	if cap(buf) < n {
		return make([]int64, n)
	}
	return buf[:n]
}

// cleanup releases codec resources and resets state. Caller must hold e.mu.
func (e *Engine) cleanup() {
	// Stop the render goroutine, dropping any frame still waiting.
	if e.render != nil {
		e.render.shutdown()
		e.render = nil
	}

	// Stop watchdog goroutine (idempotent via sync.Once)
	if e.watchdogStop != nil {
		e.watchdogOnce.Do(func() {
			close(e.watchdogStop)
		})
	}

	// Wait for any in-flight Phase 2 decodes to finish before closing
	// decoders. Release the lock briefly so blocked decoders can proceed
	// to Phase 3 (which checks state != StateActive and returns early).
	// Set cleaning=true to prevent Start() from succeeding during the window.
	e.state = StateIdle // prevents new Phase 3 work
	e.cleaning = true
	e.mu.Unlock()
	e.decodeWG.Wait()

	// Release BlendRawInto scratch so a tear-down at 4K doesn't pin ~12 MB
	// of retained memory across the next (potentially smaller) transition.
	// This must happen while e.mu is NOT held: BlendRawInto acquires
	// blendRawMu then mu, so taking blendRawMu with mu held is an ABBA
	// lock-order inversion that deadlocks against a concurrent blend tick.
	e.blendRawMu.Lock()
	e.rawScaleToBuf = nil
	e.rawScaleSBuf = nil
	e.rawAlphaScaleBuf = nil
	e.rawXCoordsFullW = nil
	e.rawXCoordsHalfW = nil
	e.blendRawMu.Unlock()

	e.mu.Lock()
	e.cleaning = false

	if e.decoderA != nil {
		e.decoderA.Close()
		e.decoderA = nil
	}
	if e.decoderB != nil {
		e.decoderB.Close()
		e.decoderB = nil
	}
	e.state = StateIdle
	e.position = 0
	e.manualPosition = 0
	e.manualControl = false
	e.easing = nil
	e.firstOutput = false
	e.latestYUVA = nil
	e.latestYUVB = nil
	e.blender = nil
	e.scaleBuf = nil
	e.stingerScaled = nil
}
