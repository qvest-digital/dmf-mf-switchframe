//go:build cgo && cuda && tensorrt

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t nv12_to_rgb_chw(
    float* rgbOut,
    const uint8_t* nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH,
    cudaStream_t stream);
cudaError_t mask_to_u8_upscale(
    uint8_t* dst, int dstW, int dstH,
    const float* src, int srcW, int srcH,
    cudaStream_t stream);
cudaError_t mask_ema(
    uint8_t* output,
    const uint8_t* prev,
    const uint8_t* curr,
    float alpha,
    int size,
    cudaStream_t stream);
cudaError_t mask_erode_3x3(
    uint8_t* dst,
    const uint8_t* src,
    int width, int height,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	// segModelW and segModelH are the input dimensions expected by u2netp.
	segModelW = 320
	segModelH = 320
)

// SegmentationEngine manages GPU-native person segmentation using TensorRT.
// Each enabled source gets a dedicated CUDA stream and pre-allocated device
// buffers. Inference runs asynchronously on the per-source stream, so it
// does not block the main pipeline stream.
//
// The engine supports deferred session creation: SetPendingConfig stores a
// config before the source resolution is known. IngestYUV (via the source
// manager) calls EnableSource with real dimensions when it detects a pending
// config with no active session.
type SegmentationEngine struct {
	ctx    *Context
	engine *TRTEngine

	mu             sync.RWMutex
	sessions       map[string]*segSession
	pendingConfigs map[string]float32 // key → smoothing value (from EdgeSmooth)
}

// segSession holds per-source TensorRT inference state.
type segSession struct {
	stream  C.cudaStream_t
	rgbBuf  unsafe.Pointer // [1,3,320,320] float32 device buffer
	maskBuf unsafe.Pointer // [1,1,320,320] float32 device buffer
	maskU8  unsafe.Pointer // [srcW*srcH*3/2] uint8 device buffer (NV12-sized: Y=mask, UV=128)
	erodeTmp unsafe.Pointer // [srcW*srcH] uint8 device buffer (erosion scratch)
	prevMask unsafe.Pointer // [srcW*srcH] uint8 device buffer (EMA temporal)
	trtCtx  *TRTContext

	smoothing float32
	hasPrev   bool
	srcW, srcH int

	// Latest mask, stored as atomic pointer for lock-free pipeline reads.
	mask atomic.Pointer[GPUFrame]

	// Dedicated mask pool: one frame per source, non-pooled (freed on disable).
	maskFrame *GPUFrame
}

// defaultModelCacheDir returns ~/.switchframe/models/ for plan caching.
func defaultModelCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".switchframe", "models")
}

// NewSegmentationEngine builds or loads a TensorRT engine from an ONNX model.
// The plan cache directory defaults to ~/.switchframe/models/.
func NewSegmentationEngine(ctx *Context, modelPath string) (*SegmentationEngine, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}
	if modelPath == "" {
		return nil, fmt.Errorf("gpu: segmentation: modelPath is empty")
	}

	// Determine plan cache path.
	cacheDir := defaultModelCacheDir()
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			slog.Warn("gpu: segmentation: failed to create model cache dir",
				"dir", cacheDir, "error", err)
			cacheDir = ""
		}
	}

	planPath := ""
	if cacheDir != "" {
		base := filepath.Base(modelPath)
		ext := filepath.Ext(base)
		planPath = filepath.Join(cacheDir, base[:len(base)-len(ext)]+".plan")
	}

	slog.Info("gpu: segmentation: building TensorRT engine",
		"onnx", modelPath, "plan_cache", planPath)

	engine, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	if err != nil {
		return nil, fmt.Errorf("gpu: segmentation: engine build: %w", err)
	}

	// Validate input/output sizes match u2netp model expectations.
	expectedInput := 1 * 3 * segModelW * segModelH  // [1,3,320,320]
	expectedOutput := 1 * 1 * segModelW * segModelH  // [1,1,320,320]
	if engine.InputSize() != expectedInput {
		engine.Close()
		return nil, fmt.Errorf("gpu: segmentation: unexpected input size: got %d, want %d",
			engine.InputSize(), expectedInput)
	}
	if engine.OutputSize() != expectedOutput {
		engine.Close()
		return nil, fmt.Errorf("gpu: segmentation: unexpected output size: got %d, want %d",
			engine.OutputSize(), expectedOutput)
	}

	slog.Info("gpu: segmentation: engine ready",
		"input_size", engine.InputSize(),
		"output_size", engine.OutputSize())

	return &SegmentationEngine{
		ctx:            ctx,
		engine:         engine,
		sessions:       make(map[string]*segSession),
		pendingConfigs: make(map[string]float32),
	}, nil
}

