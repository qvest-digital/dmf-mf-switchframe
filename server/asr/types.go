package asr

// TranscriptWord is a single word with its confidence probability
// and timing information from whisper.
type TranscriptWord struct {
	Text       string
	Confidence float32 // 0.0-1.0 from whisper token probability
	GapBefore  int     // milliseconds of silence before this word (0 = continuation)
}
