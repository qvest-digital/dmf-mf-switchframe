//go:build cgo && whisper

package main

/*
#cgo CFLAGS: -I/opt/whisper.cpp/include -I/opt/whisper.cpp/ggml/include

#include <whisper.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/zsiec/switchframe/server/asr"
)

// maxPromptWords caps the rolling window of confirmed words sent back to
// whisper as initial_prompt. Whisper docs recommend ~half of n_text_ctx
// (typically 224 / 2 = ~110), and broader real-world implementations
// settle at ~200; the model truncates internally if we go over.
const maxPromptWords = 200

// whisperASRBackend wraps whisper.cpp via cgo as an asrBackend.
type whisperASRBackend struct {
	ctx         *C.struct_whisper_context
	state       *C.struct_whisper_state // recreated each Transcribe to reset KV cache
	modelsDir   string
	modelName   string
	models      []string
	useGPU      bool
	nThreads    int
	language    string   // recognition language (default "en")
	translate   bool     // translate non-English to English (default true)
	promptWords []string // rolling window of recently confirmed words
}

func newWhisperASRBackend(modelsDir string, useGPU bool, nThreads int) (*whisperASRBackend, error) {
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
		nThreads:  nThreads,
		language:  "en",
		// translate=false by default. Setting true tells whisper to
		// translate-to-English, which is redundant for English audio
		// AND has a side effect: whisper drops the first half of
		// English utterances, presumably because the "translate"
		// task token confuses the decoder into thinking the
		// English audio is in another language. Caller can opt-in
		// via SetConfig({"translate": true}) for non-English audio.
		translate: false,
	}

	defaultModel := defaultWhisperModel(models)
	if err := b.loadModel(defaultModel); err != nil {
		return nil, err
	}

	slog.Info("whisper backend initialized", "model", defaultModel, "gpu", useGPU, "available", models)
	return b, nil
}

func (b *whisperASRBackend) loadModel(name string) error {
	// Free existing state then context (state must be freed before context).
	if b.state != nil {
		C.whisper_free_state(b.state)
		b.state = nil
	}
	if b.ctx != nil {
		C.whisper_free(b.ctx)
		b.ctx = nil
	}

	path := filepath.Join(b.modelsDir, "ggml-"+name+".bin")
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	opts := buildWhisperContextOpts(b.useGPU, name)
	params := C.whisper_context_default_params()
	params.use_gpu = C.bool(opts.UseGPU)
	params.flash_attn = C.bool(opts.FlashAttn)
	// Only touch DTW context fields when we actually want DTW. whisper-cli
	// sets dtw_aheads_preset only inside `if (params.dtw != "")`, and
	// matching that behaviour fixed our pipeline producing empty output
	// on multi-second-utterance fixtures (eval reproduced "long" first
	// 25s coming back blank purely because we'd set the preset enum
	// without enabling DTW).
	if opts.DTWTokenTimestamps {
		params.dtw_token_timestamps = C.bool(true)
		params.dtw_aheads_preset = uint32(opts.DTWAheadsPreset)
		params.dtw_mem_size = C.size_t(opts.DTWMemSize)
	}

	ctx := C.whisper_init_from_file_with_params(cPath, params)
	if ctx == nil {
		return fmt.Errorf("whisper: failed to load model %s", path)
	}

	b.ctx = ctx
	b.modelName = name
	b.promptWords = b.promptWords[:0] // discard cross-model context
	return nil
}

func (b *whisperASRBackend) Transcribe(audio []float32) []asr.TranscriptWord {
	if b.ctx == nil || len(audio) < 8000 {
		return nil
	}

	// Recreate the state per-call so whisper's internal KV cache from
	// prior calls doesn't bias this decode (eval reproduced
	// voice_samantha producing empty output when run after voice_daniel
	// speaking the same script).
	if b.state != nil {
		C.whisper_free_state(b.state)
		b.state = nil
	}
	b.state = C.whisper_init_state(b.ctx)
	if b.state == nil {
		slog.Warn("whisper_init_state returned nil")
		return nil
	}

	opts := buildWhisperFullOpts(whisperRequest{
		Language:  b.language,
		Translate: b.translate,
		NThreads:  b.nThreads,
	})

	params := C.whisper_full_default_params(C.WHISPER_SAMPLING_GREEDY)

	lang := C.CString(opts.Language)
	defer C.free(unsafe.Pointer(lang))

	// Always pass a C string for initial_prompt -- match whisper-cli.
	prompt := promptContextToString(b.promptWords)
	promptPtr := C.CString(prompt)
	defer C.free(unsafe.Pointer(promptPtr))

	params.language = lang
	params.translate = C.bool(opts.Translate)
	params.n_threads = C.int(opts.NThreads)
	params.no_context = C.bool(opts.NoContext)
	params.single_segment = C.bool(opts.SingleSegment)
	params.max_tokens = C.int(opts.MaxTokens)
	params.temperature = C.float(opts.Temperature)
	params.temperature_inc = C.float(opts.TemperatureInc)
	params.suppress_blank = C.bool(opts.SuppressBlank)
	params.suppress_nst = C.bool(opts.SuppressNonSpeech)
	params.token_timestamps = C.bool(opts.TokenTimestamps)
	params.initial_prompt = promptPtr
	if opts.NoSpeechThold > 0 {
		params.no_speech_thold = C.float(opts.NoSpeechThold)
	}
	// audio_ctx = 0 matches whisper-cli; computeAudioCtx kept for future
	// streaming-mode pipeline.
	_ = computeAudioCtx
	params.audio_ctx = 0
	params.print_progress = C.bool(false)
	params.print_realtime = C.bool(false)
	params.print_special = C.bool(false)
	params.print_timestamps = C.bool(false)

	rc := C.whisper_full_with_state(b.ctx, b.state, params, (*C.float)(unsafe.Pointer(&audio[0])), C.int(len(audio)))
	if rc != 0 {
		slog.Warn("whisper_full_with_state failed", "rc", rc)
		return nil
	}

	nSegments := int(C.whisper_full_n_segments_from_state(b.state))
	eot := C.whisper_token_eot(b.ctx)

	// Optional debug -- set ASR_DEBUG_TRANSCRIBE=1 to dump segment count
	// and segment text for each Transcribe call.
	if os.Getenv("ASR_DEBUG_TRANSCRIBE") != "" {
		slog.Info("transcribe debug", "audio_samples", len(audio), "n_segments", nSegments)
		for s := 0; s < nSegments; s++ {
			cText := C.whisper_full_get_segment_text_from_state(b.state, C.int(s))
			slog.Info("  segment", "i", s, "text", C.GoString(cText))
		}
		if dumpPath := os.Getenv("ASR_DEBUG_DUMP_WAV"); dumpPath != "" {
			dumpFloat32AsWAV(audio, 16000, dumpPath)
			slog.Info("  audio dumped", "path", dumpPath)
		}
	}

	// Collect raw tokens (preserving leading-space word boundaries) from
	// every segment, then group them into whole words. Doing the grouping
	// outside the cgo loop keeps it pure-Go and unit-testable.
	var rawTokens []rawToken
	for s := 0; s < nSegments; s++ {
		nTokens := int(C.whisper_full_n_tokens_from_state(b.state, C.int(s)))
		for tok := 0; tok < nTokens; tok++ {
			id := C.whisper_full_get_token_id_from_state(b.state, C.int(s), C.int(tok))
			if id >= eot {
				continue // skip special tokens (<|endoftext|> etc.)
			}
			cText := C.whisper_full_get_token_text_from_state(b.ctx, b.state, C.int(s), C.int(tok))
			rawTokens = append(rawTokens, rawToken{
				Text: C.GoString(cText),
				Prob: float32(C.whisper_full_get_token_p_from_state(b.state, C.int(s), C.int(tok))),
			})
		}
	}

	return filterWords(dedupePhraseRuns(tokensToWords(rawTokens)))
}

// dumpFloat32AsWAV writes float32 mono samples to a 16-bit PCM WAV file.
// Debug helper, only used when ASR_DEBUG_DUMP_WAV is set.
func dumpFloat32AsWAV(samples []float32, sampleRate int, path string) {
	f, err := os.Create(path)
	if err != nil {
		slog.Warn("dump wav create", "err", err)
		return
	}
	defer f.Close()

	dataSize := len(samples) * 2
	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binPutU32 := func(off int, v uint32) {
		hdr[off] = byte(v)
		hdr[off+1] = byte(v >> 8)
		hdr[off+2] = byte(v >> 16)
		hdr[off+3] = byte(v >> 24)
	}
	binPutU16 := func(off int, v uint16) {
		hdr[off] = byte(v)
		hdr[off+1] = byte(v >> 8)
	}
	binPutU32(4, uint32(36+dataSize))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binPutU32(16, 16) // fmt chunk size
	binPutU16(20, 1)  // PCM
	binPutU16(22, 1)  // mono
	binPutU32(24, uint32(sampleRate))
	binPutU32(28, uint32(sampleRate*2)) // byte rate
	binPutU16(32, 2)                    // block align
	binPutU16(34, 16)                   // bits per sample
	copy(hdr[36:40], "data")
	binPutU32(40, uint32(dataSize))
	if _, err := f.Write(hdr); err != nil {
		return
	}
	for _, s := range samples {
		v := int16(s * 32768)
		f.Write([]byte{byte(uint16(v)), byte(uint16(v) >> 8)})
	}
}

// AppendPromptContext extends the rolling prompt window with words that
// have been confirmed downstream by LocalAgreement-2. Called by the
// caller after a commit so the next Transcribe sees prior coherent
// context without contaminating the prompt with tentative re-decodes of
// the same audio.
func (b *whisperASRBackend) AppendPromptContext(words []string) {
	if len(words) == 0 {
		return
	}
	b.promptWords = updatePromptContext(b.promptWords, words, maxPromptWords)
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
	if b.state != nil {
		C.whisper_free_state(b.state)
		b.state = nil
	}
	if b.ctx != nil {
		C.whisper_free(b.ctx)
		b.ctx = nil
	}
}
