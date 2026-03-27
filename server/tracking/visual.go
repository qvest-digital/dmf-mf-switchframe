//go:build cgo && cuda && tensorrt

package tracking

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/zsiec/switchframe/server/gpu"
)

/*
#include <cuda_runtime.h>
#include <stdint.h>

// nv12_to_rgb_chw is the fused NV12 -> RGB float32 CHW preprocessing kernel.
extern cudaError_t nv12_to_rgb_chw(
    float* rgbOut,
    const uint8_t* nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH,
    cudaStream_t stream);

static cudaError_t visual_cuda_malloc(void** ptr, size_t size) {
    return cudaMalloc(ptr, size);
}

static void visual_cuda_free(void* ptr) {
    cudaFree(ptr);
}

static cudaError_t visual_cuda_stream_create(cudaStream_t* stream) {
    return cudaStreamCreate(stream);
}

static void visual_cuda_stream_destroy(cudaStream_t stream) {
    cudaStreamDestroy(stream);
}

static cudaError_t visual_cuda_stream_sync(cudaStream_t stream) {
    return cudaStreamSynchronize(stream);
}

static cudaError_t visual_cuda_memcpy_d2h(void* dst, const void* src, size_t size) {
    return cudaMemcpy(dst, src, size, cudaMemcpyDeviceToHost);
}
*/
import "C"

// VisualLocalizer performs visual localization by extracting SuperPoint features
// from live frames, matching them against a pre-built studio map using LightGlue,
// and solving for camera pose via PnP. All GPU inference runs on a dedicated
// CUDA stream to avoid blocking the main encode/decode paths.
type VisualLocalizer struct {
	studioMap  *StudioMap
	superpoint *gpu.TRTEngine
	spContext  *gpu.TRTContext
	lightglue  *gpu.TRTEngine
	lgContext  *gpu.TRTContext
	intrinsics CameraIntrinsics

	// GPU buffers
	stream     C.cudaStream_t
	inputBuf   unsafe.Pointer // float32 RGB CHW: superpointInputW * superpointInputH * 3 * 4
	spKpBuf    unsafe.Pointer // SuperPoint keypoints output (float32)
	spDescBuf  unsafe.Pointer // SuperPoint descriptors output (float32)
	lgMatchBuf unsafe.Pointer // LightGlue match indices output (int32)

	latest  atomic.Pointer[CameraPose]
	running atomic.Bool
	closed  atomic.Bool

	mu         sync.Mutex
	lastUpdate time.Time
}

