//go:build cgo && cuda && tensorrt

package main

/*
#cgo CFLAGS: -I/opt/onnxruntime/include
#cgo LDFLAGS: -L/opt/onnxruntime/lib -lonnxruntime -Wl,-rpath,/opt/onnxruntime/lib

#include <onnxruntime_c_api.h>
#include <stdlib.h>
#include <string.h>

// Minimal ONNX Runtime wrapper for Silero VAD.
// Fixed shapes, CPU-only, single session.

static const OrtApi* ort_api;

static int ort_init() {
	ort_api = OrtGetApiBase()->GetApi(ORT_API_VERSION);
	return ort_api ? 0 : -1;
}

static const char* ort_status_msg(OrtStatus* s) {
	if (!ort_api || !s) return "unknown error";
	return ort_api->GetErrorMessage(s);
}

static void ort_release_status(OrtStatus* s) {
	if (ort_api && s) ort_api->ReleaseStatus(s);
}

static OrtStatus* ort_create_env(OrtEnv** env) {
	return ort_api->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "silero_vad", env);
}

static OrtStatus* ort_create_session(OrtEnv* env, const char* path, OrtSession** session) {
	OrtSessionOptions* opts = NULL;
	OrtStatus* s = ort_api->CreateSessionOptions(&opts);
	if (s) return s;

	// Single thread — VAD is tiny, don't consume CPU.
	s = ort_api->SetIntraOpNumThreads(opts, 1);
	if (s) { ort_api->ReleaseSessionOptions(opts); return s; }

	s = ort_api->SetInterOpNumThreads(opts, 1);
	if (s) { ort_api->ReleaseSessionOptions(opts); return s; }

	s = ort_api->CreateSession(env, path, opts, session);
	ort_api->ReleaseSessionOptions(opts);
	return s;
}

static OrtStatus* ort_create_cpu_mem_info(OrtMemoryInfo** info) {
	return ort_api->CreateCpuMemoryInfo(OrtArenaAllocator, OrtMemTypeDefault, info);
}

static OrtStatus* ort_create_tensor_float(OrtMemoryInfo* info, float* data,
	size_t len, int64_t* shape, size_t rank, OrtValue** out) {
	return ort_api->CreateTensorWithDataAsOrtValue(
		info, data, len * sizeof(float), shape, rank,
		ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, out);
}

static OrtStatus* ort_create_tensor_int64(OrtMemoryInfo* info, int64_t* data,
	size_t len, int64_t* shape, size_t rank, OrtValue** out) {
	return ort_api->CreateTensorWithDataAsOrtValue(
		info, data, len * sizeof(int64_t), shape, rank,
		ONNX_TENSOR_ELEMENT_DATA_TYPE_INT64, out);
}

static OrtStatus* ort_run(OrtSession* session,
	const char* const* input_names, const OrtValue* const* inputs, size_t n_inputs,
	const char* const* output_names, OrtValue** outputs, size_t n_outputs) {
	return ort_api->Run(session, NULL, input_names, inputs, n_inputs,
		output_names, n_outputs, outputs);
}

static OrtStatus* ort_get_float_data(OrtValue* val, float** data) {
	return ort_api->GetTensorMutableData(val, (void**)data);
}

static void ort_release_value(OrtValue* val) {
	if (ort_api && val) ort_api->ReleaseValue(val);
}

static void ort_cleanup(OrtEnv* env, OrtSession* session, OrtMemoryInfo* info) {
	if (session) ort_api->ReleaseSession(session);
	if (info)    ort_api->ReleaseMemoryInfo(info);
	if (env)     ort_api->ReleaseEnv(env);
}
*/
import "C"

import (
	"fmt"
	"log/slog"
	"unsafe"
)

type sileroVAD struct {
	sileroVADState

	// ONNX Runtime handles.
	env     *C.OrtEnv
	session *C.OrtSession
	memInfo *C.OrtMemoryInfo

	// Host buffers (owned by Go, passed to ORT by pointer).
	hostContext []float32 // last 64 samples for context prepend
	buf         []float32 // buffered incoming samples
	state       []float32 // [2, 1, 128] LSTM recurrent state
}

func newSileroVAD(modelPath string, stream unsafe.Pointer, threshold float64, hangoverMs int) (*sileroVAD, error) {
	// Initialize ONNX Runtime API.
	if rc := C.ort_init(); rc != 0 {
		return nil, fmt.Errorf("silero: failed to initialize ONNX Runtime API")
	}

	v := &sileroVAD{
		sileroVADState: newSileroVADState(threshold, hangoverMs),
		hostContext:    make([]float32, sileroContextSize),
		state:          make([]float32, sileroStateSize),
	}

	// Create environment.
	s := C.ort_create_env(&v.env)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		return nil, fmt.Errorf("silero: create env: %s", msg)
	}

	// Create session (loads model).
	cPath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cPath))
	s = C.ort_create_session(v.env, cPath, &v.session)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		v.close()
		return nil, fmt.Errorf("silero: create session: %s", msg)
	}

	// Create CPU memory info.
	s = C.ort_create_cpu_mem_info(&v.memInfo)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		v.close()
		return nil, fmt.Errorf("silero: create mem info: %s", msg)
	}

	slog.Info("silero VAD initialized (ONNX Runtime, CPU)", "model", modelPath,
		"threshold", threshold, "hangoverMs", hangoverMs)
	return v, nil
}

