package audio

// Decoder decodes AAC frames to interleaved float32 PCM.
// Implementations: FDKDecoder (cgo), mockDecoder (tests).
type Decoder interface {
	// Decode decodes an AAC frame into interleaved float32 PCM.
	// Returns 1024 samples per channel for AAC-LC at 48kHz.
	Decode(aacFrame []byte) ([]float32, error)
	// Channels returns the interleaved channel count of the most recent
	// successful Decode. The mixer uses this to detect surround sources
	// that must be downmixed before reaching the per-channel DSP chain.
	// Returns 0 when no successful decode has happened yet.
	Channels() int
	Close() error
}

// FileDecoder is a Decoder that reports the delay it adds to its output.
// Implementation: NewFDKFileDecoder.
type FileDecoder interface {
	Decoder
	// OutputDelay returns how many samples per channel the decoder
	// delays its output by beyond the stream's own.
	OutputDelay() int
}

// Encoder encodes interleaved float32 PCM to AAC frames.
// Implementations: FDKEncoder (cgo), mockEncoder (tests).
type Encoder interface {
	// Encode encodes interleaved float32 PCM into an AAC frame.
	Encode(pcm []float32) ([]byte, error)
	// Delay returns how many samples per channel the audio in an encoded
	// frame lags the PCM passed in for it (0 when unknown).
	Delay() int
	Close() error
}

// DecoderFactory creates Decoders. Allows tests to inject mock factories.
type DecoderFactory func(sampleRate, channels int) (Decoder, error)

// EncoderFactory creates Encoders. Allows tests to inject mock factories.
type EncoderFactory func(sampleRate, channels int) (Encoder, error)
