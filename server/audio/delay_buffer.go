package audio

import (
	"sync"
	"time"

	"github.com/zsiec/prism/media"
)

const maxAudioDelayMs = 500

type delayedAudioFrame struct {
	frame     *media.AudioFrame
	arrivedAt time.Time
}

// AudioDelayBuffer delays audio frames by a configurable number of milliseconds.
// At 0ms delay, frames pass through immediately with no allocation.
type AudioDelayBuffer struct {
	mu      sync.Mutex
	delayMs int
	frames  []delayedAudioFrame
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

	b.frames = append(b.frames, delayedAudioFrame{frame: frame, arrivedAt: now})

	delay := time.Duration(b.delayMs) * time.Millisecond
	if len(b.frames) > 0 && now.Sub(b.frames[0].arrivedAt) >= delay {
		f := b.frames[0].frame
		b.frames = b.frames[1:]
		return f
	}
	return nil
}
