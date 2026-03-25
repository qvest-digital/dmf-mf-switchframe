package st2110

import (
	"math"
	"testing"
)

func TestInt24ToFloat32(t *testing.T) {
	tests := []struct {
		name     string
		input    [3]byte
		expected float32
		tol      float32
	}{
		{
			name:     "max positive 0x7FFFFF → ~1.0",
			input:    [3]byte{0x7F, 0xFF, 0xFF},
			expected: 1.0,
			tol:      0.001,
		},
		{
			name:     "zero 0x000000 → 0.0",
			input:    [3]byte{0x00, 0x00, 0x00},
			expected: 0.0,
			tol:      0.0,
		},
		{
			name:     "max negative 0x800000 → ~-1.0",
			input:    [3]byte{0x80, 0x00, 0x00},
			expected: -1.0,
			tol:      0.001,
		},
		{
			name:     "positive half 0x400000 → ~0.5",
			input:    [3]byte{0x40, 0x00, 0x00},
			expected: 0.5,
			tol:      0.001,
		},
		{
			name:     "negative half 0xC00000 → ~-0.5",
			input:    [3]byte{0xC0, 0x00, 0x00},
			expected: -0.5,
			tol:      0.001,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Int24BEToFloat32(tc.input[:])
			diff := float32(math.Abs(float64(result - tc.expected)))
			if diff > tc.tol {
				t.Errorf("Int24BEToFloat32(%02X %02X %02X) = %f, want %f (diff %f > tol %f)",
					tc.input[0], tc.input[1], tc.input[2], result, tc.expected, diff, tc.tol)
			}
		})
	}
}

func TestFloat32ToInt24(t *testing.T) {
	roundTrips := []struct {
		name string
		val  float32
		tol  float32
	}{
		{"zero", 0.0, 0.001},
		{"positive half", 0.5, 0.001},
		{"negative half", -0.5, 0.001},
		{"near max positive", 0.99, 0.001},
		{"near max negative", -0.99, 0.001},
	}

	for _, tc := range roundTrips {
		t.Run(tc.name, func(t *testing.T) {
			var buf [3]byte
			Float32ToInt24BE(tc.val, buf[:])
			result := Int24BEToFloat32(buf[:])
			diff := float32(math.Abs(float64(result - tc.val)))
			if diff > tc.tol {
				t.Errorf("round-trip(%f): got %f, diff %f > tol %f (bytes: %02X %02X %02X)",
					tc.val, result, diff, tc.tol, buf[0], buf[1], buf[2])
			}
		})
	}
}

func TestFloat32ToInt24Clamping(t *testing.T) {
	// Values beyond [-1.0, 1.0] should clamp.
	var buf [3]byte

	// Positive overflow
	Float32ToInt24BE(2.0, buf[:])
	if buf[0] != 0x7F || buf[1] != 0xFF || buf[2] != 0xFF {
		t.Errorf("clamp positive: got %02X %02X %02X, want 7F FF FF", buf[0], buf[1], buf[2])
	}

	// Negative overflow
	Float32ToInt24BE(-2.0, buf[:])
	if buf[0] != 0x80 || buf[1] != 0x00 || buf[2] != 0x00 {
		t.Errorf("clamp negative: got %02X %02X %02X, want 80 00 00", buf[0], buf[1], buf[2])
	}
}

func TestConvertInt24BufferToFloat32(t *testing.T) {
	// 2 channels, 4 samples per channel = 8 samples total = 24 bytes of silence.
	channels := 2
	samplesPerCh := 4
	totalSamples := channels * samplesPerCh
	input := make([]byte, totalSamples*3)  // all zeros = silence
	output := make([]float32, totalSamples)

	ConvertInt24BEToFloat32(input, output, channels, samplesPerCh)

	for i, v := range output {
		if v != 0.0 {
			t.Errorf("output[%d] = %f, want 0.0", i, v)
		}
	}
}

func TestConvertInt24BufferToFloat32NonSilence(t *testing.T) {
	// 1 channel, 2 samples: max positive then max negative.
	channels := 1
	samplesPerCh := 2
	input := []byte{
		0x7F, 0xFF, 0xFF, // +max
		0x80, 0x00, 0x00, // -max
	}
	output := make([]float32, 2)

	ConvertInt24BEToFloat32(input, output, channels, samplesPerCh)

	if math.Abs(float64(output[0]-1.0)) > 0.001 {
		t.Errorf("output[0] = %f, want ~1.0", output[0])
	}
	if math.Abs(float64(output[1]+1.0)) > 0.001 {
		t.Errorf("output[1] = %f, want ~-1.0", output[1])
	}
}

func TestConvertFloat32ToInt24BE(t *testing.T) {
	// Batch round-trip: 2 channels, 3 samples per channel.
	input := []float32{0.0, 0.5, -0.5, 0.25, -0.25, 0.99}
	encoded := make([]byte, len(input)*3)
	ConvertFloat32ToInt24BE(input, encoded)

	// Decode back.
	decoded := make([]float32, len(input))
	ConvertInt24BEToFloat32(encoded, decoded, 1, len(input))

	for i := range input {
		diff := float32(math.Abs(float64(decoded[i] - input[i])))
		if diff > 0.001 {
			t.Errorf("round-trip[%d]: input %f → decoded %f, diff %f > 0.001",
				i, input[i], decoded[i], diff)
		}
	}
}

func BenchmarkInt24BEToFloat32(b *testing.B) {
	buf := []byte{0x40, 0x00, 0x00}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Int24BEToFloat32(buf)
	}
}

func BenchmarkFloat32ToInt24BE(b *testing.B) {
	var buf [3]byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Float32ToInt24BE(0.5, buf[:])
	}
}

func BenchmarkConvertInt24BEToFloat32(b *testing.B) {
	// 48 channels, 1ms at 48kHz = 48 samples.
	channels := 48
	samplesPerCh := 48
	total := channels * samplesPerCh
	input := make([]byte, total*3)
	output := make([]float32, total)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ConvertInt24BEToFloat32(input, output, channels, samplesPerCh)
	}
}
