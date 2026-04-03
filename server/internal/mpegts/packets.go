package mpegts

import (
	"fmt"
	"log/slog"
)

// NullPacket returns a single 188-byte null TS packet (PID 0x1FFF).
// ISO 13818-1 defines null packets as stuffing that decoders discard.
func NullPacket() [PacketSize]byte {
	var pkt [PacketSize]byte
	pkt[0] = SyncByte
	pkt[1] = 0x1F // PID high bits (no PUSI, no TEI, no priority)
	pkt[2] = 0xFF // PID low bits -> PID = 0x1FFF
	pkt[3] = 0x10 // AFC=01 (payload only), CC=0
	for i := 4; i < PacketSize; i++ {
		pkt[i] = 0xFF // null payload
	}
	return pkt
}

// SetDiscontinuityIndicator sets the MPEG-TS discontinuity_indicator flag
// on each 188-byte TS packet in the buffer. This is the standard way to signal
// that continuity counter tracking resets at this point, used when writing
// cached PAT/PMT packets at the start of a rotated recording file.
//
// For packets with no adaptation field (payload only, AFC=01), an adaptation
// field is inserted: AFC is changed to 11 (adaptation + payload), with
// adaptation_field_length=1 and the discontinuity_indicator bit set.
//
// For packets that already have an adaptation field (AFC=10 or 11), the
// discontinuity_indicator bit (bit 7 of the flags byte) is set in the
// existing adaptation field.
func SetDiscontinuityIndicator(tsData []byte) {
	for i := 0; i+PacketSize <= len(tsData); i += PacketSize {
		if tsData[i] != SyncByte {
			continue // not a valid sync byte
		}

		afc := (tsData[i+3] >> 4) & 0x03

		switch {
		case afc >= 2:
			// Adaptation field already exists (AFC=10 or AFC=11).
			// Verify there is a flags byte (adaptation_field_length >= 1).
			if tsData[i+4] >= 1 {
				// Set discontinuity_indicator (bit 7 of flags byte at byte 5).
				tsData[i+5] |= 0x80
			}

		case afc == 1:
			// Payload only (AFC=01). We need to insert a minimal adaptation field.
			// Shifting payload right by 2 bytes drops the last 2 bytes. Verify
			// they are 0xFF stuffing before proceeding; otherwise skip this
			// packet to avoid corrupting a PAT/PMT with a full descriptor loop.
			if tsData[i+PacketSize-1] != 0xFF || tsData[i+PacketSize-2] != 0xFF {
				slog.Warn("SetDiscontinuityIndicator: skipping payload-only packet with non-stuffing trailing bytes",
					"offset", i,
					"byte[-1]", fmt.Sprintf("0x%02X", tsData[i+PacketSize-1]),
					"byte[-2]", fmt.Sprintf("0x%02X", tsData[i+PacketSize-2]))
				continue
			}

			// Change AFC from 01 to 11 (adaptation + payload), preserving CC.
			tsData[i+3] = (tsData[i+3] & 0x0F) | 0x30

			// Shift existing payload right by 2 bytes to make room for the
			// adaptation field header (length + flags). The last 2 bytes of
			// the original payload are 0xFF stuffing and are safely dropped.
			copy(tsData[i+6:i+PacketSize], tsData[i+4:i+PacketSize-2])

			// Set adaptation_field_length = 1 (just the flags byte).
			tsData[i+4] = 1

			// Set flags byte with discontinuity_indicator (bit 7).
			tsData[i+5] = 0x80
		}
	}
}
