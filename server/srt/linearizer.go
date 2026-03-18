package srt

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
}

type streamState struct {
	lastInput      int64
	frameDur       int64
	inited         bool
	lastJumpGen    int64 // generation when this stream last applied a jump correction
}

// NewPTSLinearizer creates a linearizer for a single SRT source.
func NewPTSLinearizer() *PTSLinearizer {
	return &PTSLinearizer{}
}

// Linearize converts a raw PTS to a monotonic PTS. When a discontinuity
// is detected (backward jump or >0.5s forward jump), the shared offset
// is adjusted once and both streams use it.
func (l *PTSLinearizer) Linearize(rawPTS int64, st StreamType) int64 {
	var ss *streamState
	if st == StreamVideo {
		ss = &l.video
	} else {
		ss = &l.audio
	}

	if !ss.inited {
		ss.lastInput = rawPTS
		ss.inited = true
		return (rawPTS + l.sharedOffset) & 0x1FFFFFFFF
	}

	delta := rawPTS - ss.lastInput
	if delta < 0 || delta > ptsJumpThreshold {
		// PTS discontinuity. Check if another stream already handled this jump.
		if ss.lastJumpGen == l.jumpGeneration {
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
	return (rawPTS + l.sharedOffset) & 0x1FFFFFFFF
}
