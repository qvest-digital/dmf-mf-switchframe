// tensorrt_wrapper.h — C-linkage declarations for TensorRT wrapper functions.
// This header is consumed by:
//   - Go cgo preambles (tensorrt.go, depth/SAM inference wrappers)
//   - Other C/C++ code in the cuda/ directory that needs TensorRT interop
//
// The implementation lives in tensorrt_wrapper.cpp, compiled into
// libswitchframe_tensorrt.a via the cuda/Makefile.

#ifndef SWITCHFRAME_TENSORRT_WRAPPER_H
#define SWITCHFRAME_TENSORRT_WRAPPER_H

#include <cuda_runtime.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// ---------- Opaque handles ----------

// Engine handle: wraps TRTEngineWrapper (runtime + engine + scratch).
typedef void* trt_engine_t;

// Context handle: wraps nvinfer1::IExecutionContext.
typedef void* trt_context_t;

// ---------- Engine building ----------

// trt_build_engine builds a TensorRT engine from an ONNX model and serializes
// the result to planPath. Only the batch dimension (dim 0) is treated as
// dynamic. Returns 0 on success, non-zero on failure.
int trt_build_engine(const char* onnxPath, const char* planPath,
                     int maxBatch, int useFP16, int useINT8);

// trt_build_engine_v2 builds with full dynamic dimension support. All
// dynamic dimensions (not just batch) get optimization profiles.
// maxSeqLen controls non-batch dynamic dims; maxEncSeqLen controls
// encoder-specific dims (0 = use maxSeqLen).
int trt_build_engine_v2(const char* onnxPath, const char* planPath,
                        int maxBatch, int maxSeqLen, int maxEncSeqLen,
                        int useFP16, int useINT8);

// ---------- Engine loading ----------

// trt_load_engine deserializes a .plan file into an engine.
// Returns engine handle or NULL on failure.
trt_engine_t trt_load_engine(const char* planPath);

// trt_load_engine_from_memory deserializes an engine from a memory buffer.
// Returns engine handle or NULL on failure.
trt_engine_t trt_load_engine_from_memory(const void* data, int size);

// ---------- Context management ----------

// trt_create_context creates an execution context from an engine.
// Multiple contexts can share one engine for concurrent inference.
trt_context_t trt_create_context(trt_engine_t engine);

// ---------- Single-input inference ----------

// trt_infer runs async inference with a single input/output pair.
// inputDevPtr and outputDevPtr are CUDA device pointers.
// stream is a cudaStream_t.
int trt_infer(trt_context_t context, void* inputDevPtr, void* outputDevPtr,
              int batchSize, void* stream);

// ---------- Multi-binding inference ----------

// TensorBinding describes a named tensor with its device pointer and shape.
typedef struct {
    const char* name;
    void* devPtr;
    int dims[8];
    int ndims;
} TensorBinding;

// trt_infer_multi runs inference with explicit per-tensor bindings.
// Input tensors have their shapes set via setInputShape(). Unbound output
// tensors are automatically bound to a thread-local scratch buffer.
int trt_infer_multi(void* contextHandle,
                    TensorBinding* bindings, int numBindings,
                    void* stream);

// ---------- Engine introspection ----------

// trt_get_input_size returns the total number of float elements for the
// first input tensor.
int trt_get_input_size(trt_engine_t engine);

// trt_get_output_size returns the total number of float elements for the
// first output tensor.
int trt_get_output_size(trt_engine_t engine);

// trt_get_num_io returns the total number of I/O tensors in the engine.
int trt_get_num_io(void* engineHandle);

// trt_get_tensor_info returns metadata about a tensor by index.
// name_buf receives the tensor name (null-terminated).
// is_input: 1 for input, 0 for output.
// dtype: nvinfer1::DataType enum (0=float32, 1=half, 3=int32, 6=int64).
// ndims/dims describe the shape (-1 = dynamic dimension).
int trt_get_tensor_info(void* engineHandle, int index,
                        char* name_buf, int name_buf_size,
                        int* is_input, int* dtype, int* ndims, int* dims);

// trt_dtype_size returns the element byte size for a TensorRT DataType value.
// Returns 0 for unknown types.
//   0 (kFLOAT)  -> 4
//   1 (kHALF)   -> 2
//   2 (kINT8)   -> 1
//   3 (kINT32)  -> 4
//   4 (kBOOL)   -> 1
//   6 (kINT64)  -> 8
int trt_dtype_size(int dtype);

// ---------- Cleanup ----------

// trt_destroy_context releases an execution context.
void trt_destroy_context(trt_context_t context);

// trt_destroy_engine releases an engine and its runtime.
void trt_destroy_engine(trt_engine_t engine);

// ---------- Error handling ----------

// trt_get_last_error returns the last error message (thread-local buffer).
const char* trt_get_last_error(void);

#ifdef __cplusplus
}
#endif

#endif // SWITCHFRAME_TENSORRT_WRAPPER_H