// EnableSource creates a segmentation session for the given source with
// dedicated CUDA stream and pre-allocated device buffers.
//
// smoothing controls the EMA temporal filter alpha (0.0 = no smoothing,
// higher = heavier smoothing, typical 0.3-0.7).
func (se *SegmentationEngine) EnableSource(key string, w, h int, smoothing float32) error {
	if se == nil {
		return ErrTensorRTNotAvailable
	}

	// Create per-source CUDA stream.
	stream, err := se.ctx.NewStream()
	if err != nil {
		return fmt.Errorf("gpu: segmentation: create stream for %s: %w", key, err)
	}

	// Create TRT execution context.
	trtCtx, err := se.engine.NewContext()
	if err != nil {
		se.ctx.DestroyStream(stream)
		return fmt.Errorf("gpu: segmentation: create TRT context for %s: %w", key, err)
	}

	// Allocate device buffers.
	rgbSize := 3 * segModelW * segModelH * 4 // float32
	rgbBuf, err := AllocDeviceBytes(rgbSize)
	if err != nil {
		trtCtx.Close()
		se.ctx.DestroyStream(stream)
		return fmt.Errorf("gpu: segmentation: alloc RGB buffer for %s: %w", key, err)
	}

	maskSize := 1 * segModelW * segModelH * 4 // float32
	maskBuf, err := AllocDeviceBytes(maskSize)
	if err != nil {
		FreeDeviceBytes(rgbBuf)
		trtCtx.Close()
		se.ctx.DestroyStream(stream)
		return fmt.Errorf("gpu: segmentation: alloc mask buffer for %s: %w", key, err)
	}

	// Allocate NV12-sized mask buffer: Y plane (w*h) for the actual mask +
	// UV plane (w*h/2) for neutral chroma. BlendStinger reads the UV portion
	// as scratch space for downsampled chroma alpha, so the allocation must
	// be pitch*height*3/2 — not just w*h (single-plane would cause OOB reads).
	maskU8Size := w * h * 3 / 2 // NV12 size
	maskU8, err := AllocDeviceBytes(maskU8Size)
	if err != nil {
		FreeDeviceBytes(maskBuf)
		FreeDeviceBytes(rgbBuf)
		trtCtx.Close()
		se.ctx.DestroyStream(stream)
		return fmt.Errorf("gpu: segmentation: alloc maskU8 buffer for %s: %w", key, err)
	}

	erodeTmpSize := w * h // erosion operates on Y-plane only
	erodeTmp, err := AllocDeviceBytes(erodeTmpSize)
	if err != nil {
		FreeDeviceBytes(maskU8)
		FreeDeviceBytes(maskBuf)
		FreeDeviceBytes(rgbBuf)
		trtCtx.Close()
		se.ctx.DestroyStream(stream)
		return fmt.Errorf("gpu: segmentation: alloc erode tmp for %s: %w", key, err)
	}

	prevMaskSize := w * h // EMA operates on Y-plane only
	prevMask, err := AllocDeviceBytes(prevMaskSize)
	if err != nil {
		FreeDeviceBytes(erodeTmp)
		FreeDeviceBytes(maskU8)
		FreeDeviceBytes(maskBuf)
		FreeDeviceBytes(rgbBuf)
		trtCtx.Close()
		se.ctx.DestroyStream(stream)
		return fmt.Errorf("gpu: segmentation: alloc prevMask for %s: %w", key, err)
	}

	// Initialize the UV portion of the mask buffer to 128 (neutral chroma).
	// BlendStinger uses the UV area as scratch for downsampled chroma alpha.
	// cudaMemset on the device is the most efficient way.
	uvOffset := C.size_t(w * h)
	uvSize := C.size_t(w * h / 2)
	C.cudaMemsetAsync(
		unsafe.Pointer(uintptr(maskU8)+uintptr(uvOffset)),
		C.int(128),
		uvSize,
		stream,
	)

	// Allocate a dedicated mask frame with NV12-compatible layout so
	// BlendStinger's Pitch*Height offset math reaches the UV scratch area.
	maskFrame := &GPUFrame{
		DevPtr: C.CUdeviceptr(uintptr(maskU8)),
		Width:  w,
		Height: h,
		Pitch:  w, // uint8, stride = width (no padding)
	}
	maskFrame.refs.Store(1)

	sess := &segSession{
		stream:    stream,
		rgbBuf:    rgbBuf,
		maskBuf:   maskBuf,
		maskU8:    maskU8,
		erodeTmp:  erodeTmp,
		prevMask:  prevMask,
		trtCtx:    trtCtx,
		smoothing: smoothing,
		srcW:      w,
		srcH:      h,
		maskFrame: maskFrame,
	}

	se.mu.Lock()
	// Clean up any existing session for this source.
	if old, exists := se.sessions[key]; exists {
		se.destroySessionLocked(old)
	}
	se.sessions[key] = sess
	se.mu.Unlock()

	slog.Info("gpu: segmentation: enabled source",
		"source", key, "size", [2]int{w, h},
		"smoothing", smoothing)
	return nil
}

