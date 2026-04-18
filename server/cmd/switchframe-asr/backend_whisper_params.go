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
