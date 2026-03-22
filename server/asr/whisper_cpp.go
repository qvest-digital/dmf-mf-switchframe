//go:build cgo && whisper

package asr

/*
#cgo CFLAGS: -I/opt/whisper.cpp/include -I/opt/whisper.cpp/ggml/include
#cgo LDFLAGS: -L/opt/whisper.cpp/build/src -L/opt/whisper.cpp/build/ggml/src -L/opt/whisper.cpp/build/ggml/src/ggml-cuda -lwhisper -lggml -lggml-cuda -lggml-cpu -lggml-base -lstdc++ -lm -lgomp -lcublas -lcublasLt -lcudart -L/usr/local/cuda/lib64

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

// TranscribeWithConfidence runs whisper inference on 16kHz mono float32 PCM
// audio. Returns per-word text with confidence scores from whisper's token
// probabilities. Uses beam search for quality.
func (w *WhisperCpp) TranscribeWithConfidence(samples []float32, language string) ([]TranscriptWord, error) {
	if w == nil || w.ctx == nil {
		return nil, errors.New("asr: whisper_cpp: nil context")
	}
	if len(samples) == 0 {
		return nil, nil
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
	params.suppress_nst = true      // suppress non-speech tokens
	params.no_speech_thold = 0.5    // skip segments where P(no_speech) > 50%
	params.logprob_thold = -1.5     // skip tokens with avg logprob below this
	params.entropy_thold = 2.4      // skip segments with high entropy (hallucination)
	params.beam_search.beam_size = 5
	params.token_timestamps = true  // needed for per-token data

	// Set language
	if language != "" {
		cLang := C.CString(language)
		defer C.free(unsafe.Pointer(cLang))
		params.language = cLang
	}

	// Run inference
	rc := C.whisper_full(w.ctx, params, (*C.float)(&samples[0]), C.int(len(samples)))
	if rc != 0 {
		return nil, fmt.Errorf("asr: whisper_cpp: inference failed with code %d", int(rc))
	}

	// Collect tokens with probabilities, group into words.
	// Track inter-segment timestamp gaps for speaker change detection.
	nSegments := int(C.whisper_full_n_segments(w.ctx))
	var words []TranscriptWord
	var currentWord strings.Builder
	var sumProb float32
	var tokenCount int
	var lastSegEndMs int64 = -1
	var pendingGapMs int

	for i := 0; i < nSegments; i++ {
		// Detect inter-segment gaps — whisper creates a new segment when
		// it detects a pause. Gaps > 300ms likely indicate a speaker change.
		segStartMs := int64(C.whisper_full_get_segment_t0(w.ctx, C.int(i))) * 10 // centiseconds → ms
		if lastSegEndMs >= 0 {
			gap := int(segStartMs - lastSegEndMs)
			if gap > 0 {
				pendingGapMs = gap
			}
		}
		lastSegEndMs = int64(C.whisper_full_get_segment_t1(w.ctx, C.int(i))) * 10

		nTokens := int(C.whisper_full_n_tokens(w.ctx, C.int(i)))
		for j := 0; j < nTokens; j++ {
			tdata := C.whisper_full_get_token_data(w.ctx, C.int(i), C.int(j))
			tokenText := C.GoString(C.whisper_full_get_token_text(w.ctx, C.int(i), C.int(j)))
			prob := float32(tdata.p)

			// Skip special tokens. whisper.cpp marks them with
			// whisper_token_is_special. Also filter by text pattern
			// since some builds don't set negative IDs consistently.
			tokenID := int(tdata.id)
			if tokenID < 0 || C.whisper_token_eot(w.ctx) == tdata.id {
				continue
			}
			if len(tokenText) > 0 && (tokenText[0] == '[' || tokenText[0] == '<') {
				continue // skip [_EOT_], [_BEG_], <|endoftext|>, etc.
			}

			// Whisper tokens starting with a space indicate a word boundary.
			if len(tokenText) > 0 && tokenText[0] == ' ' && currentWord.Len() > 0 {
				word := strings.TrimSpace(currentWord.String())
				if word != "" {
					avgProb := sumProb / float32(tokenCount)
					words = append(words, TranscriptWord{Text: word, Confidence: avgProb})
				}
				currentWord.Reset()
				sumProb = 0
				tokenCount = 0
			}

			// Attach pending gap to the first token of a new segment.
			if currentWord.Len() == 0 && pendingGapMs > 0 {
				// Will be attached to the word when it's flushed.
			}

			currentWord.WriteString(tokenText)
			sumProb += prob
			tokenCount++
		}

		// Flush word at segment boundary with gap info.
		if currentWord.Len() > 0 {
			word := strings.TrimSpace(currentWord.String())
			if word != "" {
				avgProb := sumProb / float32(tokenCount)
				words = append(words, TranscriptWord{
					Text: word, Confidence: avgProb, GapBefore: pendingGapMs,
				})
			}
			currentWord.Reset()
			sumProb = 0
			tokenCount = 0
			pendingGapMs = 0
		}
	}

	// Flush last word.
	if word := strings.TrimSpace(currentWord.String()); word != "" {
		avgProb := sumProb
		if tokenCount > 0 {
			avgProb /= float32(tokenCount)
		}
		words = append(words, TranscriptWord{Text: word, Confidence: avgProb, GapBefore: pendingGapMs})
	}

	return words, nil
}

// Transcribe runs whisper inference and returns plain text (no confidence).
// Convenience wrapper around TranscribeWithConfidence.
func (w *WhisperCpp) Transcribe(samples []float32, language string) (string, error) {
	words, err := w.TranscribeWithConfidence(samples, language)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, w := range words {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(w.Text)
	}
	return sb.String(), nil
}

// Close releases the whisper context.
func (w *WhisperCpp) Close() {
	if w != nil && w.ctx != nil {
		C.whisper_free(w.ctx)
		w.ctx = nil
	}
}
