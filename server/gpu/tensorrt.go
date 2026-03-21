//go:build cgo && cuda && tensorrt

package gpu

/*
#cgo CFLAGS: -I/usr/local/cuda/include -I/usr/include/x86_64-linux-gnu
#cgo LDFLAGS: -L${SRCDIR}/cuda -lswitchframe_tensorrt -L/usr/local/cuda/lib64 -lnvinfer -lnvonnxparser -lnvinfer_plugin -lstdc++

#include <cuda_runtime.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

// TensorRT is a C++ API, so we use extern "C" wrapper functions compiled
// separately in cuda/tensorrt_wrapper.cpp → libswitchframe_tensorrt.a.
// This preamble declares the C-linkage function signatures that the Go
// code calls through cgo.
typedef void* trt_engine_t;
typedef void* trt_context_t;

extern int trt_build_engine(const char* onnxPath, const char* planPath,
                            int maxBatch, int useFP16, int useINT8);
extern trt_engine_t trt_load_engine(const char* planPath);
extern trt_context_t trt_create_context(trt_engine_t engine);
extern int trt_infer(trt_context_t context, void* inputDevPtr, void* outputDevPtr,
                     int batchSize, void* stream);
extern int trt_get_input_size(trt_engine_t engine);
extern int trt_get_output_size(trt_engine_t engine);
extern void trt_destroy_context(trt_context_t context);
extern void trt_destroy_engine(trt_engine_t engine);
extern const char* trt_get_last_error(void);
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"unsafe"
)

// ErrTensorRTNotAvailable indicates TensorRT is not available.
var ErrTensorRTNotAvailable = errors.New("gpu: TensorRT not available")

// TRTEngine holds a deserialized TensorRT engine (ICudaEngine).
type TRTEngine struct {
	handle    C.trt_engine_t
	inputSize int
	outputSize int
}

// TRTContext holds an execution context for inference.
type TRTContext struct {
	handle C.trt_context_t
	engine *TRTEngine
}

// TRTEngineOpts configures engine building.
type TRTEngineOpts struct {
	MaxBatchSize  int
	UseFP16       bool
	UseINT8       bool
	PlanCachePath string // if set, cache/load serialized .plan here
}

// NewTRTEngine builds or loads a TensorRT engine from an ONNX model.
//
// If opts.PlanCachePath is set, the function first checks for a cached .plan
// file. If found, it loads directly (skipping the ONNX→engine build which can
// take 30+ seconds). If not found, it builds from ONNX and saves the .plan.
func NewTRTEngine(onnxPath string, opts TRTEngineOpts) (*TRTEngine, error) {
	if _, err := os.Stat(onnxPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("gpu: tensorrt: ONNX file not found: %s", onnxPath)
	}

	maxBatch := opts.MaxBatchSize
	if maxBatch <= 0 {
		maxBatch = 1
	}

	planPath := opts.PlanCachePath

	// Try loading cached plan first.
	if planPath != "" {
		if _, err := os.Stat(planPath); err == nil {
			return loadPlan(planPath)
		}
	}

	// Build from ONNX.
	cOnnx := C.CString(onnxPath)
	defer C.free(unsafe.Pointer(cOnnx))

	var cPlan *C.char
	if planPath != "" {
		cPlan = C.CString(planPath)
		defer C.free(unsafe.Pointer(cPlan))
	}

	useFP16 := C.int(0)
	if opts.UseFP16 {
		useFP16 = 1
	}
	useINT8 := C.int(0)
	if opts.UseINT8 {
		useINT8 = 1
	}

	rc := C.trt_build_engine(cOnnx, cPlan, C.int(maxBatch), useFP16, useINT8)
	if rc != 0 {
		errMsg := C.GoString(C.trt_get_last_error())
		return nil, fmt.Errorf("gpu: tensorrt: build failed: %s", errMsg)
	}

	// If we saved a plan, load it. Otherwise the build produced a temporary engine.
	if planPath != "" {
		return loadPlan(planPath)
	}

	// Build without plan caching: we still need to load the engine.
	// Re-build is the only option without a plan path (the C API serializes to file).
	return nil, fmt.Errorf("gpu: tensorrt: PlanCachePath is required (engine must be serialized for loading)")
}

// loadPlan loads a serialized TensorRT engine plan from disk.
func loadPlan(planPath string) (*TRTEngine, error) {
	cPlan := C.CString(planPath)
	defer C.free(unsafe.Pointer(cPlan))

	handle := C.trt_load_engine(cPlan)
	if handle == nil {
		errMsg := C.GoString(C.trt_get_last_error())
		return nil, fmt.Errorf("gpu: tensorrt: load plan failed: %s", errMsg)
	}

	inputSize := int(C.trt_get_input_size(handle))
	outputSize := int(C.trt_get_output_size(handle))

	return &TRTEngine{
		handle:     handle,
		inputSize:  inputSize,
		outputSize: outputSize,
	}, nil
}

// NewContext creates an execution context for inference from this engine.
// Multiple contexts can share one engine for concurrent inference.
func (e *TRTEngine) NewContext() (*TRTContext, error) {
	if e == nil || e.handle == nil {
		return nil, fmt.Errorf("gpu: tensorrt: nil engine")
	}

	handle := C.trt_create_context(e.handle)
	if handle == nil {
		errMsg := C.GoString(C.trt_get_last_error())
		return nil, fmt.Errorf("gpu: tensorrt: create context failed: %s", errMsg)
	}

	return &TRTContext{
		handle: handle,
		engine: e,
	}, nil
}

// InputSize returns the total number of float32 elements expected as input.
func (e *TRTEngine) InputSize() int {
	if e == nil {
		return 0
	}
	return e.inputSize
}

// OutputSize returns the total number of float32 elements produced as output.
func (e *TRTEngine) OutputSize() int {
	if e == nil {
		return 0
	}
	return e.outputSize
}

// Close releases the TensorRT engine resources.
func (e *TRTEngine) Close() {
	if e == nil || e.handle == nil {
		return
	}
	C.trt_destroy_engine(e.handle)
	e.handle = nil
}

// Infer runs async inference on the given CUDA stream.
// input and output must be CUDA device pointers with sufficient size:
//   - input:  engine.InputSize() * sizeof(float32) bytes
//   - output: engine.OutputSize() * sizeof(float32) bytes
func (c *TRTContext) Infer(input, output unsafe.Pointer, batchSize int, stream C.cudaStream_t) error {
	if c == nil || c.handle == nil {
		return fmt.Errorf("gpu: tensorrt: nil context")
	}
	if input == nil || output == nil {
		return fmt.Errorf("gpu: tensorrt: nil input or output pointer")
	}

	rc := C.trt_infer(c.handle, input, output, C.int(batchSize), unsafe.Pointer(stream))
	if rc != 0 {
		errMsg := C.GoString(C.trt_get_last_error())
		return fmt.Errorf("gpu: tensorrt: infer failed: %s", errMsg)
	}
	return nil
}

// Close releases the TensorRT execution context.
func (c *TRTContext) Close() {
	if c == nil || c.handle == nil {
		return
	}
	C.trt_destroy_context(c.handle)
	c.handle = nil
}
