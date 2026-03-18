package audio

// PCMRingBuffer is a sample-level circular buffer for processed PCM.
// NOT thread-safe — caller must provide synchronization (mixer mutex).
//
// Sources push variable-length PCM chunks as they arrive (bursty).
// The output ticker pulls exactly N samples per tick (fixed cadence).
// When the buffer has fewer than N samples, the last N samples are
// returned (freeze-repeat) to avoid silence gaps.
type PCMRingBuffer struct {
	buf       []float32 // circular sample buffer
	rd        int       // read position
	wr        int       // write position
	count     int       // current number of samples in buffer
	cap       int       // total capacity in samples
	lastFrame []float32 // last successfully popped frame (for freeze-repeat)
}

// NewPCMRingBuffer creates a ring buffer with the given capacity in samples.
// For stereo 48kHz with 10 frames of buffer: 10 * 1024 * 2 = 20480 samples.
func NewPCMRingBuffer(capacitySamples int) *PCMRingBuffer {
	if capacitySamples < 2048 {
		capacitySamples = 2048
	}
	return &PCMRingBuffer{
		buf: make([]float32, capacitySamples),
		cap: capacitySamples,
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
// than n samples are available, returns a copy of the last popped frame
// (freeze-repeat). Returns nil if no samples have ever been pushed.
func (rb *PCMRingBuffer) Pop(n int) []float32 {
	if n <= 0 {
		return nil
	}

	if rb.count < n {
		// Freeze-repeat: return last frame.
		if rb.lastFrame == nil {
			return nil
		}
		out := make([]float32, len(rb.lastFrame))
		copy(out, rb.lastFrame)
		return out
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

	// Update lastFrame for freeze-repeat.
	if cap(rb.lastFrame) >= n {
		rb.lastFrame = rb.lastFrame[:n]
	} else {
		rb.lastFrame = make([]float32, n)
	}
	copy(rb.lastFrame, out)

	return out
}

// Len returns the current number of buffered samples.
func (rb *PCMRingBuffer) Len() int {
	return rb.count
}

// Reset clears all buffered samples but preserves lastFrame for freeze-repeat.
func (rb *PCMRingBuffer) Reset() {
	rb.rd = 0
	rb.wr = 0
	rb.count = 0
}
