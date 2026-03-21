package asr

// AudioRingBuf is a circular buffer for mono float32 PCM audio.
// Stores up to maxSeconds of audio at the given sample rate.
// Thread-unsafe — caller must synchronize.
type AudioRingBuf struct {
	data     []float32
	capacity int
	writePos int
	count    int
}

// NewAudioRingBuf creates a ring buffer that holds sampleRate * maxSeconds samples.
// For Whisper ASR at 16kHz with 30s window: 16000 * 30 = 480000 samples = 1.92 MB.
func NewAudioRingBuf(sampleRate, maxSeconds int) *AudioRingBuf {
	cap := sampleRate * maxSeconds
	return &AudioRingBuf{
		data:     make([]float32, cap),
		capacity: cap,
	}
}

// Write appends samples to the ring buffer, overwriting the oldest data
// when the buffer is full.
func (b *AudioRingBuf) Write(samples []float32) {
	for _, s := range samples {
		b.data[b.writePos] = s
		b.writePos = (b.writePos + 1) % b.capacity
		if b.count < b.capacity {
			b.count++
		}
	}
}

// Snapshot returns a contiguous copy of all buffered samples in chronological order.
// Returns nil if the buffer is empty. The returned slice is safe to use without
// synchronization — it is a deep copy.
func (b *AudioRingBuf) Snapshot() []float32 {
	if b.count == 0 {
		return nil
	}
	out := make([]float32, b.count)
	if b.count < b.capacity {
		// Buffer hasn't wrapped yet — data starts at index 0.
		copy(out, b.data[:b.count])
	} else {
		// Buffer has wrapped — oldest data starts at writePos.
		firstPart := b.capacity - b.writePos
		copy(out, b.data[b.writePos:])
		copy(out[firstPart:], b.data[:b.writePos])
	}
	return out
}

// SampleCount returns the number of samples currently in the buffer.
func (b *AudioRingBuf) SampleCount() int { return b.count }

// Clear resets the buffer to empty without reallocating.
func (b *AudioRingBuf) Clear() {
	b.writePos = 0
	b.count = 0
}
