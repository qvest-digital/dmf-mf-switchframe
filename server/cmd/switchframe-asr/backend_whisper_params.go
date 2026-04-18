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

// DTW alignment-heads preset constants. Values mirror the
// whisper_alignment_heads_preset enum in whisper.h -- order must stay in
// sync. Kept as Go ints so the mapping logic is testable without cgo.
const (
	dtwAheadsNone         = 0
	dtwAheadsNTopMost     = 1
	dtwAheadsCustom       = 2
	dtwAheadsTinyEN       = 3
	dtwAheadsTiny         = 4
	dtwAheadsBaseEN       = 5
	dtwAheadsBase         = 6
	dtwAheadsSmallEN      = 7
	dtwAheadsSmall        = 8
	dtwAheadsMediumEN     = 9
	dtwAheadsMedium       = 10
	dtwAheadsLargeV1      = 11
	dtwAheadsLargeV2      = 12
	dtwAheadsLargeV3      = 13
	dtwAheadsLargeV3Turbo = 14
)

// dtwAheadsForModel maps a model name (as extracted from ggml-NAME.bin) to
// the matching DTW alignment-heads preset. Quantized variants
// (-q5_0, -q5_1, -q8_0, -q4_0, -q4_1) strip the suffix before matching.
// Unknown models fall back to N_TOP_MOST -- DTW still works, just less
// accurately than a model-specific preset.
func dtwAheadsForModel(name string) int {
	base := name
	for _, suffix := range []string{"-q5_0", "-q5_1", "-q8_0", "-q4_0", "-q4_1"} {
		if len(base) > len(suffix) && base[len(base)-len(suffix):] == suffix {
			base = base[:len(base)-len(suffix)]
			break
		}
	}

	switch base {
	case "tiny.en":
		return dtwAheadsTinyEN
	case "tiny":
		return dtwAheadsTiny
	case "base.en":
		return dtwAheadsBaseEN
	case "base":
		return dtwAheadsBase
	case "small.en":
		return dtwAheadsSmallEN
	case "small", "small.en-tdrz":
		return dtwAheadsSmall
	case "medium.en":
		return dtwAheadsMediumEN
	case "medium":
		return dtwAheadsMedium
	case "large-v1":
		return dtwAheadsLargeV1
	case "large-v2":
		return dtwAheadsLargeV2
	case "large-v3", "large":
		return dtwAheadsLargeV3
	case "large-v3-turbo":
		return dtwAheadsLargeV3Turbo
	default:
		return dtwAheadsNTopMost
	}
}

// whisperContextOpts mirrors the subset of whisper_context_params we set.
type whisperContextOpts struct {
	UseGPU             bool
	FlashAttn          bool
	DTWTokenTimestamps bool
	DTWAheadsPreset    int
	DTWMemSize         int // bytes of scratch memory for DTW
}

// buildWhisperContextOpts returns the context init flags. flash_attn is
// roughly a 2x attention speedup on both CUDA and Metal and is the bigger
// runtime win for live broadcast use, so we keep it on.
//
// whisper.cpp currently disables dtw_token_timestamps when flash_attn is
// enabled (no fused-attention DTW kernel), so we suppress the request --
// otherwise whisper logs a noisy "DTW disabled" warning on every model
// load. The dtw_aheads_preset is still set so DTW would Just Work if the
// user explicitly disables flash_attn or upstream lifts the restriction.
func buildWhisperContextOpts(useGPU bool, modelName string) whisperContextOpts {
	const flashAttn = true
	return whisperContextOpts{
		UseGPU:             useGPU,
		FlashAttn:          flashAttn,
		DTWTokenTimestamps: !flashAttn, // mutually exclusive in current whisper.cpp
		DTWAheadsPreset:    dtwAheadsForModel(modelName),
		DTWMemSize:         128 * 1024 * 1024, // 128 MiB, generous; deprecated upstream
	}
}
