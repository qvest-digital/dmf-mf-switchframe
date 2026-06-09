package srt

import "time"

// StreamType identifies whether a PTS value is from a video or audio stream.
type StreamType int

const (
	StreamVideo StreamType = iota
	StreamAudio
)

// ptsJumpThreshold is the maximum PTS delta (in 90kHz ticks) considered
// normal. Deltas larger than this or negative are treated as discontinuities
// (e.g., SRT source loop/reconnect).
const ptsJumpThreshold = 45000 // 0.5s in 90kHz ticks

// crossStreamPTSTolerance is how close a post-discontinuity raw PTS must be
// to the other stream's most recent raw PTS to be classified as a one-sided
// gap (loss burst / decode stall on this stream only) rather than a shared
// timeline jump. It allows for normal A/V interleave skew between the two
// decode callbacks.
const crossStreamPTSTolerance = 90000 // 1s in 90kHz ticks

// otherStreamFreshness is how recently the other stream must have delivered
// a PTS for its timeline to vouch for the shared clock. A stream that died
// long ago cannot distinguish a one-sided gap from a shared jump.
const otherStreamFreshness = 3 * time.Second

// PTSLinearizer maintains monotonic PTS across discontinuities (SRT source
// loops, reconnections) for both video and audio streams from a single source.
//
// Critical invariant: video and audio share a single offset so their PTS
// timelines stay aligned after jumps. When a discontinuity is detected on
// either stream, the offset is computed once (from the first stream to see
// the jump) and applied to both. The second stream recognizes that the jump
// was already handled by checking whether its raw PTS is now consistent with
// the updated offset.
type PTSLinearizer struct {
	video streamState
	audio streamState

	// Shared offset applied to both streams.
	sharedOffset int64

	// jumpGeneration increments each time a PTS discontinuity is handled.
	// Each stream tracks the last generation it saw so the second stream
	// to encounter the same jump skips the offset correction.
	jumpGeneration int64

	// now returns the wall-clock time; overridable in tests. Defaults to
	// time.Now when nil.
	now func() time.Time
}

type streamState struct {
	lastInput   int64
	frameDur    int64
	inited      bool
	lastJumpGen int64     // generation when this stream last applied a jump correction
	lastSeen    time.Time // wall-clock time of the most recent Linearize call
}

// NewPTSLinearizer creates a linearizer for a single SRT source.
func NewPTSLinearizer() *PTSLinearizer {
	return &PTSLinearizer{}
}

// Linearize converts a raw PTS to a monotonic PTS. When a discontinuity
// is detected (backward jump or >0.5s forward jump), the shared offset
// is adjusted once and both streams use it.
func (l *PTSLinearizer) Linearize(rawPTS int64, st StreamType) int64 {
	var ss, other *streamState
	if st == StreamVideo {
		ss, other = &l.video, &l.audio
	} else {
		ss, other = &l.audio, &l.video
	}

	now := time.Now()
	if l.now != nil {
		now = l.now()
	}

	if !ss.inited {
		ss.lastInput = rawPTS
		ss.inited = true
		ss.lastSeen = now
		return (rawPTS + l.sharedOffset) & 0x1FFFFFFFF
	}

	delta := rawPTS - ss.lastInput
	if delta < 0 || delta > ptsJumpThreshold {
		// PTS discontinuity on this stream's raw input.
		if consistentWithOtherStream(rawPTS, other, now) {
			// The new raw PTS lines up with the other stream's continuous
			// timeline: either a one-sided gap on this stream only (SRT loss
			// burst, decode errors/stall) or the tail of a shared jump the
			// other stream already corrected. The shared offset is already
			// right — adjusting it here would shift the other stream's
			// output backward/forward by the gap length (finding #76).
			// Mark this stream as caught up with all handled jumps.
			ss.lastJumpGen = l.jumpGeneration
		} else if ss.lastJumpGen == l.jumpGeneration {
			// This stream is the FIRST to see this jump — compute the correction.
			if ss.frameDur <= 0 {
				ss.frameDur = 3750 // default to 24fps video
			}
			l.sharedOffset += ss.frameDur - delta
			l.jumpGeneration++
			ss.lastJumpGen = l.jumpGeneration
		} else {
			// Another stream already handled a jump — just update our tracking.
			// The shared offset is already correct.
			ss.lastJumpGen = l.jumpGeneration
		}
	} else if delta > 0 {
		ss.frameDur = delta
	}

	ss.lastInput = rawPTS
	ss.lastSeen = now
	return (rawPTS + l.sharedOffset) & 0x1FFFFFFFF
}

// consistentWithOtherStream reports whether rawPTS is close enough to the
// other stream's most recent (and recent enough in wall-clock terms) raw PTS
// to prove the source timeline did not jump. When the other stream is
// uninitialized or stale, it cannot vouch for the timeline and the caller
// falls back to the shared-offset jump correction.
func consistentWithOtherStream(rawPTS int64, other *streamState, now time.Time) bool {
	if !other.inited || now.Sub(other.lastSeen) > otherStreamFreshness {
		return false
	}
	d := rawPTS - other.lastInput
	if d < 0 {
		d = -d
	}
	return d <= crossStreamPTSTolerance
}
