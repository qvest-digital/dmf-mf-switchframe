package arinfer

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// depthModel is the internal interface for depth estimation models.
// Real implementations are provided by Tasks 3-4.
type depthModel interface {
	// Infer runs depth estimation on the input GPU frame and writes
	// the depth map to outputPtr.
	Infer(inputPtr uintptr, w, h, pitch int, outputPtr uintptr) error

	// OutputPtr returns the CUDA device pointer to the model's internal
	// output buffer (float16 depth map at model native resolution).
	OutputPtr() uintptr

	// Close releases model resources.
	Close()
}

// samModel is the internal interface for SAM 2 segmentation models.
// Real implementations are provided by Tasks 3-4.
type samModel interface {
	// Encode runs the SAM image encoder on the input GPU frame.
	Encode(inputPtr uintptr, w, h, pitch int) error

	// Propagate runs mask propagation and writes the result to outputPtr.
	Propagate(inputPtr uintptr, w, h, pitch int, outputPtr uintptr) error

	// MaskOutputPtr returns the CUDA device pointer to the model's internal
	// mask output buffer (uint8 binary mask at frame resolution).
	MaskOutputPtr() uintptr

	// AddPrompt registers a new segmentation prompt.
	AddPrompt(p SAMPrompt) error

	// RemovePrompt removes a prompt by ID.
	RemovePrompt(id string) error

	// Prompts returns all active prompts.
	Prompts() []SAMPrompt

	// Close releases model resources.
	Close()
}

// ProcessFrameHook is a callback type for processing program frames through
// the inference engine. The app layer sets this hook and the switcher's
// program frame callback invokes it to feed frames to depth/SAM inference.
type ProcessFrameHook func(framePtr uintptr, w, h, pitch int)

// Engine runs depth estimation and segmentation on a dedicated CUDA stream.
// It manages the model lifecycle and produces OcclusionMaps consumed by the
// AR compositor pipeline node.
//
// Lock ordering: Engine.mu must be acquired before sam2Model.mu. Never acquire
// sam2Model.mu without holding Engine.mu first to prevent deadlocks.
type Engine struct {
	cfg Config

	mu      sync.RWMutex
	depth   depthModel  // nil until depth model is loaded
	sam     samModel    // nil until SAM model is loaded
	prompts []SAMPrompt // active SAM prompts

	// depthW and depthH store the depth model's native resolution,
	// set when EnableDepth is called. Used in ProcessFrame to populate
	// OcclusionMap.DepthW/DepthH without hardcoding.
	depthW int
	depthH int

	// samEncoded tracks whether the SAM image encoder has been run
	// on the current scene. Set to true after the first successful
	// Encode() call; reset when SAM is disabled or re-enabled.
	samEncoded bool

	lastOcclusion atomic.Pointer[OcclusionMap]

	// activeFrames tracks how many ProcessFrame calls are in-flight.
	// DisableDepth/DisableSAM wait for this to reach 0 before closing models.
	activeFrames sync.WaitGroup

	framesProcessed atomic.Int64
	framesSkipped   atomic.Int64
	depthLatencyMs  atomic.Value // stores float64
	samLatencyMs    atomic.Value // stores float64

	closed atomic.Bool
}

// New creates an inference engine with the given configuration.
// Models are not loaded until explicitly enabled; the engine starts
// in a dormant state.
func New(cfg Config) *Engine {
	if cfg.MaxInferenceMs == 0 {
		cfg.MaxInferenceMs = 20
	}

	e := &Engine{
		cfg: cfg,
	}

	// Initialize atomic values to zero float64.
	e.depthLatencyMs.Store(float64(0))
	e.samLatencyMs.Store(float64(0))

	return e
}

// Hook returns a ProcessFrameHook that calls ProcessFrame on the engine.
// The returned hook is safe to call from the switcher's program frame
// callback to feed video frames into the inference pipeline.
// Wire into the switcher's program frame callback for live inference.
func (e *Engine) Hook() ProcessFrameHook {
	return e.ProcessFrame
}

