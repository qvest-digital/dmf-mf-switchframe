package comms

import (
	"testing"
)

func TestParticipantNew(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	p, err := newParticipant("op1", "Alice")
	if err != nil {
		t.Fatalf("newParticipant: %v", err)
	}
	defer p.close()

	if p.id != "op1" {
		t.Errorf("id = %q, want %q", p.id, "op1")
	}
	if p.name != "Alice" {
		t.Errorf("name = %q, want %q", p.name, "Alice")
	}
	if p.muted {
		t.Error("new participant should not be muted")
	}
}

func TestParticipantMuteUnmute(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	p, err := newParticipant("op1", "Alice")
	if err != nil {
		t.Fatalf("newParticipant: %v", err)
	}
	defer p.close()

	p.setMuted(true)
	info := p.info()
	if !info.Muted {
		t.Error("expected muted after setMuted(true)")
	}

	p.setMuted(false)
	info = p.info()
	if info.Muted {
		t.Error("expected not muted after setMuted(false)")
	}
}

func TestParticipantDecodeEncode(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	p, err := newParticipant("op1", "Alice")
	if err != nil {
		t.Fatalf("newParticipant: %v", err)
	}
	defer p.close()

	// Encode silence.
	silence := make([]int16, FrameSize)
	buf := make([]byte, 1024)
	n, err := p.encoder.Encode(silence, FrameSize, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	opusData := buf[:n]

	// Decode back.
	pcm, err := p.decodeAudio(opusData)
	if err != nil {
		t.Fatalf("decodeAudio: %v", err)
	}
	if len(pcm) != FrameSize {
		t.Errorf("decoded %d samples, want %d", len(pcm), FrameSize)
	}
}

// TestParticipantDecodeOversizedFrame verifies that an Opus frame longer than
// 20ms (here 40ms = 2*FrameSize) decodes instead of being dropped with
// OPUS_BUFFER_TOO_SMALL, and that ALL decoded samples are preserved:
// decodeAudio splits the 40ms result into two consecutive FrameSize blocks
// on pcmQ rather than discarding the trailing half. Regression for both the
// silently-brittle non-20ms-frame case and the 50% audio-loss truncation bug.
func TestParticipantDecodeOversizedFrame(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	p, err := newParticipant("op1", "Alice")
	if err != nil {
		t.Fatalf("newParticipant: %v", err)
	}
	defer p.close()

	// Encode a 40ms frame. The hraban encoder derives the frame duration from
	// the number of PCM samples supplied, so 2*FrameSize samples yields a
	// 40ms Opus frame — a valid duration a browser encoder might negotiate.
	// Use a non-silent ramp so the two decoded halves are distinguishable.
	pcm40ms := make([]int16, 2*FrameSize)
	for i := range pcm40ms {
		pcm40ms[i] = int16(i % 8000)
	}
	buf := make([]byte, 4096)
	n, err := p.encoder.Encode(pcm40ms, 2*FrameSize, buf)
	if err != nil {
		t.Fatalf("Encode 40ms frame: %v", err)
	}

	out, err := p.decodeAudio(buf[:n])
	if err != nil {
		t.Fatalf("decodeAudio of 40ms frame returned error (frame dropped): %v", err)
	}
	// All decoded samples are returned for speaking detection.
	if len(out) != 2*FrameSize {
		t.Fatalf("decodeAudio returned %d samples, want %d (full 40ms frame)", len(out), 2*FrameSize)
	}

	// The full frame is queued as two consecutive canonical 20ms blocks whose
	// concatenation equals the decoded PCM — nothing is discarded.
	for blockIdx := 0; blockIdx < 2; blockIdx++ {
		block := p.consumePCM()
		if block == nil {
			t.Fatalf("pcmQ block %d missing: 40ms frame must queue 2 blocks", blockIdx)
		}
		if len(block) != FrameSize {
			t.Fatalf("pcmQ block %d has %d samples, want %d", blockIdx, len(block), FrameSize)
		}
		for i, s := range block {
			if want := out[blockIdx*FrameSize+i]; s != want {
				t.Fatalf("pcmQ block %d sample %d = %d, want %d (samples not preserved in order)",
					blockIdx, i, s, want)
			}
		}
	}
	if extra := p.consumePCM(); extra != nil {
		t.Errorf("pcmQ has unexpected extra block of %d samples after 2 blocks", len(extra))
	}
}

func TestParticipantSpeakingDetection(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	p, err := newParticipant("op1", "Alice")
	if err != nil {
		t.Fatalf("newParticipant: %v", err)
	}
	defer p.close()

	// Silence should not trigger speaking.
	silence := make([]int16, FrameSize)
	p.updateSpeaking(silence)
	info := p.info()
	if info.Speaking {
		t.Error("silence should not be speaking")
	}

	// Loud signal should trigger speaking.
	loud := make([]int16, FrameSize)
	for i := range loud {
		loud[i] = 10000
	}
	p.updateSpeaking(loud)
	info = p.info()
	if !info.Speaking {
		t.Error("loud signal should be speaking")
	}
}

func TestParticipantConsumePCM(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	p, err := newParticipant("op1", "Alice")
	if err != nil {
		t.Fatalf("newParticipant: %v", err)
	}
	defer p.close()

	// Before any decode, consumePCM should return nil.
	if got := p.consumePCM(); got != nil {
		t.Error("consumePCM before decode should return nil")
	}

	// Encode then decode to populate pcmBuf.
	silence := make([]int16, FrameSize)
	buf := make([]byte, 1024)
	n, err := p.encoder.Encode(silence, FrameSize, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := p.decodeAudio(buf[:n]); err != nil {
		t.Fatalf("decodeAudio: %v", err)
	}

	// First consume should return data.
	got := p.consumePCM()
	if got == nil {
		t.Fatal("consumePCM after decode should return data")
	}
	if len(got) != FrameSize {
		t.Errorf("consumePCM returned %d samples, want %d", len(got), FrameSize)
	}

	// Second consume should return nil.
	if got := p.consumePCM(); got != nil {
		t.Error("second consumePCM should return nil")
	}
}

func TestParticipantConsumePCMMuted(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus codec not available")
	}

	p, err := newParticipant("op1", "Alice")
	if err != nil {
		t.Fatalf("newParticipant: %v", err)
	}
	defer p.close()

	// Encode then decode to populate pcmBuf.
	silence := make([]int16, FrameSize)
	buf := make([]byte, 1024)
	n, err := p.encoder.Encode(silence, FrameSize, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := p.decodeAudio(buf[:n]); err != nil {
		t.Fatalf("decodeAudio: %v", err)
	}

	// Mute the participant.
	p.setMuted(true)

	// consumePCM should return nil when muted.
	if got := p.consumePCM(); got != nil {
		t.Error("consumePCM when muted should return nil")
	}
}
