package output

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// mockFilterWriter captures all Write calls for verification.
type mockFilterWriter struct {
	mu       sync.Mutex
	writes   [][]byte
	writeErr error
	closed   bool
}

func (m *mockFilterWriter) ID() string { return "mock" }

func (m *mockFilterWriter) Start(_ context.Context) error { return nil }

func (m *mockFilterWriter) Write(data []byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.writes = append(m.writes, cp)
	return len(data), nil
}

func (m *mockFilterWriter) Close() error {
	m.closed = true
	return nil
}

func (m *mockFilterWriter) Status() AdapterStatus {
	return AdapterStatus{State: StateActive}
}

// makeTSPacketFill builds a 188-byte TS packet with the given PID and a fill byte.
func makeTSPacketFill(pid uint16, fill byte) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47                // sync byte
	pkt[1] = byte(pid>>8) & 0x1F // PID high 5 bits
	pkt[2] = byte(pid & 0xFF)    // PID low 8 bits
	pkt[3] = 0x10                // adaptation field control = payload only
	for i := 4; i < 188; i++ {
		pkt[i] = fill
	}
	return pkt
}

func TestSCTE35Filter_StripsMatchingPID(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	// Build a buffer with 3 packets: video(0x100), scte35(0x102), audio(0x101).
	var data []byte
	data = append(data, makeTSPacketFill(0x100, 0xAA)...)            // video
	data = append(data, makeTSPacketFill(defaultSCTE35PID, 0xBB)...) // SCTE-35 — should be stripped
	data = append(data, makeTSPacketFill(0x101, 0xCC)...)            // audio

	n, err := f.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected n=%d, got %d", len(data), n)
	}

	if len(inner.writes) != 1 {
		t.Fatalf("expected 1 write to inner, got %d", len(inner.writes))
	}

	// Should have 2 packets (video + audio), not 3.
	filtered := inner.writes[0]
	if len(filtered) != 2*188 {
		t.Fatalf("expected %d bytes, got %d", 2*188, len(filtered))
	}

	// First packet should be video (PID 0x100).
	pid0 := uint16(filtered[1]&0x1F)<<8 | uint16(filtered[2])
	if pid0 != 0x100 {
		t.Errorf("first packet PID = 0x%03X, want 0x100", pid0)
	}

	// Second packet should be audio (PID 0x101).
	pid1 := uint16(filtered[189]&0x1F)<<8 | uint16(filtered[190])
	if pid1 != 0x101 {
		t.Errorf("second packet PID = 0x%03X, want 0x101", pid1)
	}
}

func TestSCTE35Filter_PassesNonMatchingPID(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	// Build a buffer with 2 non-SCTE-35 packets.
	var data []byte
	data = append(data, makeTSPacketFill(0x100, 0xAA)...)
	data = append(data, makeTSPacketFill(0x101, 0xBB)...)

	n, err := f.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected n=%d, got %d", len(data), n)
	}

	if len(inner.writes) != 1 {
		t.Fatalf("expected 1 write to inner, got %d", len(inner.writes))
	}

	// All packets should pass through unchanged.
	filtered := inner.writes[0]
	if len(filtered) != 2*188 {
		t.Fatalf("expected %d bytes, got %d", 2*188, len(filtered))
	}

	// Verify data is identical to input.
	for i := range data {
		if filtered[i] != data[i] {
			t.Fatalf("byte %d differs: got 0x%02X, want 0x%02X", i, filtered[i], data[i])
		}
	}
}

func TestSCTE35Filter_EmptyAfterFiltering(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	// Build a buffer with only SCTE-35 packets.
	var data []byte
	data = append(data, makeTSPacketFill(defaultSCTE35PID, 0xAA)...)
	data = append(data, makeTSPacketFill(defaultSCTE35PID, 0xBB)...)

	n, err := f.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected n=%d, got %d", len(data), n)
	}

	// Inner should NOT be called when all packets are stripped.
	if len(inner.writes) != 0 {
		t.Fatalf("expected 0 writes to inner, got %d", len(inner.writes))
	}
}

func TestSCTE35Filter_Close(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	if err := f.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !inner.closed {
		t.Error("Close did not delegate to inner adapter")
	}
}

func TestSCTE35Filter_SubPacketData(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	// Data smaller than a TS packet should pass through as-is.
	data := []byte{0x47, 0x01, 0x02, 0x10}
	n, err := f.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected n=%d, got %d", len(data), n)
	}

	if len(inner.writes) != 1 {
		t.Fatalf("expected 1 write to inner, got %d", len(inner.writes))
	}
}

func TestSCTE35Filter_InnerWriteError(t *testing.T) {
	inner := &mockFilterWriter{writeErr: errors.New("disk full")}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	data := makeTSPacketFill(0x100, 0xAA)
	_, err := f.Write(data)
	if err == nil {
		t.Fatal("expected error from inner write, got nil")
	}
}

func TestSCTE35Filter_ID(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	if f.ID() != "mock" {
		t.Errorf("expected ID 'mock', got %q", f.ID())
	}
}

func TestSCTE35Filter_Status(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	status := f.Status()
	if status.State != StateActive {
		t.Errorf("expected state %q, got %q", StateActive, status.State)
	}
}

func TestSCTE35Filter_MultipleSCTE35Packets(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	// 5 packets: video, scte35, scte35, audio, scte35
	var data []byte
	data = append(data, makeTSPacketFill(0x100, 0x11)...)            // video
	data = append(data, makeTSPacketFill(defaultSCTE35PID, 0x22)...) // strip
	data = append(data, makeTSPacketFill(defaultSCTE35PID, 0x33)...) // strip
	data = append(data, makeTSPacketFill(0x101, 0x44)...)            // audio
	data = append(data, makeTSPacketFill(defaultSCTE35PID, 0x55)...) // strip

	n, err := f.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected n=%d, got %d", len(data), n)
	}

	if len(inner.writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(inner.writes))
	}

	filtered := inner.writes[0]
	if len(filtered) != 2*188 {
		t.Fatalf("expected %d bytes, got %d", 2*188, len(filtered))
	}
}

func TestSCTE35Filter_InvalidSyncByte(t *testing.T) {
	inner := &mockFilterWriter{}
	f := newSCTE35Filter(inner, defaultSCTE35PID)

	// Build a 188-byte packet with invalid sync byte but PID matching SCTE-35.
	pkt := make([]byte, 188)
	pkt[0] = 0x00 // invalid sync byte
	pkt[1] = byte(defaultSCTE35PID>>8) & 0x1F
	pkt[2] = byte(defaultSCTE35PID & 0xFF)

	n, err := f.Write(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 188 {
		t.Fatalf("expected n=188, got %d", n)
	}

	// Packet with bad sync byte should pass through (not filtered).
	if len(inner.writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(inner.writes))
	}
	if len(inner.writes[0]) != 188 {
		t.Errorf("expected 188 bytes, got %d", len(inner.writes[0]))
	}
}
