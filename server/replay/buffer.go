package replay

import (
	"sync"
	"time"

	"github.com/zsiec/prism/media"
)

// replayBuffer stores encoded H.264 frames in a circular, GOP-aligned buffer.
// When capacity is exceeded, the oldest complete GOP is removed.
type replayBuffer struct {
	mu          sync.RWMutex
	frames      []bufferedFrame
	gops        []gopDescriptor
	maxDuration time.Duration
	bytesUsed   int64
}

// newReplayBuffer creates a replay buffer with the given maximum duration in seconds.
func newReplayBuffer(durationSecs int) *replayBuffer {
	return &replayBuffer{
		maxDuration: time.Duration(durationSecs) * time.Second,
	}
}

// RecordFrame records an encoded video frame into the buffer.
// Delta frames before the first keyframe are silently dropped.
// All data is deep-copied; the caller's buffers are not retained.
func (b *replayBuffer) RecordFrame(frame *media.VideoFrame) {
	b.recordFrameAt(frame, time.Now())
}

// recordFrameAt records a frame with a specific wall-clock time (for testing).
func (b *replayBuffer) recordFrameAt(frame *media.VideoFrame, wallTime time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Drop delta frames if we have no GOPs yet (no keyframe seen).
	if !frame.IsKeyframe && len(b.gops) == 0 {
		return
	}

	// Deep-copy frame data.
	bf := bufferedFrame{
		pts:        frame.PTS,
		isKeyframe: frame.IsKeyframe,
		wallTime:   wallTime,
	}
	if len(frame.WireData) > 0 {
		bf.wireData = make([]byte, len(frame.WireData))
		copy(bf.wireData, frame.WireData)
		b.bytesUsed += int64(len(frame.WireData))
	}
	if frame.IsKeyframe {
		if len(frame.SPS) > 0 {
			bf.sps = make([]byte, len(frame.SPS))
			copy(bf.sps, frame.SPS)
			b.bytesUsed += int64(len(frame.SPS))
		}
		if len(frame.PPS) > 0 {
			bf.pps = make([]byte, len(frame.PPS))
			copy(bf.pps, frame.PPS)
			b.bytesUsed += int64(len(frame.PPS))
		}
	}

	idx := len(b.frames)
	b.frames = append(b.frames, bf)

	if frame.IsKeyframe {
		// Start a new GOP descriptor.
		b.gops = append(b.gops, gopDescriptor{
			startIdx: idx,
			endIdx:   idx,
			wallTime: wallTime,
		})
	} else if len(b.gops) > 0 {
		// Extend the current (last) GOP.
		b.gops[len(b.gops)-1].endIdx = idx
	}

	// Trim oldest GOPs if buffer exceeds max duration.
	b.trimLocked()
}

// trimLocked removes the oldest complete GOPs until the buffer duration
// fits within maxDuration. Must be called with mu held.
func (b *replayBuffer) trimLocked() {
	if len(b.gops) < 2 {
		return // Keep at least one GOP.
	}

	trimmed := false
	for len(b.gops) >= 2 {
		// Use actual frame wall times for accurate duration measurement.
		newest := b.frames[len(b.frames)-1].wallTime
		oldest := b.frames[0].wallTime
		if newest.Sub(oldest) <= b.maxDuration {
			break
		}
		// Remove oldest GOP.
		trimmed = true
		removeEnd := b.gops[0].endIdx + 1
		for i := b.gops[0].startIdx; i <= b.gops[0].endIdx && i < len(b.frames); i++ {
			b.bytesUsed -= int64(len(b.frames[i].wireData))
			b.bytesUsed -= int64(len(b.frames[i].sps))
			b.bytesUsed -= int64(len(b.frames[i].pps))
		}
		b.frames = b.frames[removeEnd:]
		b.gops = b.gops[1:]
		// Reindex remaining GOPs.
		offset := removeEnd
		for i := range b.gops {
			b.gops[i].startIdx -= offset
			b.gops[i].endIdx -= offset
		}
	}

	// Compact slices to release old backing array memory.
	if trimmed {
		newFrames := make([]bufferedFrame, len(b.frames))
		copy(newFrames, b.frames)
		b.frames = newFrames

		newGops := make([]gopDescriptor, len(b.gops))
		copy(newGops, b.gops)
		b.gops = newGops
	}
}

