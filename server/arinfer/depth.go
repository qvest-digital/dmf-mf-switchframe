//go:build cgo && cuda && tensorrt

package arinfer

import (
	"fmt"
	"unsafe"

	"github.com/zsiec/switchframe/server/gpu"
)

/*
#include <cuda_runtime.h>
#include <stdint.h>

// nv12_to_rgb_chw is the fused NV12 -> RGB float32 CHW preprocessing kernel.
// Declared in gpu/cuda/preprocess.cu, linked via the gpu package's cgo directives.
extern cudaError_t nv12_to_rgb_chw(
    float* rgbOut,
    const uint8_t* nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH,
    cudaStream_t stream);

// p210_to_rgb_chw is the fused P210 (10-bit 4:2:2) -> RGB float32 CHW preprocessing kernel.
extern cudaError_t p210_to_rgb_chw(
    float* rgbOut,
    const uint16_t* p210,
    int srcW, int srcH, int srcPitch16,
    int outW, int outH,
    cudaStream_t stream);

static cudaError_t depth_cuda_malloc(void** ptr, size_t size) {
    return cudaMalloc(ptr, size);
}

static void depth_cuda_free(void* ptr) {
    cudaFree(ptr);
}

static cudaError_t depth_cuda_stream_sync(cudaStream_t stream) {
    return cudaStreamSynchronize(stream);
}
*/
import "C"

// depthModelInputW is the default Depth Anything v2 ViT-S input width.
// The ONNX model was exported at 518x294; the TRT engine must match.
const depthModelInputW = 518

// depthModelInputH is the default Depth Anything v2 ViT-S input height.
// The ONNX model was exported at 518x294; the TRT engine must match.
const depthModelInputH = 294

// depthAnythingV2 wraps Depth Anything v2 ViT-S for monocular depth estimation
// using TensorRT for GPU-accelerated inference.
//
// Pipeline per frame:
//  1. Preprocess: NV12 GPU frame -> RGB float32 CHW, bilinear-resize to inputW x inputH
//  2. TensorRT inference: RGB CHW -> float16 depth map (inputW x inputH)
//  3. Output depth map is left on the GPU for downstream consumption
type depthAnythingV2 struct {
	engine  *gpu.TRTEngine
	context *gpu.TRTContext
	inputW  int // model input width (default 518)
	inputH  int // model input height (default 294)

	// GPU buffers owned by this model
	inputBuf  unsafe.Pointer // float32 RGB CHW: inputW * inputH * 3 * 4 bytes
	outputBuf unsafe.Pointer // float16 depth: inputW * inputH * 2 bytes
}

// newDepthAnythingV2 loads a pre-built TensorRT plan for Depth Anything v2.
// planPath should point to a .plan file previously built from the ONNX model.
// inputW and inputH specify the model's expected input resolution; use 0 for defaults.
func newDepthAnythingV2(planPath string, inputW, inputH int) (*depthAnythingV2, error) {
	if inputW <= 0 {
		inputW = depthModelInputW
	}
	if inputH <= 0 {
		inputH = depthModelInputH
	}

	engine, err := gpu.LoadTRTPlan(planPath)
	if err != nil {
		return nil, fmt.Errorf("load depth model: %w", err)
	}

	ctx, err := engine.NewContext()
	if err != nil {
		engine.Close()
		return nil, fmt.Errorf("create depth inference context: %w", err)
	}

	// Allocate GPU buffer for preprocessed RGB input: float32 CHW
	inputBytes := C.size_t(inputW * inputH * 3 * 4) // 3 channels * sizeof(float32)
	var inputBuf unsafe.Pointer
	if rc := C.depth_cuda_malloc(&inputBuf, inputBytes); rc != C.cudaSuccess {
		ctx.Close()
		engine.Close()
		return nil, fmt.Errorf("alloc depth input buffer (%d bytes): CUDA error %d", inputBytes, rc)
	}

	// Allocate GPU buffer for depth output: float16 single-channel
	outputBytes := C.size_t(inputW * inputH * 2) // 1 channel * sizeof(float16)
	var outputBuf unsafe.Pointer
	if rc := C.depth_cuda_malloc(&outputBuf, outputBytes); rc != C.cudaSuccess {
		C.depth_cuda_free(inputBuf)
		ctx.Close()
		engine.Close()
		return nil, fmt.Errorf("alloc depth output buffer (%d bytes): CUDA error %d", outputBytes, rc)
	}

	return &depthAnythingV2{
		engine:    engine,
		context:   ctx,
		inputW:    inputW,
		inputH:    inputH,
		inputBuf:  inputBuf,
		outputBuf: outputBuf,
	}, nil
}

