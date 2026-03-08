package audio

import (
	"sync"
	"time"

	"github.com/zsiec/prism/media"
)

const maxAudioDelayMs = 500

// audioDelayRingSize is the fixed capacity of the circular buffer.
// At 500ms max delay with ~23ms AAC frames, worst case is ~22 frames.
// 32 provides comfortable headroom.
const audioDelayRingSize = 32

type delayedAudioFrame struct {
	frame     *media.AudioFrame
	arrivedAt time.Time
}

// AudioDelayBuffer delays audio frames by a configurable number of milliseconds.
// At 0ms delay, frames pass through immediately with no allocation.
// Uses a fixed-size circular buffer to avoid unbounded slice growth.
type AudioDelayBuffer struct {
	mu      sync.Mutex
	delayMs int
	ring    [audioDelayRingSize]delayedAudioFrame
	head    int // read position
	tail    int // write position
	count   int // number of valid frames in ring
}

// NewAudioDelayBuffer creates a new AudioDelayBuffer with the given delay in ms.
// Delay is clamped to [0, 500].
func NewAudioDelayBuffer(delayMs int) *AudioDelayBuffer {
	if delayMs > maxAudioDelayMs {
		delayMs = maxAudioDelayMs
	}
	if delayMs < 0 {
		delayMs = 0
	}
	return &AudioDelayBuffer{delayMs: delayMs}
}

// SetDelayMs updates the delay. Values are clamped to [0, 500].
func (b *AudioDelayBuffer) SetDelayMs(ms int) {
	if ms > maxAudioDelayMs {
		ms = maxAudioDelayMs
	}
	if ms < 0 {
		ms = 0
	}
	b.mu.Lock()
	b.delayMs = ms
	b.mu.Unlock()
}

// DelayMs returns the current delay in milliseconds.
func (b *AudioDelayBuffer) DelayMs() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.delayMs
}

// Ingest stores a frame and returns the oldest frame that has aged past the delay.
// At 0ms delay, the input frame is returned immediately with no buffering.
func (b *AudioDelayBuffer) Ingest(frame *media.AudioFrame) *media.AudioFrame {
	return b.ingestAt(frame, time.Now())
}

// ingestAt is the testable core of Ingest, accepting a clock parameter.
func (b *AudioDelayBuffer) ingestAt(frame *media.AudioFrame, now time.Time) *media.AudioFrame {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.delayMs == 0 {
		return frame
	}

	// Write to tail position.
	b.ring[b.tail] = delayedAudioFrame{frame: frame, arrivedAt: now}
	b.tail = (b.tail + 1) % audioDelayRingSize
	if b.count < audioDelayRingSize {
		b.count++
	} else {
		// Ring full — advance head to drop the oldest frame.
		b.head = (b.head + 1) % audioDelayRingSize
	}

	// Check if the oldest frame has aged past the delay.
	delay := time.Duration(b.delayMs) * time.Millisecond
	if b.count > 0 && now.Sub(b.ring[b.head].arrivedAt) >= delay {
		f := b.ring[b.head].frame
		b.ring[b.head] = delayedAudioFrame{} // clear to avoid retaining pointer
		b.head = (b.head + 1) % audioDelayRingSize
		b.count--
		return f
	}
	return nil
}
