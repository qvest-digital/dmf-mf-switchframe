package scte35

import (
	"errors"
	"fmt"

	"github.com/zsiec/switchframe/server/internal/mpegts"
)

var (
	// ErrEmptyData is returned when ParseFromTS receives nil or empty input.
	ErrEmptyData = errors.New("scte35: empty TS data")

	// ErrNoSCTE35Data is returned when no SCTE-35 section is found on the target PID.
	ErrNoSCTE35Data = errors.New("scte35: no SCTE-35 section found on target PID")

	// ErrInvalidPacket is returned when TS packet structure is invalid.
	ErrInvalidPacket = errors.New("scte35: invalid TS packet")
)

// ParseFromTS extracts and decodes a SCTE-35 section from MPEG-TS packet(s) on
// the given PID. Returns the decoded CueMessage or an error.
//
// The function scans 188-byte TS packets looking for the target PID, collects
// section data from matching packets, and delegates decoding to Decode() which
// uses scte35-go with automatic CRC-32 validation.
func ParseFromTS(pid uint16, data []byte) (*CueMessage, error) {
	if len(data) == 0 {
		return nil, ErrEmptyData
	}

	if len(data) < mpegts.PacketSize {
		return nil, fmt.Errorf("%w: data length %d is less than packet size %d", ErrInvalidPacket, len(data), mpegts.PacketSize)
	}

	var sectionData []byte
	collecting := false

	// Track how many bytes of the section we expect (from section_length field).
	expectedLen := 0

	for offset := 0; offset+mpegts.PacketSize <= len(data); offset += mpegts.PacketSize {
		pkt := data[offset : offset+mpegts.PacketSize]

		// Validate sync byte.
		if pkt[0] != mpegts.SyncByte {
			return nil, fmt.Errorf("%w: expected sync byte 0x47, got 0x%02x at offset %d", ErrInvalidPacket, pkt[0], offset)
		}

		// Extract PID from bytes 1-2 (13-bit field).
		pktPID := mpegts.ExtractPID(pkt)
		if pktPID != pid {
			continue
		}

		// Check payload_unit_start_indicator (PUSI) — bit 6 of byte 1.
		pusi := pkt[1]&0x40 != 0

		// Determine payload start position.
		// Byte 3: adaptation_field_control (bits 5-4) + continuity_counter (bits 3-0).
		adaptationFieldControl := (pkt[3] >> 4) & 0x03
		payloadStart := 4

		// Handle adaptation field.
		if adaptationFieldControl == 0x02 {
			// Adaptation field only, no payload.
			continue
		}
		if adaptationFieldControl == 0x03 {
			// Adaptation field followed by payload.
			if payloadStart >= mpegts.PacketSize {
				continue
			}
			adaptationFieldLength := int(pkt[payloadStart])
			payloadStart += 1 + adaptationFieldLength
		}
		// adaptationFieldControl == 0x01: payload only (payloadStart stays at 4)

		if payloadStart >= mpegts.PacketSize {
			continue
		}

		payload := pkt[payloadStart:]

		if pusi {
			// Pointer field indicates where the section starts.
			if len(payload) == 0 {
				continue
			}
			pointerField := int(payload[0])
			payload = payload[1:]

			if pointerField > 0 {
				if pointerField > len(payload) {
					continue // malformed pointer field
				}
				// If we were collecting a previous section, append pointer bytes.
				if collecting {
					sectionData = append(sectionData, payload[:pointerField]...)
					// Check if we have enough data.
					if expectedLen > 0 && len(sectionData) >= expectedLen {
						break
					}
				}
				payload = payload[pointerField:]
			}

			// Start a new section. Deep-copy to avoid aliasing the input buffer,
			// which would allow append() in multi-packet assembly to corrupt
			// subsequent TS packets.
			sectionData = append([]byte(nil), payload...)
			collecting = true

			// Extract section_length to know how much data to collect.
			if len(sectionData) >= 3 {
				sectionLength := int(sectionData[1]&0x0F)<<8 | int(sectionData[2])
				expectedLen = 3 + sectionLength // table_id(1) + flags+length(2) + section_length bytes
			}

			// If section fits in a single packet, trim padding.
			if expectedLen > 0 && len(sectionData) >= expectedLen {
				sectionData = sectionData[:expectedLen]
				break
			}
		} else if collecting {
			// Continuation packet — append payload.
			sectionData = append(sectionData, payload...)

			// Check if we've collected enough.
			if expectedLen > 0 && len(sectionData) >= expectedLen {
				sectionData = sectionData[:expectedLen]
				break
			}
		}
	}

	if !collecting || len(sectionData) == 0 {
		return nil, ErrNoSCTE35Data
	}

	// Trim to expected length if we have it.
	if expectedLen > 0 && len(sectionData) > expectedLen {
		sectionData = sectionData[:expectedLen]
	}

	// Validate table_id.
	if sectionData[0] != mpegts.TableIDSCTE35 {
		return nil, fmt.Errorf("scte35: unexpected table_id 0x%02x (expected 0xFC)", sectionData[0])
	}

	// Delegate to Decode() which handles scte35-go decoding + CRC validation.
	return Decode(sectionData)
}

// DetectSCTE35PID scans PMT data for elementary streams with stream_type 0x86
// (SCTE-35). Returns a slice of PIDs that carry SCTE-35 data. Returns nil if
// no SCTE-35 streams are found or the data is invalid.
func DetectSCTE35PID(pmtData []byte) []uint16 {
	// Minimum PMT size: table_id(1) + flags+length(2) + header(7) + CRC(4) = 14
	if len(pmtData) < 14 {
		return nil
	}

	// Validate table_id.
	if pmtData[0] != mpegts.TableIDPMT {
		return nil
	}

	// Extract section_length from bytes 1-2.
	sectionLength := int(pmtData[1]&0x0F)<<8 | int(pmtData[2])

	// Total section data length (from table_id to end of CRC).
	totalLen := 3 + sectionLength
	if totalLen > len(pmtData) {
		totalLen = len(pmtData)
	}

	// Skip to program_info_length (at offset 10-11, relative to start).
	// PMT header after table_id + section_length:
	//   program_number(2) + version_flags(1) + section_number(1) + last_section_number(1) + PCR_PID(2) = 7
	//   program_info_length(2) = offset 10-11
	if totalLen < 12 {
		return nil
	}

	programInfoLength := int(pmtData[10]&0x0F)<<8 | int(pmtData[11])

	// Elementary stream loop starts after program descriptors.
	offset := 12 + programInfoLength

	// Elementary stream loop ends 4 bytes before section end (CRC).
	loopEnd := totalLen - 4

	var pids []uint16

	for offset+5 <= loopEnd {
		streamType := pmtData[offset]
		// PMT ES entries store the 13-bit PID at the same bit position
		// (bytes 1-2, masked 0x1F) as TS packet headers, so ExtractPID works here.
		esPID := mpegts.ExtractPID(pmtData[offset:])
		esInfoLength := int(pmtData[offset+3]&0x0F)<<8 | int(pmtData[offset+4])

		if streamType == mpegts.StreamTypeSCTE35 {
			pids = append(pids, esPID)
		}

		offset += 5 + esInfoLength
	}

	return pids
}
