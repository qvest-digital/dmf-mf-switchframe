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
//   - Full SOM format: an 11-byte header followed by the operation data.
//
// Full SOM header per SCTE 104 (11 bytes after the opID):
//
//	messageSize(2) + result(2) + result_extension(2) +
//	protocol_version(1) + AS_index(1) + message_number(1) + DPI_PID_index(2)
//
// Unlike the multiple_operation_message header, the SOM header carries no
// SCTE35_protocol_version byte; operation data follows DPI_PID_index directly.
//
// Detection heuristic: per SCTE 104, messageSize is the size of the COMPLETE
// message including the 2-byte opID, i.e. len(payload)+2. If the field
// matches that convention (and the protocol_version byte is plausible),
// treat the payload as a full SOM with the header; otherwise it is the
// abbreviated VANC form where the operation data starts immediately.
func decodeSOM(opID uint16, payload []byte) (*Message, error) {
	// Check if this is a full SOM with header fields. The smallest full SOM
	// payload is the bare 11-byte header (e.g. splice_null with no op data).
	if len(payload) >= 11 {
		messageSize := int(binary.BigEndian.Uint16(payload[0:2]))
		// Additional validation: check protocol version to avoid false
		// positives when abbreviated payload bytes accidentally match the
		// length. SCTE-104 protocol versions are small integers (0-3 in
		// current revisions); anything above 15 is clearly not a SOM header.
		if messageSize == len(payload)+2 && payload[6] <= 15 {
			// Full SOM format: parse the 11-byte header, then operation data.
			msg := &Message{
				ProtocolVersion: payload[6],
				ASIndex:         payload[7],
				MessageNumber:   payload[8],
				DPIPIDIndex:     binary.BigEndian.Uint16(payload[9:11]),
			}
			op, err := decodeOperationData(opID, payload[11:])
			if err != nil {
				return nil, fmt.Errorf("scte104 SOM: %w", err)
			}
			msg.Operations = []Operation{op}
			return msg, nil
		}
	}

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
			// auto_return_flag is a full 8-bit field per SCTE 104 Table 8-5;
			// spec senders write the value 1. Accept any non-zero value for
			// tolerance of legacy bit-7 encoders.
			AutoReturnFlag: data[13] != 0,
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

// decodeSegmentationDescriptor decodes an insert_segmentation_descriptor_request
// payload.
//
// Wire format per SCTE 104 Table 8-29 (byte-aligned; every field is present
// regardless of the cancel indicator):
//
//	segmentation_event_id               uint32  (4 bytes)
//	segmentation_event_cancel_indicator uint8   (full byte, value 0/1)
//	duration                            uint16  (2 bytes, in SECONDS)
//	segmentation_upid_type              uint8
//	segmentation_upid_length            uint8
//	segmentation_upid                   [upid_length]byte
//	segmentation_type_id                uint8
//	segment_num                         uint8
//	segments_expected                   uint8
//	duration_extension_frames           uint8
//	delivery_not_restricted_flag        uint8
//	web_delivery_allowed_flag           uint8
//	no_regional_blackout_flag           uint8
//	archive_allowed_flag                uint8
//	device_restrictions                 uint8
//
// Note this is NOT the SCTE-35 segmentation_descriptor() bit layout: there is
// no bit-packed flags byte, no program_segmentation_flag, no component loop
// and no 40-bit 90 kHz duration in SCTE 104.
//
// The six trailing bytes (duration_extension_frames + the five delivery
// restriction fields) are parsed leniently: some older automation systems
// omit them, in which case "delivery not restricted" is assumed.
func decodeSegmentationDescriptor(data []byte) (*SegmentationDescriptorRequest, error) {
	// Fixed prefix: event_id(4) + cancel(1) + duration(2) + upid_type(1) +
	// upid_length(1) = 9 bytes.
	if len(data) < 9 {
		return nil, fmt.Errorf("segmentation_descriptor_request requires at least 9 bytes, got %d", len(data))
	}

	sd := &SegmentationDescriptorRequest{
		SegEventID:      binary.BigEndian.Uint32(data[0:4]),
		CancelIndicator: data[4] != 0,
		Duration:        binary.BigEndian.Uint16(data[5:7]),
		UPIDType:        data[7],
	}

	upidLen := int(data[8])
	offset := 9

	// Need: upid(N) + type_id(1) + segment_num(1) + segments_expected(1).
	if offset+upidLen+3 > len(data) {
		return nil, fmt.Errorf("segmentation_descriptor_request requires %d bytes, got %d",
			offset+upidLen+3, len(data))
	}

	if upidLen > 0 {
		sd.UPID = make([]byte, upidLen)
		copy(sd.UPID, data[offset:offset+upidLen])
	}
	offset += upidLen

	sd.SegmentationTypeID = data[offset]
	sd.SegNum = data[offset+1]
	sd.SegExpected = data[offset+2]
	offset += 3

	// duration_extension_frames (lenient: may be absent in older senders).
	if offset < len(data) {
		sd.DurationExtensionFrames = data[offset]
		offset++
	}

	// Five delivery restriction bytes (lenient: absent means unrestricted).
	if offset+5 <= len(data) {
		sd.DeliveryNotRestricted = data[offset] != 0
		sd.WebDeliveryAllowed = data[offset+1] != 0
		sd.NoRegionalBlackout = data[offset+2] != 0
		sd.ArchiveAllowed = data[offset+3] != 0
		sd.DeviceRestrictions = data[offset+4]
	} else {
		sd.DeliveryNotRestricted = true
	}

	return sd, nil
}
