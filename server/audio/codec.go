package audio

// Decoder decodes AAC frames to interleaved float32 PCM.
// Implementations: FDKDecoder (cgo), mockDecoder (tests).
type Decoder interface {
	// Decode decodes an AAC frame into interleaved float32 PCM.
	// Returns 1024 samples per channel for AAC-LC at 48kHz.
	Decode(aacFrame []byte) ([]float32, error)
	Close() error
}

// Encoder encodes interleaved float32 PCM to AAC frames.
// Implementations: FDKEncoder (cgo), mockEncoder (tests).
type Encoder interface {
	// Encode encodes interleaved float32 PCM into an AAC frame.
	Encode(pcm []float32) ([]byte, error)
	Close() error
}

// DecoderFactory creates Decoders. Allows tests to inject mock factories.
type DecoderFactory func(sampleRate, channels int) (Decoder, error)

// EncoderFactory creates Encoders. Allows tests to inject mock factories.
type EncoderFactory func(sampleRate, channels int) (Encoder, error)
