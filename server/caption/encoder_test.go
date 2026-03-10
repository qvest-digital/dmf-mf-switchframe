package caption

import "testing"

func TestOddParity(t *testing.T) {
	tests := []struct {
		in   byte
		want byte
	}{
		{0x00, 0x80}, // 0 ones → even → set bit 7
		{0x01, 0x01}, // 1 one → odd → clear bit 7
		{0x14, 0x94}, // cc608Ctrl: 2 ones → even → set bit 7
		{0x25, 0x25}, // cc608RU2: 3 ones → odd → clear bit 7
		{0x2D, 0xAD}, // cc608CR: 4 ones → even → set bit 7
		{0x2C, 0x2C}, // cc608EDM: 3 ones → odd → clear bit 7
		{0x41, 0xC1}, // 'A': 2 ones → even → set bit 7
		{0x7F, 0x7F}, // DEL: 7 ones → odd → clear bit 7
		{0xFF, 0x7F}, // high bit already set — should be cleared to 0x7F
	}

	for _, tt := range tests {
		got := oddParity(tt.in)
		if got != tt.want {
			t.Errorf("oddParity(0x%02X) = 0x%02X, want 0x%02X", tt.in, got, tt.want)
		}
	}
}

func TestEncoderInit(t *testing.T) {
	enc := NewEncoder(2)

	// First IngestText should trigger init sequence.
	enc.IngestText("A")

	// Queue should contain: RU2, RU2, PAC, "A\x80"
	if got := enc.QueueLen(); got != 4 {
		t.Fatalf("QueueLen = %d, want 4 (2 RU2 + PAC + char)", got)
	}

	// First two pairs are RU2 commands (with odd parity).
	p := enc.NextPair()
	if p.Data[0] != oddParity(cc608Ctrl) || p.Data[1] != oddParity(cc608RU2) {
		t.Errorf("pair 1 = %02X %02X, want %02X %02X (RU2)",
			p.Data[0], p.Data[1], oddParity(cc608Ctrl), oddParity(cc608RU2))
	}
	p = enc.NextPair()
	if p.Data[0] != oddParity(cc608Ctrl) || p.Data[1] != oddParity(cc608RU2) {
		t.Errorf("pair 2 = %02X %02X, want %02X %02X (RU2)",
			p.Data[0], p.Data[1], oddParity(cc608Ctrl), oddParity(cc608RU2))
	}

	// Third pair is PAC (row 14).
	p = enc.NextPair()
	if p.Data[0] != oddParity(cc608Ctrl) || p.Data[1] != oddParity(0x60) {
		t.Errorf("pair 3 = %02X %02X, want %02X %02X (PAC)",
			p.Data[0], p.Data[1], oddParity(cc608Ctrl), oddParity(0x60))
	}

	// Fourth pair is the character with odd parity.
	p = enc.NextPair()
	if p.Data[0] != oddParity('A') || p.Data[1] != 0x80 {
		t.Errorf("pair 4 = %02X %02X, want %02X %02X",
			p.Data[0], p.Data[1], oddParity('A'), byte(0x80))
	}
}

func TestEncoderCharPairing(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("AB") // already triggers init

	// Skip init pairs (3).
	for i := 0; i < 3; i++ {
		enc.NextPair()
	}

	// "AB" should be one pair with odd parity.
	p := enc.NextPair()
	if p.Data[0] != oddParity('A') || p.Data[1] != oddParity('B') {
		t.Errorf("pair = %02X %02X, want %02X %02X",
			p.Data[0], p.Data[1], oddParity('A'), oddParity('B'))
	}

	if enc.NextPair() != nil {
		t.Error("expected nil after queue exhausted")
	}
}

func TestEncoderOddCharacter(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("ABC")

	// Skip init (3).
	for i := 0; i < 3; i++ {
		enc.NextPair()
	}

	// "AB" pair.
	p := enc.NextPair()
	if p.Data[0] != oddParity('A') || p.Data[1] != oddParity('B') {
		t.Errorf("pair 1 = %02X %02X, want %02X %02X",
			p.Data[0], p.Data[1], oddParity('A'), oddParity('B'))
	}

	// "C" + null pad.
	p = enc.NextPair()
	if p.Data[0] != oddParity('C') || p.Data[1] != 0x80 {
		t.Errorf("pair 2 = %02X %02X, want %02X %02X",
			p.Data[0], p.Data[1], oddParity('C'), byte(0x80))
	}
}

