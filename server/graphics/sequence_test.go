package graphics

import (
	"testing"
)

// helper to create a fake RGBA frame with a distinguishing byte.
func fakeFrame(tag byte, size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = tag
	}
	return b
}

func TestSequenceState_InLoopOut(t *testing.T) {
	s := &sequenceState{
		inFrames:   [][]byte{fakeFrame(1, 4), fakeFrame(2, 4)},
		loopFrames: [][]byte{fakeFrame(10, 4), fakeFrame(11, 4), fakeFrame(12, 4)},
		outFrames:  [][]byte{fakeFrame(20, 4), fakeFrame(21, 4)},
		cropW:      1, cropH: 1,
	}

	// starts idle
	if s.phase != seqPhaseIdle {
		t.Fatalf("expected idle, got %d", s.phase)
	}
	if f := s.currentFrame(); f != nil {
		t.Fatal("idle should return nil frame")
	}

	// start in
	s.startIn()
	if s.phase != seqPhaseIn {
		t.Fatalf("expected in, got %d", s.phase)
	}
	if s.frameIdx != 0 {
		t.Fatalf("expected frameIdx 0, got %d", s.frameIdx)
	}
	// first in frame
	f := s.currentFrame()
	if f == nil || f[0] != 1 {
		t.Fatalf("expected in frame 0 (tag=1), got %v", f)
	}

	// advance through in
	s.advance()
	if s.phase != seqPhaseIn {
		t.Fatalf("expected still in, got %d", s.phase)
	}
	f = s.currentFrame()
	if f == nil || f[0] != 2 {
		t.Fatalf("expected in frame 1 (tag=2), got %v", f)
	}

	// advance past in → loop
	s.advance()
	if s.phase != seqPhaseLoop {
		t.Fatalf("expected loop after in, got %d", s.phase)
	}
	if s.frameIdx != 0 {
		t.Fatalf("expected frameIdx 0, got %d", s.frameIdx)
	}
	f = s.currentFrame()
	if f == nil || f[0] != 10 {
		t.Fatalf("expected loop frame 0 (tag=10), got %v", f)
	}

	// advance through loop
	s.advance()
	f = s.currentFrame()
	if f == nil || f[0] != 11 {
		t.Fatalf("expected loop frame 1 (tag=11), got %v", f)
	}
	s.advance()
	f = s.currentFrame()
	if f == nil || f[0] != 12 {
		t.Fatalf("expected loop frame 2 (tag=12), got %v", f)
	}

	// loop wraps
	s.advance()
	if s.phase != seqPhaseLoop {
		t.Fatalf("expected still loop, got %d", s.phase)
	}
	if s.frameIdx != 0 {
		t.Fatalf("expected frameIdx 0 after wrap, got %d", s.frameIdx)
	}
	f = s.currentFrame()
	if f == nil || f[0] != 10 {
		t.Fatalf("expected loop frame 0 again (tag=10), got %v", f)
	}

	// start out
	s.startOut()
	if s.phase != seqPhaseOut {
		t.Fatalf("expected out, got %d", s.phase)
	}
	if s.frameIdx != 0 {
		t.Fatalf("expected frameIdx 0, got %d", s.frameIdx)
	}
	f = s.currentFrame()
	if f == nil || f[0] != 20 {
		t.Fatalf("expected out frame 0 (tag=20), got %v", f)
	}

	s.advance()
	f = s.currentFrame()
	if f == nil || f[0] != 21 {
		t.Fatalf("expected out frame 1 (tag=21), got %v", f)
	}

	// advance past out → done
	s.advance()
	if s.phase != seqPhaseDone {
		t.Fatalf("expected done after out, got %d", s.phase)
	}
	if f := s.currentFrame(); f != nil {
		t.Fatal("done should return nil frame")
	}
}

func TestSequenceState_LoopWraps(t *testing.T) {
	s := &sequenceState{
		loopFrames: [][]byte{fakeFrame(1, 4), fakeFrame(2, 4)},
		cropW:      1, cropH: 1,
	}
	s.startIn() // no in frames → loop
	if s.phase != seqPhaseLoop {
		t.Fatalf("expected loop, got %d", s.phase)
	}

	// Wrap multiple times
	for i := 0; i < 7; i++ {
		s.advance()
	}
	// 7 advances on 2-frame loop: frame index should be (7 % 2) = 1
	if s.phase != seqPhaseLoop {
		t.Fatalf("expected still loop, got %d", s.phase)
	}
	if s.frameIdx != 1 {
		t.Fatalf("expected frameIdx 1, got %d", s.frameIdx)
	}
}

