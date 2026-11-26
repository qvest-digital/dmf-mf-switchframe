package scte104

import (
	"errors"
	"fmt"
)

// ST 291 DID/SDID constants for SCTE-104 in VANC.
const (
	// ST291DID is the Data Identifier for SCTE-104 VANC packets (SMPTE ST 291).
	ST291DID = 0x41

	// ST291SDID is the Secondary Data Identifier for SCTE-104 VANC packets.
	ST291SDID = 0x07
)

// ST 291 payload descriptor bit masks (per ST 2010).
const (
	// st291PayloadDescSCTE104 indicates the payload is an SCTE-104 message (bit 0 = 0).
	st291PayloadDescSCTE104 = 0x00

	// st291PayloadDescFollowing indicates more packets follow (bit 1).
	st291PayloadDescFollowing = 0x02

	// st291PayloadDescContinued indicates continuation of a previous packet (bit 2).
	st291PayloadDescContinued = 0x04
)

// st291MaxPayload is the maximum SCTE-104 data bytes in a single non-fragmented
// packet. DC (data count) is an 8-bit field (max 255); with one byte reserved
// for the payload descriptor, the max SCTE-104 payload is 254 bytes (DC=255).
const st291MaxPayload = 254

var (
	// ErrST291TooShort indicates the ST 291 packet is too short to parse.
	ErrST291TooShort = errors.New("st291: packet too short")

	// ErrST291InvalidDID indicates the DID does not match the expected value for SCTE-104.
	ErrST291InvalidDID = errors.New("st291: invalid DID, expected 0x41")

	// ErrST291InvalidSDID indicates the SDID does not match the expected value for SCTE-104.
	ErrST291InvalidSDID = errors.New("st291: invalid SDID, expected 0x07")

	// ErrST291BadChecksum indicates the packet checksum does not match.
	ErrST291BadChecksum = errors.New("st291: checksum mismatch")

	// ErrST291Fragmented indicates the packet uses fragmentation which is not supported.
	ErrST291Fragmented = errors.New("st291: fragmented packets not supported")

	// ErrST291PayloadTooLarge indicates the payload exceeds the maximum size for a single packet.
	ErrST291PayloadTooLarge = errors.New("st291: payload too large for single packet")
)

// ParseST291 parses an ST 291 VANC packet and returns the SCTE-104 payload.
//
// The wire format is:
//
//	[DID] [SDID] [DC] [UDW bytes...] [CS]
//
// where CS = (sum of all preceding bytes) & 0xFF.
//
// The first UDW byte is a payload descriptor (per ST 2010). For non-fragmented
// SCTE-104 messages, the payload descriptor is 0x00. The descriptor byte is
// stripped from the returned data.
//
// Only single-packet (non-fragmented) messages are supported. Fragmented
// packets (continued_pkt or following_pkt set) return ErrST291Fragmented.
func ParseST291(packet []byte) ([]byte, error) {
	// Minimum: DID(1) + SDID(1) + DC(1) + CS(1) = 4 bytes
	if len(packet) < 4 {
		return nil, fmt.Errorf("%w: need at least 4 bytes, got %d", ErrST291TooShort, len(packet))
	}

	did := packet[0]
	sdid := packet[1]
	dc := int(packet[2])

	if did != ST291DID {
		return nil, fmt.Errorf("%w: got 0x%02X", ErrST291InvalidDID, did)
	}
	if sdid != ST291SDID {
		return nil, fmt.Errorf("%w: got 0x%02X", ErrST291InvalidSDID, sdid)
	}

	// Total expected length: DID(1) + SDID(1) + DC(1) + UDW[DC] + CS(1) = 3 + DC + 1
	expectedLen := 3 + dc + 1
	if len(packet) < expectedLen {
		return nil, fmt.Errorf("%w: DC=%d requires %d bytes, got %d",
			ErrST291TooShort, dc, expectedLen, len(packet))
	}

	// Verify checksum: sum of all bytes before the checksum byte, mod 256.
	csIndex := 3 + dc
	expectedCS := computeChecksum(packet[:csIndex])
	if packet[csIndex] != expectedCS {
		return nil, fmt.Errorf("%w: expected 0x%02X, got 0x%02X",
			ErrST291BadChecksum, expectedCS, packet[csIndex])
	}

	return fromUDW(packet[3 : 3+dc])
}

