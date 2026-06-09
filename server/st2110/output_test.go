package st2110

import (
	"sync"
	"testing"
	"time"
)

// mockVideoTx captures frames submitted via PutFrame for test assertions.
type mockVideoTx struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool
}

func (m *mockVideoTx) PutFrame(yuv []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(yuv))
	copy(cp, yuv)
	m.frames = append(m.frames, cp)
	return nil
}

func (m *mockVideoTx) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockVideoTx) getFrames() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.frames))
	copy(out, m.frames)
	return out
}

// mockAudioTx captures PCM frames submitted via PutFrame for test assertions.
type mockAudioTx struct {
	mu     sync.Mutex
	frames [][]float32
	closed bool
}

func (m *mockAudioTx) PutFrame(pcm []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]float32, len(pcm))
	copy(cp, pcm)
	m.frames = append(m.frames, cp)
	return nil
}

func (m *mockAudioTx) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockAudioTx) getFrames() [][]float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]float32, len(m.frames))
	copy(out, m.frames)
	return out
}

// mockAncTx captures ancillary data submitted via PutFrame.
type mockAncTx struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool
}

func (m *mockAncTx) PutFrame(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.frames = append(m.frames, cp)
	return nil
}

func (m *mockAncTx) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func TestOutput_WriteVideo(t *testing.T) {
	vtx := &mockVideoTx{}
	atx := &mockAudioTx{}
	anc := &mockAncTx{}

	// Use a high FPS so the ticker fires quickly in the test.
	out := NewOutput(OutputConfig{
		FPSNum: 1000,
		FPSDen: 1,
	})
	out.Start(vtx, atx, anc)

	// Submit a small YUV420p frame (4x2 = 12 bytes Y + 4 bytes U + 4 bytes V = 20 bytes, but
	// for simplicity we just send a recognizable byte pattern).
	frame := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	out.WriteVideo(frame, 4, 2, 1000)

	// Wait for ticker to pick it up.
	deadline := time.After(500 * time.Millisecond)
	for {
		frames := vtx.getFrames()
		if len(frames) > 0 {
			// Verify the frame content matches what we submitted.
			if len(frames[0]) != len(frame) {
				t.Fatalf("frame length mismatch: got %d, want %d", len(frames[0]), len(frame))
			}
			for i, b := range frames[0] {
				if b != frame[i] {
					t.Fatalf("frame byte %d mismatch: got %x, want %x", i, b, frame[i])
				}
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for video frame to be delivered to transmitter")
		default:
			time.Sleep(1 * time.Millisecond)
		}
	}

	out.Stop()
}

func TestOutput_WriteVideoDimensionGuard(t *testing.T) {
	vtx := &mockVideoTx{}

	const w, h = 8, 4
	frameSize := w * h * 3 / 2 // YUV420p

	out := NewOutput(OutputConfig{
		FPSNum: 1000,
		FPSDen: 1,
		Width:  w,
		Height: h,
	})
	out.Start(vtx, nil, nil)
	defer out.Stop()

	// Mismatched dimensions must be dropped (would otherwise feed a
	// wrong-size buffer to the fixed-size TX converter -> OOB/corruption).
	out.WriteVideo(make([]byte, (w/2)*(h/2)*3/2), w/2, h/2, 1)

	// Give the ticker time to fire; nothing should be delivered.
	deadline := time.After(200 * time.Millisecond)
	for {
		if len(vtx.getFrames()) != 0 {
			t.Fatal("mismatched-dimension frame was not dropped")
		}
		select {
		case <-deadline:
			goto matching
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}

matching:
	// A frame at the configured resolution must be delivered.
	out.WriteVideo(make([]byte, frameSize), w, h, 2)

	got := time.After(500 * time.Millisecond)
	for {
		frames := vtx.getFrames()
		if len(frames) > 0 {
			if len(frames[0]) != frameSize {
				t.Fatalf("delivered frame size: got %d, want %d", len(frames[0]), frameSize)
			}
			return
		}
		select {
		case <-got:
			t.Fatal("matching-dimension frame was not delivered")
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
}

func TestOutput_WriteAudio(t *testing.T) {
	vtx := &mockVideoTx{}
	atx := &mockAudioTx{}
	anc := &mockAncTx{}

	out := NewOutput(OutputConfig{})
	out.Start(vtx, atx, anc)

	// Submit PCM audio — should be delivered immediately (no ticker).
	pcm := []float32{0.1, 0.2, 0.3, 0.4}
	out.WriteAudio(pcm, 1000, 48000, 2)

	// Audio is synchronous, so check immediately.
	frames := atx.getFrames()
	if len(frames) != 1 {
		t.Fatalf("expected 1 audio frame, got %d", len(frames))
	}
	if len(frames[0]) != len(pcm) {
		t.Fatalf("audio frame length mismatch: got %d, want %d", len(frames[0]), len(pcm))
	}
	for i, v := range frames[0] {
		if v != pcm[i] {
			t.Fatalf("audio sample %d mismatch: got %f, want %f", i, v, pcm[i])
		}
	}

	out.Stop()
}

func TestOutput_WriteAncillary(t *testing.T) {
	vtx := &mockVideoTx{}
	atx := &mockAudioTx{}
	anc := &mockAncTx{}

	out := NewOutput(OutputConfig{})
	out.Start(vtx, atx, anc)

	data := []byte{0x01, 0x02, 0x03}
	out.WriteAncillary(data)

	// Ancillary is synchronous, check immediately.
	// Must release lock before Stop() which calls Close() on the mock.
	func() {
		anc.mu.Lock()
		defer anc.mu.Unlock()
		if len(anc.frames) != 1 {
			t.Fatalf("expected 1 ancillary frame, got %d", len(anc.frames))
		}
		if len(anc.frames[0]) != len(data) {
			t.Fatalf("ancillary frame length mismatch: got %d, want %d", len(anc.frames[0]), len(data))
		}
		for i, b := range anc.frames[0] {
			if b != data[i] {
				t.Fatalf("ancillary byte %d mismatch: got %x, want %x", i, b, data[i])
			}
		}
	}()

	out.Stop()
}

func TestOutput_StopIdempotent(t *testing.T) {
	vtx := &mockVideoTx{}
	atx := &mockAudioTx{}
	anc := &mockAncTx{}

	out := NewOutput(OutputConfig{})
	out.Start(vtx, atx, anc)

	// Stop twice — must not panic or deadlock.
	out.Stop()
	out.Stop()

	// Verify transmitters are closed.
	vtx.mu.Lock()
	defer vtx.mu.Unlock()
	if !vtx.closed {
		t.Fatal("video transmitter not closed")
	}

	atx.mu.Lock()
	defer atx.mu.Unlock()
	if !atx.closed {
		t.Fatal("audio transmitter not closed")
	}

	anc.mu.Lock()
	defer anc.mu.Unlock()
	if !anc.closed {
		t.Fatal("ancillary transmitter not closed")
	}
}

func TestOutput_WriteAfterStop(t *testing.T) {
	vtx := &mockVideoTx{}
	atx := &mockAudioTx{}
	anc := &mockAncTx{}

	out := NewOutput(OutputConfig{})
	out.Start(vtx, atx, anc)
	out.Stop()

	// These should not panic — just silently drop.
	out.WriteVideo([]byte{0x01}, 1, 1, 0)
	out.WriteAudio([]float32{0.5}, 0, 48000, 1)
	out.WriteAncillary([]byte{0x02})
}
