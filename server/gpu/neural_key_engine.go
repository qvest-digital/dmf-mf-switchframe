//go:build cgo && cuda && tensorrt

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

// Preprocessing: NV12 → RGB float32 CHW with bilinear scale (from preprocess.cu).
cudaError_t nv12_to_rgb_chw(
    float* rgbOut,
    const uint8_t* nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH,
    cudaStream_t stream);

// ImageNet normalize RGB CHW in-place (from neural_key.cu).
cudaError_t ck_imagenet_normalize(
    float* rgb,
    int H, int W,
    cudaStream_t stream);

// Resize uint8 mask → float32 at model resolution (from neural_key.cu).
cudaError_t ck_resize_mask_u8_f32(
    float* dst,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    int dstW, int dstH,
    cudaStream_t stream);

// Fused composite: upscale alpha+fg from model res, despill, blend onto NV12 (from neural_key.cu).
cudaError_t ck_composite_neural(
    uint8_t* nv12,
    const float* alpha, const float* fg,
    int progW, int progH, int progPitch,
    int modelW, int modelH,
    cudaStream_t stream);

// Chroma key for generating coarse hint mask (from key.cu).
cudaError_t chroma_key_nv12(
    uint8_t* nv12,
    uint8_t* mask,
    int width, int height, int pitch,
    uint8_t keyCb, uint8_t keyCr,
    int simDistSq, int totalDistSq,
    float spillSuppress,
    uint8_t spillReplaceCb, uint8_t spillReplaceCr,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"unsafe"
)

const (
	// ckModelW and ckModelH are the fixed inference resolution for CorridorKey.
	ckModelW = 512
	ckModelH = 512
)

// NeuralKeyEngine manages GPU-native neural green screen keying via TensorRT.
// Unlike SegmentationEngine which runs per-source asynchronously, NeuralKeyEngine
// runs inline on the pipeline stream for the program source only.
type NeuralKeyEngine struct {
	ctx    *Context
	engine *TRTEngine
	trtCtx *TRTContext

	// Pre-allocated device buffers (float32).
	inputBuf  unsafe.Pointer // [1, 4, ckModelH, ckModelW] — RGB + hint concatenated
	alphaBuf  unsafe.Pointer // [1, 1, ckModelH, ckModelW] — model alpha output
	fgBuf     unsafe.Pointer // [1, 3, ckModelH, ckModelW] — model foreground output
	hintBuf   unsafe.Pointer // [1, 1, ckModelH, ckModelW] — resized chroma hint

	// Pre-allocated uint8 chroma hint mask at program resolution.
	chromaHint unsafe.Pointer // [progH, progW] uint8
	progW, progH int

	warmupDone atomic.Bool
}