// DisableSource destroys the segmentation session for the given source,
// freeing all associated GPU resources.
func (se *SegmentationEngine) DisableSource(key string) {
	if se == nil {
		return
	}

	se.mu.Lock()
	sess, exists := se.sessions[key]
	if exists {
		delete(se.sessions, key)
	}
	delete(se.pendingConfigs, key)
	se.mu.Unlock()

	if !exists {
		return
	}

	se.destroySessionLocked(sess)
	slog.Info("gpu: segmentation: disabled source", "source", key)
}

// SetPendingConfig stores a deferred segmentation config for a source.
// Called by the REST API when the source resolution is not yet known.
// The source manager will call EnableSource with real dimensions on first
// IngestYUV when it detects a pending config with no active session.
func (se *SegmentationEngine) SetPendingConfig(key string, smoothing float32) {
	if se == nil {
		return
	}
	se.mu.Lock()
	se.pendingConfigs[key] = smoothing
	se.mu.Unlock()
}

// HasPendingConfig returns true if a deferred config exists for the source
// but no active session has been created yet.
func (se *SegmentationEngine) HasPendingConfig(key string) bool {
	if se == nil {
		return false
	}
	se.mu.RLock()
	_, hasPending := se.pendingConfigs[key]
	_, hasSession := se.sessions[key]
	se.mu.RUnlock()
	return hasPending && !hasSession
}

// PendingSmoothing returns the smoothing value for a pending config.
// Returns 0 if no pending config exists.
func (se *SegmentationEngine) PendingSmoothing(key string) float32 {
	if se == nil {
		return 0
	}
	se.mu.RLock()
	s := se.pendingConfigs[key]
	se.mu.RUnlock()
	return s
}

// ClearPendingConfig removes the deferred config for a source.
// Called after EnableSource succeeds or when the source is disabled.
func (se *SegmentationEngine) ClearPendingConfig(key string) {
	if se == nil {
		return
	}
	se.mu.Lock()
	delete(se.pendingConfigs, key)
	se.mu.Unlock()
}

