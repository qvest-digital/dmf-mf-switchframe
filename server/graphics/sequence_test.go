package graphics

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
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
	// Without loop frames, sequence should hold last in-frame (static bug).
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	pngData := makePNG(t, 4, 4, 1, 1, white)

	files := map[string][]byte{
		"in/001.png": pngData,
		"in/002.png": pngData,
	}
	zipData := buildZIP(t, files)

	seq, err := LoadSequenceZIP(zipData)
	if err != nil {
		t.Fatalf("expected success for in-only sequence, got: %v", err)
	}
	if len(seq.inFrames) != 2 {
		t.Fatalf("expected 2 in frames, got %d", len(seq.inFrames))
	}
	if len(seq.loopFrames) != 0 {
		t.Fatalf("expected 0 loop frames, got %d", len(seq.loopFrames))
	}

	// Verify hold behaviour
	seq.startIn()
	seq.advance()
	seq.advance()
	if seq.phase != seqPhaseHold {
		t.Fatalf("expected hold phase, got %v", seq.phase)
	}
	if seq.holdFrame() == nil {
		t.Fatal("holdFrame should return last in-frame")
	}
	if seq.currentFrame() == nil {
		t.Fatal("currentFrame during hold should return last in-frame")
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

func TestLoadSequenceZIPLargeFrameSmallCrop(t *testing.T) {
	// Simulates a network bug: large source (128x128) but tiny visible region.
	// At source dimensions, 200 frames × 128×128×4 = 12.5MB (under limit).
	// But the key point is the memory check uses CROPPED dimensions, not source.
	// A 1920x1080 source with 1322 frames would be 10.2GB at source dims but
	// only ~62MB cropped. This test verifies that works.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Create 50 "large" frames with a tiny visible region at (100,100).
	for i := 0; i < 50; i++ {
		img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
		img.SetNRGBA(100, 100, color.NRGBA{R: 255, G: uint8(i), A: 255})
		name := fmt.Sprintf("frame_%04d.png", i+1)
		w, _ := zw.Create(name)
		_ = png.Encode(w, img)
	}
	_ = zw.Close()

	seq, err := LoadSequenceZIP(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	// Crop should be around (100,100), NOT the full 128x128.
	if seq.cropW >= 128 || seq.cropH >= 128 {
		t.Fatalf("expected small crop, got %dx%d", seq.cropW, seq.cropH)
	}
	if seq.cropX != 100 || seq.cropY != 100 {
		t.Fatalf("expected crop at (100,100), got (%d,%d)", seq.cropX, seq.cropY)
	}

	// Each frame should be tiny (cropW * cropH * 4).
	expectedSize := seq.cropW * seq.cropH * 4
	if len(seq.loopFrames[0]) != expectedSize {
		t.Fatalf("frame size %d, expected %d", len(seq.loopFrames[0]), expectedSize)
	}
}

func TestLoadSequenceZIPMacOSResourceForks(t *testing.T) {
	// macOS Finder adds __MACOSX/._foo.png entries to ZIPs.
	// These should be silently skipped.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Real PNG
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.SetNRGBA(1, 1, color.NRGBA{R: 255, A: 255})
	w, _ := zw.Create("frame_0001.png")
	_ = png.Encode(w, img)

	// macOS resource fork (would fail PNG decode)
	w2, _ := zw.Create("__MACOSX/._frame_0001.png")
	_, _ = w2.Write([]byte("not a valid png"))

	// Another resource fork pattern
	w3, _ := zw.Create("__MACOSX/in/._frame_0001.png")
	_, _ = w3.Write([]byte("also not valid"))

	_ = zw.Close()

	seq, err := LoadSequenceZIP(buf.Bytes())
	if err != nil {
		t.Fatalf("expected success (skip resource forks), got: %v", err)
	}
	if len(seq.loopFrames) != 1 {
		t.Fatalf("expected 1 loop frame, got %d", len(seq.loopFrames))
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

func TestSequenceProcessYUVAdvancement(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	id, _ := c.AddLayer()

	// Install a sequence with 2 loop frames (2x2 RGBA)
	frame1 := make([]byte, 2*2*4)
	frame1[3] = 255 // pixel at (0,0) opaque
	frame2 := make([]byte, 2*2*4)
	frame2[7] = 255 // pixel at (1,0) opaque

	c.mu.Lock()
	layer := c.layers[id]
	layer.seq = &sequenceState{
		loopFrames: [][]byte{frame1, frame2},
		cropX:      0, cropY: 0, cropW: 2, cropH: 2,
		phase: seqPhaseLoop, frameIdx: 0,
	}
	layer.overlay = frame1
	layer.overlayWidth = 2
	layer.overlayHeight = 2
	layer.rect = image.Rect(0, 0, 2, 2)
	layer.active = true
	layer.fadePosition = 1.0
	c.mu.Unlock()

	yuv := make([]byte, 4*4*3/2) // 4x4 min for YUV420

	// First ProcessYUV: renders frame 0, advances to 1
	c.ProcessYUV(yuv, 4, 4, nil, nil)

	c.mu.RLock()
	idx := layer.seq.frameIdx
	c.mu.RUnlock()
	if idx != 1 {
		t.Fatalf("expected frameIdx 1, got %d", idx)
	}

	// Second ProcessYUV: renders frame 1, wraps to 0
	c.ProcessYUV(yuv, 4, 4, nil, nil)

	c.mu.RLock()
	idx = layer.seq.frameIdx
	c.mu.RUnlock()
	if idx != 0 {
		t.Fatalf("expected frameIdx 0 after wrap, got %d", idx)
	}
}

func TestSequenceOutDeactivatesLayer(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetPipelineFPS(30000, 1001)

	id, _ := c.AddLayer()

	outFrame := make([]byte, 2*2*4)
	outFrame[3] = 255

	c.mu.Lock()
	layer := c.layers[id]
	layer.seq = &sequenceState{
		loopFrames: [][]byte{outFrame},
		outFrames:  [][]byte{outFrame},
		cropX:      0, cropY: 0, cropW: 2, cropH: 2,
		phase: seqPhaseOut, frameIdx: 0,
	}
	layer.overlay = outFrame
	layer.overlayWidth = 2
	layer.overlayHeight = 2
	layer.rect = image.Rect(0, 0, 2, 2)
	layer.active = true
	layer.fadePosition = 1.0
	c.mu.Unlock()

	yuv := make([]byte, 4*4*3/2)
	c.ProcessYUV(yuv, 4, 4, nil, nil)

	c.mu.RLock()
	active := layer.active
	phase := layer.seq.phase
	c.mu.RUnlock()

	if active {
		t.Fatal("layer should be deactivated after out phase completes")
	}
	if phase != seqPhaseDone {
		t.Fatalf("expected done, got %v", phase)
	}
}
