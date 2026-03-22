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
	// segModelW and segModelH are the input dimensions expected by the RVM model.
	segModelW = 320
	segModelH = 320

	// RVM hidden state sizes at 320x320 input.
	rvmR1C, rvmR1H, rvmR1W = 16, 160, 160
	rvmR2C, rvmR2H, rvmR2W = 20, 80, 80
	rvmR3C, rvmR3H, rvmR3W = 40, 40, 40
	rvmR4C, rvmR4H, rvmR4W = 64, 20, 20
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
	rgbBuf  unsafe.Pointer // [1,3,segModelH,segModelW] float32
	maskBuf unsafe.Pointer // [1,1,segModelH,segModelW] float32 (pha output)
	fgrBuf  unsafe.Pointer // [1,3,segModelH,segModelW] float32 (fgr output, unused but required)
	maskU8  unsafe.Pointer // [srcW*srcH*3/2] uint8 (NV12-sized)

	// RVM recurrent hidden states. Two sets for ping-pong swap:
	// rIn is read during inference, rOut is written. After inference, swap pointers.
	r1In, r1Out unsafe.Pointer // [1,rvmR1C,rvmR1H,rvmR1W] float32
	r2In, r2Out unsafe.Pointer // [1,rvmR2C,rvmR2H,rvmR2W] float32
	r3In, r3Out unsafe.Pointer // [1,rvmR3C,rvmR3H,rvmR3W] float32
	r4In, r4Out unsafe.Pointer // [1,rvmR4C,rvmR4H,rvmR4W] float32

	trtCtx     *TRTContext
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

	slog.Info("gpu: segmentation: building TensorRT engine (RVM)",
		"onnx", modelPath, "plan_cache", planPath)

	// Use v1 builder — the ONNX model has been pre-processed with fixed shapes
	// (downsample_ratio baked as constant, all input dims concrete).
	engine, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	if err != nil {
		return nil, fmt.Errorf("gpu: segmentation: engine build: %w", err)
	}

	// Log I/O tensor details for debugging (RVM has multiple I/O tensors).
	numIO := engine.NumIOTensors()
	slog.Info("gpu: segmentation: engine ready",
		"num_io_tensors", numIO)
	for i := 0; i < numIO; i++ {
		info, infoErr := engine.TensorInfoAt(i)
		if infoErr != nil {
			continue
		}
		slog.Info("gpu: segmentation: tensor",
			"index", i, "name", info.Name,
			"is_input", info.IsInput, "dims", info.Dims)
	}

	// Validate that hidden-state tensor shapes match our hardcoded constants.
	// A mismatch means the ONNX model was exported with different settings
	// than expected, and inference would produce corrupt results.
	expectedShapes := map[string][]int{
		"r1i": {1, rvmR1C, rvmR1H, rvmR1W},
		"r2i": {1, rvmR2C, rvmR2H, rvmR2W},
		"r3i": {1, rvmR3C, rvmR3H, rvmR3W},
		"r4i": {1, rvmR4C, rvmR4H, rvmR4W},
	}
	for i := 0; i < numIO; i++ {
		info, infoErr := engine.TensorInfoAt(i)
		if infoErr != nil {
			continue
		}
		expected, ok := expectedShapes[info.Name]
		if !ok {
			continue
		}
		if len(info.Dims) != len(expected) {
			engine.Close()
			return nil, fmt.Errorf("gpu: segmentation: tensor %s has %d dims, expected %d",
				info.Name, len(info.Dims), len(expected))
		}
		for d := 0; d < len(expected); d++ {
			if info.Dims[d] != expected[d] {
				engine.Close()
				return nil, fmt.Errorf("gpu: segmentation: tensor %s dim[%d]=%d, expected %d",
					info.Name, d, info.Dims[d], expected[d])
			}
		}
	}

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
// smoothing is accepted for API compatibility but ignored — RVM's recurrent
// hidden states provide temporal consistency without explicit EMA smoothing.
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

	// Helper to clean up on allocation failure.
	var allocated []unsafe.Pointer
	cleanup := func() {
		for i := len(allocated) - 1; i >= 0; i-- {
			FreeDeviceBytes(allocated[i])
		}
		trtCtx.Close()
		se.ctx.DestroyStream(stream)
	}
	alloc := func(size int, label string) (unsafe.Pointer, error) {
		buf, allocErr := AllocDeviceBytes(size)
		if allocErr != nil {
			cleanup()
			return nil, fmt.Errorf("gpu: segmentation: alloc %s for %s: %w", label, key, allocErr)
		}
		allocated = append(allocated, buf)
		return buf, nil
	}

	// Allocate model I/O buffers (float32).
	rgbBuf, err := alloc(3*segModelW*segModelH*4, "RGB")
	if err != nil {
		return err
	}
	maskBuf, err := alloc(1*segModelW*segModelH*4, "maskBuf")
	if err != nil {
		return err
	}
	fgrBuf, err := alloc(3*segModelW*segModelH*4, "fgrBuf")
	if err != nil {
		return err
	}

	// RVM hidden state buffers (float32), two sets for ping-pong swap.
	r1Size := rvmR1C * rvmR1H * rvmR1W * 4
	r2Size := rvmR2C * rvmR2H * rvmR2W * 4
	r3Size := rvmR3C * rvmR3H * rvmR3W * 4
	r4Size := rvmR4C * rvmR4H * rvmR4W * 4

	r1In, err := alloc(r1Size, "r1In")
	if err != nil {
		return err
	}
	r1Out, err := alloc(r1Size, "r1Out")
	if err != nil {
		return err
	}
	r2In, err := alloc(r2Size, "r2In")
	if err != nil {
		return err
	}
	r2Out, err := alloc(r2Size, "r2Out")
	if err != nil {
		return err
	}
	r3In, err := alloc(r3Size, "r3In")
	if err != nil {
		return err
	}
	r3Out, err := alloc(r3Size, "r3Out")
	if err != nil {
		return err
	}
	r4In, err := alloc(r4Size, "r4In")
	if err != nil {
		return err
	}
	r4Out, err := alloc(r4Size, "r4Out")
	if err != nil {
		return err
	}

	// Allocate NV12-sized mask buffer: Y plane (w*h) for the actual mask +
	// UV plane (w*h/2) for neutral chroma. BlendStinger reads the UV portion
	// as scratch space for downsampled chroma alpha, so the allocation must
	// be pitch*height*3/2 — not just w*h (single-plane would cause OOB reads).
	maskU8, err := alloc(w*h*3/2, "maskU8")
	if err != nil {
		return err
	}

	// Zero-initialize hidden state inputs (RVM starts from zeros).
	if rc := C.cudaMemsetAsync(r1In, 0, C.size_t(r1Size), stream); rc != C.cudaSuccess {
		cleanup()
		return fmt.Errorf("gpu: segmentation: memset r1In for %s: cuda error %d", key, rc)
	}
	if rc := C.cudaMemsetAsync(r2In, 0, C.size_t(r2Size), stream); rc != C.cudaSuccess {
		cleanup()
		return fmt.Errorf("gpu: segmentation: memset r2In for %s: cuda error %d", key, rc)
	}
	if rc := C.cudaMemsetAsync(r3In, 0, C.size_t(r3Size), stream); rc != C.cudaSuccess {
		cleanup()
		return fmt.Errorf("gpu: segmentation: memset r3In for %s: cuda error %d", key, rc)
	}
	if rc := C.cudaMemsetAsync(r4In, 0, C.size_t(r4Size), stream); rc != C.cudaSuccess {
		cleanup()
		return fmt.Errorf("gpu: segmentation: memset r4In for %s: cuda error %d", key, rc)
	}

	// Initialize the UV portion of the mask buffer to 128 (neutral chroma).
	uvOffset := C.size_t(w * h)
	uvSize := C.size_t(w * h / 2)
	if rc := C.cudaMemsetAsync(
		unsafe.Pointer(uintptr(maskU8)+uintptr(uvOffset)),
		C.int(128),
		uvSize,
		stream,
	); rc != C.cudaSuccess {
		cleanup()
		return fmt.Errorf("gpu: segmentation: memset UV for %s: cuda error %d", key, rc)
	}

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
		fgrBuf:    fgrBuf,
		maskU8:    maskU8,
		r1In:      r1In,
		r1Out:     r1Out,
		r2In:      r2In,
		r2Out:     r2Out,
		r3In:      r3In,
		r3Out:     r3Out,
		r4In:      r4In,
		r4Out:     r4Out,
		trtCtx:    trtCtx,
		srcW:      w,
		srcH:      h,
		maskFrame: maskFrame,
	}

	se.mu.Lock()
	// Clean up any existing session for this source.
	if old, exists := se.sessions[key]; exists {
		se.destroySession(old)
	}
	se.sessions[key] = sess
	se.mu.Unlock()

	slog.Info("gpu: segmentation: enabled source (RVM)",
		"source", key, "size", [2]int{w, h})
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

	se.destroySession(sess)
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

