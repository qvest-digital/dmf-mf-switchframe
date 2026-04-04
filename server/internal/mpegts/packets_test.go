package mpegts

import "testing"

func TestNullPacket_Size(t *testing.T) {
	if n := len(NullPacket()); n != PacketSize {
		t.Fatalf("expected %d bytes, got %d", PacketSize, n)
	}
}

func TestNullPacket_SyncByte(t *testing.T) {
	pkt := NullPacket()
	if pkt[0] != SyncByte {
		t.Fatalf("expected sync byte 0x%02X, got 0x%02X", SyncByte, pkt[0])
	}
}

func TestNullPacket_PID(t *testing.T) {
	pkt := NullPacket()
	pid := ExtractPID(pkt[:])
	if pid != NullPID {
		t.Fatalf("expected PID 0x%04X, got 0x%04X", NullPID, pid)
	}
}

func TestNullPacket_AFC(t *testing.T) {
	pkt := NullPacket()
	afc := (pkt[3] >> 4) & 0x03
	if afc != 0x01 {
		t.Fatalf("expected AFC 0x01 (payload only), got 0x%02X", afc)
	}
}

func TestNullPacket_PayloadAllFF(t *testing.T) {
	pkt := NullPacket()
	for i := 4; i < PacketSize; i++ {
		if pkt[i] != 0xFF {
			t.Fatalf("expected 0xFF at byte %d, got 0x%02X", i, pkt[i])
		}
	}
}

func TestSetDiscontinuityIndicator_ExistingAdaptationFieldAFC11(t *testing.T) {
	// AFC=0b11 (adaptation + payload), adaptation_field_length=1, flags=0x40 (RAI set).
	pkt := makePacket(SyncByte, 0x01, 0x00, 0x30, 0x01, 0x40)

	// Precondition: discontinuity not set.
	if pkt[5]&0x80 != 0 {
		t.Fatal("precondition: discontinuity_indicator should not be set")
	}

	SetDiscontinuityIndicator(pkt)

	// Discontinuity indicator should be set.
	if pkt[5]&0x80 == 0 {
		t.Fatal("discontinuity_indicator should be set")
	}

	// RAI flag should be preserved.
	if pkt[5]&0x40 == 0 {
		t.Fatal("RAI flag should be preserved")
	}

	// AFC should remain 11.
	afc := (pkt[3] >> 4) & 0x03
	if afc != 3 {
		t.Fatalf("AFC should remain 3, got %d", afc)
	}
}

func TestSetDiscontinuityIndicator_ExistingAdaptationFieldAFC10(t *testing.T) {
	// AFC=0b10 (adaptation only), adaptation_field_length=1, flags=0x00.
	pkt := makePacket(SyncByte, 0x01, 0x00, 0x20, 0x01, 0x00)

	SetDiscontinuityIndicator(pkt)

	// Discontinuity indicator should be set.
	if pkt[5]&0x80 == 0 {
		t.Fatal("discontinuity_indicator should be set")
	}

	// AFC should remain 10.
	afc := (pkt[3] >> 4) & 0x03
	if afc != 2 {
		t.Fatalf("AFC should remain 2, got %d", afc)
	}
}

func TestSetDiscontinuityIndicator_PayloadOnlyWithStuffing(t *testing.T) {
	// AFC=0b01 (payload only), CC=5, trailing 0xFF stuffing.
	pkt := makePacket(SyncByte, 0x00, 0x00, 0x15)
	pkt[PacketSize-1] = 0xFF
	pkt[PacketSize-2] = 0xFF

	SetDiscontinuityIndicator(pkt)

	// AFC should now be 11 (adaptation + payload).
	afc := (pkt[3] >> 4) & 0x03
	if afc != 3 {
		t.Fatalf("AFC should be changed to 3, got %d", afc)
	}

	// CC should be preserved.
	cc := pkt[3] & 0x0F
	if cc != 5 {
		t.Fatalf("CC should be 5, got %d", cc)
	}

	// Adaptation field length should be 1.
	if pkt[4] != 1 {
		t.Fatalf("adaptation_field_length should be 1, got %d", pkt[4])
	}

	// Discontinuity indicator should be set.
	if pkt[5]&0x80 == 0 {
		t.Fatal("discontinuity_indicator should be set")
	}
}

func TestSetDiscontinuityIndicator_PayloadOnlyNonStuffingSkipped(t *testing.T) {
	// AFC=0b01 (payload only), trailing bytes are NOT 0xFF.
	pkt := makePacket(SyncByte, 0x00, 0x00, 0x15)
	pkt[PacketSize-1] = 0xAB
	pkt[PacketSize-2] = 0xCD

	// Save original for comparison.
	original := make([]byte, PacketSize)
	copy(original, pkt)

	SetDiscontinuityIndicator(pkt)

	// Packet should be unchanged.
	for i := range pkt {
		if pkt[i] != original[i] {
			t.Fatalf("byte %d changed: expected 0x%02X, got 0x%02X", i, original[i], pkt[i])
		}
	}
}

func TestSetDiscontinuityIndicator_MultiPacketBuffer(t *testing.T) {
	// Two packets: first with AFC=11, second with AFC=10.
	pkt1 := makePacket(SyncByte, 0x01, 0x00, 0x30, 0x01, 0x00)
	pkt2 := makePacket(SyncByte, 0x02, 0x00, 0x20, 0x01, 0x00)
	buf := append(pkt1, pkt2...)

	SetDiscontinuityIndicator(buf)

	// Both packets should have discontinuity flag set.
	if buf[5]&0x80 == 0 {
		t.Fatal("packet 1: discontinuity_indicator should be set")
	}
	if buf[PacketSize+5]&0x80 == 0 {
		t.Fatal("packet 2: discontinuity_indicator should be set")
	}
}

func TestSetDiscontinuityIndicator_EmptyBuffer(t *testing.T) {
	// Should not panic on empty input.
	SetDiscontinuityIndicator(nil)
	SetDiscontinuityIndicator([]byte{})
}

func TestSetDiscontinuityIndicator_ShortBuffer(t *testing.T) {
	// Buffer shorter than one packet should not panic.
	SetDiscontinuityIndicator([]byte{SyncByte, 0x00, 0x00, 0x30})
}
