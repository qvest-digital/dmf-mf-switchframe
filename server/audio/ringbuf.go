package audio

// PCMRingBuffer is a sample-level circular buffer for processed PCM.
// NOT thread-safe — caller must provide synchronization (mixer mutex).
//
// Sources push variable-length PCM chunks as they arrive (bursty).
// The mixer pulls exactly N samples per period (fixed cadence).
// When the buffer has fewer than N samples, silence (zeros) is
// returned — less audible than freeze-repeat with bursty SRT delivery.
type PCMRingBuffer struct {
	buf      []float32 // circular sample buffer
	rd       int       // read position
	wr       int       // write position
	count    int       // current number of samples in buffer
	cap      int       // total capacity in samples
	channels int       // interleaved channel count (>=1)
}

// NewPCMRingBuffer creates a ring buffer with the given capacity in samples.
// For stereo 48kHz with 10 frames of buffer: 10 * 1024 * 2 = 20480 samples.
// The channel count defaults to 1, so FrameCount() == Len() until callers
// use NewPCMRingBufferWithChannels.
func NewPCMRingBuffer(capacitySamples int) *PCMRingBuffer {
	return NewPCMRingBufferWithChannels(capacitySamples, 1)
}

// NewPCMRingBufferWithChannels creates a ring buffer that knows its
// interleaved channel count. FrameCount() returns samples/channels so
// time conversions don't leak channel arithmetic into callers.
func NewPCMRingBufferWithChannels(capacitySamples, channels int) *PCMRingBuffer {
	if capacitySamples < 2048 {
		capacitySamples = 2048
	}
	if channels < 1 {
		channels = 1
	}
	return &PCMRingBuffer{
		buf:      make([]float32, capacitySamples),
		cap:      capacitySamples,
		channels: channels,
	}
}

// Push appends processed PCM samples to the buffer. If the buffer would
// overflow, the oldest samples are discarded (newest-wins).
func (rb *PCMRingBuffer) Push(pcm []float32) {
	n := len(pcm)
	if n == 0 {
		return
	}

	// If pushing more than capacity, only keep the newest samples.
	if n > rb.cap {
		pcm = pcm[n-rb.cap:]
		n = rb.cap
	}

	// Make room if needed by advancing read pointer past oldest data.
	avail := rb.cap - rb.count
	if n > avail {
		drop := n - avail
		rb.rd = (rb.rd + drop) % rb.cap
		rb.count -= drop
	}

	// Write samples, wrapping around if needed.
	first := rb.cap - rb.wr
	if first >= n {
		copy(rb.buf[rb.wr:rb.wr+n], pcm)
	} else {
		copy(rb.buf[rb.wr:], pcm[:first])
		copy(rb.buf[:n-first], pcm[first:])
	}
	rb.wr = (rb.wr + n) % rb.cap
	rb.count += n
}

// Pop removes and returns exactly n samples from the buffer. If fewer
// than n samples are available, returns silence (zeros). Returns nil
// if n <= 0.
func (rb *PCMRingBuffer) Pop(n int) []float32 {
	if n <= 0 {
		return nil
	}

	if rb.count < n {
		// Not enough samples — return silence (zeros).
		// Silence is less audible than repeating the last chunk,
		// which sounds robotic with bursty SRT delivery.
		return make([]float32, n)
	}

	// Read n samples from read position.
	out := make([]float32, n)
	first := rb.cap - rb.rd
	if first >= n {
		copy(out, rb.buf[rb.rd:rb.rd+n])
	} else {
		copy(out[:first], rb.buf[rb.rd:])
		copy(out[first:], rb.buf[:n-first])
	}
	rb.rd = (rb.rd + n) % rb.cap
	rb.count -= n

	return out
}

// Len returns the current number of buffered samples (interleaved values).
func (rb *PCMRingBuffer) Len() int {
	return rb.count
}

// FrameCount returns the current number of buffered frames (samples / channels).
// Prefer this over Len() for time-domain math — it avoids callers redoing
// channel division themselves and getting it wrong for non-stereo configs.
func (rb *PCMRingBuffer) FrameCount() int {
	if rb.channels <= 1 {
		return rb.count
	}
	return rb.count / rb.channels
}

// Reset clears all buffered samples.
func (rb *PCMRingBuffer) Reset() {
	rb.rd = 0
	rb.wr = 0
	rb.count = 0
}
