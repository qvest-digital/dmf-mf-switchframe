//go:build cgo && cuda && tensorrt

package main

/*
#cgo CFLAGS: -I/usr/local/cuda/include
#cgo LDFLAGS: -L/usr/local/cuda/lib64 -lcudart

#include <cuda_runtime.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/zsiec/switchframe/server/gpu"
)

const (
	maxMelFrames = 600 // 5s * 100 frames/s + margin
	maxEncFrames = 80  // 600 / 8 subsampling + margin
	maxTargetLen = 128
	encoderDim   = 1024
	stateSize    = 640
	stateLayers  = 2
)

// trtBackend wraps TensorRT engines for encoder and decoder inference.
// The mel spectrogram is computed in pure Go (no GPU needed).
type trtBackend struct {
	mel    *melFrontend
	gpuCtx *gpu.Context
	stream unsafe.Pointer // CUDA stream

	// Encoder engine + context.
	encEngine  *gpu.TRTEngine
	encContext *gpu.TRTContext

	// Decoder engine + context.
	decEngine  *gpu.TRTEngine
	decContext *gpu.TRTContext

	// Encoder device memory.
	encAudioDev  unsafe.Pointer // [1, 80, maxMelFrames] float32
	encLenDev    unsafe.Pointer // [1] int64
	encOutputDev unsafe.Pointer // [1, 1024, maxEncFrames] float32
	encOutLenDev unsafe.Pointer // [1] int64

	// Decoder device memory.
	decEncDev     unsafe.Pointer // [1, 1024, 1] float32 (single encoder frame)
	decTargetsDev unsafe.Pointer // [1, maxTargetLen] int64
	decTargLenDev unsafe.Pointer // [1] int64
	decState1Dev  unsafe.Pointer // [2, 1, 640] float32
	decState2Dev  unsafe.Pointer // [2, 1, 640] float32
	decOutputDev  unsafe.Pointer // [1, 1, maxTargetLen, vocabSize+numDurBins] float32
	decPredLenDev unsafe.Pointer // [1] int64
	decNewS1Dev   unsafe.Pointer // [2, 1, 640] float32
	decNewS2Dev   unsafe.Pointer // [2, 1, 640] float32

	config *modelConfig
}

func newInferenceBackend(modelDir string, deviceID int, cfg *modelConfig) (inferenceBackend, error) {
	// Initialize GPU context.
	gpuCtx, err := gpu.NewContext()
	if err != nil {
		return nil, fmt.Errorf("trt: gpu context: %w", err)
	}

	// Create a dedicated CUDA stream for ASR inference.
	stream, err := gpuCtx.NewStream()
	if err != nil {
		gpuCtx.Close()
		return nil, fmt.Errorf("trt: create stream: %w", err)
	}

	b := &trtBackend{
		mel:    newMelFrontend(),
		gpuCtx: gpuCtx,
		stream: unsafe.Pointer(stream),
		config: cfg,
	}

	// Determine plan cache directory.
	cacheDir := ""
	home, err := os.UserHomeDir()
	if err == nil {
		cacheDir = filepath.Join(home, ".switchframe", "models", "asr")
		if mkErr := os.MkdirAll(cacheDir, 0o755); mkErr != nil {
			slog.Warn("trt: failed to create plan cache dir", "dir", cacheDir, "error", mkErr)
			cacheDir = ""
		}
	}

	// Build/load encoder engine.
	encOnnx := filepath.Join(modelDir, "encoder-model.onnx")
	encPlan := ""
	if cacheDir != "" {
		encPlan = filepath.Join(cacheDir, "encoder.plan")
	}
	b.encEngine, err = gpu.NewTRTEngineV2(encOnnx, gpu.TRTEngineOptsV2{
		MaxBatchSize:  1,
		MaxSeqLen:     maxMelFrames,
		MaxEncSeqLen:  maxEncFrames,
		UseFP16:       true,
		PlanCachePath: encPlan,
	})
	if err != nil {
		b.close()
		return nil, fmt.Errorf("trt: encoder engine: %w", err)
	}

	b.encContext, err = b.encEngine.NewContext()
	if err != nil {
		b.close()
		return nil, fmt.Errorf("trt: encoder context: %w", err)
	}

	// Build/load decoder engine.
	decOnnx := filepath.Join(modelDir, "decoder_joint-model.onnx")
	decPlan := ""
	if cacheDir != "" {
		decPlan = filepath.Join(cacheDir, "decoder.plan")
	}
	b.decEngine, err = gpu.NewTRTEngineV2(decOnnx, gpu.TRTEngineOptsV2{
		MaxBatchSize:  1,
		MaxSeqLen:     maxTargetLen,
		MaxEncSeqLen:  maxEncFrames,
		UseFP16:       true,
		PlanCachePath: decPlan,
	})
	if err != nil {
		b.close()
		return nil, fmt.Errorf("trt: decoder engine: %w", err)
	}

	b.decContext, err = b.decEngine.NewContext()
	if err != nil {
		b.close()
		return nil, fmt.Errorf("trt: decoder context: %w", err)
	}

	// Allocate device memory for encoder I/O.
	if err := b.allocEncoderBuffers(); err != nil {
		b.close()
		return nil, err
	}

	// Allocate device memory for decoder I/O.
	if err := b.allocDecoderBuffers(); err != nil {
		b.close()
		return nil, err
	}

	slog.Info("trt: backend initialized",
		"encoder", encOnnx, "decoder", decOnnx,
		"device", deviceID)

	return b, nil
}

func (b *trtBackend) allocEncoderBuffers() error {
	var err error

	// Input: audio_signal [1, 80, maxMelFrames] float32
	b.encAudioDev, err = gpu.AllocDeviceBytes(1 * melNumBins * maxMelFrames * 4)
	if err != nil {
		return fmt.Errorf("trt: alloc enc audio: %w", err)
	}

	// Input: length [1] int64
	b.encLenDev, err = gpu.AllocDeviceBytes(1 * 8) // int64 = 8 bytes
	if err != nil {
		return fmt.Errorf("trt: alloc enc length: %w", err)
	}

	// Output: outputs [1, 1024, maxEncFrames] float32
	b.encOutputDev, err = gpu.AllocDeviceBytes(1 * encoderDim * maxEncFrames * 4)
	if err != nil {
		return fmt.Errorf("trt: alloc enc output: %w", err)
	}

	// Output: encoded_lengths [1] int64
	b.encOutLenDev, err = gpu.AllocDeviceBytes(1 * 8)
	if err != nil {
		return fmt.Errorf("trt: alloc enc output length: %w", err)
	}

	return nil
}

func (b *trtBackend) allocDecoderBuffers() error {
	var err error
	vocabDur := b.config.VocabSize + b.config.NumDurationBins

	// Input: encoder_outputs [1, 1024, 1] float32 (single frame)
	b.decEncDev, err = gpu.AllocDeviceBytes(1 * encoderDim * 1 * 4)
	if err != nil {
		return fmt.Errorf("trt: alloc dec enc: %w", err)
	}

	// Input: targets [1, maxTargetLen] int64
	b.decTargetsDev, err = gpu.AllocDeviceBytes(1 * maxTargetLen * 8)
	if err != nil {
		return fmt.Errorf("trt: alloc dec targets: %w", err)
	}

	// Input: target_length [1] int64
	b.decTargLenDev, err = gpu.AllocDeviceBytes(1 * 8)
	if err != nil {
		return fmt.Errorf("trt: alloc dec target length: %w", err)
	}

	// Input: input_states_1 [2, 1, 640] float32
	b.decState1Dev, err = gpu.AllocDeviceBytes(stateLayers * 1 * stateSize * 4)
	if err != nil {
		return fmt.Errorf("trt: alloc dec state1: %w", err)
	}

	// Input: input_states_2 [2, 1, 640] float32
	b.decState2Dev, err = gpu.AllocDeviceBytes(stateLayers * 1 * stateSize * 4)
	if err != nil {
		return fmt.Errorf("trt: alloc dec state2: %w", err)
	}

	// Output: outputs [1, 1, maxTargetLen, vocabSize+numDurBins] float32
	b.decOutputDev, err = gpu.AllocDeviceBytes(1 * 1 * maxTargetLen * vocabDur * 4)
	if err != nil {
		return fmt.Errorf("trt: alloc dec output: %w", err)
	}

	// Output: prednet_lengths [1] int64
	b.decPredLenDev, err = gpu.AllocDeviceBytes(1 * 8)
	if err != nil {
		return fmt.Errorf("trt: alloc dec pred length: %w", err)
	}

	// Output: output_states_1 [2, 1, 640] float32
	b.decNewS1Dev, err = gpu.AllocDeviceBytes(stateLayers * 1 * stateSize * 4)
	if err != nil {
		return fmt.Errorf("trt: alloc dec new state1: %w", err)
	}

	// Output: output_states_2 [2, 1, 640] float32
	b.decNewS2Dev, err = gpu.AllocDeviceBytes(stateLayers * 1 * stateSize * 4)
	if err != nil {
		return fmt.Errorf("trt: alloc dec new state2: %w", err)
	}

	// Zero-initialize state buffers.
	zeros := make([]byte, stateLayers*1*stateSize*4)
	if err := gpu.UploadBytes(b.decState1Dev, zeros); err != nil {
		return fmt.Errorf("trt: zero dec state1: %w", err)
	}
	if err := gpu.UploadBytes(b.decState2Dev, zeros); err != nil {
		return fmt.Errorf("trt: zero dec state2: %w", err)
	}

	return nil
}

func (b *trtBackend) runMel(audio []float32) ([]float32, int, error) {
	features, numFrames := b.mel.Compute(audio)
	return features, numFrames, nil
}

func (b *trtBackend) runEncoder(melFeatures []float32, melFrames int) ([]float32, int, error) {
	if melFrames > maxMelFrames {
		return nil, 0, fmt.Errorf("trt: mel frames %d exceeds max %d", melFrames, maxMelFrames)
	}
	if melFrames == 0 {
		return nil, 0, nil
	}

	// Upload mel features to GPU.
	melBytes := float32SliceToBytes(melFeatures)
	if err := gpu.UploadBytes(b.encAudioDev, melBytes); err != nil {
		return nil, 0, fmt.Errorf("trt: upload mel: %w", err)
	}

	// Upload length as int64.
	lenBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(lenBuf, uint64(melFrames))
	if err := gpu.UploadBytes(b.encLenDev, lenBuf); err != nil {
		return nil, 0, fmt.Errorf("trt: upload enc length: %w", err)
	}

	// Estimate encoded frames (subsampling by factor of 8, with ceiling).
	encFrames := (melFrames + b.config.SubsamplingFactor - 1) / b.config.SubsamplingFactor
	if encFrames > maxEncFrames {
		encFrames = maxEncFrames
	}

	// Run encoder inference.
	bindings := []gpu.TRTBinding{
		{Name: "audio_signal", DevPtr: b.encAudioDev, Dims: []int{1, melNumBins, melFrames}},
		{Name: "length", DevPtr: b.encLenDev, Dims: []int{1}},
		{Name: "outputs", DevPtr: b.encOutputDev, Dims: []int{1, encoderDim, encFrames}},
		{Name: "encoded_lengths", DevPtr: b.encOutLenDev, Dims: []int{1}},
	}
	if err := b.encContext.InferMulti(bindings, b.stream); err != nil {
		return nil, 0, fmt.Errorf("trt: encoder infer: %w", err)
	}

	// Sync stream.
	if rc := C.cudaStreamSynchronize(C.cudaStream_t(b.stream)); rc != 0 {
		return nil, 0, fmt.Errorf("trt: encoder stream sync failed: cuda error %d", rc)
	}

	// Download encoded_lengths to get actual output frame count.
	outLenBuf := make([]byte, 8)
	if err := downloadBytes(b.encOutLenDev, outLenBuf); err != nil {
		return nil, 0, fmt.Errorf("trt: download enc length: %w", err)
	}
	actualEncFrames := int(binary.LittleEndian.Uint64(outLenBuf))
	if actualEncFrames <= 0 {
		return nil, 0, nil
	}
	if actualEncFrames > maxEncFrames {
		actualEncFrames = maxEncFrames
	}

	// Download encoded output — shape is [1, encoderDim, encFrames] (feature-major).
	raw := make([]float32, encoderDim*actualEncFrames)
	if err := downloadFloat32(raw, b.encOutputDev); err != nil {
		return nil, 0, fmt.Errorf("trt: download enc output: %w", err)
	}

	// Transpose from [1, encoderDim, encFrames] to [1, encFrames, encoderDim]
	// so that contiguous slices of encoderDim give per-frame feature vectors.
	encoded := make([]float32, encoderDim*actualEncFrames)
	for d := 0; d < encoderDim; d++ {
		for f := 0; f < actualEncFrames; f++ {
			encoded[f*encoderDim+d] = raw[d*actualEncFrames+f]
		}
	}

	return encoded, actualEncFrames, nil
}

func (b *trtBackend) runDecoder(encoderFrame []float32, targets []int32, states1, states2 []float32) ([]float32, []float32, []float32, error) {
	vocabDur := b.config.VocabSize + b.config.NumDurationBins

	// Upload encoder frame [1, 1024, 1].
	encBytes := float32SliceToBytes(encoderFrame)
	if err := gpu.UploadBytes(b.decEncDev, encBytes); err != nil {
		return nil, nil, nil, fmt.Errorf("trt: upload dec enc frame: %w", err)
	}

	// Upload targets as int64.
	targetLen := len(targets)
	if targetLen == 0 {
		targetLen = 1
		targets = []int32{int32(b.config.BlankTokenID)}
	}
	if targetLen > maxTargetLen {
		targets = targets[targetLen-maxTargetLen:]
		targetLen = maxTargetLen
	}
	targetI64 := make([]byte, targetLen*8)
	for i, t := range targets {
		binary.LittleEndian.PutUint64(targetI64[i*8:], uint64(int64(t)))
	}
	if err := gpu.UploadBytes(b.decTargetsDev, targetI64); err != nil {
		return nil, nil, nil, fmt.Errorf("trt: upload dec targets: %w", err)
	}

	// Upload target_length as int64.
	tlenBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(tlenBuf, uint64(targetLen))
	if err := gpu.UploadBytes(b.decTargLenDev, tlenBuf); err != nil {
		return nil, nil, nil, fmt.Errorf("trt: upload dec target length: %w", err)
	}

	// Upload LSTM states (or zeros if nil).
	stateBytes := stateLayers * 1 * stateSize * 4
	if states1 != nil {
		if err := gpu.UploadBytes(b.decState1Dev, float32SliceToBytes(states1)); err != nil {
			return nil, nil, nil, fmt.Errorf("trt: upload dec state1: %w", err)
		}
	} else {
		if err := gpu.UploadBytes(b.decState1Dev, make([]byte, stateBytes)); err != nil {
			return nil, nil, nil, fmt.Errorf("trt: zero dec state1: %w", err)
		}
	}
	if states2 != nil {
		if err := gpu.UploadBytes(b.decState2Dev, float32SliceToBytes(states2)); err != nil {
			return nil, nil, nil, fmt.Errorf("trt: upload dec state2: %w", err)
		}
	} else {
		if err := gpu.UploadBytes(b.decState2Dev, make([]byte, stateBytes)); err != nil {
			return nil, nil, nil, fmt.Errorf("trt: zero dec state2: %w", err)
		}
	}

	// Run decoder inference.
	bindings := []gpu.TRTBinding{
		{Name: "encoder_outputs", DevPtr: b.decEncDev, Dims: []int{1, encoderDim, 1}},
		{Name: "targets", DevPtr: b.decTargetsDev, Dims: []int{1, targetLen}},
		{Name: "target_length", DevPtr: b.decTargLenDev, Dims: []int{1}},
		{Name: "input_states_1", DevPtr: b.decState1Dev, Dims: []int{stateLayers, 1, stateSize}},
		{Name: "input_states_2", DevPtr: b.decState2Dev, Dims: []int{stateLayers, 1, stateSize}},
		{Name: "outputs", DevPtr: b.decOutputDev, Dims: []int{1, 1, targetLen, vocabDur}},
		{Name: "prednet_lengths", DevPtr: b.decPredLenDev, Dims: []int{1}},
		{Name: "output_states_1", DevPtr: b.decNewS1Dev, Dims: []int{stateLayers, 1, stateSize}},
		{Name: "output_states_2", DevPtr: b.decNewS2Dev, Dims: []int{stateLayers, 1, stateSize}},
	}
	if err := b.decContext.InferMulti(bindings, b.stream); err != nil {
		return nil, nil, nil, fmt.Errorf("trt: decoder infer: %w", err)
	}

	// Sync stream.
	if rc := C.cudaStreamSynchronize(C.cudaStream_t(b.stream)); rc != 0 {
		return nil, nil, nil, fmt.Errorf("trt: decoder stream sync failed: cuda error %d", rc)
	}

	// Download logits — we only need the last target position's output.
	// Output shape is [1, 1, targetLen, vocabDur], we want [targetLen-1, :].
	logits := make([]float32, vocabDur)
	logitOffset := (targetLen - 1) * vocabDur * 4
	if err := downloadFloat32AtOffset(logits, b.decOutputDev, logitOffset); err != nil {
		return nil, nil, nil, fmt.Errorf("trt: download dec logits: %w", err)
	}

	// Download new states.
	newStates1 := make([]float32, stateLayers*1*stateSize)
	if err := downloadFloat32(newStates1, b.decNewS1Dev); err != nil {
		return nil, nil, nil, fmt.Errorf("trt: download dec new state1: %w", err)
	}
	newStates2 := make([]float32, stateLayers*1*stateSize)
	if err := downloadFloat32(newStates2, b.decNewS2Dev); err != nil {
		return nil, nil, nil, fmt.Errorf("trt: download dec new state2: %w", err)
	}

	return logits, newStates1, newStates2, nil
}

func (b *trtBackend) gpuStream() unsafe.Pointer { return b.stream }

func (b *trtBackend) close() {
	if b.encContext != nil {
		b.encContext.Close()
		b.encContext = nil
	}
	if b.encEngine != nil {
		b.encEngine.Close()
		b.encEngine = nil
	}
	if b.decContext != nil {
		b.decContext.Close()
		b.decContext = nil
	}
	if b.decEngine != nil {
		b.decEngine.Close()
		b.decEngine = nil
	}

	// Free encoder device memory.
	for _, ptr := range []unsafe.Pointer{
		b.encAudioDev, b.encLenDev, b.encOutputDev, b.encOutLenDev,
	} {
		if ptr != nil {
			gpu.FreeDeviceBytes(ptr)
		}
	}
	b.encAudioDev = nil
	b.encLenDev = nil
	b.encOutputDev = nil
	b.encOutLenDev = nil

	// Free decoder device memory.
	for _, ptr := range []unsafe.Pointer{
		b.decEncDev, b.decTargetsDev, b.decTargLenDev,
		b.decState1Dev, b.decState2Dev,
		b.decOutputDev, b.decPredLenDev,
		b.decNewS1Dev, b.decNewS2Dev,
	} {
		if ptr != nil {
			gpu.FreeDeviceBytes(ptr)
		}
	}
	b.decEncDev = nil
	b.decTargetsDev = nil
	b.decTargLenDev = nil
	b.decState1Dev = nil
	b.decState2Dev = nil
	b.decOutputDev = nil
	b.decPredLenDev = nil
	b.decNewS1Dev = nil
	b.decNewS2Dev = nil

	if b.gpuCtx != nil {
		b.gpuCtx.Close()
		b.gpuCtx = nil
	}
}

// float32SliceToBytes reinterprets a float32 slice as a byte slice.
func float32SliceToBytes(data []float32) []byte {
	if len(data) == 0 {
		return nil
	}
	buf := make([]byte, len(data)*4)
	for i, v := range data {
		bits := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}

// downloadBytes copies device memory to a host byte slice.
func downloadBytes(devPtr unsafe.Pointer, dst []byte) error {
	if devPtr == nil || len(dst) == 0 {
		return fmt.Errorf("trt: downloadBytes: nil pointer or empty dst")
	}
	rc := C.cudaMemcpy(unsafe.Pointer(&dst[0]), devPtr, C.size_t(len(dst)), C.cudaMemcpyDeviceToHost)
	if rc != C.cudaSuccess {
		return fmt.Errorf("trt: cudaMemcpy D2H failed: %d", rc)
	}
	return nil
}

// downloadFloat32 copies float32 data from device memory to a host slice.
func downloadFloat32(dst []float32, devPtr unsafe.Pointer) error {
	if devPtr == nil || len(dst) == 0 {
		return fmt.Errorf("trt: downloadFloat32: nil pointer or empty dst")
	}
	size := C.size_t(len(dst) * 4)
	rc := C.cudaMemcpy(unsafe.Pointer(&dst[0]), devPtr, size, C.cudaMemcpyDeviceToHost)
	if rc != C.cudaSuccess {
		return fmt.Errorf("trt: cudaMemcpy D2H failed: %d", rc)
	}
	return nil
}

// downloadFloat32AtOffset copies float32 data from device memory at a byte offset.
func downloadFloat32AtOffset(dst []float32, devPtr unsafe.Pointer, byteOffset int) error {
	if devPtr == nil || len(dst) == 0 {
		return fmt.Errorf("trt: downloadFloat32AtOffset: nil pointer or empty dst")
	}
	offsetPtr := unsafe.Pointer(uintptr(devPtr) + uintptr(byteOffset))
	size := C.size_t(len(dst) * 4)
	rc := C.cudaMemcpy(unsafe.Pointer(&dst[0]), offsetPtr, size, C.cudaMemcpyDeviceToHost)
	if rc != C.cudaSuccess {
		return fmt.Errorf("trt: cudaMemcpy D2H at offset failed: %d", rc)
	}
	return nil
}

// buildTRTEngines pre-builds TensorRT engine plans from ONNX models.
// This is called with --build-engines to avoid the slow first-run build.
func buildTRTEngines(modelDir string, deviceID int) error {
	gpuCtx, err := gpu.NewContext()
	if err != nil {
		return fmt.Errorf("gpu context: %w", err)
	}
	defer gpuCtx.Close()

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	cacheDir := filepath.Join(home, ".switchframe", "models", "asr")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	// Build encoder.
	encOnnx := filepath.Join(modelDir, "encoder-model.onnx")
	encPlan := filepath.Join(cacheDir, "encoder.plan")
	slog.Info("building encoder engine", "onnx", encOnnx, "plan", encPlan)
	start := time.Now()
	enc, err := gpu.NewTRTEngineV2(encOnnx, gpu.TRTEngineOptsV2{
		MaxBatchSize: 1, MaxSeqLen: maxMelFrames, MaxEncSeqLen: maxEncFrames,
		UseFP16: true, PlanCachePath: encPlan,
	})
	if err != nil {
		return fmt.Errorf("encoder: %w", err)
	}
	enc.Close()
	slog.Info("encoder engine built", "elapsed", time.Since(start))

	// Build decoder.
	decOnnx := filepath.Join(modelDir, "decoder_joint-model.onnx")
	decPlan := filepath.Join(cacheDir, "decoder.plan")
	slog.Info("building decoder engine", "onnx", decOnnx, "plan", decPlan)
	start = time.Now()
	dec, err := gpu.NewTRTEngineV2(decOnnx, gpu.TRTEngineOptsV2{
		MaxBatchSize: 1, MaxSeqLen: maxTargetLen, MaxEncSeqLen: maxEncFrames,
		UseFP16: true, PlanCachePath: decPlan,
	})
	if err != nil {
		return fmt.Errorf("decoder: %w", err)
	}
	dec.Close()
	slog.Info("decoder engine built", "elapsed", time.Since(start))

	slog.Info("all engines built successfully", "encoder", encPlan, "decoder", decPlan)
	return nil
}
