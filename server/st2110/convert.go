package st2110

// Int24BEToFloat32 converts a big-endian 24-bit signed integer to float32
// in the range [-1.0, 1.0]. The input slice must be at least 3 bytes.
// Sign extension is performed from bit 23.
func Int24BEToFloat32(b []byte) float32 {
	// Assemble 24-bit value from big-endian bytes.
	val := int32(b[0])<<16 | int32(b[1])<<8 | int32(b[2])

	// Sign-extend from bit 23: if the sign bit is set, extend to 32 bits.
	if val&0x800000 != 0 {
		val |= ^0xFFFFFF // set upper bits to 1
	}

	// Normalize: 0x7FFFFF → ~1.0, 0x800000 → -1.0
	return float32(val) / float32(0x7FFFFF)
}

// Float32ToInt24BE converts a float32 in [-1.0, 1.0] to a big-endian 24-bit
// signed integer. Values outside the range are clamped. The output slice must
// be at least 3 bytes.
func Float32ToInt24BE(f float32, b []byte) {
	// Scale and clamp.
	scaled := f * float32(0x7FFFFF)

	var val int32
	if scaled >= float32(0x7FFFFF) {
		val = 0x7FFFFF
	} else if scaled <= -float32(0x800000) {
		val = -0x800000
	} else if scaled >= 0 {
		val = int32(scaled + 0.5) // round to nearest
	} else {
		val = int32(scaled - 0.5) // round to nearest (negative)
	}

	// Mask to 24 bits and write big-endian.
	u := uint32(val) & 0xFFFFFF
	b[0] = byte(u >> 16)
	b[1] = byte(u >> 8)
	b[2] = byte(u)
}

// ConvertInt24BEToFloat32 converts a batch of interleaved big-endian 24-bit
// PCM samples to float32. The input must contain channels*samplesPerCh*3 bytes.
// The output must have room for channels*samplesPerCh float32 values.
// Sample ordering is preserved (interleaved in, interleaved out).
func ConvertInt24BEToFloat32(input []byte, output []float32, channels, samplesPerCh int) {
	total := channels * samplesPerCh
	for i := 0; i < total; i++ {
		offset := i * 3
		output[i] = Int24BEToFloat32(input[offset : offset+3])
	}
}

// ConvertFloat32ToInt24BE converts a batch of interleaved float32 samples to
// big-endian 24-bit PCM. The output must have room for len(input)*3 bytes.
func ConvertFloat32ToInt24BE(input []float32, output []byte) {
	for i, f := range input {
		offset := i * 3
		Float32ToInt24BE(f, output[offset:offset+3])
	}
}