// ExtractClip extracts deep copies of buffered frames between inTime and outTime.
// The clip is GOP-aligned: it starts from the keyframe of the GOP that contains
// or precedes inTime. Returns ErrEmptyClip if no frames fall in the range.
func (b *replayBuffer) ExtractClip(inTime, outTime time.Time) ([]bufferedFrame, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.frames) == 0 {
		return nil, ErrEmptyClip
	}

	// Find the GOP whose keyframe is at or before inTime.
	gopIdx := -1
	for i := len(b.gops) - 1; i >= 0; i-- {
		if !b.gops[i].wallTime.After(inTime) {
			gopIdx = i
			break
		}
	}
	// If no GOP starts at or before inTime, check if the first GOP has
	// frames that overlap the requested range.
	if gopIdx < 0 {
		if len(b.gops) > 0 && !b.gops[0].wallTime.After(outTime) {
			gopIdx = 0
		} else {
			return nil, ErrEmptyClip
		}
	}

	// Verify the GOP's frames actually overlap with the requested range.
	// The GOP keyframe must start before or at outTime.
	if b.gops[gopIdx].wallTime.After(outTime) {
		return nil, ErrEmptyClip
	}

	startIdx := b.gops[gopIdx].startIdx

	// Find the last frame at or before outTime.
	endIdx := -1
	for i := len(b.frames) - 1; i >= startIdx; i-- {
		if !b.frames[i].wallTime.After(outTime) {
			endIdx = i
			break
		}
	}
	if endIdx < startIdx {
		return nil, ErrEmptyClip
	}

	// Verify at least one frame in the range falls at or after inTime,
	// or the range includes the GOP keyframe needed for decoding.
	lastFrameTime := b.frames[endIdx].wallTime
	if lastFrameTime.Before(inTime) && gopIdx == len(b.gops)-1 {
		// All frames are before inTime and there's no later GOP
		return nil, ErrEmptyClip
	}
	// If there's a later GOP whose keyframe is still before inTime,
	// use that one instead for a tighter clip.
	for gopIdx+1 < len(b.gops) && !b.gops[gopIdx+1].wallTime.After(inTime) {
		gopIdx++
		startIdx = b.gops[gopIdx].startIdx
	}

	// Deep-copy the clip frames.
	clip := make([]bufferedFrame, endIdx-startIdx+1)
	for i := startIdx; i <= endIdx; i++ {
		src := &b.frames[i]
		dst := &clip[i-startIdx]
		dst.pts = src.pts
		dst.isKeyframe = src.isKeyframe
		dst.wallTime = src.wallTime
		if len(src.wireData) > 0 {
			dst.wireData = make([]byte, len(src.wireData))
			copy(dst.wireData, src.wireData)
		}
		if len(src.sps) > 0 {
			dst.sps = make([]byte, len(src.sps))
			copy(dst.sps, src.sps)
		}
		if len(src.pps) > 0 {
			dst.pps = make([]byte, len(src.pps))
			copy(dst.pps, src.pps)
		}
	}

	return clip, nil
}

// Status returns the current buffer status.
func (b *replayBuffer) Status() SourceBufferInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()

	info := SourceBufferInfo{
		FrameCount: len(b.frames),
		GOPCount:   len(b.gops),
		BytesUsed:  b.bytesUsed,
	}

	if len(b.frames) >= 2 {
		first := b.frames[0].wallTime
		last := b.frames[len(b.frames)-1].wallTime
		info.DurationSecs = last.Sub(first).Seconds()
	}

	return info
}
