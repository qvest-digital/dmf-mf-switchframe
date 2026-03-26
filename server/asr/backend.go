package asr

import "encoding/json"

// Backend is the interface that all ASR backends must implement.
// Each backend owns its entire inference loop — Whisper uses batch
// grow-and-deduplicate, Nemotron uses continuous chunk streaming.
type Backend interface {
	// Name returns the backend identifier (e.g., "whisper", "nemotron").
	Name() string

	// Start begins the inference loop. The backend calls onTranscript
	// with recognized words. Audio flows in via IngestAudio after Start.
	Start(onTranscript func(words []TranscriptWord, isFinal bool)) error

	// IngestAudio feeds raw PCM to the backend. Each backend handles
	// its own resampling, buffering, and VAD internally.
	IngestAudio(pcm []float32, pts int64, sampleRate, channels int)

	// Reset clears internal audio buffers and inference state.
	// Called on program source changes.
	Reset()

	// Stop halts the inference loop but keeps the model loaded.
	Stop()

	// Close releases all resources (model, GPU memory).
	Close()

	// Models lists available models for this backend.
	Models() []string

	// SetModel swaps the model at runtime within this backend.
	SetModel(name string) error

	// ModelName returns the currently loaded model's display name.
	ModelName() string

	// Config returns the backend-specific configuration as JSON.
	Config() json.RawMessage

	// SetConfig applies backend-specific configuration from JSON.
	SetConfig(raw json.RawMessage) error
}

// TranscriptWord is a single word with its confidence probability
// and timing information from the ASR backend.
type TranscriptWord struct {
	Text       string
	Confidence float32 // 0.0-1.0
	GapBefore  int     // milliseconds of silence before this word (0 = continuation)
}