// IsActive returns true if any inference model is loaded.
// Cheaper than Status() — suitable for hot-path checks.
func (e *Engine) IsActive() bool {
	e.mu.RLock()
	active := e.depth != nil || e.sam != nil
	e.mu.RUnlock()
	return active
}

// Status returns a snapshot of the engine's current state.
func (e *Engine) Status() Status {
	e.mu.RLock()
	depthReady := e.depth != nil
	samReady := e.sam != nil
	e.mu.RUnlock()

	depthLat, _ := e.depthLatencyMs.Load().(float64)
	samLat, _ := e.samLatencyMs.Load().(float64)

	return Status{
		DepthReady:      depthReady,
		SAMReady:        samReady,
		DepthLatencyMs:  depthLat,
		SAMLatencyMs:    samLat,
		FramesProcessed: e.framesProcessed.Load(),
		FramesSkipped:   e.framesSkipped.Load(),
		// GPUMemUsedMB will be populated when real models are loaded.
	}
}

// LastOcclusion returns the most recently computed occlusion map,
// or nil if no inference has been performed yet.
func (e *Engine) LastOcclusion() *OcclusionMap {
	return e.lastOcclusion.Load()
}

// ProcessFrame runs depth + SAM inference on the current program frame.
// Called from the switcher's frame routing with the program frame's GPU pointer.
// Non-blocking when running on a dedicated CUDA stream.
func (e *Engine) ProcessFrame(framePtr uintptr, w, h, pitch int) {
	if e.closed.Load() {
		return
	}

	e.activeFrames.Add(1)
	defer e.activeFrames.Done()

	// S2: Copy interface values under the lock, then call methods on the copies.
	e.mu.RLock()
	depth := e.depth
	sam := e.sam
	hasPrompts := len(e.prompts) > 0
	samEncoded := e.samEncoded
	dw := e.depthW
	dh := e.depthH
	e.mu.RUnlock()

	hasDepth := depth != nil
	hasSAM := sam != nil && hasPrompts

	if !hasDepth && !hasSAM {
		return // nothing to do
	}

	start := time.Now()

	occ := &OcclusionMap{
		Width:  w,
		Height: h,
	}

	// Run depth estimation.
	// Pass 0 as outputPtr — the model writes to its internal buffer.
	// We read the buffer address via OutputPtr() and store it in the OcclusionMap.
	if hasDepth {
		depthStart := time.Now()
		if err := depth.Infer(framePtr, w, h, pitch, 0); err != nil {
			e.framesSkipped.Add(1)
			return // use previous frame's occlusion map
		}
		occ.DepthPtr = depth.OutputPtr()
		occ.DepthW = dw
		occ.DepthH = dh
		depthElapsed := float64(time.Since(depthStart).Microseconds()) / 1000.0
		e.depthLatencyMs.Store(depthElapsed)
	}

	// Run SAM inference.
	// On the first frame (or after re-enable), run the encoder to produce
	// image embeddings. Then run propagation to produce the mask.
	if hasSAM {
		samStart := time.Now()

		// Encode on first frame to produce image embeddings.
		if !samEncoded {
			if err := sam.Encode(framePtr, w, h, pitch); err != nil {
				// Encoder failure is non-fatal; skip SAM this frame.
			} else {
				e.mu.Lock()
				e.samEncoded = true
				e.mu.Unlock()
				samEncoded = true
			}
		}

		if samEncoded {
			if err := sam.Propagate(framePtr, w, h, pitch, 0); err != nil {
				// Continue with depth-only occlusion; SAM failure is non-fatal.
			} else {
				occ.MaskPtr = sam.MaskOutputPtr()
			}
		}
		samElapsed := float64(time.Since(samStart).Microseconds()) / 1000.0
		e.samLatencyMs.Store(samElapsed)
	}

	// Store result atomically.
	e.lastOcclusion.Store(occ)
	e.framesProcessed.Add(1)
	_ = start // total timing available for future metrics
}

