package scte104

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var (
	// ErrTooShort indicates the message data is too short to decode.
	ErrTooShort = errors.New("scte104: message too short")

	// ErrUnknownOpID indicates an unrecognized single-operation ID.
	ErrUnknownOpID = errors.New("scte104: unknown operation ID")
)

// Decode parses SCTE-104 binary data into a Message.
//
// The decoder handles both Single Operation Messages (SOM) and Multiple
// Operation Messages (MOM). If the first two bytes match a known single-op
// OpID (not 0xFFFF), the message is treated as a SOM with just OpID + data
// and no MOM header. Otherwise it is parsed as a MOM with full headers.
func Decode(data []byte) (*Message, error) {
	if len(data) < 2 {
		return nil, ErrTooShort
	}

	opID := binary.BigEndian.Uint16(data[0:2])

	if isKnownSingleOpID(opID) {
		return decodeSOM(opID, data[2:])
	}

	if opID == OpMultipleOperationMessage {
		return decodeMOM(data[2:])
	}

	return nil, fmt.Errorf("%w: 0x%04X", ErrUnknownOpID, opID)
}

// decodeSOM decodes a Single Operation Message. The opID has already been
// consumed; payload contains either:
//   - Abbreviated (VANC) format: the operation data bytes directly.
//   - Full SOM format: a 12-byte header followed by the operation data.
//
// Full SOM header (12 bytes):
//
//	messageSize(2) + result(2) + result_extension(2) +
//	protocol_version(1) + AS_index(1) + message_number(1) + DPI_PID_index(2) +
//	SCTE35_protocol_version(1)
//
// Detection heuristic: if len(payload) >= 14 (12 header + at least 2 bytes of
// operation data) and the first 2 bytes (messageSize) match either convention
// (spec: len-2, or legacy: len), treat as full SOM with the header.
func decodeSOM(opID uint16, payload []byte) (*Message, error) {
	// Check if this is a full SOM with header fields.
	if len(payload) >= 14 { // 12 header + minimum 2 bytes for operation data
		messageSize := int(binary.BigEndian.Uint16(payload[0:2]))
		// Dual-convention heuristic: spec says messageSize counts bytes after
		// the messageSize field itself. Legacy implementations set it to total length.
		if messageSize == len(payload)-2 || messageSize == len(payload) {
			// Additional validation: check protocol version to avoid false
			// positives when abbreviated payload bytes accidentally match the
			// length. Valid SCTE-104 protocol versions are 0 and 1.
			protocolVersion := payload[2]
			if protocolVersion > 1 {
				// Not a valid SOM — fall through to abbreviated format.
				goto abbreviated
			}
			// Full SOM format: parse the 12-byte header, then operation data.
			msg := &Message{
				ProtocolVersion: payload[6],
				ASIndex:         payload[7],
				MessageNumber:   payload[8],
				DPIPIDIndex:     binary.BigEndian.Uint16(payload[9:11]),
			}
			// payload[11] = SCTE35_protocol_version (skip)
			op, err := decodeOperationData(opID, payload[12:])
			if err != nil {
				return nil, fmt.Errorf("scte104 SOM: %w", err)
			}
			msg.Operations = []Operation{op}
			return msg, nil
		}
	}

abbreviated:
	// Abbreviated format (VANC): payload is directly the operation data.
	op, err := decodeOperationData(opID, payload)
	if err != nil {
		return nil, fmt.Errorf("scte104 SOM: %w", err)
	}
	return &Message{
		Operations: []Operation{op},
	}, nil
}

