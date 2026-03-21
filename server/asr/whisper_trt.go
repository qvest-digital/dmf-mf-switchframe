//go:build cgo && cuda && tensorrt

package asr

/*
#cgo CFLAGS: -I/usr/local/cuda/include
#cgo LDFLAGS: -L/usr/local/cuda/lib64 -lcudart

#include <cuda_runtime.h>
#include <stdlib.h>
#include <string.h>

// cudaMemcpyKind aliases for Go access.
static const int kCudaMemcpyHostToDevice   = cudaMemcpyHostToDevice;
static const int kCudaMemcpyDeviceToHost   = cudaMemcpyDeviceToHost;
*/
import "C"

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/zsiec/switchframe/server/gpu"
)

// ErrASRNotAvailable is never returned when TensorRT is available, but is
// defined here so that both build-tagged files export the same symbol.
var ErrASRNotAvailable error

// Whisper model dimension constants.
const (
	whisperEncoderInputSize  = melNMels * melMaxFrames // 80 * 3000 = 240000 floats
	whisperEncoderOutputDim  = 512                     // encoder hidden dim (small model)
	whisperEncoderOutputLen  = 1500                    // encoder output sequence length
	whisperEncoderOutputSize = whisperEncoderOutputDim * whisperEncoderOutputLen // 768000 floats
	whisperMaxTokens         = 448                     // max decoder tokens per segment
)

// WhisperTRT wraps TensorRT engines for Whisper encoder and decoder inference.
// It manages separate encoder and decoder engines with their own execution
// contexts, CUDA device memory for input/output tensors, and a shared CUDA
// stream for asynchronous operations.
type WhisperTRT struct {
	encoderEngine  *gpu.TRTEngine
	encoderContext *gpu.TRTContext
	decoderEngine  *gpu.TRTEngine
	decoderContext *gpu.TRTContext

	// CUDA device memory for encoder
	encoderInputDev  unsafe.Pointer // mel spectrogram input
	encoderOutputDev unsafe.Pointer // encoder hidden states

	// CUDA device memory for decoder
	decoderTokensDev unsafe.Pointer // input_ids (int32, max 448)
	decoderLogitsDev unsafe.Pointer // logits (float32, max 448*51865)

	// CUDA stream for async operations
	stream C.cudaStream_t
}

// WhisperTRTConfig configures the TensorRT Whisper inference wrapper.
type WhisperTRTConfig struct {
	ModelDir string // directory containing encoder.onnx, decoder.onnx
	UseFP16  bool   // enable FP16 precision (requires GPU with FP16 support)
}