// IsEnabled returns true if segmentation is active for the given source.
func (se *SegmentationEngine) IsEnabled(key string) bool {
	if se == nil {
		return false
	}
	se.mu.RLock()
	_, exists := se.sessions[key]
	se.mu.RUnlock()
	return exists
}

// Segment runs the full segmentation pipeline for a source frame:
//
//  1. NV12 → 320x320 RGB float32 CHW preprocessing (on session stream)
//  2. TensorRT inference (on session stream)
//  3. Float32 mask → uint8 upscale to source resolution (on session stream)
//  4. 3x3 morphological erosion for edge cleanup (on session stream)
//  5. EMA temporal smoothing with previous mask (on session stream)
//  6. Sync session stream
//  7. Store result in atomic pointer for lock-free pipeline reads
//
// This function is called from GPUSourceManager.IngestYUV() — it runs on
// the source manager goroutine. The dedicated CUDA stream ensures inference
// does not block the main pipeline stream.
func (se *SegmentationEngine) Segment(key string, frame *GPUFrame) {
	if se == nil || frame == nil {
		return
	}

	se.mu.RLock()
	sess, exists := se.sessions[key]
	se.mu.RUnlock()
	if !exists {
		return
	}

	// Step 1: NV12 → RGB CHW preprocessing on session stream.
	rc := C.nv12_to_rgb_chw(
		(*C.float)(sess.rgbBuf),
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.int(segModelW), C.int(segModelH),
		sess.stream,
	)
	if rc != C.cudaSuccess {
		slog.Warn("gpu: segmentation: preprocess failed",
			"source", key, "cuda_rc", rc)
		return
	}

	// Step 2: TensorRT inference on session stream.
	if err := sess.trtCtx.Infer(sess.rgbBuf, sess.maskBuf, 1, sess.stream); err != nil {
		slog.Warn("gpu: segmentation: inference failed",
			"source", key, "error", err)
		return
	}

	// Step 3: Float32 mask → uint8, upscale to source resolution.
	// Write to erodeTmp first (erosion input).
	rc = C.mask_to_u8_upscale(
		(*C.uint8_t)(sess.erodeTmp),
		C.int(sess.srcW), C.int(sess.srcH),
		(*C.float)(sess.maskBuf),
		C.int(segModelW), C.int(segModelH),
		sess.stream,
	)
	if rc != C.cudaSuccess {
		slog.Warn("gpu: segmentation: mask upscale failed",
			"source", key, "cuda_rc", rc)
		return
	}

	// Step 4: 3x3 erosion to clean up edge artifacts.
	// erodeTmp → maskU8.
	rc = C.mask_erode_3x3(
		(*C.uint8_t)(sess.maskU8),
		(*C.uint8_t)(sess.erodeTmp),
		C.int(sess.srcW), C.int(sess.srcH),
		sess.stream,
	)
	if rc != C.cudaSuccess {
		slog.Warn("gpu: segmentation: mask erode failed",
			"source", key, "cuda_rc", rc)
		return
	}

	// Step 5: Temporal EMA smoothing.
	if sess.hasPrev && sess.smoothing > 0 {
		// Blend: output = prev * alpha + curr * (1 - alpha).
		// Use erodeTmp as output to avoid in-place aliasing.
		maskSize := sess.srcW * sess.srcH
		rc = C.mask_ema(
			(*C.uint8_t)(sess.erodeTmp),
			(*C.uint8_t)(sess.prevMask),
			(*C.uint8_t)(sess.maskU8),
			C.float(sess.smoothing),
			C.int(maskSize),
			sess.stream,
		)
		if rc != C.cudaSuccess {
			slog.Warn("gpu: segmentation: mask EMA failed",
				"source", key, "cuda_rc", rc)
			// Fall through — use un-smoothed mask.
		} else {
			// Copy smoothed result back to maskU8.
			copySize := C.size_t(maskSize)
			C.cudaMemcpyAsync(
				sess.maskU8,
				sess.erodeTmp,
				copySize,
				C.cudaMemcpyDeviceToDevice,
				sess.stream,
			)
		}
	}

	// Copy current mask to prevMask for next frame's EMA.
	{
		maskSize := C.size_t(sess.srcW * sess.srcH)
		C.cudaMemcpyAsync(
			sess.prevMask,
			sess.maskU8,
			maskSize,
			C.cudaMemcpyDeviceToDevice,
			sess.stream,
		)
		sess.hasPrev = true
	}

	// Step 6: Synchronize session stream — all GPU work must complete
	// before we expose the mask to the pipeline.
	if err := se.ctx.SyncStream(sess.stream); err != nil {
		slog.Warn("gpu: segmentation: stream sync failed",
			"source", key, "error", err)
		return
	}

	// Step 7: Update the atomic mask pointer.
	// The maskFrame's DevPtr points to maskU8 which now has the final mask.
	// We store a reference to the same frame each time — pipeline readers
	// just need to read the mask data at the DevPtr, which is updated in-place.
	sess.mask.Store(sess.maskFrame)
}

