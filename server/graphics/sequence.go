package graphics

// seqPhase represents the current phase of an animated PNG sequence.
type seqPhase int

const (
	seqPhaseIdle seqPhase = iota // not playing
	seqPhaseIn                   // playing animate-in segment
	seqPhaseLoop                 // playing loop segment (wraps)
	seqPhaseOut                  // playing animate-out segment
	seqPhaseDone                 // finished playing
)

// sequenceState holds the frame data and playback state for an animated PNG
// sequence on a graphics layer. It is advanced once per ProcessYUV call,
// giving frame-locked playback at the pipeline frame rate.
type sequenceState struct {
	inFrames   [][]byte // cropped RGBA frames for animate-in
	loopFrames [][]byte // cropped RGBA frames for loop
	outFrames  [][]byte // cropped RGBA frames for animate-out

	cropX, cropY int // offset within program frame
	cropW, cropH int // cropped frame dimensions

	phase    seqPhase
	frameIdx int
}

// startIn begins playback from the animate-in segment. If there are no in
// frames, it transitions directly to the loop phase.
func (s *sequenceState) startIn() {
	if len(s.inFrames) > 0 {
		s.phase = seqPhaseIn
		s.frameIdx = 0
		return
	}
	s.startLoop()
}

// startLoop begins playback from the loop segment directly, skipping any
// animate-in. This is used for "cut on" behaviour.
func (s *sequenceState) startLoop() {
	s.phase = seqPhaseLoop
	s.frameIdx = 0
}

// startOut begins playback of the animate-out segment. If there are no out
// frames, it transitions directly to the done phase.
func (s *sequenceState) startOut() {
	if len(s.outFrames) > 0 {
		s.phase = seqPhaseOut
		s.frameIdx = 0
		return
	}
	s.phase = seqPhaseDone
}

// advance moves to the next frame within the current phase. Phase transitions
// are handled automatically: in→loop, loop wraps, out→done.
func (s *sequenceState) advance() {
	switch s.phase {
	case seqPhaseIn:
		s.frameIdx++
		if s.frameIdx >= len(s.inFrames) {
			s.startLoop()
		}
	case seqPhaseLoop:
		s.frameIdx++
		if s.frameIdx >= len(s.loopFrames) {
			s.frameIdx = 0
		}
	case seqPhaseOut:
		s.frameIdx++
		if s.frameIdx >= len(s.outFrames) {
			s.phase = seqPhaseDone
			s.frameIdx = 0
		}
	case seqPhaseIdle, seqPhaseDone:
		// no-op
	}
}

// currentFrame returns the RGBA pixel data for the current frame, or nil if
// the sequence is idle or done.
func (s *sequenceState) currentFrame() []byte {
	switch s.phase {
	case seqPhaseIn:
		if s.frameIdx < len(s.inFrames) {
			return s.inFrames[s.frameIdx]
		}
	case seqPhaseLoop:
		if s.frameIdx < len(s.loopFrames) {
			return s.loopFrames[s.frameIdx]
		}
	case seqPhaseOut:
		if s.frameIdx < len(s.outFrames) {
			return s.outFrames[s.frameIdx]
		}
	}
	return nil
}
