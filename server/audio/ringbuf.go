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
	head      int       // next write position
	count     int       // current number of samples in buffer
	cap       int       // total capacity in samples
	lastFrame []float32 // last successfully popped frame (for freeze-repeat)
}

// NewPCMRingBuffer creates a ring buffer with the given capacity in samples.
// For stereo 48kHz with 3 frames of buffer: 3 * 1024 * 2 = 6144 samples.
func NewPCMRingBuffer(capacitySamples int) *PCMRingBuffer {
	if capacitySamples < 1024 {
		capacitySamples = 1024
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

	// Make room if needed by dropping oldest samples.
	if rb.count+n > rb.cap {
		drop := rb.count + n - rb.cap
		rb.count -= drop
		// No need to move tail pointer — we use head-based addressing.
	}

	// Write samples. May wrap around.
	writeStart := (rb.head) % rb.cap
	firstChunk := rb.cap - writeStart
	if firstChunk >= n {
		copy(rb.buf[writeStart:writeStart+n], pcm)
	} else {
		copy(rb.buf[writeStart:], pcm[:firstChunk])
		copy(rb.buf[:n-firstChunk], pcm[firstChunk:])
	}
	rb.head = (rb.head + n) % rb.cap
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

	// Read n samples from tail position.
	tail := (rb.head - rb.count + rb.cap) % rb.cap
	out := make([]float32, n)

	firstChunk := rb.cap - tail
	if firstChunk >= n {
		copy(out, rb.buf[tail:tail+n])
	} else {
		copy(out[:firstChunk], rb.buf[tail:])
		copy(out[firstChunk:], rb.buf[:n-firstChunk])
	}
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
	rb.head = 0
	rb.count = 0
}
