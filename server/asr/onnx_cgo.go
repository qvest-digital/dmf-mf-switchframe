//go:build cgo && cuda

package asr

/*
#cgo CFLAGS: -I/opt/onnxruntime/include
#cgo LDFLAGS: -L/opt/onnxruntime/lib -lonnxruntime -Wl,-rpath,/opt/onnxruntime/lib

#include <onnxruntime_c_api.h>
#include <stdlib.h>

// Helper: get the OrtApi pointer. Called once at init.
static const OrtApi* getOrtApi() {
    return OrtGetApiBase()->GetApi(ORT_API_VERSION);
}

// Helper: create environment.
static OrtStatusPtr createEnv(const OrtApi* api, OrtEnv** env) {
    return api->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "switchframe", env);
}

// Helper: create session options.
static OrtStatusPtr createSessionOptions(const OrtApi* api, OrtSessionOptions** opts) {
    return api->CreateSessionOptions(opts);
}

// Helper: set intra-op thread count.
static OrtStatusPtr setIntraOpThreads(const OrtApi* api, OrtSessionOptions* opts, int count) {
    return api->SetIntraOpNumThreads(opts, count);
}

// Helper: set graph optimization level.
static OrtStatusPtr setGraphOptLevel(const OrtApi* api, OrtSessionOptions* opts) {
    return api->SetSessionGraphOptimizationLevel(opts, ORT_ENABLE_ALL);
}

// Helper: append CUDA execution provider.
static OrtStatusPtr appendCUDA(const OrtApi* api, OrtSessionOptions* opts) {
    OrtCUDAProviderOptionsV2* cuda_opts = NULL;
    OrtStatusPtr status = api->CreateCUDAProviderOptions(&cuda_opts);
    if (status != NULL) return status;

    // Set device_id = 0.
    const char* keys[] = {"device_id"};
    const char* values[] = {"0"};
    status = api->UpdateCUDAProviderOptions(cuda_opts, keys, values, 1);
    if (status != NULL) {
        api->ReleaseCUDAProviderOptions(cuda_opts);
        return status;
    }

    status = api->SessionOptionsAppendExecutionProvider_CUDA_V2(opts, cuda_opts);
    api->ReleaseCUDAProviderOptions(cuda_opts);
    return status;
}

// Helper: create session from file.
static OrtStatusPtr createSession(const OrtApi* api, OrtEnv* env,
    const char* model_path, OrtSessionOptions* opts, OrtSession** session) {
    return api->CreateSession(env, model_path, opts, session);
}

// Helper: create CPU memory info.
static OrtStatusPtr createCpuMemInfo(const OrtApi* api, OrtMemoryInfo** mem_info) {
    return api->CreateCpuMemoryInfo(OrtArenaAllocator, OrtMemTypeDefault, mem_info);
}

// Helper: create float32 tensor.
static OrtStatusPtr createTensorFloat(const OrtApi* api, OrtMemoryInfo* mem_info,
    float* data, size_t data_len, int64_t* shape, size_t shape_len, OrtValue** value) {
    return api->CreateTensorWithDataAsOrtValue(mem_info, data, data_len * sizeof(float),
        shape, shape_len, ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, value);
}

// Helper: create int64 tensor.
static OrtStatusPtr createTensorInt64(const OrtApi* api, OrtMemoryInfo* mem_info,
    int64_t* data, size_t data_len, int64_t* shape, size_t shape_len, OrtValue** value) {
    return api->CreateTensorWithDataAsOrtValue(mem_info, data, data_len * sizeof(int64_t),
        shape, shape_len, ONNX_TENSOR_ELEMENT_DATA_TYPE_INT64, value);
}

// Helper: create int32 tensor.
static OrtStatusPtr createTensorInt32(const OrtApi* api, OrtMemoryInfo* mem_info,
    int32_t* data, size_t data_len, int64_t* shape, size_t shape_len, OrtValue** value) {
    return api->CreateTensorWithDataAsOrtValue(mem_info, data, data_len * sizeof(int32_t),
        shape, shape_len, ONNX_TENSOR_ELEMENT_DATA_TYPE_INT32, value);
}

// Helper: run session. Uses arrays of C strings and OrtValue pointers.
static OrtStatusPtr runSession(const OrtApi* api, OrtSession* session,
    const char** input_names, const OrtValue* const* inputs, size_t num_inputs,
    const char** output_names, OrtValue** outputs, size_t num_outputs) {
    return api->Run(session, NULL, input_names, inputs, num_inputs,
        output_names, num_outputs, outputs);
}

// Helper: get tensor float data pointer.
static OrtStatusPtr getTensorFloatData(const OrtApi* api, OrtValue* value, float** data) {
    return api->GetTensorMutableData(value, (void**)data);
}

// Helper: get tensor int64 data pointer.
static OrtStatusPtr getTensorInt64Data(const OrtApi* api, OrtValue* value, int64_t** data) {
    return api->GetTensorMutableData(value, (void**)data);
}

// Helper: get tensor int32 data pointer.
static OrtStatusPtr getTensorInt32Data(const OrtApi* api, OrtValue* value, int32_t** data) {
    return api->GetTensorMutableData(value, (void**)data);
}

// Helper: get tensor shape info.
static OrtStatusPtr getTensorShapeInfo(const OrtApi* api, OrtValue* value,
    OrtTensorTypeAndShapeInfo** info) {
    return api->GetTensorTypeAndShape(value, info);
}

// Helper: get shape dimensions.
static OrtStatusPtr getShapeDims(const OrtApi* api, OrtTensorTypeAndShapeInfo* info,
    int64_t* dims, size_t count) {
    return api->GetDimensions(info, dims, count);
}

// Helper: get shape dimension count.
static OrtStatusPtr getShapeNumDims(const OrtApi* api, OrtTensorTypeAndShapeInfo* info,
    size_t* count) {
    return api->GetDimensionsCount(info, count);
}

// Helper: get element count.
static OrtStatusPtr getElementCount(const OrtApi* api, OrtTensorTypeAndShapeInfo* info,
    size_t* count) {
    return api->GetTensorShapeElementCount(info, count);
}

// Helper: get error message.
static const char* getErrorMessage(const OrtApi* api, OrtStatusPtr status) {
    return api->GetErrorMessage(status);
}

// Helper: release status.
static void releaseStatus(const OrtApi* api, OrtStatusPtr status) {
    api->ReleaseStatus(status);
}

// Helper: release value.
static void releaseValue(const OrtApi* api, OrtValue* value) {
    api->ReleaseValue(value);
}

// Helper: release session.
static void releaseSession(const OrtApi* api, OrtSession* session) {
    api->ReleaseSession(session);
}

// Helper: release session options.
static void releaseSessionOptions(const OrtApi* api, OrtSessionOptions* opts) {
    api->ReleaseSessionOptions(opts);
}

// Helper: release env.
static void releaseEnv(const OrtApi* api, OrtEnv* env) {
    api->ReleaseEnv(env);
}

// Helper: release memory info.
static void releaseMemoryInfo(const OrtApi* api, OrtMemoryInfo* mem_info) {
    api->ReleaseMemoryInfo(mem_info);
}

// Helper: release tensor type and shape info.
static void releaseTensorTypeAndShapeInfo(const OrtApi* api, OrtTensorTypeAndShapeInfo* info) {
    api->ReleaseTensorTypeAndShapeInfo(info);
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// Shared ORT environment — one per process. ONNX Runtime recommends a single
// OrtEnv for all sessions. The shared env is never released (lives for process
// lifetime); individual sessions release only their own resources.
var (
	sharedOrtOnce sync.Once
	sharedOrtApi  *C.OrtApi
	sharedOrtEnv  *C.OrtEnv
	sharedOrtErr  error
)

func getSharedOrtEnv() (*C.OrtApi, *C.OrtEnv, error) {
	sharedOrtOnce.Do(func() {
		api := C.getOrtApi()
		if api == nil {
			sharedOrtErr = fmt.Errorf("onnxruntime: failed to get API")
			return
		}
		var env *C.OrtEnv
		if err := onnxError(api, C.createEnv(api, &env)); err != nil {
			sharedOrtErr = fmt.Errorf("onnxruntime: create env: %w", err)
			return
		}
		sharedOrtApi = api
		sharedOrtEnv = env
	})
	return sharedOrtApi, sharedOrtEnv, sharedOrtErr
}

// OnnxSession wraps an ONNX Runtime session for model inference.
type OnnxSession struct {
	api      *C.OrtApi
	env      *C.OrtEnv
	session  *C.OrtSession
	memInfo  *C.OrtMemoryInfo
	ownsEnv  bool // false when using shared env
}

// OnnxValue wraps an OrtValue tensor.
type OnnxValue struct {
	api   *C.OrtApi
	value *C.OrtValue
}

// onnxError converts an OrtStatus to a Go error, releasing the status.
func onnxError(api *C.OrtApi, status C.OrtStatusPtr) error {
	if status == nil {
		return nil
	}
	msg := C.GoString(C.getErrorMessage(api, status))
	C.releaseStatus(api, status)
	return fmt.Errorf("onnxruntime: %s", msg)
}

// NewOnnxSession creates an ONNX Runtime session for the given model file.
// If useGPU is true, the CUDA execution provider is appended.
// All sessions share a single process-level OrtEnv (ONNX Runtime best practice).
func NewOnnxSession(modelPath string, useGPU bool) (*OnnxSession, error) {
	api, env, err := getSharedOrtEnv()
	if err != nil {
		return nil, err
	}

	// Create session options.
	var opts *C.OrtSessionOptions
	if err := onnxError(api, C.createSessionOptions(api, &opts)); err != nil {
		return nil, fmt.Errorf("onnxruntime: create session options: %w", err)
	}

	// Set threading and optimization.
	if err := onnxError(api, C.setIntraOpThreads(api, opts, 4)); err != nil {
		C.releaseSessionOptions(api, opts)
		return nil, fmt.Errorf("onnxruntime: set threads: %w", err)
	}
	if err := onnxError(api, C.setGraphOptLevel(api, opts)); err != nil {
		C.releaseSessionOptions(api, opts)
		return nil, fmt.Errorf("onnxruntime: set opt level: %w", err)
	}

	// Append CUDA if requested.
	if useGPU {
		if err := onnxError(api, C.appendCUDA(api, opts)); err != nil {
			C.releaseSessionOptions(api, opts)
			return nil, fmt.Errorf("onnxruntime: append CUDA: %w", err)
		}
	}

	// Create session.
	cPath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cPath))

	var session *C.OrtSession
	if err := onnxError(api, C.createSession(api, env, cPath, opts, &session)); err != nil {
		C.releaseSessionOptions(api, opts)
		return nil, fmt.Errorf("onnxruntime: create session for %s: %w", modelPath, err)
	}
	C.releaseSessionOptions(api, opts)

	// Create CPU memory info for tensor creation.
	var memInfo *C.OrtMemoryInfo
	if err := onnxError(api, C.createCpuMemInfo(api, &memInfo)); err != nil {
		C.releaseSession(api, session)
		return nil, fmt.Errorf("onnxruntime: create memory info: %w", err)
	}

	return &OnnxSession{
		api:     api,
		env:     env,
		session: session,
		memInfo: memInfo,
		ownsEnv: false, // shared env — not released by this session
	}, nil
}

// Run executes the model with the given inputs and returns the requested outputs.
// The caller must Close() each returned OnnxValue when done.
func (s *OnnxSession) Run(inputNames []string, inputValues []*OnnxValue, outputNames []string) ([]*OnnxValue, error) {
	if s.session == nil {
		return nil, fmt.Errorf("onnxruntime: session is nil")
	}

	numInputs := len(inputNames)
	numOutputs := len(outputNames)

	// Convert input names to C strings.
	cInputNames := make([]*C.char, numInputs)
	for i, name := range inputNames {
		cInputNames[i] = C.CString(name)
	}
	defer func() {
		for _, p := range cInputNames {
			C.free(unsafe.Pointer(p))
		}
	}()

	// Convert output names to C strings.
	cOutputNames := make([]*C.char, numOutputs)
	for i, name := range outputNames {
		cOutputNames[i] = C.CString(name)
	}
	defer func() {
		for _, p := range cOutputNames {
			C.free(unsafe.Pointer(p))
		}
	}()

	// Build input OrtValue array.
	cInputs := make([]*C.OrtValue, numInputs)
	for i, v := range inputValues {
		cInputs[i] = v.value
	}

	// Allocate output OrtValue array.
	cOutputs := make([]*C.OrtValue, numOutputs)

	// Run inference.
	status := C.runSession(s.api, s.session,
		(**C.char)(unsafe.Pointer(&cInputNames[0])),
		(**C.OrtValue)(unsafe.Pointer(&cInputs[0])),
		C.size_t(numInputs),
		(**C.char)(unsafe.Pointer(&cOutputNames[0])),
		(**C.OrtValue)(unsafe.Pointer(&cOutputs[0])),
		C.size_t(numOutputs),
	)
	if err := onnxError(s.api, status); err != nil {
		return nil, fmt.Errorf("onnxruntime: run: %w", err)
	}

	// Wrap outputs.
	results := make([]*OnnxValue, numOutputs)
	for i := 0; i < numOutputs; i++ {
		results[i] = &OnnxValue{api: s.api, value: cOutputs[i]}
	}
	return results, nil
}

// NewOnnxValueFloat32 creates a float32 tensor OnnxValue. The data slice must
// remain valid for the lifetime of the OnnxValue (no copy is made).
func (s *OnnxSession) NewOnnxValueFloat32(data []float32, shape []int64) (*OnnxValue, error) {
	var value *C.OrtValue
	totalElements := int64(1)
	for _, d := range shape {
		totalElements *= d
	}
	if int64(len(data)) != totalElements {
		return nil, fmt.Errorf("onnxruntime: data length %d != shape product %d", len(data), totalElements)
	}

	status := C.createTensorFloat(s.api, s.memInfo,
		(*C.float)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		(*C.int64_t)(unsafe.Pointer(&shape[0])),
		C.size_t(len(shape)),
		&value,
	)
	if err := onnxError(s.api, status); err != nil {
		return nil, fmt.Errorf("onnxruntime: create float32 tensor: %w", err)
	}
	return &OnnxValue{api: s.api, value: value}, nil
}

// NewOnnxValueInt64 creates an int64 tensor OnnxValue.
func (s *OnnxSession) NewOnnxValueInt64(data []int64, shape []int64) (*OnnxValue, error) {
	var value *C.OrtValue
	totalElements := int64(1)
	for _, d := range shape {
		totalElements *= d
	}
	if int64(len(data)) != totalElements {
		return nil, fmt.Errorf("onnxruntime: data length %d != shape product %d", len(data), totalElements)
	}

	status := C.createTensorInt64(s.api, s.memInfo,
		(*C.int64_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		(*C.int64_t)(unsafe.Pointer(&shape[0])),
		C.size_t(len(shape)),
		&value,
	)
	if err := onnxError(s.api, status); err != nil {
		return nil, fmt.Errorf("onnxruntime: create int64 tensor: %w", err)
	}
	return &OnnxValue{api: s.api, value: value}, nil
}

// NewOnnxValueInt32 creates an int32 tensor OnnxValue.
func (s *OnnxSession) NewOnnxValueInt32(data []int32, shape []int64) (*OnnxValue, error) {
	var value *C.OrtValue
	totalElements := int64(1)
	for _, d := range shape {
		totalElements *= d
	}
	if int64(len(data)) != totalElements {
		return nil, fmt.Errorf("onnxruntime: data length %d != shape product %d", len(data), totalElements)
	}

	status := C.createTensorInt32(s.api, s.memInfo,
		(*C.int32_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		(*C.int64_t)(unsafe.Pointer(&shape[0])),
		C.size_t(len(shape)),
		&value,
	)
	if err := onnxError(s.api, status); err != nil {
		return nil, fmt.Errorf("onnxruntime: create int32 tensor: %w", err)
	}
	return &OnnxValue{api: s.api, value: value}, nil
}

// GetFloat32Data extracts float32 data from an output tensor.
// The returned slice points to ONNX Runtime-owned memory and is valid until Close().
func (v *OnnxValue) GetFloat32Data() ([]float32, error) {
	if v.value == nil {
		return nil, fmt.Errorf("onnxruntime: nil value")
	}

	var ptr *C.float
	if err := onnxError(v.api, C.getTensorFloatData(v.api, v.value, &ptr)); err != nil {
		return nil, err
	}

	count, err := v.elementCount()
	if err != nil {
		return nil, err
	}

	return unsafe.Slice((*float32)(unsafe.Pointer(ptr)), count), nil
}

// GetInt64Data extracts int64 data from an output tensor.
func (v *OnnxValue) GetInt64Data() ([]int64, error) {
	if v.value == nil {
		return nil, fmt.Errorf("onnxruntime: nil value")
	}

	var ptr *C.int64_t
	if err := onnxError(v.api, C.getTensorInt64Data(v.api, v.value, &ptr)); err != nil {
		return nil, err
	}

	count, err := v.elementCount()
	if err != nil {
		return nil, err
	}

	return unsafe.Slice((*int64)(unsafe.Pointer(ptr)), count), nil
}

// GetInt32Data extracts int32 data from an output tensor.
func (v *OnnxValue) GetInt32Data() ([]int32, error) {
	if v.value == nil {
		return nil, fmt.Errorf("onnxruntime: nil value")
	}

	var ptr *C.int32_t
	if err := onnxError(v.api, C.getTensorInt32Data(v.api, v.value, &ptr)); err != nil {
		return nil, err
	}

	count, err := v.elementCount()
	if err != nil {
		return nil, err
	}

	return unsafe.Slice((*int32)(unsafe.Pointer(ptr)), count), nil
}

// Shape returns the tensor's dimension sizes.
func (v *OnnxValue) Shape() ([]int64, error) {
	if v.value == nil {
		return nil, fmt.Errorf("onnxruntime: nil value")
	}

	var info *C.OrtTensorTypeAndShapeInfo
	if err := onnxError(v.api, C.getTensorShapeInfo(v.api, v.value, &info)); err != nil {
		return nil, err
	}
	defer C.releaseTensorTypeAndShapeInfo(v.api, info)

	var numDims C.size_t
	if err := onnxError(v.api, C.getShapeNumDims(v.api, info, &numDims)); err != nil {
		return nil, err
	}

	dims := make([]int64, int(numDims))
	if numDims > 0 {
		if err := onnxError(v.api, C.getShapeDims(v.api, info, (*C.int64_t)(unsafe.Pointer(&dims[0])), numDims)); err != nil {
			return nil, err
		}
	}
	return dims, nil
}

// elementCount returns the total number of elements in the tensor.
func (v *OnnxValue) elementCount() (int, error) {
	var info *C.OrtTensorTypeAndShapeInfo
	if err := onnxError(v.api, C.getTensorShapeInfo(v.api, v.value, &info)); err != nil {
		return 0, err
	}
	defer C.releaseTensorTypeAndShapeInfo(v.api, info)

	var count C.size_t
	if err := onnxError(v.api, C.getElementCount(v.api, info, &count)); err != nil {
		return 0, err
	}
	return int(count), nil
}

// Close releases the OrtValue.
func (v *OnnxValue) Close() {
	if v != nil && v.value != nil {
		C.releaseValue(v.api, v.value)
		v.value = nil
	}
}

// Close releases the session and memory info. The shared OrtEnv is never
// released (it lives for the process lifetime).
func (s *OnnxSession) Close() {
	if s == nil {
		return
	}
	if s.memInfo != nil {
		C.releaseMemoryInfo(s.api, s.memInfo)
		s.memInfo = nil
	}
	if s.session != nil {
		C.releaseSession(s.api, s.session)
		s.session = nil
	}
	// Shared env is not released — owned by the package-level singleton.
	s.env = nil
}
