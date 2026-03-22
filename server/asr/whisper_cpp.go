//go:build cgo && whisper

package asr

/*
#cgo CFLAGS: -I/opt/whisper.cpp/include -I/opt/whisper.cpp/ggml/include
#cgo LDFLAGS: -L/opt/whisper.cpp/build/src -L/opt/whisper.cpp/build/ggml/src -L/opt/whisper.cpp/build/ggml/src/ggml-cuda -lwhisper -lggml -lggml-cuda -lggml-cpu -lggml-base -lstdc++ -lm -lcublas -lcublasLt -lcudart -L/usr/local/cuda/lib64

#include <whisper.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"
)

// ErrASRNotAvailable is never returned when whisper is available.
var ErrASRNotAvailable error

// WhisperCpp wraps the whisper.cpp C library for speech-to-text inference.
type WhisperCpp struct {
	ctx *C.struct_whisper_context
}

// WhisperCppConfig configures the whisper.cpp wrapper.
type WhisperCppConfig struct {
	ModelPath string // Path to ggml model file (e.g., ggml-base.bin)
	UseGPU    bool   // Enable CUDA GPU acceleration
}

// NewWhisperCpp loads a whisper model and creates a context.
func NewWhisperCpp(cfg WhisperCppConfig) (*WhisperCpp, error) {
	if cfg.ModelPath == "" {
		return nil, errors.New("asr: whisper_cpp: ModelPath is required")
	}

	cPath := C.CString(cfg.ModelPath)
	defer C.free(unsafe.Pointer(cPath))

	cparams := C.whisper_context_default_params()
	cparams.use_gpu = C.bool(cfg.UseGPU)

	ctx := C.whisper_init_from_file_with_params(cPath, cparams)
	if ctx == nil {
		return nil, fmt.Errorf("asr: whisper_cpp: failed to load model from %s", cfg.ModelPath)
	}

	return &WhisperCpp{ctx: ctx}, nil
}

// Transcribe runs whisper inference on 16kHz mono float32 PCM audio.
// Returns the transcribed text. Uses beam search for quality.
func (w *WhisperCpp) Transcribe(samples []float32, language string) (string, error) {
	if w == nil || w.ctx == nil {
		return "", errors.New("asr: whisper_cpp: nil context")
	}
	if len(samples) == 0 {
		return "", nil
	}

	params := C.whisper_full_default_params(C.WHISPER_SAMPLING_BEAM_SEARCH)
	params.n_threads = 4
	params.no_timestamps = true
	params.single_segment = false
	params.print_special = false
	params.print_progress = false
	params.print_realtime = false
	params.print_timestamps = false
	params.suppress_blank = true
	params.suppress_nst = true // suppress non-speech tokens
	params.beam_search.beam_size = 5

	// Set language
	if language != "" {
		cLang := C.CString(language)
		defer C.free(unsafe.Pointer(cLang))
		params.language = cLang
	}

	// Run inference
	rc := C.whisper_full(w.ctx, params, (*C.float)(&samples[0]), C.int(len(samples)))
	if rc != 0 {
		return "", fmt.Errorf("asr: whisper_cpp: inference failed with code %d", int(rc))
	}

	// Collect segments
	nSegments := int(C.whisper_full_n_segments(w.ctx))
	var sb strings.Builder
	for i := 0; i < nSegments; i++ {
		text := C.GoString(C.whisper_full_get_segment_text(w.ctx, C.int(i)))
		sb.WriteString(text)
	}

	return strings.TrimSpace(sb.String()), nil
}

// Close releases the whisper context.
func (w *WhisperCpp) Close() {
	if w != nil && w.ctx != nil {
		C.whisper_free(w.ctx)
		w.ctx = nil
	}
}