// EnableDepth loads and enables the depth estimation model.
// planPath is the path to the TensorRT .plan file. If empty,
// the engine auto-discovers the model in Config.ModelsDir.
// Returns nil if depth is already enabled (idempotent).
func (e *Engine) EnableDepth(planPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.depth != nil {
		return nil // already enabled
	}

	// Auto-discover model path from ModelsDir when not explicitly provided.
	if planPath == "" && e.cfg.ModelsDir != "" {
		planPath = filepath.Join(e.cfg.ModelsDir, "depth_anything_v2.plan")
	}

	const depthW, depthH = 518, 294
	model, err := newDepthAnythingV2(planPath, depthW, depthH)
	if err != nil {
		return fmt.Errorf("enable depth: %w", err)
	}
	e.depth = model
	e.depthW = depthW
	e.depthH = depthH
	return nil
}

// DisableDepth tears down the depth estimation model and clears stale data.
// Safe to call when depth is already disabled (no-op).
func (e *Engine) DisableDepth() {
	// Wait for any in-flight ProcessFrame calls to finish before closing
	// the model, preventing use-after-free on GPU resources.
	e.activeFrames.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.depth != nil {
		e.depth.Close()
		e.depth = nil
	}
	e.lastOcclusion.Store(nil) // clear stale data
}

// EnableSAM loads and enables the SAM 2 segmentation model.
// encoderPlanPath and decoderPlanPath are paths to the TensorRT .plan files.
// If empty, the engine auto-discovers models in Config.ModelsDir.
// Returns nil if SAM is already enabled (idempotent).
func (e *Engine) EnableSAM(encoderPlanPath, decoderPlanPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sam != nil {
		return nil // already enabled
	}

	// Auto-discover model paths from ModelsDir when not explicitly provided.
	if encoderPlanPath == "" && e.cfg.ModelsDir != "" {
		encoderPlanPath = filepath.Join(e.cfg.ModelsDir, "sam2_encoder.plan")
	}
	if decoderPlanPath == "" && e.cfg.ModelsDir != "" {
		decoderPlanPath = filepath.Join(e.cfg.ModelsDir, "sam2_decoder.plan")
	}

	model, err := newSAM2Model(encoderPlanPath, decoderPlanPath)
	if err != nil {
		return fmt.Errorf("enable SAM: %w", err)
	}
	e.sam = model
	e.samEncoded = false // reset so first ProcessFrame runs the encoder
	return nil
}

// DisableSAM tears down the SAM 2 segmentation model.
// Safe to call when SAM is already disabled (no-op).
func (e *Engine) DisableSAM() {
	// Wait for any in-flight ProcessFrame calls to finish before closing
	// the model, preventing use-after-free on GPU resources.
	e.activeFrames.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sam != nil {
		e.sam.Close()
		e.sam = nil
	}
	e.prompts = nil
	e.samEncoded = false
}

// AddPrompt registers a new segmentation prompt with the SAM model.
// Returns ErrNotReady if SAM is not enabled.
func (e *Engine) AddPrompt(p SAMPrompt) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sam == nil {
		return ErrNotReady
	}

	if err := e.sam.AddPrompt(p); err != nil {
		return err
	}
	e.prompts = e.sam.Prompts()
	return nil
}

// RemovePrompt removes a segmentation prompt by ID.
// Returns ErrNotReady if SAM is not enabled.
func (e *Engine) RemovePrompt(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.sam == nil {
		return ErrNotReady
	}

	if err := e.sam.RemovePrompt(id); err != nil {
		return err
	}
	e.prompts = e.sam.Prompts()
	return nil
}

// Prompts returns a copy of the active SAM prompts.
func (e *Engine) Prompts() []SAMPrompt {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.prompts) == 0 {
		return nil
	}

	out := make([]SAMPrompt, len(e.prompts))
	copy(out, e.prompts)
	return out
}

// Close shuts down the engine and releases all resources.
// Safe to call multiple times.
func (e *Engine) Close() {
	if !e.closed.CompareAndSwap(false, true) {
		return // already closed
	}

	e.activeFrames.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.depth != nil {
		e.depth.Close()
		e.depth = nil
	}
	if e.sam != nil {
		e.sam.Close()
		e.sam = nil
	}
}
