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

extern int trt_build_engine_v2(const char* onnxPath, const char* planPath,
                               int maxBatch, int maxSeqLen, int useFP16, int useINT8);
extern int trt_get_num_io(void* engineHandle);
extern int trt_get_tensor_info(void* engineHandle, int index,
                               char* name_buf, int name_buf_size,
                               int* is_input, int* dtype, int* ndims, int* dims);

typedef struct {
    const char* name;
    void* devPtr;
    int dims[8];
    int ndims;
} TensorBinding;

extern int trt_infer_multi(void* contextHandle,
                           TensorBinding* bindings, int numBindings,
                           void* stream);
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
func (c *TRTContext) Infer(input, output unsafe.Pointer, batchSize int, stream unsafe.Pointer) error {
	if c == nil || c.handle == nil {
		return fmt.Errorf("gpu: tensorrt: nil context")
	}
	if input == nil || output == nil {
		return fmt.Errorf("gpu: tensorrt: nil input or output pointer")
	}

	rc := C.trt_infer(c.handle, input, output, C.int(batchSize), stream)
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

// TensorInfo describes an I/O tensor in a TensorRT engine.
type TensorInfo struct {
	Name    string
	IsInput bool
	DType   int   // 0=float32, 1=half, 3=int32, 6=int64
	Dims    []int // -1 = dynamic
}

// NumIOTensors returns the total number of I/O tensors in the engine.
func (e *TRTEngine) NumIOTensors() int {
	if e == nil || e.handle == nil {
		return 0
	}
	return int(C.trt_get_num_io(unsafe.Pointer(e.handle)))
}

// TensorInfoAt returns metadata for the i-th I/O tensor.
func (e *TRTEngine) TensorInfoAt(index int) (TensorInfo, error) {
	if e == nil || e.handle == nil {
		return TensorInfo{}, fmt.Errorf("gpu: tensorrt: nil engine")
	}

	var nameBuf [256]C.char
	var isInput, dtype, ndims C.int
	var dims [8]C.int

	rc := C.trt_get_tensor_info(unsafe.Pointer(e.handle), C.int(index),
		&nameBuf[0], 256,
		&isInput, &dtype, &ndims, &dims[0])
	if rc != 0 {
		errMsg := C.GoString(C.trt_get_last_error())
		return TensorInfo{}, fmt.Errorf("gpu: tensorrt: get tensor info: %s", errMsg)
	}

	info := TensorInfo{
		Name:    C.GoString(&nameBuf[0]),
		IsInput: isInput != 0,
		DType:   int(dtype),
	}
	for i := 0; i < int(ndims); i++ {
		info.Dims = append(info.Dims, int(dims[i]))
	}
	return info, nil
}

// TRTEngineOptsV2 extends TRTEngineOpts with sequence length for decoder models.
type TRTEngineOptsV2 struct {
	MaxBatchSize  int
	MaxSeqLen     int    // Max sequence length for dynamic dims (e.g., 448 for Whisper)
	UseFP16       bool
	UseINT8       bool
	PlanCachePath string
}

// NewTRTEngineV2 builds a TensorRT engine with full dynamic dimension support.
// Unlike NewTRTEngine, this handles dynamic dimensions on ANY axis (not just batch).
func NewTRTEngineV2(onnxPath string, opts TRTEngineOptsV2) (*TRTEngine, error) {
	if _, err := os.Stat(onnxPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("gpu: tensorrt: ONNX file not found: %s", onnxPath)
	}

	maxBatch := opts.MaxBatchSize
	if maxBatch <= 0 {
		maxBatch = 1
	}
	maxSeqLen := opts.MaxSeqLen
	if maxSeqLen <= 0 {
		maxSeqLen = 448
	}

	planPath := opts.PlanCachePath

	// Try loading cached plan first.
	if planPath != "" {
		if _, err := os.Stat(planPath); err == nil {
			return loadPlan(planPath)
		}
	}

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

	rc := C.trt_build_engine_v2(cOnnx, cPlan, C.int(maxBatch), C.int(maxSeqLen), useFP16, useINT8)
	if rc != 0 {
		errMsg := C.GoString(C.trt_get_last_error())
		return nil, fmt.Errorf("gpu: tensorrt: build v2 failed: %s", errMsg)
	}

	if planPath != "" {
		return loadPlan(planPath)
	}
	return nil, fmt.Errorf("gpu: tensorrt: PlanCachePath is required")
}

// TRTBinding describes a single named tensor binding for multi-input inference.
type TRTBinding struct {
	Name   string
	DevPtr unsafe.Pointer
	Dims   []int
}

// InferMulti runs inference with explicit per-tensor bindings.
// Each binding specifies a tensor name, device pointer, and actual dimensions.
func (c *TRTContext) InferMulti(bindings []TRTBinding, stream unsafe.Pointer) error {
	if c == nil || c.handle == nil {
		return fmt.Errorf("gpu: tensorrt: nil context")
	}
	if len(bindings) == 0 {
		return fmt.Errorf("gpu: tensorrt: no bindings provided")
	}

	// Allocate C bindings array.
	cBindings := make([]C.TensorBinding, len(bindings))
	cNames := make([]*C.char, len(bindings)) // prevent GC

	for i, b := range bindings {
		cNames[i] = C.CString(b.Name)
		cBindings[i].name = cNames[i]
		cBindings[i].devPtr = b.DevPtr
		cBindings[i].ndims = C.int(len(b.Dims))
		for d := 0; d < len(b.Dims) && d < 8; d++ {
			cBindings[i].dims[d] = C.int(b.Dims[d])
		}
	}
	defer func() {
		for _, cn := range cNames {
			C.free(unsafe.Pointer(cn))
		}
	}()

	rc := C.trt_infer_multi(unsafe.Pointer(c.handle), &cBindings[0], C.int(len(cBindings)), stream)
	if rc != 0 {
		errMsg := C.GoString(C.trt_get_last_error())
		return fmt.Errorf("gpu: tensorrt: infer_multi failed: %s", errMsg)
	}
	return nil
}
