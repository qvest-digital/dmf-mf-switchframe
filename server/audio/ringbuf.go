package audio

// PCMRingBuffer is a sample-level circular buffer for processed PCM.
// NOT thread-safe — caller must provide synchronization (mixer mutex).
//
// Sources push variable-length PCM chunks as they arrive (bursty).
// The output ticker pulls exactly N samples per tick (fixed cadence).
// When the buffer has fewer than N samples, silence (zeros) is
// returned — less audible than freeze-repeat with bursty SRT delivery.
type PCMRingBuffer struct {
	buf       []float32 // circular sample buffer
	rd        int       // read position
	wr        int       // write position
	count     int       // current number of samples in buffer
	cap       int       // total capacity in samples
	channels  int       // interleaved channel count (>=1)
	lastFrame []float32 // last successfully popped frame (for freeze-repeat)
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

	// Update lastFrame for freeze-repeat.
	if cap(rb.lastFrame) >= n {
		rb.lastFrame = rb.lastFrame[:n]
	} else {
		rb.lastFrame = make([]float32, n)
	}
	copy(rb.lastFrame, out)

	return out
}

// PopDriftCompensated reads samples with adaptive drift compensation.
// Always returns exactly n samples. Adjusts the actual number consumed
// from the buffer to steer depth toward targetDepth:
//
//   - depth > target + threshold: consume n + channels (micro speed-up)
//   - depth < target - threshold: consume n - channels (micro slow-down)
//   - otherwise: consume exactly n (no correction)
//
// The ±channels adjustment is absorbed by linear interpolation so the
// output is always exactly n samples with no discontinuities. This
// corrects clock drift between push source and pop consumer at a rate
// of ±1 sample per frame (~20μs at 48kHz) — completely inaudible.
//
// channels is needed to keep adjustments frame-aligned (stereo pairs).
// targetDepth is in total sample values (samples × channels).
func (rb *PCMRingBuffer) PopDriftCompensated(n, targetDepth, channels int) []float32 {
	if n <= 0 {
		return nil
	}
	if channels < 1 {
		channels = 1
	}

	// Not enough data for even a reduced read — return silence.
	if rb.count < n-channels {
		return make([]float32, n)
	}

	// Determine how many samples to actually consume.
	threshold := 1024 * channels // 1 AAC frame hysteresis
	actual := n
	if rb.count > targetDepth+threshold && rb.count >= n+channels {
		actual = n + channels // consume 1 extra frame-pair (speed up)
	} else if rb.count < targetDepth-threshold && rb.count >= n-channels && n > channels {
		actual = n - channels // consume 1 fewer frame-pair (slow down)
	}

	// Read 'actual' samples from ring buffer.
	raw := make([]float32, actual)
	first := rb.cap - rb.rd
	if first >= actual {
		copy(raw, rb.buf[rb.rd:rb.rd+actual])
	} else {
		copy(raw[:first], rb.buf[rb.rd:])
		copy(raw[first:], rb.buf[:actual-first])
	}
	rb.rd = (rb.rd + actual) % rb.cap
	rb.count -= actual

	// If actual == n, no resampling needed.
	if actual == n {
		if cap(rb.lastFrame) >= n {
			rb.lastFrame = rb.lastFrame[:n]
		} else {
			rb.lastFrame = make([]float32, n)
		}
		copy(rb.lastFrame, raw)
		return raw
	}

	// Linear interpolation: resample 'actual' samples to exactly 'n'.
	out := make([]float32, n)
	frames := n / channels
	srcFrames := actual / channels

	// Guard: need at least 2 source frames to interpolate.
	if srcFrames < 2 || frames < 2 {
		copy(out, raw)
		if cap(rb.lastFrame) >= n {
			rb.lastFrame = rb.lastFrame[:n]
		} else {
			rb.lastFrame = make([]float32, n)
		}
		copy(rb.lastFrame, out)
		return out
	}

	for f := 0; f < frames; f++ {
		srcPos := float64(f) * float64(srcFrames-1) / float64(frames-1)
		srcIdx := int(srcPos)
		frac := float32(srcPos - float64(srcIdx))

		if srcIdx >= srcFrames-1 {
			srcIdx = srcFrames - 2
			frac = 1.0
		}
		if srcIdx < 0 {
			srcIdx = 0
			frac = 0
		}

		for c := 0; c < channels; c++ {
			s0 := raw[srcIdx*channels+c]
			s1 := raw[(srcIdx+1)*channels+c]
			out[f*channels+c] = s0 + frac*(s1-s0)
		}
	}

	if cap(rb.lastFrame) >= n {
		rb.lastFrame = rb.lastFrame[:n]
	} else {
		rb.lastFrame = make([]float32, n)
	}
	copy(rb.lastFrame, out)
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

// Reset clears all buffered samples but preserves lastFrame for freeze-repeat.
func (rb *PCMRingBuffer) Reset() {
	rb.rd = 0
	rb.wr = 0
	rb.count = 0
}