func TestEncoderNewline(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestNewline()

	// Skip init (3), then 2 CR pairs.
	for i := 0; i < 3; i++ {
		enc.NextPair()
	}

	p := enc.NextPair()
	if p.Data[0] != oddParity(cc608Ctrl) || p.Data[1] != oddParity(cc608CR) {
		t.Errorf("pair = %02X %02X, want %02X %02X (CR)",
			p.Data[0], p.Data[1], oddParity(cc608Ctrl), oddParity(cc608CR))
	}
	p = enc.NextPair()
	if p.Data[0] != oddParity(cc608Ctrl) || p.Data[1] != oddParity(cc608CR) {
		t.Errorf("pair = %02X %02X, want %02X %02X (CR)",
			p.Data[0], p.Data[1], oddParity(cc608Ctrl), oddParity(cc608CR))
	}
}

func TestEncoderClear(t *testing.T) {
	enc := NewEncoder(2)
	enc.Clear()

	// Clear does not trigger init — EDM is standalone.
	if got := enc.QueueLen(); got != 2 {
		t.Fatalf("QueueLen = %d, want 2 (EDM twice)", got)
	}

	p := enc.NextPair()
	if p.Data[0] != oddParity(cc608Ctrl) || p.Data[1] != oddParity(cc608EDM) {
		t.Errorf("pair = %02X %02X, want %02X %02X (EDM)",
			p.Data[0], p.Data[1], oddParity(cc608Ctrl), oddParity(cc608EDM))
	}
}

func TestEncoderRU3(t *testing.T) {
	enc := NewEncoder(3)
	enc.IngestText("A")

	p := enc.NextPair()
	if p.Data[0] != oddParity(cc608Ctrl) || p.Data[1] != oddParity(cc608RU3) {
		t.Errorf("pair = %02X %02X, want %02X %02X (RU3)",
			p.Data[0], p.Data[1], oddParity(cc608Ctrl), oddParity(cc608RU3))
	}
}

func TestEncoderRU4(t *testing.T) {
	enc := NewEncoder(4)
	enc.IngestText("A")

	p := enc.NextPair()
	if p.Data[0] != oddParity(cc608Ctrl) || p.Data[1] != oddParity(cc608RU4) {
		t.Errorf("pair = %02X %02X, want %02X %02X (RU4)",
			p.Data[0], p.Data[1], oddParity(cc608Ctrl), oddParity(cc608RU4))
	}
}

func TestEncoderInvalidRollUp(t *testing.T) {
	// Should default to 2.
	enc := NewEncoder(0)
	enc.IngestText("A")
	p := enc.NextPair()
	if p.Data[1] != oddParity(cc608RU2) {
		t.Errorf("invalid rollUp should default to RU2, got %02X (want %02X)",
			p.Data[1], oddParity(cc608RU2))
	}
}

func TestEncoderReset(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("Hello")
	enc.Reset()

	if enc.QueueLen() != 0 {
		t.Error("queue should be empty after reset")
	}

	// Next IngestText should re-init.
	enc.IngestText("A")
	if enc.QueueLen() != 4 {
		t.Errorf("QueueLen = %d, want 4 (re-init + char)", enc.QueueLen())
	}
}

func TestEncoderNonPrintable(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("\x00\x01\x1F") // all non-printable

	// Should have only init, no character pairs.
	if enc.QueueLen() != 3 {
		t.Errorf("QueueLen = %d, want 3 (init only, no chars)", enc.QueueLen())
	}
}

func TestEncoderDELExcluded(t *testing.T) {
	enc := NewEncoder(2)
	enc.IngestText("\x7F") // DEL character

	// Should have only init, no character pairs (DEL excluded).
	if enc.QueueLen() != 3 {
		t.Errorf("QueueLen = %d, want 3 (init only, DEL excluded)", enc.QueueLen())
	}
}

func TestEncoderQueueCompaction(t *testing.T) {
	enc := NewEncoder(2)

	enc.IngestText("AB")

	// Drain all pairs.
	for enc.NextPair() != nil {
		// drain
	}

	// After full drain, queue should be nil (compacted to free backing array).
	enc.mu.Lock()
	isNil := enc.queue == nil
	enc.mu.Unlock()

	if !isNil {
		t.Error("queue should be nil after full drain (compaction)")
	}
}
