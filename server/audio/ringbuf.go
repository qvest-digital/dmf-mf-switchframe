package audio

// PCMRingBuffer is a fixed-capacity FIFO for processed PCM frames.
// NOT thread-safe — caller must provide synchronization (mixer mutex).
//
// Sources push processed PCM frames as they arrive (bursty). An output
// ticker pops frames at a fixed rate (~21ms). When the buffer is empty,
// Pop returns a copy of the last successfully popped frame (freeze-repeat)
// to avoid silence gaps.
type PCMRingBuffer struct {
	frames    [][]float32 // circular buffer slots (pre-allocated)
	head      int         // next write position
	tail      int         // next read position
	count     int         // current number of frames in buffer
	cap       int         // max frames
	lastFrame []float32   // last successfully popped frame (for freeze-repeat)
}

// NewPCMRingBuffer creates a ring buffer with the given capacity.
// Capacity must be at least 1.
func NewPCMRingBuffer(capacity int) *PCMRingBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &PCMRingBuffer{
		frames: make([][]float32, capacity),
		cap:    capacity,
	}
}

// Push adds a processed PCM frame. If full, drops the oldest frame
// (newest-wins policy). The input slice is deep-copied.
func (rb *PCMRingBuffer) Push(pcm []float32) {
	if rb.count == rb.cap {
		// Buffer full — drop oldest by advancing tail.
		rb.tail = (rb.tail + 1) % rb.cap
		rb.count--
	}

	// Reuse existing slot slice if capacity is sufficient (growBuf pattern).
	slot := rb.frames[rb.head]
	if cap(slot) >= len(pcm) {
		slot = slot[:len(pcm)]
	} else {
		slot = make([]float32, len(pcm))
	}
	copy(slot, pcm)
	rb.frames[rb.head] = slot

	rb.head = (rb.head + 1) % rb.cap
	rb.count++
}

// Pop removes and returns the oldest frame as a deep copy. If the buffer
// is empty, returns a copy of the last successfully popped frame
// (freeze-repeat). Returns nil if no frame has ever been pushed.
func (rb *PCMRingBuffer) Pop() []float32 {
	if rb.count == 0 {
		// Freeze-repeat: return a copy of lastFrame.
		if rb.lastFrame == nil {
			return nil
		}
		out := make([]float32, len(rb.lastFrame))
		copy(out, rb.lastFrame)
		return out
	}

	// Deep-copy from internal slot to caller.
	src := rb.frames[rb.tail]
	out := make([]float32, len(src))
	copy(out, src)

	rb.tail = (rb.tail + 1) % rb.cap
	rb.count--

	// Update lastFrame for freeze-repeat. Reuse allocation if possible.
	if cap(rb.lastFrame) >= len(out) {
		rb.lastFrame = rb.lastFrame[:len(out)]
	} else {
		rb.lastFrame = make([]float32, len(out))
	}
	copy(rb.lastFrame, out)

	return out
}

// Len returns the current number of buffered frames.
func (rb *PCMRingBuffer) Len() int {
	return rb.count
}

// Reset clears all buffered frames but preserves lastFrame for freeze-repeat.
func (rb *PCMRingBuffer) Reset() {
	rb.head = 0
	rb.tail = 0
	rb.count = 0
	// Note: we keep rb.frames allocated (slots stay for reuse) and
	// rb.lastFrame intact so freeze-repeat continues to work.
}
