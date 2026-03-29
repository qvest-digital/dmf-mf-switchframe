package audio

// PCMRingBuffer is a sample-level circular buffer for processed PCM.
// NOT thread-safe — caller must provide synchronization (mixer mutex).
//
// Sources push variable-length PCM chunks as they arrive (bursty).
// The output ticker pulls exactly N samples per tick (fixed cadence).
// When the buffer has fewer than N samples, silence (zeros) is
// returned — less audible than freeze-repeat with bursty SRT delivery.
//
// When targetDepth > 0, Pop trims excess samples from the front before
// reading. This keeps the FIFO depth bounded, preventing unbounded
// A/V content offset in the ref clock architecture (video is newest-wins,
// audio is FIFO — without trim, the FIFO accumulates stale content).
type PCMRingBuffer struct {
	buf         []float32 // circular sample buffer
	rd          int       // read position
	wr          int       // write position
	count       int       // current number of samples in buffer
	cap         int       // total capacity in samples
	targetDepth int       // target FIFO depth (0 = no trim)
	lastFrame   []float32 // last successfully popped frame (for freeze-repeat)
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

// SetTargetDepth sets the target FIFO depth in samples. When set > 0,
// Pop trims excess samples before reading, keeping the buffer depth
// bounded to targetDepth + requested samples. This prevents unbounded
// A/V content offset from accumulating in the FIFO.
//
// Typical value: 2048 (stereo interleaved = 1024 frames = ~21ms at 48kHz).
func (rb *PCMRingBuffer) SetTargetDepth(samples int) {
	rb.targetDepth = samples
}

// Pop removes and returns exactly n samples from the buffer. If fewer
// than n samples are available, returns silence (zeros). Returns nil
// if n <= 0.
//
// When targetDepth > 0 and the buffer holds more than targetDepth + n
// samples, the oldest excess samples are discarded before reading.
// This bounds the FIFO latency to ~targetDepth samples regardless of
// how much data accumulated from bursty source delivery or clock drift.
func (rb *PCMRingBuffer) Pop(n int) []float32 {
	if n <= 0 {
		return nil
	}

	// Trim excess: if buffer is deeper than target + requested, discard
	// the oldest samples to keep FIFO latency bounded. This is the audio
	// equivalent of video's "newest-wins" — we keep the freshest content.
	if rb.targetDepth > 0 && rb.count > rb.targetDepth+n {
		excess := rb.count - rb.targetDepth - n
		rb.rd = (rb.rd + excess) % rb.cap
		rb.count -= excess
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