// NewNeuralKeyEngine builds or loads a TensorRT engine from the CorridorKey ONNX model.
func NewNeuralKeyEngine(ctx *Context, modelPath string, progW, progH int) (*NeuralKeyEngine, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}
	if modelPath == "" {
		return nil, fmt.Errorf("gpu: neural_key: modelPath is empty")
	}

	// Plan cache path.
	cacheDir := defaultModelCacheDir()
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			slog.Warn("gpu: neural_key: failed to create model cache dir",
				"dir", cacheDir, "error", err)
			cacheDir = ""
		}
	}

	planPath := ""
	if cacheDir != "" {
		base := filepath.Base(modelPath)
		ext := filepath.Ext(base)
		planPath = filepath.Join(cacheDir, base[:len(base)-len(ext)]+"_neuralkey.plan")
	}

	slog.Info("gpu: neural_key: building TensorRT engine (CorridorKey)",
		"onnx", modelPath, "plan_cache", planPath,
		"model_size", [2]int{ckModelW, ckModelH})

	engine, err := NewTRTEngine(modelPath, TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       true,
		PlanCachePath: planPath,
	})
	if err != nil {
		return nil, fmt.Errorf("gpu: neural_key: engine build: %w", err)
	}

	// Log tensor info for debugging.
	numIO := engine.NumIOTensors()
	slog.Info("gpu: neural_key: engine ready", "num_io_tensors", numIO)
	for i := 0; i < numIO; i++ {
		info, infoErr := engine.TensorInfoAt(i)
		if infoErr != nil {
			continue
		}
		slog.Info("gpu: neural_key: tensor",
			"index", i, "name", info.Name,
			"is_input", info.IsInput, "dims", info.Dims)
	}

	// Create TRT execution context.
	trtCtx, err := engine.NewContext()
	if err != nil {
		engine.Close()
		return nil, fmt.Errorf("gpu: neural_key: create TRT context: %w", err)
	}

	// Allocate device buffers.
	var allocated []unsafe.Pointer
	cleanup := func() {
		for i := len(allocated) - 1; i >= 0; i-- {
			FreeDeviceBytes(allocated[i])
		}
		trtCtx.Close()
		engine.Close()
	}
	alloc := func(size int, label string) (unsafe.Pointer, error) {
		buf, allocErr := AllocDeviceBytes(size)
		if allocErr != nil {
			cleanup()
			return nil, fmt.Errorf("gpu: neural_key: alloc %s: %w", label, allocErr)
		}
		allocated = append(allocated, buf)
		return buf, nil
	}

	// Model I/O buffers (float32).
	inputBuf, err := alloc(4*ckModelW*ckModelH*4, "input")  // [1,4,512,512]
	if err != nil {
		return nil, err
	}
	alphaBuf, err := alloc(1*ckModelW*ckModelH*4, "alpha")   // [1,1,512,512]
	if err != nil {
		return nil, err
	}
	fgBuf, err := alloc(3*ckModelW*ckModelH*4, "fg")         // [1,3,512,512]
	if err != nil {
		return nil, err
	}
	hintBuf, err := alloc(1*ckModelW*ckModelH*4, "hint")     // [1,1,512,512]
	if err != nil {
		return nil, err
	}

	// Chroma hint mask at program resolution (uint8).
	chromaHint, err := alloc(progW*progH, "chromaHint")
	if err != nil {
		return nil, err
	}

	nke := &NeuralKeyEngine{
		ctx:        ctx,
		engine:     engine,
		trtCtx:     trtCtx,
		inputBuf:   inputBuf,
		alphaBuf:   alphaBuf,
		fgBuf:      fgBuf,
		hintBuf:    hintBuf,
		chromaHint: chromaHint,
		progW:      progW,
		progH:      progH,
	}

	slog.Info("gpu: neural_key: engine initialized",
		"prog_size", [2]int{progW, progH})
	return nke, nil
}