// NewWhisperTRT loads or builds TensorRT engines for the Whisper encoder and
// decoder from ONNX models, allocates CUDA device memory for I/O tensors,
// and creates a CUDA stream for inference.
//
// Engine plans are cached at <modelDir>/plans/ to skip the expensive ONNX build
// on subsequent launches.
func NewWhisperTRT(cfg WhisperTRTConfig) (*WhisperTRT, error) {
	if cfg.ModelDir == "" {
		return nil, errors.New("asr: whisper_trt: ModelDir is required")
	}

	encoderONNX := filepath.Join(cfg.ModelDir, "encoder.onnx")
	decoderONNX := filepath.Join(cfg.ModelDir, "decoder.onnx")

	// Verify ONNX files exist.
	if _, err := os.Stat(encoderONNX); err != nil {
		return nil, fmt.Errorf("asr: whisper_trt: encoder model not found: %w", err)
	}
	if _, err := os.Stat(decoderONNX); err != nil {
		return nil, fmt.Errorf("asr: whisper_trt: decoder model not found: %w", err)
	}

	// Ensure plan cache directory exists.
	planDir := filepath.Join(cfg.ModelDir, "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return nil, fmt.Errorf("asr: whisper_trt: create plan dir: %w", err)
	}

	w := &WhisperTRT{}

	// Build or load encoder engine.
	encoderPlan := filepath.Join(planDir, "encoder.plan")
	encoderEngine, err := gpu.NewTRTEngine(encoderONNX, gpu.TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       cfg.UseFP16,
		PlanCachePath: encoderPlan,
	})
	if err != nil {
		return nil, fmt.Errorf("asr: whisper_trt: build encoder: %w", err)
	}
	w.encoderEngine = encoderEngine

	encoderCtx, err := encoderEngine.NewContext()
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("asr: whisper_trt: encoder context: %w", err)
	}
	w.encoderContext = encoderCtx

	// Build or load decoder engine (V2: supports dynamic sequence length).
	decoderPlan := filepath.Join(planDir, "decoder.plan")
	decoderEngine, err := gpu.NewTRTEngineV2(decoderONNX, gpu.TRTEngineOptsV2{
		MaxBatchSize:  1,
		MaxSeqLen:     whisperMaxTokens,
		UseFP16:       cfg.UseFP16,
		PlanCachePath: decoderPlan,
	})
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("asr: whisper_trt: build decoder: %w", err)
	}
	w.decoderEngine = decoderEngine

	decoderCtx, err := decoderEngine.NewContext()
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("asr: whisper_trt: decoder context: %w", err)
	}
	w.decoderContext = decoderCtx

	// Create CUDA stream for async operations.
	rc := C.cudaStreamCreate(&w.stream)
	if rc != C.cudaSuccess {
		w.Close()
		return nil, fmt.Errorf("asr: whisper_trt: cudaStreamCreate failed: %d", int(rc))
	}

	// Allocate device memory for encoder I/O.
	if err := w.allocEncoderBuffers(); err != nil {
		w.Close()
		return nil, err
	}

	// Allocate device memory for decoder I/O.
	if err := w.allocDecoderBuffers(); err != nil {
		w.Close()
		return nil, err
	}

	return w, nil
}

// allocEncoderBuffers allocates CUDA device memory for encoder input (mel
// spectrogram) and output (hidden states).
func (w *WhisperTRT) allocEncoderBuffers() error {
	inputBytes := C.size_t(whisperEncoderInputSize * 4) // float32
	rc := C.cudaMalloc(&w.encoderInputDev, inputBytes)
	if rc != C.cudaSuccess {
		return fmt.Errorf("asr: whisper_trt: cudaMalloc encoder input: %d", int(rc))
	}

	outputBytes := C.size_t(whisperEncoderOutputSize * 4) // float32
	rc = C.cudaMalloc(&w.encoderOutputDev, outputBytes)
	if rc != C.cudaSuccess {
		return fmt.Errorf("asr: whisper_trt: cudaMalloc encoder output: %d", int(rc))
	}

	return nil
}

// allocDecoderBuffers allocates CUDA device memory for decoder token input
// and logit output. The encoder output buffer (encoderOutputDev) is shared
// between encoder output and decoder cross-attention input.
func (w *WhisperTRT) allocDecoderBuffers() error {
	// Decoder tokens: max sequence as int32.
	tokenBytes := C.size_t(whisperMaxTokens * 4) // int32
	rc := C.cudaMalloc(&w.decoderTokensDev, tokenBytes)
	if rc != C.cudaSuccess {
		return fmt.Errorf("asr: whisper_trt: cudaMalloc decoder tokens: %d", int(rc))
	}

	// Decoder logits: full sequence output over vocabulary.
	// At max, [1, 448, 51865] = 23,235,720 float32 = ~89MB.
	logitBytes := C.size_t(whisperMaxTokens * vocabSize * 4) // float32
	rc = C.cudaMalloc(&w.decoderLogitsDev, logitBytes)
	if rc != C.cudaSuccess {
		return fmt.Errorf("asr: whisper_trt: cudaMalloc decoder logits: %d", int(rc))
	}

	return nil
}

