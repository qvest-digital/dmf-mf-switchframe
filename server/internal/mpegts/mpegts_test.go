package mpegts

import "testing"

func TestConstants(t *testing.T) {
	if PacketSize != 188 {
		t.Fatalf("PacketSize: expected 188, got %d", PacketSize)
	}
	if SyncByte != 0x47 {
		t.Fatalf("SyncByte: expected 0x47, got 0x%02X", SyncByte)
	}
	if NullPID != 0x1FFF {
		t.Fatalf("NullPID: expected 0x1FFF, got 0x%04X", NullPID)
	}
	if LiveMaxPayload != 1316 {
		t.Fatalf("LiveMaxPayload: expected 1316, got %d", LiveMaxPayload)
	}
}

func TestExtractPID(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
		want   uint16
	}{
		{
			name:   "null PID 0x1FFF",
			packet: []byte{SyncByte, 0x1F, 0xFF},
			want:   NullPID,
		},
		{
			name:   "PID 0x100",
			packet: []byte{SyncByte, 0x01, 0x00},
			want:   0x100,
		},
		{
			name:   "PID 0x000 (PAT)",
			packet: []byte{SyncByte, 0x00, 0x00},
			want:   0x000,
		},
		{
			name:   "high bits masked off (TEI+PUSI+priority)",
			packet: []byte{SyncByte, 0xE1, 0x00}, // TEI=1, PUSI=1, priority=1, PID=0x100
			want:   0x100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractPID(tt.packet)
			if got != tt.want {
				t.Fatalf("ExtractPID: expected 0x%04X, got 0x%04X", tt.want, got)
			}
		})
	}
}

// makePacket builds a 188-byte TS packet with the given header bytes.
// Remaining bytes are zero-filled.
func makePacket(header ...byte) []byte {
	pkt := make([]byte, PacketSize)
	copy(pkt, header)
	return pkt
}

func TestContainsKeyframe(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "single packet with RAI",
			// sync=0x47, PID bytes, AFC=0b10 (adaptation only) in byte 3,
			// adaptation_field_length=1, RAI flag=0x40
			data: makePacket(SyncByte, 0x00, 0x00, 0x20, 0x01, 0x40),
			want: true,
		},
		{
			name: "single packet without RAI",
			// AFC=0b10, adaptation_field_length=1, flags=0x00 (no RAI)
			data: makePacket(SyncByte, 0x00, 0x00, 0x20, 0x01, 0x00),
			want: false,
		},
		{
			name: "no adaptation field (AFC=0b01, payload only)",
			data: makePacket(SyncByte, 0x00, 0x00, 0x10),
			want: false,
		},
		{
			name: "AFC=0b11 with RAI",
			// AFC=0b11 (adaptation + payload), length=1, RAI=0x40
			data: makePacket(SyncByte, 0x00, 0x00, 0x30, 0x01, 0x40),
			want: true,
		},
		{
			name: "multi-packet, second has RAI",
			data: func() []byte {
				pkt1 := makePacket(SyncByte, 0x00, 0x00, 0x10) // no adaptation
				pkt2 := makePacket(SyncByte, 0x00, 0x00, 0x20, 0x01, 0x40)
				return append(pkt1, pkt2...)
			}(),
			want: true,
		},
		{
			name: "adaptation_field_length=0 (no flags byte)",
			data: makePacket(SyncByte, 0x00, 0x00, 0x20, 0x00),
			want: false,
		},
		{
			name: "empty data",
			data: []byte{},
			want: false,
		},
		{
			name: "nil data",
			data: nil,
			want: false,
		},
		{
			name: "short data (less than 188 bytes)",
			data: []byte{SyncByte, 0x00, 0x00, 0x20, 0x01, 0x40},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsKeyframe(tt.data)
			if got != tt.want {
				t.Fatalf("ContainsKeyframe: expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestAlignToSyncByte(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{
			name: "starts at offset 0",
			data: []byte{SyncByte, 0x00, 0x00},
			want: 0,
		},
		{
			name: "starts at offset 3",
			data: []byte{0x00, 0x00, 0x00, SyncByte, 0x00},
			want: 3,
		},
		{
			name: "not found",
			data: []byte{0x00, 0x01, 0x02, 0x03},
			want: -1,
		},
		{
			name: "empty",
			data: []byte{},
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AlignToSyncByte(tt.data)
			if got != tt.want {
				t.Fatalf("AlignToSyncByte: expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestTrimToPacketBoundary(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{"exact single packet", 188, 188},
		{"exact two packets", 376, 376},
		{"non-multiple rounds down", 200, 188},
		{"zero", 0, 0},
		{"187 rounds to 0", 187, 0},
		{"large non-multiple", 1500, 1316},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TrimToPacketBoundary(tt.n)
			if got != tt.want {
				t.Fatalf("TrimToPacketBoundary(%d): expected %d, got %d", tt.n, tt.want, got)
			}
		})
	}
}

func TestHasAdaptationField(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
		want   bool
	}{
		{
			name:   "AFC=0b10 (adaptation only)",
			packet: []byte{SyncByte, 0x00, 0x00, 0x20},
			want:   true,
		},
		{
			name:   "AFC=0b11 (adaptation + payload)",
			packet: []byte{SyncByte, 0x00, 0x00, 0x30},
			want:   true,
		},
		{
			name:   "AFC=0b01 (payload only)",
			packet: []byte{SyncByte, 0x00, 0x00, 0x10},
			want:   false,
		},
		{
			name:   "AFC=0b00 (reserved)",
			packet: []byte{SyncByte, 0x00, 0x00, 0x00},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasAdaptationField(tt.packet)
			if got != tt.want {
				t.Fatalf("HasAdaptationField: expected %v, got %v", tt.want, got)
			}
		})
	}
}