// Infer runs the full neural key pipeline on the program frame:
//
//  1. Generate chroma hint mask on program NV12 frame
//  2. NV12 → RGB float32 CHW at 512×512
//  3. ImageNet normalize
//  4. Resize chroma hint → 512×512 float32
//  5. TRT inference → alpha [1,1,512,512] + fg [1,3,512,512]
//  6. Composite: upscale + despill + alpha blend onto NV12
//
// All operations run on the provided CUDA stream. Caller must sync after.
func (nke *NeuralKeyEngine) Infer(
	frame *GPUFrame,
	keyCb, keyCr uint8,
	similarity, smoothness float32,
	stream C.cudaStream_t,
) error {
	w := frame.Width
	h := frame.Height
	pitch := frame.Pitch
	nv12 := unsafe.Pointer(uintptr(frame.DevPtr))

	// Step 1: Generate coarse chroma hint mask (uint8 [progH, progW]).
	// Convert similarity/smoothness to squared distance thresholds.
	simDist := similarity * 255.0
	totalDist := (similarity + smoothness) * 255.0
	simDistSq := int(simDist * simDist)
	totalDistSq := int(totalDist * totalDist)
	if totalDistSq <= simDistSq {
		totalDistSq = simDistSq + 1
	}
	rc := C.chroma_key_nv12(
		(*C.uint8_t)(nv12),
		(*C.uint8_t)(nke.chromaHint),
		C.int(w), C.int(h), C.int(pitch),
		C.uint8_t(keyCb), C.uint8_t(keyCr),
		C.int(simDistSq), C.int(totalDistSq),
		C.float(0), // no spill suppress for hint generation
		C.uint8_t(128), C.uint8_t(128), // neutral replace
		stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: neural_key: chroma hint failed: cuda %d", rc)
	}

	// Step 2: NV12 → RGB float32 CHW at model resolution.
	// inputBuf layout: [1, 4, 512, 512]. Write RGB to first 3 channels.
	rgbPtr := nke.inputBuf // offset 0: channels 0-2
	rc = C.nv12_to_rgb_chw(
		(*C.float)(rgbPtr),
		(*C.uint8_t)(nv12),
		C.int(w), C.int(h), C.int(pitch),
		C.int(ckModelW), C.int(ckModelH),
		stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: neural_key: nv12_to_rgb failed: cuda %d", rc)
	}

	// Step 3: ImageNet normalize RGB in-place.
	rc = C.ck_imagenet_normalize(
		(*C.float)(rgbPtr),
		C.int(ckModelH), C.int(ckModelW),
		stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: neural_key: normalize failed: cuda %d", rc)
	}

	// Step 4: Resize chroma hint (uint8 [progH, progW]) → float32 [512, 512].
	rc = C.ck_resize_mask_u8_f32(
		(*C.float)(nke.hintBuf),
		(*C.uint8_t)(nke.chromaHint), C.int(w), C.int(h), C.int(w),
		C.int(ckModelW), C.int(ckModelH),
		stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: neural_key: hint resize failed: cuda %d", rc)
	}

	// Step 4b: Copy hint into channel 3 of the input tensor.
	// inputBuf is [1, 4, 512, 512]. Channels 0-2 are RGB, channel 3 is the hint.
	hintDstOffset := uintptr(3 * ckModelW * ckModelH * 4) // float32 offset for channel 3
	rc = C.cudaMemcpyAsync(
		unsafe.Pointer(uintptr(nke.inputBuf)+hintDstOffset),
		nke.hintBuf,
		C.size_t(ckModelW*ckModelH*4),
		C.cudaMemcpyDeviceToDevice,
		stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: neural_key: hint copy failed: cuda %d", rc)
	}

	// Step 5: TensorRT inference.
	// CorridorKey model: input "input" [1,4,512,512] → output "alpha" [1,1,512,512] + "fg" [1,3,512,512]
	bindings := []TRTBinding{
		{Name: "input", DevPtr: nke.inputBuf, Dims: []int{1, 4, ckModelH, ckModelW}},
		{Name: "alpha", DevPtr: nke.alphaBuf, Dims: []int{1, 1, ckModelH, ckModelW}},
		{Name: "fg", DevPtr: nke.fgBuf, Dims: []int{1, 3, ckModelH, ckModelW}},
	}
	if err := nke.trtCtx.InferMulti(bindings, unsafe.Pointer(stream)); err != nil {
		return fmt.Errorf("gpu: neural_key: TRT inference failed: %w", err)
	}

	// Diagnostic: sync stream and read back input + alpha values to verify pipeline.
	if !nke.warmupDone.Load() {
		C.cudaStreamSynchronize(stream)
		// Sample input tensor: first 4 values of each channel (R,G,B,Hint at pixel 0-3)
		var inputSample [16]float32
		// Channel 0 (R) first 4 pixels
		C.cudaMemcpy(unsafe.Pointer(&inputSample[0]), nke.inputBuf,
			C.size_t(4*4), C.cudaMemcpyDeviceToHost)
		// Channel 3 (Hint) first 4 pixels
		ch3Offset := uintptr(3*ckModelW*ckModelH) * 4
		C.cudaMemcpy(unsafe.Pointer(&inputSample[4]),
			unsafe.Pointer(uintptr(nke.inputBuf)+ch3Offset),
			C.size_t(4*4), C.cudaMemcpyDeviceToHost)
		// Middle of hint channel
		ch3MidOffset := ch3Offset + uintptr(ckModelW*ckModelH/2)*4
		C.cudaMemcpy(unsafe.Pointer(&inputSample[8]),
			unsafe.Pointer(uintptr(nke.inputBuf)+ch3MidOffset),
			C.size_t(4*4), C.cudaMemcpyDeviceToHost)
		slog.Info("gpu: neural_key: INPUT sample",
			"R[0:4]", fmt.Sprintf("%.3f %.3f %.3f %.3f", inputSample[0], inputSample[1], inputSample[2], inputSample[3]),
			"Hint[0:4]", fmt.Sprintf("%.3f %.3f %.3f %.3f", inputSample[4], inputSample[5], inputSample[6], inputSample[7]),
			"HintMid[0:4]", fmt.Sprintf("%.3f %.3f %.3f %.3f", inputSample[8], inputSample[9], inputSample[10], inputSample[11]))
	}

	// Diagnostic: sync stream and read back alpha values to verify model output.
	if !nke.warmupDone.Load() {
		C.cudaStreamSynchronize(stream)
		var sample [16]float32
		C.cudaMemcpy(unsafe.Pointer(&sample[0]), nke.alphaBuf,
			C.size_t(16*4), C.cudaMemcpyDeviceToHost)
		slog.Info("gpu: neural_key: alpha sample (first 16 pixels)",
			"values", fmt.Sprintf("%.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f",
				sample[0], sample[1], sample[2], sample[3],
				sample[4], sample[5], sample[6], sample[7],
				sample[8], sample[9], sample[10], sample[11],
				sample[12], sample[13], sample[14], sample[15]))
		// Also sample from middle of alpha buffer
		midOffset := uintptr(ckModelW*ckModelH/2) * 4
		C.cudaMemcpy(unsafe.Pointer(&sample[0]),
			unsafe.Pointer(uintptr(nke.alphaBuf)+midOffset),
			C.size_t(16*4), C.cudaMemcpyDeviceToHost)
		// Sample at 25% and 75% of the alpha buffer for broader view
		q1Offset := uintptr(ckModelW*ckModelH/4) * 4
		C.cudaMemcpy(unsafe.Pointer(&sample[0]),
			unsafe.Pointer(uintptr(nke.alphaBuf)+q1Offset),
			C.size_t(16*4), C.cudaMemcpyDeviceToHost)
		slog.Info("gpu: neural_key: alpha sample (25% pixels)",
			"values", fmt.Sprintf("%.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f",
				sample[0], sample[1], sample[2], sample[3],
				sample[4], sample[5], sample[6], sample[7]))
		q3Offset := uintptr(ckModelW*ckModelH*3/4) * 4
		C.cudaMemcpy(unsafe.Pointer(&sample[0]),
			unsafe.Pointer(uintptr(nke.alphaBuf)+q3Offset),
			C.size_t(16*4), C.cudaMemcpyDeviceToHost)
		slog.Info("gpu: neural_key: alpha sample (75% pixels)",
			"values", fmt.Sprintf("%.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f",
				sample[0], sample[1], sample[2], sample[3],
				sample[4], sample[5], sample[6], sample[7]))
		// Also find max alpha across entire buffer
		allAlpha := make([]float32, ckModelW*ckModelH)
		C.cudaMemcpy(unsafe.Pointer(&allAlpha[0]), nke.alphaBuf,
			C.size_t(ckModelW*ckModelH*4), C.cudaMemcpyDeviceToHost)
		maxA := float32(0)
		minA := float32(1)
		sumA := float32(0)
		for _, v := range allAlpha {
			if v > maxA { maxA = v }
			if v < minA { minA = v }
			sumA += v
		}
		meanA := sumA / float32(len(allAlpha))
		slog.Info("gpu: neural_key: alpha stats",
			"min", fmt.Sprintf("%.4f", minA),
			"max", fmt.Sprintf("%.4f", maxA),
			"mean", fmt.Sprintf("%.4f", meanA))

		slog.Info("gpu: neural_key: alpha sample (middle 16 pixels)",
			"values", fmt.Sprintf("%.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f %.3f",
				sample[0], sample[1], sample[2], sample[3],
				sample[4], sample[5], sample[6], sample[7],
				sample[8], sample[9], sample[10], sample[11],
				sample[12], sample[13], sample[14], sample[15]))
	}

	// Step 6: Composite — upscale alpha+fg from 512 to program res, despill, blend.
	rc = C.ck_composite_neural(
		(*C.uint8_t)(nv12),
		(*C.float)(nke.alphaBuf),
		(*C.float)(nke.fgBuf),
		C.int(w), C.int(h), C.int(pitch),
		C.int(ckModelW), C.int(ckModelH),
		stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: neural_key: composite failed: cuda %d", rc)
	}

	nke.warmupDone.Store(true)
	return nil
}

// WarmupDone returns true after the first successful inference.
func (nke *NeuralKeyEngine) WarmupDone() bool {
	return nke.warmupDone.Load()
}

// Close releases all GPU resources.
func (nke *NeuralKeyEngine) Close() {
	if nke == nil {
		return
	}
	if nke.inputBuf != nil {
		FreeDeviceBytes(nke.inputBuf)
	}
	if nke.alphaBuf != nil {
		FreeDeviceBytes(nke.alphaBuf)
	}
	if nke.fgBuf != nil {
		FreeDeviceBytes(nke.fgBuf)
	}
	if nke.hintBuf != nil {
		FreeDeviceBytes(nke.hintBuf)
	}
	if nke.chromaHint != nil {
		FreeDeviceBytes(nke.chromaHint)
	}
	if nke.trtCtx != nil {
		nke.trtCtx.Close()
	}
	if nke.engine != nil {
		nke.engine.Close()
	}
	slog.Info("gpu: neural_key: engine closed")
}
