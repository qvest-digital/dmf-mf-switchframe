package caption

import (
	"errors"
	"fmt"
)

// SMPTE ST 291 DID/SDID constants for closed captions.
const (
	// CaptionDID is the Data Identifier for CEA-708 Caption Distribution Packets in VANC.
	CaptionDID = 0x61

	// CaptionSDID is the Secondary Data Identifier for CEA-708 CDP (SMPTE ST 334-1).
	CaptionSDID = 0x01
)

// CDP (Caption Distribution Packet) constants per SMPTE ST 334-1.
const (
	cdpMagic0 = 0x96 // CDP header identifier byte 0
	cdpMagic1 = 0x69 // CDP header identifier byte 1

	// CDP frame rate codes per SMPTE ST 334-1 Table 3.
	CDPFrameRate24    byte = 0x01 // 24fps (film)
	CDPFrameRate25    byte = 0x02 // 25fps (PAL)
	CDPFrameRate2997  byte = 0x04 // 29.97fps (NTSC)
	CDPFrameRate30    byte = 0x05 // 30fps
	CDPFrameRate50    byte = 0x06 // 50fps
	CDPFrameRate5994  byte = 0x07 // 59.94fps
	CDPFrameRate60    byte = 0x08 // 60fps
	cdpFrameRate2997       = CDPFrameRate2997 // backward compat
)

// VANC packet limits.
const (
	// vancMaxPayload is the maximum CDP data in a single non-fragmented ST 291 packet.
	// DC is 8-bit (max 255), minus 1 byte for payload descriptor.
	vancMaxPayload = 254
)

// Sentinel errors.
var (
	ErrVANCTooShort     = errors.New("vanc: packet too short")
	ErrVANCInvalidDID   = errors.New("vanc: invalid DID for captions")
	ErrVANCInvalidSDID  = errors.New("vanc: invalid SDID for captions")
	ErrVANCBadChecksum  = errors.New("vanc: checksum mismatch")
	ErrVANCTooLarge     = errors.New("vanc: payload too large for single packet")
)

// BuildCDP constructs a SMPTE ST 334-1 Caption Distribution Packet
// containing the given CEA-608 cc_data pairs.
//
// CDP structure:
//
//	[cdp_identifier: 0x96 0x69]
//	[cdp_length]
//	[cdp_frame_rate(4) | reserved(4)]
//	[ccdata_present(1) | caption_service_active(1) | reserved(6)]
//	[cdp_hdr_sequence_counter (16-bit BE)]
//	[cc_data section: 0x72 | cc_count(5) | cc_data triples...]
//	[cdp_footer: 0x74 | cdp_hdr_sequence_counter (16-bit BE) | checksum]
func BuildCDP(pairs []CCPair, seq uint16, frameRate byte) []byte {
	if len(pairs) == 0 {
		return nil
	}

	if frameRate == 0 {
		frameRate = cdpFrameRate2997
	}

	ccCount := len(pairs)

	// CDP layout:
	// Header: magic(2) + length(1) + frame_rate(1) + flags(1) + seq(2) = 7
	// cc_data section: marker(1) + cc_count(1) + triples(3*N) = 2 + 3*N
	// Footer: marker(1) + seq(2) + checksum(1) = 4
	cdpLen := 7 + 2 + 3*ccCount + 4

	buf := make([]byte, cdpLen)
	pos := 0

	// CDP header.
	buf[pos] = cdpMagic0
	buf[pos+1] = cdpMagic1
	pos += 2

	// cdp_length.
	buf[pos] = byte(cdpLen)
	pos++

	// cdp_frame_rate (upper nibble) | reserved (lower nibble = 0xF).
	buf[pos] = (frameRate << 4) | 0x0F
	pos++

	// Flags: ccdata_present=1, caption_service_active=1, reserved=0.
	buf[pos] = 0xC0 // 1100_0000 — both flags set
	pos++

	// cdp_hdr_sequence_counter (16-bit big-endian).
	buf[pos] = byte(seq >> 8)
	buf[pos+1] = byte(seq)
	pos += 2

	// cc_data section marker.
	buf[pos] = 0x72
	pos++

	// cc_count (5 bits) | reserved (3 bits = 111).
	buf[pos] = byte(ccCount&0x1F) | 0xE0
	pos++

	// cc_data triples: [cc_valid|cc_type] [cc_data_1] [cc_data_2].
	for _, pair := range pairs {
		// cc_valid=1, cc_type=00 (CEA-608 field 1): 0xFC
		buf[pos] = 0xFC
		buf[pos+1] = pair.Data[0]
		buf[pos+2] = pair.Data[1]
		pos += 3
	}

	// CDP footer marker.
	buf[pos] = 0x74
	pos++

	// Footer sequence counter (mirrors header).
	buf[pos] = byte(seq >> 8)
	buf[pos+1] = byte(seq)
	pos += 2

	// Checksum: sum of all preceding bytes mod 256, then 256 - sum.
	var sum int
	for i := 0; i < pos; i++ {
		sum += int(buf[i])
	}
	buf[pos] = byte(256 - (sum % 256))

	return buf
}