// Segment runs the RVM segmentation pipeline for a source frame:
//
//  1. NV12 → 320x320 RGB float32 CHW preprocessing (on session stream)
//  2. RVM inference via InferMulti with hidden state bindings
//  3. Swap hidden state pointers (zero-copy ping-pong)
//  4. Alpha matte → uint8 upscale to source resolution
//  5. Sync session stream
//  6. Store result in atomic pointer for lock-free pipeline reads
//
// RVM's recurrent hidden states provide temporal consistency across frames,
// eliminating the need for explicit erosion or EMA smoothing.
//
// This function is called from GPUSourceManager.IngestYUV() — it runs on
// the source manager goroutine. The dedicated CUDA stream ensures inference
// does not block the main pipeline stream.
func (se *SegmentationEngine) Segment(key string, frame *GPUFrame) {
	if se == nil || frame == nil {
		return
	}

	se.mu.RLock()
	defer se.mu.RUnlock()
	sess, exists := se.sessions[key]
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

	// Step 2: RVM inference via InferMulti (5 inputs, 6 outputs).
	// downsample_ratio is baked into the ONNX model as a constant (1.0).
	bindings := []TRTBinding{
		{Name: "src", DevPtr: sess.rgbBuf, Dims: []int{1, 3, segModelH, segModelW}},
		{Name: "r1i", DevPtr: sess.r1In, Dims: []int{1, rvmR1C, rvmR1H, rvmR1W}},
		{Name: "r2i", DevPtr: sess.r2In, Dims: []int{1, rvmR2C, rvmR2H, rvmR2W}},
		{Name: "r3i", DevPtr: sess.r3In, Dims: []int{1, rvmR3C, rvmR3H, rvmR3W}},
		{Name: "r4i", DevPtr: sess.r4In, Dims: []int{1, rvmR4C, rvmR4H, rvmR4W}},
		{Name: "fgr", DevPtr: sess.fgrBuf, Dims: []int{1, 3, segModelH, segModelW}},
		{Name: "pha", DevPtr: sess.maskBuf, Dims: []int{1, 1, segModelH, segModelW}},
		{Name: "r1o", DevPtr: sess.r1Out, Dims: []int{1, rvmR1C, rvmR1H, rvmR1W}},
		{Name: "r2o", DevPtr: sess.r2Out, Dims: []int{1, rvmR2C, rvmR2H, rvmR2W}},
		{Name: "r3o", DevPtr: sess.r3Out, Dims: []int{1, rvmR3C, rvmR3H, rvmR3W}},
		{Name: "r4o", DevPtr: sess.r4Out, Dims: []int{1, rvmR4C, rvmR4H, rvmR4W}},
	}
	if err := sess.trtCtx.InferMulti(bindings, unsafe.Pointer(sess.stream)); err != nil {
		slog.Warn("gpu: segmentation: RVM inference failed",
			"source", key, "error", err)
		return
	}

	// Step 3: Swap hidden states (pointer swap, zero-copy).
	sess.r1In, sess.r1Out = sess.r1Out, sess.r1In
	sess.r2In, sess.r2Out = sess.r2Out, sess.r2In
	sess.r3In, sess.r3Out = sess.r3Out, sess.r3In
	sess.r4In, sess.r4Out = sess.r4Out, sess.r4In

	// Step 4: Alpha matte (pha) → uint8, upscale to source resolution.
	// Writes directly to maskU8 Y-plane.
	rc = C.mask_to_u8_upscale(
		(*C.uint8_t)(sess.maskU8),
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

	// Step 5: Synchronize session stream — all GPU work must complete
	// before we expose the mask to the pipeline.
	if err := se.ctx.SyncStream(sess.stream); err != nil {
		slog.Warn("gpu: segmentation: stream sync failed",
			"source", key, "error", err)
		return
	}

	// Step 6: Update the atomic mask pointer.
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
		se.destroySession(sess)
	}

	if se.engine != nil {
		se.engine.Close()
		se.engine = nil
	}

	slog.Info("gpu: segmentation: engine closed")
}

// destroySession frees all GPU resources for a session.
// The session must already be removed from the sessions map.
func (se *SegmentationEngine) destroySession(sess *segSession) {
	// Clear the atomic mask pointer.
	sess.mask.Store(nil)

	if sess.trtCtx != nil {
		sess.trtCtx.Close()
		sess.trtCtx = nil
	}

	// Free device buffers.
	for _, buf := range []unsafe.Pointer{
		sess.rgbBuf, sess.maskBuf, sess.fgrBuf,
		sess.r1In, sess.r1Out,
		sess.r2In, sess.r2Out,
		sess.r3In, sess.r3Out,
		sess.r4In, sess.r4Out,
	} {
		if buf != nil {
			FreeDeviceBytes(buf)
		}
	}
	sess.rgbBuf = nil
	sess.maskBuf = nil
	sess.fgrBuf = nil
	sess.r1In = nil
	sess.r1Out = nil
	sess.r2In = nil
	sess.r2Out = nil
	sess.r3In = nil
	sess.r3Out = nil
	sess.r4In = nil
	sess.r4Out = nil

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