// MaskForSource returns the latest segmentation mask for a source.
// Returns nil if segmentation is not enabled or no mask has been produced yet.
// The returned GPUFrame is a uint8 single-plane mask at source resolution
// (Width x Height, Pitch = Width). Pixel value 255 = foreground, 0 = background.
//
// This is a lock-free atomic read, safe for use from the pipeline goroutine.
func (se *SegmentationEngine) MaskForSource(key string) *GPUFrame {
	if se == nil {
		return nil
	}

	se.mu.RLock()
	sess, exists := se.sessions[key]
	se.mu.RUnlock()
	if !exists {
		return nil
	}

	return sess.mask.Load()
}

// Close destroys all sessions and the TensorRT engine.
func (se *SegmentationEngine) Close() {
	if se == nil {
		return
	}

	se.mu.Lock()
	sessions := se.sessions
	se.sessions = make(map[string]*segSession)
	se.mu.Unlock()

	for _, sess := range sessions {
		se.destroySessionLocked(sess)
	}

	if se.engine != nil {
		se.engine.Close()
		se.engine = nil
	}

	slog.Info("gpu: segmentation: engine closed")
}

// destroySessionLocked frees all GPU resources for a session.
// The session must already be removed from the sessions map.
func (se *SegmentationEngine) destroySessionLocked(sess *segSession) {
	// Clear the atomic mask pointer.
	sess.mask.Store(nil)

	if sess.trtCtx != nil {
		sess.trtCtx.Close()
		sess.trtCtx = nil
	}

	// Free device buffers (maskU8 is owned by maskFrame, don't double-free).
	if sess.rgbBuf != nil {
		FreeDeviceBytes(sess.rgbBuf)
		sess.rgbBuf = nil
	}
	if sess.maskBuf != nil {
		FreeDeviceBytes(sess.maskBuf)
		sess.maskBuf = nil
	}
	if sess.erodeTmp != nil {
		FreeDeviceBytes(sess.erodeTmp)
		sess.erodeTmp = nil
	}
	if sess.prevMask != nil {
		FreeDeviceBytes(sess.prevMask)
		sess.prevMask = nil
	}
	// maskU8 is the backing memory for maskFrame.DevPtr — free it directly.
	if sess.maskU8 != nil {
		FreeDeviceBytes(sess.maskU8)
		sess.maskU8 = nil
	}
	// Prevent maskFrame from trying to free via pool or cudaFree (already freed above).
	if sess.maskFrame != nil {
		sess.maskFrame.DevPtr = 0
		sess.maskFrame = nil
	}

	if sess.stream != nil {
		se.ctx.DestroyStream(sess.stream)
		sess.stream = nil
	}
}