// WrapCaptionST291 wraps CDP data into an ST 291 VANC packet for MXL output.
//
// Output format:
//
//	[DID=0x61] [SDID=0x01] [DC] [payload_descriptor=0x00] [cdpData...] [CS]
func WrapCaptionST291(cdpData []byte) ([]byte, error) {
	if len(cdpData) > vancMaxPayload {
		return nil, fmt.Errorf("%w: length %d exceeds maximum %d",
			ErrVANCTooLarge, len(cdpData), vancMaxPayload)
	}

	// DC = payload descriptor (1 byte) + cdpData length.
	dc := len(cdpData) + 1

	// Total: DID(1) + SDID(1) + DC(1) + UDW[DC] + CS(1) = 3 + DC + 1
	packetLen := 3 + dc + 1
	packet := make([]byte, packetLen)

	packet[0] = CaptionDID
	packet[1] = CaptionSDID
	packet[2] = byte(dc)
	packet[3] = 0x00 // payload descriptor: non-fragmented

	copy(packet[4:], cdpData)

	// Checksum: simplified 8-bit byte sum for byte-oriented transports (MXL data grains).
	// Note: full ST 291 uses a 9-bit checksum over 10-bit words with bit 8 inverted,
	// but for byte-oriented payloads this reduced form is standard practice.
	csIndex := 3 + dc
	var sum byte
	for i := 0; i < csIndex; i++ {
		sum += packet[i]
	}
	packet[csIndex] = sum

	return packet, nil
}

// ParseCaptionST291 parses an ST 291 VANC packet with caption DID/SDID
// and returns the CDP payload.
func ParseCaptionST291(packet []byte) ([]byte, error) {
	if len(packet) < 4 {
		return nil, fmt.Errorf("%w: need at least 4 bytes, got %d", ErrVANCTooShort, len(packet))
	}

	if packet[0] != CaptionDID {
		return nil, fmt.Errorf("%w: got 0x%02X", ErrVANCInvalidDID, packet[0])
	}
	if packet[1] != CaptionSDID {
		return nil, fmt.Errorf("%w: got 0x%02X", ErrVANCInvalidSDID, packet[1])
	}

	dc := int(packet[2])
	expectedLen := 3 + dc + 1
	if len(packet) < expectedLen {
		return nil, fmt.Errorf("%w: DC=%d requires %d bytes, got %d",
			ErrVANCTooShort, dc, expectedLen, len(packet))
	}

	// Verify checksum.
	csIndex := 3 + dc
	var sum byte
	for i := 0; i < csIndex; i++ {
		sum += packet[i]
	}
	if packet[csIndex] != sum {
		return nil, fmt.Errorf("%w: expected 0x%02X, got 0x%02X",
			ErrVANCBadChecksum, sum, packet[csIndex])
	}

	if dc == 0 {
		return []byte{}, nil
	}

	// Check for fragmented payload (payload descriptor != 0x00).
	// 0x00 = non-fragmented, 0x80 = first fragment, 0x40 = continuation, 0xC0 = last fragment.
	if packet[3] != 0x00 {
		return nil, fmt.Errorf("fragmented caption VANC packets not supported (descriptor=0x%02X)", packet[3])
	}

	// Strip payload descriptor byte (first UDW byte).
	udwData := packet[4 : 3+dc]
	result := make([]byte, len(udwData))
	copy(result, udwData)

	return result, nil
}