func (v *sileroVAD) process(samples []float32) {
	v.buf = append(v.buf, samples...)
	for len(v.buf) >= sileroChunkSize {
		chunk := v.buf[:sileroChunkSize]
		v.buf = v.buf[sileroChunkSize:]
		prob, err := v.infer(chunk)
		if err != nil {
			slog.Warn("silero VAD inference failed", "error", err)
			continue
		}
		v.updateState(prob)
	}
}

func (v *sileroVAD) infer(chunk []float32) (float64, error) {
	// Build input: [context(64) | chunk(512)] = 576 samples.
	input := make([]float32, sileroInputSize)
	copy(input[:sileroContextSize], v.hostContext)
	copy(input[sileroContextSize:], chunk)

	// Shapes.
	inputShape := [2]C.int64_t{1, C.int64_t(sileroInputSize)}
	stateShape := [3]C.int64_t{2, 1, 128}
	srShape := [1]C.int64_t{1}

	// Sample rate value.
	sr := C.int64_t(sileroSampleRate)

	// Create input tensors. These reference Go memory directly — valid until
	// the slices are not moved by GC during the Run call. The slices are
	// pinned by being referenced in this stack frame.
	var inputVal, stateVal, srVal *C.OrtValue

	s := C.ort_create_tensor_float(v.memInfo,
		(*C.float)(unsafe.Pointer(&input[0])), C.size_t(len(input)),
		&inputShape[0], 2, &inputVal)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		return 0, fmt.Errorf("silero: create input tensor: %s", msg)
	}
	defer C.ort_release_value(inputVal)

	s = C.ort_create_tensor_float(v.memInfo,
		(*C.float)(unsafe.Pointer(&v.state[0])), C.size_t(len(v.state)),
		&stateShape[0], 3, &stateVal)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		return 0, fmt.Errorf("silero: create state tensor: %s", msg)
	}
	defer C.ort_release_value(stateVal)

	s = C.ort_create_tensor_int64(v.memInfo,
		&sr, 1,
		&srShape[0], 1, &srVal)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		return 0, fmt.Errorf("silero: create sr tensor: %s", msg)
	}
	defer C.ort_release_value(srVal)

	// Input/output names — must match the ONNX model.
	inputNameC := C.CString("input")
	stateNameC := C.CString("state")
	srNameC := C.CString("sr")
	outputNameC := C.CString("output")
	stateNNameC := C.CString("stateN")
	defer C.free(unsafe.Pointer(inputNameC))
	defer C.free(unsafe.Pointer(stateNameC))
	defer C.free(unsafe.Pointer(srNameC))
	defer C.free(unsafe.Pointer(outputNameC))
	defer C.free(unsafe.Pointer(stateNNameC))

	inputNames := [3]*C.char{inputNameC, stateNameC, srNameC}
	inputs := [3]*C.OrtValue{inputVal, stateVal, srVal}
	outputNames := [2]*C.char{outputNameC, stateNNameC}
	var outputs [2]*C.OrtValue

	// Run inference.
	s = C.ort_run(v.session,
		(**C.char)(unsafe.Pointer(&inputNames[0])),
		(**C.OrtValue)(unsafe.Pointer(&inputs[0])), 3,
		(**C.char)(unsafe.Pointer(&outputNames[0])),
		(**C.OrtValue)(unsafe.Pointer(&outputs[0])), 2)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		return 0, fmt.Errorf("silero: run: %s", msg)
	}
	defer C.ort_release_value(outputs[0])
	defer C.ort_release_value(outputs[1])

	// Read output probability.
	var probPtr *C.float
	s = C.ort_get_float_data(outputs[0], &probPtr)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		return 0, fmt.Errorf("silero: get output data: %s", msg)
	}
	prob := float64(*probPtr)

	// Copy new state from output tensor into our persistent state buffer.
	var stateNPtr *C.float
	s = C.ort_get_float_data(outputs[1], &stateNPtr)
	if s != nil {
		msg := C.GoString(C.ort_status_msg(s))
		C.ort_release_status(s)
		return 0, fmt.Errorf("silero: get stateN data: %s", msg)
	}
	C.memcpy(unsafe.Pointer(&v.state[0]), unsafe.Pointer(stateNPtr),
		C.size_t(sileroStateSize*4))

	// Update host context with last 64 samples of this chunk.
	copy(v.hostContext, chunk[sileroChunkSize-sileroContextSize:])

	return prob, nil
}

func (v *sileroVAD) isSpeaking() bool { return v.speaking }

func (v *sileroVAD) reset() {
	v.buf = v.buf[:0]
	v.speaking = false
	v.hangover = 0

	// Zero host context.
	for i := range v.hostContext {
		v.hostContext[i] = 0
	}

	// Zero LSTM state.
	for i := range v.state {
		v.state[i] = 0
	}
}

func (v *sileroVAD) close() {
	C.ort_cleanup(v.env, v.session, v.memInfo)
	v.env = nil
	v.session = nil
	v.memInfo = nil
}