// Encode runs the Whisper encoder on mel spectrogram features.
//
// Input: flattened mel spectrogram [80 * 3000] = 240000 float32 values
// (row-major: mel band 0 frames 0..2999, mel band 1 frames 0..2999, etc.)
//
// Output: encoder hidden states [1500 * 512] = 768000 float32 values.
func (w *WhisperTRT) Encode(mel []float32) ([]float32, error) {
	if w == nil {
		return nil, errors.New("asr: whisper_trt: nil WhisperTRT")
	}
	if len(mel) != whisperEncoderInputSize {
		return nil, fmt.Errorf("asr: whisper_trt: expected %d mel values, got %d",
			whisperEncoderInputSize, len(mel))
	}

	// Copy mel spectrogram to device.
	inputBytes := C.size_t(whisperEncoderInputSize * 4)
	rc := C.cudaMemcpyAsync(
		w.encoderInputDev,
		unsafe.Pointer(&mel[0]),
		inputBytes,
		C.cudaMemcpyHostToDevice,
		w.stream,
	)
	if rc != C.cudaSuccess {
		return nil, fmt.Errorf("asr: whisper_trt: cudaMemcpy encoder input: %d", int(rc))
	}

	// Run encoder inference.
	if err := w.encoderContext.Infer(w.encoderInputDev, w.encoderOutputDev, 1, unsafe.Pointer(w.stream)); err != nil {
		return nil, fmt.Errorf("asr: whisper_trt: encoder infer: %w", err)
	}

	// Synchronize to ensure inference is complete.
	rc = C.cudaStreamSynchronize(w.stream)
	if rc != C.cudaSuccess {
		return nil, fmt.Errorf("asr: whisper_trt: cudaStreamSynchronize: %d", int(rc))
	}

	// Copy encoder output back to host.
	output := make([]float32, whisperEncoderOutputSize)
	outputBytes := C.size_t(whisperEncoderOutputSize * 4)
	rc = C.cudaMemcpy(
		unsafe.Pointer(&output[0]),
		w.encoderOutputDev,
		outputBytes,
		C.cudaMemcpyDeviceToHost,
	)
	if rc != C.cudaSuccess {
		return nil, fmt.Errorf("asr: whisper_trt: cudaMemcpy encoder output: %d", int(rc))
	}

	return output, nil
}

