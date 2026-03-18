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
