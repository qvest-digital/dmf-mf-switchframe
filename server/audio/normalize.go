package audio

// normalizeInt16 converts an int16 PCM sample to float32 in the range [-1.0, 1.0].
// Division by 32768.0 (not 32767) ensures that -32768 maps to exactly -1.0.
func normalizeInt16(v int16) float32 {
	return float32(v) / 32768.0
}