// Decode runs autoregressive greedy decoding using the Whisper decoder.
//
// encoderOutput: the encoder hidden states from Encode() [1500 * 512].
// initialTokens: starting tokens (typically [SOT, lang, transcribe, noTimestamps]).
//
// Returns the sequence of generated token IDs (excluding initial tokens),
// terminated by EOT or after whisperMaxTokens iterations.
//
// The decoder ONNX model has 2 inputs (input_ids + encoder_hidden_states) and
// 1 output (logits). InferMulti is used to bind all three tensors by name with
// their actual dynamic dimensions each step.
func (w *WhisperTRT) Decode(encoderOutput []float32, initialTokens []int) ([]int, error) {
	if w == nil {
		return nil, errors.New("asr: whisper_trt: nil WhisperTRT")
	}
	if len(encoderOutput) != whisperEncoderOutputSize {
		return nil, fmt.Errorf("asr: whisper_trt: expected %d encoder output values, got %d",
			whisperEncoderOutputSize, len(encoderOutput))
	}
	if len(initialTokens) == 0 {
		return nil, errors.New("asr: whisper_trt: initialTokens must not be empty")
	}

	// Copy encoder output to device (stays constant for entire decode loop).
	encOutBytes := C.size_t(whisperEncoderOutputSize * 4)
	rc := C.cudaMemcpy(
		w.encoderOutputDev,
		unsafe.Pointer(&encoderOutput[0]),
		encOutBytes,
		C.cudaMemcpyHostToDevice,
	)
	if rc != C.cudaSuccess {
		return nil, fmt.Errorf("asr: whisper_trt: cudaMemcpy encoder output: %d", int(rc))
	}

	// Build token sequence. Int64->Int32 cast happens here.
	tokens := make([]int32, 0, whisperMaxTokens)
	for _, t := range initialTokens {
		tokens = append(tokens, int32(t))
	}

	var generatedTokens []int
	logits := make([]float32, vocabSize)

	for step := 0; step < whisperMaxTokens-len(initialTokens); step++ {
		nTokens := len(tokens)

		// Copy current int32 token sequence to device.
		tokenBytes := C.size_t(nTokens * 4)
		rc := C.cudaMemcpyAsync(
			w.decoderTokensDev,
			unsafe.Pointer(&tokens[0]),
			tokenBytes,
			C.cudaMemcpyHostToDevice,
			w.stream,
		)
		if rc != C.cudaSuccess {
			return nil, fmt.Errorf("asr: whisper_trt: cudaMemcpy tokens step %d: %d", step, int(rc))
		}

		// Run decoder with named multi-input bindings.
		bindings := []gpu.TRTBinding{
			{
				Name:   "input_ids",
				DevPtr: w.decoderTokensDev,
				Dims:   []int{1, nTokens},
			},
			{
				Name:   "encoder_hidden_states",
				DevPtr: w.encoderOutputDev,
				Dims:   []int{1, whisperEncoderOutputLen, whisperEncoderOutputDim},
			},
			{
				Name:   "logits",
				DevPtr: w.decoderLogitsDev,
				Dims:   []int{1, nTokens, vocabSize},
			},
		}

		if err := w.decoderContext.InferMulti(bindings, unsafe.Pointer(w.stream)); err != nil {
			return nil, fmt.Errorf("asr: whisper_trt: decoder infer step %d: %w", step, err)
		}

		// Synchronize.
		rc = C.cudaStreamSynchronize(w.stream)
		if rc != C.cudaSuccess {
			return nil, fmt.Errorf("asr: whisper_trt: sync step %d: %d", step, int(rc))
		}

		// Read ONLY the last token's logits from device.
		// Logits are [1, nTokens, vocabSize]. Last token at offset (nTokens-1)*vocabSize.
		lastTokenOffset := C.size_t((nTokens - 1) * vocabSize * 4)
		logitBytes := C.size_t(vocabSize * 4)
		rc = C.cudaMemcpy(
			unsafe.Pointer(&logits[0]),
			unsafe.Pointer(uintptr(w.decoderLogitsDev)+uintptr(lastTokenOffset)),
			logitBytes,
			C.cudaMemcpyDeviceToHost,
		)
		if rc != C.cudaSuccess {
			return nil, fmt.Errorf("asr: whisper_trt: cudaMemcpy logits step %d: %d", step, int(rc))
		}

		// Greedy argmax over vocabulary to get next token.
		nextToken := argmax(logits)

		// Check for end-of-text.
		if nextToken == eotToken {
			break
		}

		generatedTokens = append(generatedTokens, nextToken)
		tokens = append(tokens, int32(nextToken))
	}

	return generatedTokens, nil
}

// argmax returns the index of the maximum value in the slice.
func argmax(values []float32) int {
	bestIdx := 0
	bestVal := float32(math.Inf(-1))
	for i, v := range values {
		if v > bestVal {
			bestVal = v
			bestIdx = i
		}
	}
	return bestIdx
}

// Close releases all GPU resources: CUDA device memory, TensorRT contexts
// and engines, and the CUDA stream.
func (w *WhisperTRT) Close() {
	if w == nil {
		return
	}

	// Free device memory.
	if w.encoderInputDev != nil {
		C.cudaFree(w.encoderInputDev)
		w.encoderInputDev = nil
	}
	if w.encoderOutputDev != nil {
		C.cudaFree(w.encoderOutputDev)
		w.encoderOutputDev = nil
	}
	if w.decoderTokensDev != nil {
		C.cudaFree(w.decoderTokensDev)
		w.decoderTokensDev = nil
	}
	if w.decoderLogitsDev != nil {
		C.cudaFree(w.decoderLogitsDev)
		w.decoderLogitsDev = nil
	}

	// Destroy TensorRT contexts and engines.
	if w.encoderContext != nil {
		w.encoderContext.Close()
		w.encoderContext = nil
	}
	if w.encoderEngine != nil {
		w.encoderEngine.Close()
		w.encoderEngine = nil
	}
	if w.decoderContext != nil {
		w.decoderContext.Close()
		w.decoderContext = nil
	}
	if w.decoderEngine != nil {
		w.decoderEngine.Close()
		w.decoderEngine = nil
	}

	// Destroy CUDA stream.
	if w.stream != nil {
		C.cudaStreamDestroy(w.stream)
		w.stream = nil
	}
}