// NewVisualLocalizer creates a visual localizer with pre-built TensorRT plan files
// for SuperPoint and LightGlue. The mapDir is the path to a COLMAP-derived
// studio map directory.
func NewVisualLocalizer(mapDir string, superpointPlan, lightgluePlan string, intrinsics CameraIntrinsics) (*VisualLocalizer, error) {
	studioMap, err := LoadStudioMap(mapDir)
	if err != nil {
		return nil, fmt.Errorf("tracking: load studio map: %w", err)
	}

	spEngine, err := gpu.LoadTRTPlan(superpointPlan)
	if err != nil {
		return nil, fmt.Errorf("tracking: load superpoint model: %w", err)
	}

	spCtx, err := spEngine.NewContext()
	if err != nil {
		spEngine.Close()
		return nil, fmt.Errorf("tracking: create superpoint context: %w", err)
	}

	lgEngine, err := gpu.LoadTRTPlan(lightgluePlan)
	if err != nil {
		spCtx.Close()
		spEngine.Close()
		return nil, fmt.Errorf("tracking: load lightglue model: %w", err)
	}

	lgCtx, err := lgEngine.NewContext()
	if err != nil {
		lgEngine.Close()
		spCtx.Close()
		spEngine.Close()
		return nil, fmt.Errorf("tracking: create lightglue context: %w", err)
	}

	// Create a dedicated CUDA stream for visual tracking.
	var stream C.cudaStream_t
	if rc := C.visual_cuda_stream_create(&stream); rc != C.cudaSuccess {
		lgCtx.Close()
		lgEngine.Close()
		spCtx.Close()
		spEngine.Close()
		return nil, fmt.Errorf("tracking: create CUDA stream: error %d", rc)
	}

	// Allocate GPU buffers.
	inputBytes := C.size_t(superpointInputW * superpointInputH * 3 * 4) // RGB float32 CHW
	var inputBuf unsafe.Pointer
	if rc := C.visual_cuda_malloc(&inputBuf, inputBytes); rc != C.cudaSuccess {
		C.visual_cuda_stream_destroy(stream)
		lgCtx.Close()
		lgEngine.Close()
		spCtx.Close()
		spEngine.Close()
		return nil, fmt.Errorf("tracking: alloc input buffer: CUDA error %d", rc)
	}

	// Keypoint buffer: maxKeypoints * 2 (x,y) * float32
	kpBytes := C.size_t(maxKeypoints * 2 * 4)
	var spKpBuf unsafe.Pointer
	if rc := C.visual_cuda_malloc(&spKpBuf, kpBytes); rc != C.cudaSuccess {
		C.visual_cuda_free(inputBuf)
		C.visual_cuda_stream_destroy(stream)
		lgCtx.Close()
		lgEngine.Close()
		spCtx.Close()
		spEngine.Close()
		return nil, fmt.Errorf("tracking: alloc keypoint buffer: CUDA error %d", rc)
	}

	// Descriptor buffer: maxKeypoints * 256 * float32
	descBytes := C.size_t(maxKeypoints * superpointDescDim * 4)
	var spDescBuf unsafe.Pointer
	if rc := C.visual_cuda_malloc(&spDescBuf, descBytes); rc != C.cudaSuccess {
		C.visual_cuda_free(spKpBuf)
		C.visual_cuda_free(inputBuf)
		C.visual_cuda_stream_destroy(stream)
		lgCtx.Close()
		lgEngine.Close()
		spCtx.Close()
		spEngine.Close()
		return nil, fmt.Errorf("tracking: alloc descriptor buffer: CUDA error %d", rc)
	}

	// Match buffer: maxKeypoints * int32 (match index, -1 for no match)
	matchBytes := C.size_t(maxKeypoints * 4)
	var lgMatchBuf unsafe.Pointer
	if rc := C.visual_cuda_malloc(&lgMatchBuf, matchBytes); rc != C.cudaSuccess {
		C.visual_cuda_free(spDescBuf)
		C.visual_cuda_free(spKpBuf)
		C.visual_cuda_free(inputBuf)
		C.visual_cuda_stream_destroy(stream)
		lgCtx.Close()
		lgEngine.Close()
		spCtx.Close()
		spEngine.Close()
		return nil, fmt.Errorf("tracking: alloc match buffer: CUDA error %d", rc)
	}

	return &VisualLocalizer{
		studioMap:  studioMap,
		superpoint: spEngine,
		spContext:  spCtx,
		lightglue:  lgEngine,
		lgContext:  lgCtx,
		intrinsics: intrinsics,
		stream:     stream,
		inputBuf:   inputBuf,
		spKpBuf:    spKpBuf,
		spDescBuf:  spDescBuf,
		lgMatchBuf: lgMatchBuf,
	}, nil
}

// Name returns the unique name of this tracking source.
func (v *VisualLocalizer) Name() string { return "visual" }

// Type returns the tracking system type.
func (v *VisualLocalizer) Type() string { return "visual" }

// Start begins visual tracking processing.
func (v *VisualLocalizer) Start() error {
	if v.closed.Load() {
		return fmt.Errorf("tracking: visual localizer is closed")
	}
	if v.running.Load() {
		return fmt.Errorf("tracking: visual localizer already running")
	}
	if !v.studioMap.Ready {
		return ErrMapNotReady
	}
	v.running.Store(true)
	return nil
}

// Stop halts visual tracking without releasing resources.
func (v *VisualLocalizer) Stop() error {
	v.running.Store(false)
	return nil
}

// Running reports whether the localizer is actively tracking.
func (v *VisualLocalizer) Running() bool {
	return v.running.Load()
}

// LatestPose returns the most recent camera pose, or nil if unavailable.
func (v *VisualLocalizer) LatestPose() *CameraPose {
	return v.latest.Load()
}