// Infer runs depth estimation on a single GPU frame.
//
// inputPtr is the device pointer to the frame (NV12 or P210, selected by format).
// w, h, pitch describe the source frame dimensions and row pitch.
// outputPtr receives the float16 depth map at the model's native resolution
// (inputW x inputH). The caller is responsible for any upscaling needed.
func (d *depthAnythingV2) Infer(inputPtr uintptr, w, h, pitch int, outputPtr uintptr, format FrameFormat) error {
	if d == nil || d.engine == nil {
		return ErrDisabled
	}

	// Step 1: Preprocess frame -> RGB float32 CHW, resize to model input dims.
	// Dispatch between NV12 and P210 preprocessing kernels.
	var rc C.cudaError_t
	switch format {
	case FormatP210:
		// P210: uint16 semi-planar 4:2:2, pitch in uint16 units = pitch_bytes / 2.
		// CUDA guarantees even pitch alignment from cudaMallocPitch.
		if pitch%2 != 0 {
			return fmt.Errorf("depth preprocess P210: odd pitch %d (must be even for uint16 division)", pitch)
		}
		rc = C.p210_to_rgb_chw(
			(*C.float)(d.inputBuf),
			(*C.uint16_t)(unsafe.Pointer(inputPtr)),
			C.int(w), C.int(h), C.int(pitch/2),
			C.int(d.inputW), C.int(d.inputH),
			nil,
		)
		if rc != C.cudaSuccess {
			return fmt.Errorf("depth preprocess P210->RGB: CUDA error %d", rc)
		}
	default: // FormatNV12
		rc = C.nv12_to_rgb_chw(
			(*C.float)(d.inputBuf),
			(*C.uint8_t)(unsafe.Pointer(inputPtr)),
			C.int(w), C.int(h), C.int(pitch),
			C.int(d.inputW), C.int(d.inputH),
			nil,
		)
		if rc != C.cudaSuccess {
			return fmt.Errorf("depth preprocess NV12->RGB: CUDA error %d", rc)
		}
	}

	// Step 2: Run TensorRT inference.
	// Depth Anything v2 ViT-S: input is [1, 3, inputH, inputW] float32 CHW,
	// output is [1, 1, inputH, inputW] float16 inverse depth.
	if err := d.context.Infer(d.inputBuf, d.outputBuf, 1, nil); err != nil {
		return fmt.Errorf("depth TensorRT inference: %w", err)
	}

	// Step 3: Synchronize and copy output to caller's buffer.
	// cudaMemcpy device-to-device from our internal outputBuf to outputPtr.
	if rc := C.depth_cuda_stream_sync(nil); rc != C.cudaSuccess {
		return fmt.Errorf("depth stream sync: CUDA error %d", rc)
	}

	// Copy depth output to caller's output pointer if it differs from our buffer.
	// Guard against outputPtr=0 (null): callers may pass 0 to leave the result
	// in the internal buffer accessible via OutputPtr().
	if outputPtr != 0 && uintptr(d.outputBuf) != outputPtr {
		depthBytes := C.size_t(d.inputW * d.inputH * 2) // float16
		if rc := C.cudaMemcpy(
			unsafe.Pointer(outputPtr),
			d.outputBuf,
			depthBytes,
			C.cudaMemcpyDeviceToDevice,
		); rc != C.cudaSuccess {
			return fmt.Errorf("depth output copy: CUDA error %d", rc)
		}
	}

	return nil
}

// OutputPtr returns the CUDA device pointer to the model's internal depth
// output buffer (float16, inputW x inputH). Returns 0 if the model is nil.
func (d *depthAnythingV2) OutputPtr() uintptr {
	if d == nil || d.outputBuf == nil {
		return 0
	}
	return uintptr(d.outputBuf)
}

// Close releases all GPU resources held by the depth model.
// Safe to call on a nil receiver or multiple times.
func (d *depthAnythingV2) Close() {
	if d == nil {
		return
	}
	if d.inputBuf != nil {
		C.depth_cuda_free(d.inputBuf)
		d.inputBuf = nil
	}
	if d.outputBuf != nil {
		C.depth_cuda_free(d.outputBuf)
		d.outputBuf = nil
	}
	if d.context != nil {
		d.context.Close()
		d.context = nil
	}
	if d.engine != nil {
		d.engine.Close()
		d.engine = nil
	}
}