// fromUDW strips the ST 2010 payload descriptor from the user data words
// of one SCTE-104 packet and returns a copy of the message.
func fromUDW(udw []byte) ([]byte, error) {
	// Zero-length UDW: no payload descriptor, no data.
	if len(udw) == 0 {
		return []byte{}, nil
	}

	// First UDW byte is the payload descriptor (per ST 2010).
	payloadDesc := udw[0]

	// Check for fragmentation.
	if payloadDesc&st291PayloadDescContinued != 0 || payloadDesc&st291PayloadDescFollowing != 0 {
		return nil, ErrST291Fragmented
	}

	// Return a copy to avoid aliasing the input slice.
	result := make([]byte, len(udw)-1)
	copy(result, udw[1:])

	return result, nil
}

// ParseDataGrain returns the SCTE-104 messages one MXL data grain carries.
//
// A grain is either RFC 8331 ancillary data, the layout of a video/smpte291
// flow, or the byte-oriented packet WrapST291 writes. The byte-oriented
// packet starts with DID 0x41 and SDID 0x07; read as RFC 8331, those two
// bytes are a Length of 16647, which a grain shorter than that plus the
// header cannot hold. Such a grain is parsed as the byte-oriented packet,
// anything else as RFC 8331.
//
// A grain without SCTE-104 packets, such as the empty one written for most
// pictures, returns no messages and no error. Packets with another DID or
// SDID are skipped. A malformed RFC 8331 grain returns an error wrapping
// ErrRFC8331Malformed.
func ParseDataGrain(grain []byte) ([][]byte, error) {
	if isByteOrientedST291(grain) {
		msg, err := ParseST291(grain)
		if err != nil {
			return nil, err
		}
		return [][]byte{msg}, nil
	}

	packets, err := ParseRFC8331(grain)
	if err != nil {
		return nil, err
	}
	var msgs [][]byte
	var errs []error
	for _, p := range packets {
		if p.DID != ST291DID || p.SDID != ST291SDID {
			continue
		}
		msg, err := fromUDW(p.UDW)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		msgs = append(msgs, msg)
	}
	return msgs, errors.Join(errs...)
}

func isByteOrientedST291(grain []byte) bool {
	return len(grain) >= 2 && grain[0] == ST291DID && grain[1] == ST291SDID &&
		len(grain) < rfc8331HeaderLen+(ST291DID<<8|ST291SDID)
}

// WrapST291 wraps SCTE-104 data into an ST 291 VANC packet.
//
// The output format is:
//
//	[DID=0x41] [SDID=0x07] [DC] [payload_descriptor=0x00] [scte104Data...] [CS]
//
// where DC = len(scte104Data) + 1 (for the payload descriptor byte) and
// CS = (sum of all preceding bytes) & 0xFF.
//
// Returns ErrST291PayloadTooLarge if scte104Data exceeds 254 bytes
// (fragmentation not implemented).
func WrapST291(scte104Data []byte) ([]byte, error) {
	if len(scte104Data) > st291MaxPayload {
		return nil, fmt.Errorf("%w: length %d exceeds maximum %d",
			ErrST291PayloadTooLarge, len(scte104Data), st291MaxPayload)
	}

	// DC = payload descriptor (1 byte) + scte104Data length.
	dc := len(scte104Data) + 1

	// Total packet: DID(1) + SDID(1) + DC(1) + UDW[DC] + CS(1) = 3 + DC + 1
	packetLen := 3 + dc + 1
	packet := make([]byte, packetLen)

	packet[0] = ST291DID
	packet[1] = ST291SDID
	packet[2] = byte(dc)
	packet[3] = st291PayloadDescSCTE104 // 0x00 for non-fragmented SCTE-104

	copy(packet[4:], scte104Data)

	// Checksum covers all bytes before the checksum position.
	csIndex := 3 + dc
	packet[csIndex] = computeChecksum(packet[:csIndex])

	return packet, nil
}

// computeChecksum returns (sum of bytes) & 0xFF.
//
// This is a simplified 8-bit byte-sum checksum for the byte-oriented packet.
// SMPTE ST 291 defines a 9-bit checksum over 10-bit words with parity, which
// RFC 8331 carries; see checksumWord.
func computeChecksum(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum += b
	}
	return sum
}
