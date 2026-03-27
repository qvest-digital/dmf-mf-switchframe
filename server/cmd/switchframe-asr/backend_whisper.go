//go:build cgo && cuda && tensorrt

package main

/*
#cgo CFLAGS: -I/opt/whisper.cpp/include -I/opt/whisper.cpp/ggml/include
#cgo LDFLAGS: -L/opt/whisper.cpp/build/src -lwhisper -L/opt/whisper.cpp/build/ggml/src -lggml -lggml-base -lggml-cpu -L/opt/whisper.cpp/build/ggml/src/ggml-cuda -lggml-cuda -L/usr/local/cuda/lib64 -lcudart -lcublas -lstdc++ -lm -lgomp

#include <whisper.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
	"strings"
	"unsafe"

	"github.com/zsiec/switchframe/server/asr"
)

// whisperASRBackend wraps whisper.cpp via cgo as an asrBackend.
type whisperASRBackend struct {
	ctx       *C.struct_whisper_context
	modelsDir string
	modelName string
	models    []string
	useGPU    bool
	language  string // recognition language (default "en")
	translate bool   // translate non-English to English (default true)
}

func newWhisperASRBackend(modelsDir string, useGPU bool) (*whisperASRBackend, error) {
	models, err := scanWhisperModels(modelsDir)
	if err != nil {
		return nil, fmt.Errorf("whisper: scan models: %w", err)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("whisper: no ggml-*.bin models found in %s", modelsDir)
	}

	b := &whisperASRBackend{
		modelsDir: modelsDir,
		models:    models,
		useGPU:    useGPU,
		language:  "en",
		translate: true,
	}

	defaultModel := defaultWhisperModel(models)
	if err := b.loadModel(defaultModel); err != nil {
		return nil, err
	}

	slog.Info("whisper backend initialized", "model", defaultModel, "gpu", useGPU, "available", models)
	return b, nil
}

func (b *whisperASRBackend) loadModel(name string) error {
	// Free existing context.
	if b.ctx != nil {
		C.whisper_free(b.ctx)
		b.ctx = nil
	}

	path := whisperModelPath(b.modelsDir, name)
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	params := C.whisper_context_default_params()
	if b.useGPU {
		params.use_gpu = C.bool(true)
	} else {
		params.use_gpu = C.bool(false)
	}

	ctx := C.whisper_init_from_file_with_params(cPath, params)
	if ctx == nil {
		return fmt.Errorf("whisper: failed to load model %s", path)
	}

	b.ctx = ctx
	b.modelName = name
	return nil
}

func (b *whisperASRBackend) Transcribe(audio []float32) []asr.TranscriptWord {
	if b.ctx == nil || len(audio) < 8000 {
		return nil
	}

	params := C.whisper_full_default_params(C.WHISPER_SAMPLING_GREEDY)

	lang := C.CString(b.language)
	defer C.free(unsafe.Pointer(lang))

	params.language = lang
	params.translate = C.bool(b.translate)
	params.n_threads = 4
	params.token_timestamps = C.bool(true)
	params.print_progress = C.bool(false)
	params.print_realtime = C.bool(false)
	params.print_special = C.bool(false)
	params.print_timestamps = C.bool(false)

	rc := C.whisper_full(b.ctx, params, (*C.float)(unsafe.Pointer(&audio[0])), C.int(len(audio)))
	if rc != 0 {
		slog.Warn("whisper_full failed", "rc", rc)
		return nil
	}

	nSegments := int(C.whisper_full_n_segments(b.ctx))
	eot := C.whisper_token_eot(b.ctx)

	var words []asr.TranscriptWord
	for s := 0; s < nSegments; s++ {
		nTokens := int(C.whisper_full_n_tokens(b.ctx, C.int(s)))
		for tok := 0; tok < nTokens; tok++ {
			id := C.whisper_full_get_token_id(b.ctx, C.int(s), C.int(tok))
			if id >= eot {
				continue // skip special tokens
			}

			cText := C.whisper_full_get_token_text(b.ctx, C.int(s), C.int(tok))
			text := strings.TrimSpace(C.GoString(cText))
			if text == "" {
				continue
			}

			prob := float32(C.whisper_full_get_token_p(b.ctx, C.int(s), C.int(tok)))

			words = append(words, asr.TranscriptWord{
				Text:       text,
				Confidence: prob,
			})
		}
	}

	return filterWords(words)
}

func (b *whisperASRBackend) Name() string      { return "whisper" }
func (b *whisperASRBackend) Models() []string  { return b.models }
func (b *whisperASRBackend) ModelName() string { return b.modelName }

func (b *whisperASRBackend) SetModel(name string) error {
	if name == b.modelName {
		return nil
	}
	// Verify the model exists.
	found := false
	for _, m := range b.models {
		if m == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("whisper: model %q not found", name)
	}
	return b.loadModel(name)
}

func (b *whisperASRBackend) Config() map[string]interface{} {
	return map[string]interface{}{
		"language":  b.language,
		"translate": b.translate,
	}
}

func (b *whisperASRBackend) SetConfig(cfg map[string]interface{}) {
	if v, ok := cfg["language"].(string); ok && v != "" {
		b.language = v
	}
	if v, ok := cfg["translate"].(bool); ok {
		b.translate = v
	}
}

func (b *whisperASRBackend) Close() {
	if b.ctx != nil {
		C.whisper_free(b.ctx)
		b.ctx = nil
	}
}