func TestSequenceState_NoInFrames(t *testing.T) {
	s := &sequenceState{
		loopFrames: [][]byte{fakeFrame(10, 4)},
		outFrames:  [][]byte{fakeFrame(20, 4)},
		cropW:      1, cropH: 1,
	}

	// startIn with no in frames goes straight to loop
	s.startIn()
	if s.phase != seqPhaseLoop {
		t.Fatalf("expected loop (no in frames), got %d", s.phase)
	}
	f := s.currentFrame()
	if f == nil || f[0] != 10 {
		t.Fatalf("expected loop frame 0 (tag=10), got %v", f)
	}
}

func TestSequenceState_NoOutFrames(t *testing.T) {
	s := &sequenceState{
		loopFrames: [][]byte{fakeFrame(10, 4)},
		cropW:      1, cropH: 1,
	}

	s.startIn()
	if s.phase != seqPhaseLoop {
		t.Fatalf("expected loop, got %d", s.phase)
	}

	// startOut with no out frames goes straight to done
	s.startOut()
	if s.phase != seqPhaseDone {
		t.Fatalf("expected done (no out frames), got %d", s.phase)
	}
	if f := s.currentFrame(); f != nil {
		t.Fatal("done should return nil")
	}
}

func TestSequenceState_CutOn(t *testing.T) {
	s := &sequenceState{
		inFrames:   [][]byte{fakeFrame(1, 4), fakeFrame(2, 4)},
		loopFrames: [][]byte{fakeFrame(10, 4), fakeFrame(11, 4)},
		outFrames:  [][]byte{fakeFrame(20, 4)},
		cropW:      1, cropH: 1,
	}

	// Cut on skips in, starts at loop directly
	s.startLoop()
	if s.phase != seqPhaseLoop {
		t.Fatalf("expected loop, got %d", s.phase)
	}
	if s.frameIdx != 0 {
		t.Fatalf("expected frameIdx 0, got %d", s.frameIdx)
	}
	f := s.currentFrame()
	if f == nil || f[0] != 10 {
		t.Fatalf("expected loop frame 0 (tag=10), got %v", f)
	}
}

func TestSequenceState_AdvanceIdleNoop(t *testing.T) {
	s := &sequenceState{
		loopFrames: [][]byte{fakeFrame(1, 4)},
		cropW:      1, cropH: 1,
	}
	// advance while idle should be a no-op
	s.advance()
	if s.phase != seqPhaseIdle {
		t.Fatalf("expected idle after noop advance, got %d", s.phase)
	}
}

func TestSequenceState_AdvanceDoneNoop(t *testing.T) {
	s := &sequenceState{
		loopFrames: [][]byte{fakeFrame(1, 4)},
		cropW:      1, cropH: 1,
	}
	s.startIn()
	s.startOut() // no out frames → done
	if s.phase != seqPhaseDone {
		t.Fatalf("expected done, got %d", s.phase)
	}
	s.advance()
	if s.phase != seqPhaseDone {
		t.Fatalf("expected still done after noop advance, got %d", s.phase)
	}
}

func TestSequenceState_CurrentFramePerPhase(t *testing.T) {
	s := &sequenceState{
		inFrames:   [][]byte{fakeFrame(1, 4)},
		loopFrames: [][]byte{fakeFrame(10, 4)},
		outFrames:  [][]byte{fakeFrame(20, 4)},
		cropW:      1, cropH: 1,
	}

	// idle → nil
	if f := s.currentFrame(); f != nil {
		t.Fatal("idle: expected nil")
	}

	// in → in frame
	s.startIn()
	f := s.currentFrame()
	if f == nil || f[0] != 1 {
		t.Fatalf("in: expected tag=1, got %v", f)
	}

	// advance in→loop
	s.advance()
	f = s.currentFrame()
	if f == nil || f[0] != 10 {
		t.Fatalf("loop: expected tag=10, got %v", f)
	}

	// out
	s.startOut()
	f = s.currentFrame()
	if f == nil || f[0] != 20 {
		t.Fatalf("out: expected tag=20, got %v", f)
	}

	// advance out→done
	s.advance()
	if f := s.currentFrame(); f != nil {
		t.Fatal("done: expected nil")
	}
}
