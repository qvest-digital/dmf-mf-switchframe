package graphics

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
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

// --- ZIP loading tests ---

// makePNG creates a small PNG image with a single non-transparent pixel at (px, py).
func makePNG(t *testing.T, w, h, px, py int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	// all pixels start as transparent (zero value)
	img.SetNRGBA(px, py, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// buildZIP creates a ZIP archive from a map of filename→content.
func buildZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestLoadSequenceZIP(t *testing.T) {
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	// All 4x4 images with a non-transparent pixel at (1,1).
	pngData := makePNG(t, 4, 4, 1, 1, white)

	files := map[string][]byte{
		"in/001.png":  pngData,
		"in/002.png":  pngData,
		"loop001.png": pngData,
		"loop002.png": pngData,
		"loop003.png": pngData,
		"out/001.png": pngData,
	}
	zipData := buildZIP(t, files)

	seq, err := LoadSequenceZIP(zipData)
	if err != nil {
		t.Fatalf("LoadSequenceZIP: %v", err)
	}

	// frame counts
	if len(seq.inFrames) != 2 {
		t.Errorf("in frames: got %d, want 2", len(seq.inFrames))
	}
	if len(seq.loopFrames) != 3 {
		t.Errorf("loop frames: got %d, want 3", len(seq.loopFrames))
	}
	if len(seq.outFrames) != 1 {
		t.Errorf("out frames: got %d, want 1", len(seq.outFrames))
	}

	// crop position should be even-aligned
	if seq.cropX%2 != 0 {
		t.Errorf("cropX %d not even", seq.cropX)
	}
	if seq.cropY%2 != 0 {
		t.Errorf("cropY %d not even", seq.cropY)
	}
	if seq.cropW%2 != 0 {
		t.Errorf("cropW %d not even", seq.cropW)
	}
	if seq.cropH%2 != 0 {
		t.Errorf("cropH %d not even", seq.cropH)
	}

	// crop must contain pixel (1,1) — at least cropX <= 1, cropY <= 1
	if seq.cropX > 1 || seq.cropY > 1 {
		t.Errorf("crop origin (%d,%d) does not contain pixel (1,1)", seq.cropX, seq.cropY)
	}

	// minimum 2x2
	if seq.cropW < 2 || seq.cropH < 2 {
		t.Errorf("crop size %dx%d less than 2x2", seq.cropW, seq.cropH)
	}

	// frame data size matches crop
	expectedSize := seq.cropW * seq.cropH * 4
	for i, f := range seq.loopFrames {
		if len(f) != expectedSize {
			t.Errorf("loop frame %d: size %d, want %d", i, len(f), expectedSize)
		}
	}

	// phase should be idle
	if seq.phase != seqPhaseIdle {
		t.Errorf("expected idle phase, got %d", seq.phase)
	}
}

func TestLoadSequenceZIPNoLoop(t *testing.T) {
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	pngData := makePNG(t, 4, 4, 1, 1, white)

	files := map[string][]byte{
		"in/001.png": pngData,
		"in/002.png": pngData,
	}
	zipData := buildZIP(t, files)

	_, err := LoadSequenceZIP(zipData)
	if !errors.Is(err, ErrSequenceNoLoopFrames) {
		t.Fatalf("expected ErrSequenceNoLoopFrames, got: %v", err)
	}
}

func TestLoadSequenceZIPEmpty(t *testing.T) {
	// ZIP with no PNG files at all
	files := map[string][]byte{
		"readme.txt": []byte("not a png"),
	}
	zipData := buildZIP(t, files)

	_, err := LoadSequenceZIP(zipData)
	if !errors.Is(err, ErrSequenceEmpty) {
		t.Fatalf("expected ErrSequenceEmpty, got: %v", err)
	}
}

func TestLoadSequenceZIPSizeMismatch(t *testing.T) {
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	png4 := makePNG(t, 4, 4, 1, 1, white)
	png8 := makePNG(t, 8, 8, 1, 1, white)

	files := map[string][]byte{
		"loop001.png": png4,
		"loop002.png": png8,
	}
	zipData := buildZIP(t, files)

	_, err := LoadSequenceZIP(zipData)
	if !errors.Is(err, ErrSequenceSizeMismatch) {
		t.Fatalf("expected ErrSequenceSizeMismatch, got: %v", err)
	}
}

func TestLoadSequenceZIPMemoryLimit(t *testing.T) {
	// Just verify the constant is reasonable (512 MB)
	if maxSequenceMemory != 512<<20 {
		t.Fatalf("maxSequenceMemory: got %d, want %d", maxSequenceMemory, 512<<20)
	}
	if maxSequenceFrames != 7200 {
		t.Fatalf("maxSequenceFrames: got %d, want %d", maxSequenceFrames, 7200)
	}
}

func TestFrameBBox(t *testing.T) {
	// 8x8 image with non-transparent pixels at (2,3) and (5,6)
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	img.SetNRGBA(2, 3, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(5, 6, color.NRGBA{G: 255, A: 128})

	r := frameBBox(img)
	if r.Min.X != 2 || r.Min.Y != 3 {
		t.Errorf("bbox min: got (%d,%d), want (2,3)", r.Min.X, r.Min.Y)
	}
	// Max is exclusive: (5+1, 6+1)
	if r.Max.X != 6 || r.Max.Y != 7 {
		t.Errorf("bbox max: got (%d,%d), want (6,7)", r.Max.X, r.Max.Y)
	}
}

func TestFrameBBoxEmpty(t *testing.T) {
	// Fully transparent image
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	r := frameBBox(img)
	if !r.Empty() {
		t.Errorf("expected empty rect for fully transparent image, got %v", r)
	}
}
