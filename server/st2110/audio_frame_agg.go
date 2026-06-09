package st2110

// audioFrameAggregator re-slices an arbitrary-size stream of L24 audio bytes
// into fixed-size ST 2110-30 framebuffers. The MTL audio TX framebuffer has a
// fixed size (10ms of audio at session creation), but callers (the mixer
// output ticker) deliver PCM in chunks of unrelated sizes. Submitting a short
// chunk directly would transmit stale bytes from the previous frame in the
// framebuffer tail, and a long chunk would be silently truncated, dropping
// samples. The aggregator buffers partial frames so every transmitted frame
// contains exactly frameSize bytes of contiguous audio.
//
// Not safe for concurrent use; AudioTx serializes access with its mutex.
type audioFrameAggregator struct {
	frameSize int
	maxBytes  int // pending cap; oldest whole frames dropped on overflow
	pending   []byte
}

// newAudioFrameAggregator creates an aggregator that emits frameSize-byte
// frames and buffers at most maxFrames frames of pending audio. When the
// pending buffer overflows (TX stalled), the oldest data is dropped in whole
// frame units so sample/channel alignment is preserved.
func newAudioFrameAggregator(frameSize, maxFrames int) *audioFrameAggregator {
	if maxFrames < 1 {
		maxFrames = 1
	}
	return &audioFrameAggregator{
		frameSize: frameSize,
		maxBytes:  frameSize * maxFrames,
	}
}

// Push appends L24 bytes to the pending buffer. It returns the number of
// bytes dropped (oldest first, whole frames) to keep the buffer bounded;
// zero in normal operation.
func (a *audioFrameAggregator) Push(b []byte) (droppedBytes int) {
	a.pending = append(a.pending, b...)
	if len(a.pending) > a.maxBytes {
		excess := len(a.pending) - a.maxBytes
		// Round up to whole frames so the stream stays frame-aligned and
		// interleaved channels are not shifted.
		drop := (excess + a.frameSize - 1) / a.frameSize * a.frameSize
		if drop > len(a.pending) {
			drop = len(a.pending)
		}
		a.pending = a.pending[:copy(a.pending, a.pending[drop:])]
		droppedBytes = drop
	}
	return droppedBytes
}

// Frame returns the next full frame of pending audio, or nil if less than a
// full frame is buffered. The returned slice is only valid until the next
// Push or Consume call.
func (a *audioFrameAggregator) Frame() []byte {
	if len(a.pending) < a.frameSize {
		return nil
	}
	return a.pending[:a.frameSize]
}

// Consume discards the frame most recently returned by Frame. It is a no-op
// when less than a full frame is pending.
func (a *audioFrameAggregator) Consume() {
	if len(a.pending) < a.frameSize {
		return
	}
	a.pending = a.pending[:copy(a.pending, a.pending[a.frameSize:])]
}

// PendingBytes returns the number of buffered bytes not yet emitted.
func (a *audioFrameAggregator) PendingBytes() int {
	return len(a.pending)
}
