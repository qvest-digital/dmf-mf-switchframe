package main

// prerollBuffer is a simple circular buffer holding the most recent N
// audio samples. Used to prepend a short audio context to a new
// utterance so the first phoneme isn't clipped at the VAD edge.
type prerollBuffer struct {
	buf    []float32
	cap    int
	head   int  // next write position
	filled bool // true once we've wrapped at least once
}

func newPrerollBuffer(capacity int) *prerollBuffer {
	if capacity < 0 {
		capacity = 0
	}
	return &prerollBuffer{
		buf: make([]float32, capacity),
		cap: capacity,
	}
}

// Write appends samples, dropping oldest if over capacity.
func (r *prerollBuffer) Write(samples []float32) {
	if r.cap == 0 {
		return
	}
	// If a single Write is bigger than capacity, just keep its tail.
	if len(samples) >= r.cap {
		copy(r.buf, samples[len(samples)-r.cap:])
		r.head = 0
		r.filled = true
		return
	}
	for _, s := range samples {
		r.buf[r.head] = s
		r.head++
		if r.head >= r.cap {
			r.head = 0
			r.filled = true
		}
	}
}

// Snapshot returns a fresh copy of the current contents in chronological
// order (oldest first, newest last).
func (r *prerollBuffer) Snapshot() []float32 {
	if r.cap == 0 {
		return nil
	}
	if !r.filled {
		out := make([]float32, r.head)
		copy(out, r.buf[:r.head])
		return out
	}
	out := make([]float32, r.cap)
	copy(out, r.buf[r.head:])
	copy(out[r.cap-r.head:], r.buf[:r.head])
	return out
}

// Reset empties the buffer.
func (r *prerollBuffer) Reset() {
	r.head = 0
	r.filled = false
}
