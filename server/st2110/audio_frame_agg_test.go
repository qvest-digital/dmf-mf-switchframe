package st2110

import (
	"bytes"
	"testing"
)

// patternBytes returns n bytes of a deterministic rolling pattern starting at
// offset off, so continuity of the emitted stream can be verified.
func patternBytes(off, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((off + i) % 251) // prime modulus avoids frame-size aliasing
	}
	return b
}

// drainFrames pops all currently-available full frames from the aggregator.
func drainFrames(a *audioFrameAggregator) [][]byte {
	var out [][]byte
	for {
		f := a.Frame()
		if f == nil {
			return out
		}
		cp := make([]byte, len(f))
		copy(cp, f)
		out = append(out, cp)
		a.Consume()
	}
}

// TestAudioFrameAggregatorMismatchedChunks reproduces finding #31: the mixer
// delivers 1024-sample stereo chunks (6144 L24 bytes) while the ST 2110-30
// framebuffer holds exactly 10ms (2880 bytes). Before the aggregator existed,
// PutFrame truncated each chunk to one framebuffer, silently dropping >50% of
// samples and transmitting stale tail bytes for short chunks. The aggregator
// must re-slice the stream losslessly into exact frame-size pieces.
func TestAudioFrameAggregatorMismatchedChunks(t *testing.T) {
	const frameSize = 2880 // 10ms, 48kHz stereo L24
	const chunkSize = 6144 // 1024 stereo sample-frames, L24
	const chunks = 30

	agg := newAudioFrameAggregator(frameSize, 100)

	var emitted []byte
	for i := 0; i < chunks; i++ {
		if dropped := agg.Push(patternBytes(i*chunkSize, chunkSize)); dropped != 0 {
			t.Fatalf("chunk %d: unexpected drop of %d bytes", i, dropped)
		}
		for _, f := range drainFrames(agg) {
			if len(f) != frameSize {
				t.Fatalf("emitted frame of %d bytes, want %d", len(f), frameSize)
			}
			emitted = append(emitted, f...)
		}
	}

	total := chunks * chunkSize
	wantFrames := total / frameSize
	if got := len(emitted) / frameSize; got != wantFrames {
		t.Fatalf("emitted %d frames, want %d", got, wantFrames)
	}
	// Every emitted byte must be the exact contiguous input stream: no stale
	// bytes, no dropped samples, no reordering.
	if !bytes.Equal(emitted, patternBytes(0, len(emitted))) {
		t.Fatal("emitted stream is not a contiguous copy of the input (stale or dropped bytes)")
	}
	if got := agg.PendingBytes(); got != total-len(emitted) {
		t.Fatalf("pending = %d bytes, want %d", got, total-len(emitted))
	}
}

// TestAudioFrameAggregatorSmallChunks verifies chunks smaller than one frame
// are buffered (not padded with stale bytes) until a full frame accumulates.
func TestAudioFrameAggregatorSmallChunks(t *testing.T) {
	const frameSize = 2880
	agg := newAudioFrameAggregator(frameSize, 100)

	agg.Push(patternBytes(0, 1000))
	if f := agg.Frame(); f != nil {
		t.Fatalf("Frame() returned %d bytes with only 1000 pending; want nil", len(f))
	}
	agg.Push(patternBytes(1000, 1000))
	if f := agg.Frame(); f != nil {
		t.Fatal("Frame() returned a frame with only 2000 pending bytes")
	}
	agg.Push(patternBytes(2000, 1000))

	f := agg.Frame()
	if f == nil {
		t.Fatal("Frame() returned nil with 3000 pending bytes")
	}
	if !bytes.Equal(f, patternBytes(0, frameSize)) {
		t.Fatal("frame content does not match input stream")
	}
	agg.Consume()
	if got := agg.PendingBytes(); got != 3000-frameSize {
		t.Fatalf("pending = %d, want %d", got, 3000-frameSize)
	}
}

// TestAudioFrameAggregatorExactFrames verifies the pass-through case.
func TestAudioFrameAggregatorExactFrames(t *testing.T) {
	const frameSize = 2880
	agg := newAudioFrameAggregator(frameSize, 100)

	agg.Push(patternBytes(0, frameSize))
	frames := drainFrames(agg)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], patternBytes(0, frameSize)) {
		t.Fatal("frame content mismatch")
	}
	if agg.PendingBytes() != 0 {
		t.Fatalf("pending = %d, want 0", agg.PendingBytes())
	}
}

// TestAudioFrameAggregatorOverflowDropsWholeFrames verifies that when the TX
// stalls (frames not consumed), the pending buffer stays bounded and data is
// dropped in whole-frame units so sample/channel alignment is preserved.
func TestAudioFrameAggregatorOverflowDropsWholeFrames(t *testing.T) {
	const frameSize = 100
	const maxFrames = 3
	agg := newAudioFrameAggregator(frameSize, maxFrames)

	off := 0
	totalDropped := 0
	for i := 0; i < 10; i++ {
		totalDropped += agg.Push(patternBytes(off, 70)) // not frame-aligned input
		off += 70
	}

	if got := agg.PendingBytes(); got > maxFrames*frameSize {
		t.Fatalf("pending = %d bytes, want <= %d", got, maxFrames*frameSize)
	}
	if totalDropped == 0 {
		t.Fatal("expected overflow to drop bytes")
	}
	if totalDropped%frameSize != 0 {
		t.Fatalf("dropped %d bytes; must be a multiple of frameSize %d to keep alignment", totalDropped, frameSize)
	}
	// Remaining stream must be a contiguous suffix of the input starting at
	// the drop point.
	f := agg.Frame()
	if f == nil {
		t.Fatal("expected at least one full frame pending after overflow")
	}
	if !bytes.Equal(f, patternBytes(totalDropped, frameSize)) {
		t.Fatal("post-overflow frame is not contiguous with the drop point")
	}
}