// ProcessFrame runs the full visual localization pipeline on a GPU frame:
//  1. Preprocess NV12 -> RGB float32 CHW, resize to SuperPoint input dims
//  2. SuperPoint inference: extract keypoints + descriptors
//  3. LightGlue inference: match frame descriptors against studio map
//  4. PnP solve: compute camera pose from 2D-3D correspondences
//
// framePtr is a CUDA device pointer to the NV12 frame.
// All GPU work runs on the dedicated visual tracking CUDA stream.
func (v *VisualLocalizer) ProcessFrame(framePtr uintptr, w, h, pitch int) {
	if !v.running.Load() || v.closed.Load() {
		return
	}
	if v.studioMap == nil || !v.studioMap.Ready {
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	// Rate-limit to avoid overwhelming the GPU. Visual localization is
	// computationally expensive and typically only needs 5-10 Hz updates.
	if time.Since(v.lastUpdate) < 100*time.Millisecond {
		return
	}

	// Step 1: Preprocess NV12 -> RGB float32 CHW.
	rc := C.nv12_to_rgb_chw(
		(*C.float)(v.inputBuf),
		(*C.uint8_t)(unsafe.Pointer(framePtr)),
		C.int(w), C.int(h), C.int(pitch),
		C.int(superpointInputW), C.int(superpointInputH),
		v.stream,
	)
	if rc != C.cudaSuccess {
		return
	}

	// Step 2: SuperPoint inference.
	// Input: [1, 1, H, W] grayscale (we use the R channel as luminance approx).
	// Output: keypoints [N, 2] and descriptors [N, 256].
	if err := v.spContext.Infer(v.inputBuf, v.spKpBuf, 1, unsafe.Pointer(v.stream)); err != nil {
		return
	}

	// Synchronize to read keypoint data back to CPU.
	if rc := C.visual_cuda_stream_sync(v.stream); rc != C.cudaSuccess {
		return
	}

	// Read keypoints from GPU.
	kpData := make([]float32, maxKeypoints*2)
	kpBytes := C.size_t(maxKeypoints * 2 * 4)
	if rc := C.visual_cuda_memcpy_d2h(unsafe.Pointer(&kpData[0]), v.spKpBuf, kpBytes); rc != C.cudaSuccess {
		return
	}

	// Read descriptors from GPU.
	descData := make([]float32, maxKeypoints*superpointDescDim)
	descBytes := C.size_t(maxKeypoints * superpointDescDim * 4)
	if rc := C.visual_cuda_memcpy_d2h(unsafe.Pointer(&descData[0]), v.spDescBuf, descBytes); rc != C.cudaSuccess {
		return
	}

	// Count valid keypoints (non-zero).
	numKp := 0
	for i := 0; i < maxKeypoints; i++ {
		if kpData[i*2] != 0 || kpData[i*2+1] != 0 {
			numKp++
		} else {
			break
		}
	}

	if numKp < 6 {
		return // Not enough keypoints for PnP
	}

	// Step 3: Match frame descriptors against studio map descriptors.
	// Use brute-force nearest-neighbor matching (CPU fallback for now;
	// LightGlue TensorRT can be used when available).
	matches := matchDescriptors(
		descData[:numKp*superpointDescDim],
		v.studioMap.Descriptors,
		numKp,
		0.7, // Lowe's ratio threshold
	)

	if len(matches) < 6 {
		return // Not enough matches for PnP
	}

	// Build 2D-3D correspondence arrays.
	points2D := make([][2]float64, len(matches))
	points3D := make([][3]float64, len(matches))
	for i, m := range matches {
		points2D[i] = [2]float64{
			float64(kpData[m.queryIdx*2]),
			float64(kpData[m.queryIdx*2+1]),
		}
		points3D[i] = v.studioMap.Points3D[m.trainIdx]
	}

	// Step 4: PnP solve.
	focalLength := 50.0 // default 50mm
	if len(v.intrinsics.FocalLengthCurve) > 0 {
		focalLength = v.intrinsics.FocalLengthCurve[0][1]
	}

	pose, err := SolvePnP(points2D, points3D, v.intrinsics, focalLength)
	if err != nil {
		return
	}

	pose.Timestamp = time.Now().UnixNano() / 1e6 * 90 // 90kHz PTS (divide to ms first to avoid int64 overflow)
	v.latest.Store(pose)
	v.lastUpdate = time.Now()
}

// Close releases all GPU resources held by the visual localizer.
func (v *VisualLocalizer) Close() error {
	if v.closed.Swap(true) {
		return nil // already closed
	}
	v.running.Store(false)

	if v.lgMatchBuf != nil {
		C.visual_cuda_free(v.lgMatchBuf)
		v.lgMatchBuf = nil
	}
	if v.spDescBuf != nil {
		C.visual_cuda_free(v.spDescBuf)
		v.spDescBuf = nil
	}
	if v.spKpBuf != nil {
		C.visual_cuda_free(v.spKpBuf)
		v.spKpBuf = nil
	}
	if v.inputBuf != nil {
		C.visual_cuda_free(v.inputBuf)
		v.inputBuf = nil
	}
	if v.stream != nil {
		C.visual_cuda_stream_destroy(v.stream)
		v.stream = nil
	}
	if v.lgContext != nil {
		v.lgContext.Close()
		v.lgContext = nil
	}
	if v.lightglue != nil {
		v.lightglue.Close()
		v.lightglue = nil
	}
	if v.spContext != nil {
		v.spContext.Close()
		v.spContext = nil
	}
	if v.superpoint != nil {
		v.superpoint.Close()
		v.superpoint = nil
	}

	return nil
}
