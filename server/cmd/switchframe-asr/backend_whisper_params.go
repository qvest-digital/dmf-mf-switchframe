package main

// whisperRequest captures inputs that vary per Transcribe call.
type whisperRequest struct {
	Language  string
	Translate bool
	NThreads  int
}

// whisperFullOpts mirrors the subset of whisper_full_params we set explicitly.
// Kept as a pure-Go struct so the values are unit-testable without cgo.
type whisperFullOpts struct {
	Language          string
	Translate         bool
	NThreads          int
	NoContext         bool
	SingleSegment     bool
	MaxTokens         int
	Temperature       float32
	TemperatureInc    float32
	SuppressBlank     bool
	SuppressNonSpeech bool
	TokenTimestamps   bool
}

// buildWhisperFullOpts returns the broadcast-tuned defaults for live captioning.
// All values chosen to favor low, predictable latency over peak accuracy on
// long-form audio: greedy decode, no temperature fallback, no cross-chunk
// context (use initial_prompt for coherence instead), single segment per call,
// hard token cap.
func buildWhisperFullOpts(req whisperRequest) whisperFullOpts {
	return whisperFullOpts{
		Language:          req.Language,
		Translate:         req.Translate,
		NThreads:          req.NThreads,
		NoContext:         true,
		SingleSegment:     true,
		MaxTokens:         32,
		Temperature:       0,
		TemperatureInc:    0,
		SuppressBlank:     true,
		SuppressNonSpeech: true,
		TokenTimestamps:   true,
	}
}

// resolveWhisperThreads picks the inference thread count. A positive
// requested value passes through. Zero (the default) means "auto" and uses
// the CPU count clamped at 8 -- whisper.cpp doesn't scale linearly past
// that on most workloads and oversubscribing wastes power. Negative or
// zero numCPU clamps to 1.
func resolveWhisperThreads(requested, numCPU int) int {
	if requested > 0 {
		return requested
	}
	if requested < 0 {
		return 1
	}
	if numCPU > 8 {
		return 8
	}
	if numCPU < 1 {
		return 1
	}
	return numCPU
}

// computeAudioCtx returns the encoder context size to use for a chunk of
// the given length, expressed as the audio_ctx whisper_full_params field
// (positions in the encoder's downsampled frame grid). Whisper's encoder
// always processes a 30s mel; setting audio_ctx smaller skips the unused
// tail and gives a documented ~3x speedup on short clips.
//
// Returns 0 (whisper default = full 30s context) when the chunk meets or
// exceeds 30s. For chunks shorter than ~5s we floor at the 5s value --
// going lower triggers repeat-token glitches because of how far the
// runtime context deviates from the model's training distribution.
//
// The returned value is rounded up to a multiple of 64 (encoder block size).
func computeAudioCtx(samples, sampleRate int) int {
	if samples <= 0 || sampleRate <= 0 {
		return 0
	}
	seconds := float64(samples) / float64(sampleRate)
	if seconds >= 30.0 {
		return 0
	}
	if seconds < 5.0 {
		seconds = 5.0
	}
	raw := int(seconds/30.0*1500.0) + 128
	return ((raw + 63) / 64) * 64
}

// whisperContextOpts mirrors the subset of whisper_context_params we set.
type whisperContextOpts struct {
	UseGPU    bool
	FlashAttn bool
}

// buildWhisperContextOpts returns the context init flags. flash_attn is
// roughly a 2x attention speedup on both CUDA and Metal and is keep-on by
// default in modern whisper.cpp; we set it explicitly so the value isn't
// silently flipped by an upstream default change.
func buildWhisperContextOpts(useGPU bool) whisperContextOpts {
	return whisperContextOpts{
		UseGPU:    useGPU,
		FlashAttn: true,
	}
}