// decodeMOM decodes a Multiple Operation Message. The 0xFFFF opID has already
// been consumed; data starts at the messageSize field.
//
// MOM wire format after OpID:
//
//	messageSize          uint16  (total bytes remaining including this field)
//	protocolVersion      uint8
//	AS_index             uint8
//	message_number       uint8
//	DPI_PID_index        uint16
//	SCTE35_protocol_ver  uint8
//	timestamp            variable (depends on time_type byte)
//	num_ops              uint8
//	[operations...]
//
// Timestamp formats by time_type:
//
//	0: no timestamp   (1 byte  — just the time_type field)
//	1: UTC            (7 bytes — time_type(1) + GPS_seconds(4) + GPS_microseconds(2))
//	2: VITC           (5 bytes — time_type(1) + hours(1) + minutes(1) + seconds(1) + frames(1))
//	3: GPI            (3 bytes — time_type(1) + number(1) + edge(1))
func decodeMOM(data []byte) (*Message, error) {
	// Minimum MOM header before timestamp: messageSize(2) + protocolVersion(1) +
	// AS_index(1) + message_number(1) + DPI_PID_index(2) + SCTE35_protocol_version(1) +
	// time_type(1) = 9 bytes
	if len(data) < 9 {
		return nil, fmt.Errorf("%w: MOM header requires at least 9 bytes, got %d", ErrTooShort, len(data))
	}

	// Use messageSize to bound the operation loop when it matches a known convention.
	messageSize := int(binary.BigEndian.Uint16(data[0:2]))
	endOffset := len(data)
	if messageSize == len(data)-2 {
		endOffset = messageSize + 2 // spec convention: counts bytes after messageSize field
	} else if messageSize == len(data) {
		endOffset = messageSize // legacy convention: counts total
	}
	// Otherwise endOffset stays at len(data) for robustness.

	msg := &Message{
		ProtocolVersion: data[2],
		ASIndex:         data[3],
		MessageNumber:   data[4],
		DPIPIDIndex:     binary.BigEndian.Uint16(data[5:7]),
	}

	// data[7] = SCTE35 protocol version (skip)

	// data[8] = time_type byte — determines variable-length timestamp.
	timeType := data[8]
	var timestampLen int
	switch timeType {
	case 0:
		timestampLen = 1 // no timestamp, just the type field
	case 1:
		timestampLen = 7 // UTC: type(1) + GPS_seconds(4) + GPS_microseconds(2)
	case 2:
		timestampLen = 5 // VITC: type(1) + hours(1) + minutes(1) + seconds(1) + frames(1)
	case 3:
		timestampLen = 3 // GPI: type(1) + number(1) + edge(1)
	default:
		timestampLen = 1 // treat unknown as no timestamp
	}

	// Verify we have enough data for the full timestamp + num_ops byte.
	minHeader := 8 + timestampLen + 1 // fixed fields + timestamp + num_ops
	if len(data) < minHeader {
		return nil, fmt.Errorf("%w: MOM header requires %d bytes for time_type %d, got %d",
			ErrTooShort, minHeader, timeType, len(data))
	}

	numOps := int(data[8+timestampLen])

	offset := 9 + timestampLen
	for i := 0; i < numOps; i++ {
		// Each operation: opID(2) + data_length(2) + data[data_length]
		if offset+4 > endOffset {
			return nil, fmt.Errorf("%w: operation %d header at offset %d", ErrTooShort, i, offset)
		}

		opID := binary.BigEndian.Uint16(data[offset : offset+2])
		dataLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4

		if offset+dataLen > endOffset {
			return nil, fmt.Errorf("%w: operation %d data needs %d bytes at offset %d, have %d",
				ErrTooShort, i, dataLen, offset, endOffset-offset)
		}

		opData := data[offset : offset+dataLen]
		offset += dataLen

		op, err := decodeOperationData(opID, opData)
		if err != nil {
			return nil, fmt.Errorf("scte104 MOM operation %d: %w", i, err)
		}

		msg.Operations = append(msg.Operations, op)
	}

	return msg, nil
}

// decodeOperationData decodes a single operation's data bytes into an Operation.
func decodeOperationData(opID uint16, data []byte) (Operation, error) {
	op := Operation{OpID: opID}

	switch opID {
	case OpSpliceRequest:
		if len(data) < 14 {
			return op, fmt.Errorf("splice_request_data requires 14 bytes, got %d", len(data))
		}
		srd := &SpliceRequestData{
			SpliceInsertType: data[0],
			SpliceEventID:    binary.BigEndian.Uint32(data[1:5]),
			UniqueProgramID:  binary.BigEndian.Uint16(data[5:7]),
			PreRollTime:      binary.BigEndian.Uint16(data[7:9]),
			BreakDuration:    binary.BigEndian.Uint16(data[9:11]),
			AvailNum:         data[11],
			AvailsExpected:   data[12],
			AutoReturnFlag:   data[13]&0x80 != 0,
		}
		op.Data = srd

	case OpSpliceNull:
		// No payload.
		op.Data = nil

	case OpTimeSignalRequest:
		if len(data) < 2 {
			return op, fmt.Errorf("time_signal_request_data requires 2 bytes, got %d", len(data))
		}
		tsr := &TimeSignalRequestData{
			PreRollTime: binary.BigEndian.Uint16(data[0:2]),
		}
		op.Data = tsr

	case OpSegmentationDescriptorRequest:
		sd, err := decodeSegmentationDescriptor(data)
		if err != nil {
			return op, err
		}
		op.Data = sd

	default:
		// Unknown operation: skip the data, preserve the OpID.
		op.Data = nil
	}

	return op, nil
}

