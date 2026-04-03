package mpegts

const (
	// PacketSize is the fixed size of an MPEG-TS packet (ISO 13818-1).
	PacketSize = 188

	// SyncByte is the first byte of every valid TS packet.
	SyncByte = 0x47

	// NullPID is the PID for null/stuffing packets that decoders discard.
	NullPID = 0x1FFF

	// LiveMaxPayload is the maximum payload per SRT write in live mode.
	// 7 TS packets fit within a standard 1500-byte MTU minus SRT/UDP/IP overhead.
	LiveMaxPayload = 7 * PacketSize // 1316 bytes
)

// ExtractPID returns the 13-bit PID from bytes 1-2 of a TS packet header.
// Caller must ensure packet is at least 3 bytes (no bounds check).
func ExtractPID(packet []byte) uint16 {
	return uint16(packet[1]&0x1F)<<8 | uint16(packet[2])
}

// ContainsKeyframe scans 188-byte aligned TS packets for the Random Access
// Indicator (RAI) flag in the adaptation field, which signals an IDR/keyframe.
func ContainsKeyframe(data []byte) bool {
	for i := 0; i+PacketSize <= len(data); i += PacketSize {
		if data[i] != SyncByte {
			continue
		}
		afc := (data[i+3] >> 4) & 0x03
		if afc < 2 {
			continue
		}
		if data[i+4] == 0 {
			continue
		}
		if data[i+5]&0x40 != 0 {
			return true
		}
	}
	return false
}

// AlignToSyncByte returns the offset of the first 0x47 byte in data.
// Returns -1 if not found. Note: this performs a naive scan and may match
// 0x47 in payload data. Callers should use this only on nearly-aligned
// TS data (e.g., ring buffer after overflow) where a true sync byte is
// expected within one packet's worth of bytes.
func AlignToSyncByte(data []byte) int {
	for i, b := range data {
		if b == SyncByte {
			return i
		}
	}
	return -1
}

// TrimToPacketBoundary truncates a byte count to the nearest multiple of PacketSize.
func TrimToPacketBoundary(n int) int {
	return (n / PacketSize) * PacketSize
}

// HasAdaptationField returns true if the AFC bits in byte 3 indicate an
// adaptation field is present (AFC >= 0b10).
// Caller must ensure packet is at least 4 bytes (no bounds check).
func HasAdaptationField(packet []byte) bool {
	afc := (packet[3] >> 4) & 0x03
	return afc >= 2
}