// decodeSegmentationDescriptor decodes a segmentation_descriptor_request payload.
//
// Wire format per SCTE 104 2021 Table 8-29:
//
//	seg_event_id                     uint32  (4 bytes)
//	flags_byte                       uint8   (bit 7 = cancel_indicator, bits 6-1 = reserved, bit 0 = program_segmentation_flag)
//	[if !cancel && !program_seg: component_count(1) + component_tag(1) * N]
//	seg_duration                     uint40  (5 bytes, 90kHz ticks) [if !cancel]
//	upid_type                        uint8   [if !cancel]
//	upid_length                      uint8   [if !cancel]
//	upid                             [upid_length]byte [if !cancel]
//	segmentation_type_id             uint8   [if !cancel]
//	seg_num                          uint8   [if !cancel]
//	segs_expected                    uint8   [if !cancel]
func decodeSegmentationDescriptor(data []byte) (*SegmentationDescriptorRequest, error) {
	// Minimum: seg_event_id(4) + flags(1) = 5 bytes
	if len(data) < 5 {
		return nil, fmt.Errorf("segmentation_descriptor_request requires at least 5 bytes, got %d", len(data))
	}

	sd := &SegmentationDescriptorRequest{
		SegEventID: binary.BigEndian.Uint32(data[0:4]),
	}

	flagsByte := data[4]

	// Check cancel indicator (bit 7).
	if flagsByte&0x80 != 0 {
		sd.CancelIndicator = true
		// Cancel format: just seg_event_id(4) + flags(1) = 5 bytes. No type_id.
		return sd, nil
	}

	sd.ProgramSegmentationFlag = (flagsByte & 0x01) != 0

	offset := 5

	// If not program-level segmentation, skip component data.
	if !sd.ProgramSegmentationFlag {
		if offset >= len(data) {
			return nil, fmt.Errorf("segmentation_descriptor_request: truncated at component_count, got %d bytes", len(data))
		}
		componentCount := int(data[offset])
		offset++
		// Skip component_tag bytes (1 byte per component).
		offset += componentCount
		if offset > len(data) {
			return nil, fmt.Errorf("segmentation_descriptor_request: truncated in component data, got %d bytes", len(data))
		}
	}

	// Need: duration(5) + upid_type(1) + upid_length(1) = 7 bytes minimum
	if offset+7 > len(data) {
		return nil, fmt.Errorf("segmentation_descriptor_request requires at least %d bytes for non-cancel, got %d", offset+7, len(data))
	}

	// 5-byte (40-bit) duration in 90kHz ticks, big-endian.
	sd.DurationTicks = uint64(data[offset])<<32 |
		uint64(data[offset+1])<<24 |
		uint64(data[offset+2])<<16 |
		uint64(data[offset+3])<<8 |
		uint64(data[offset+4])
	offset += 5

	sd.UPIDType = data[offset]
	offset++
	upidLen := int(data[offset])
	offset++

	// Need: upid(N) + type_id(1) + seg_num(1) + segs_expected(1) = N+3 bytes
	expectedEnd := offset + upidLen + 3
	if expectedEnd > len(data) {
		return nil, fmt.Errorf("segmentation_descriptor_request requires %d bytes, got %d", expectedEnd, len(data))
	}

	if upidLen > 0 {
		sd.UPID = make([]byte, upidLen)
		copy(sd.UPID, data[offset:offset+upidLen])
	}
	offset += upidLen

	sd.SegmentationTypeID = data[offset]
	offset++
	sd.SegNum = data[offset]
	offset++
	sd.SegExpected = data[offset]
	offset++

	// Per SCTE 104 2021 Table 8-29 and SCTE-35 Table 22, sub_segment_num
	// and sub_segments_expected follow segs_expected only for certain
	// segmentation types. Parse gracefully only if the type carries
	// sub-segment fields and bytes remain (older senders may omit them).
	if hasSubSegmentFields(sd.SegmentationTypeID) && offset+2 <= len(data) {
		sd.SubSegmentNum = data[offset]
		sd.SubSegmentsExpected = data[offset+1]
	}

	return sd, nil
}
