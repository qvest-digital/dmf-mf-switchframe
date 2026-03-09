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
// consumed; payload contains the operation data bytes.
func decodeSOM(opID uint16, payload []byte) (*Message, error) {
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
//	timestamp            uint8   (skip/ignore)
//	num_ops              uint8
//	[operations...]
func decodeMOM(data []byte) (*Message, error) {
	// Minimum MOM header: messageSize(2) + protocolVersion(1) + AS_index(1) +
	// message_number(1) + DPI_PID_index(2) + SCTE35_protocol_version(1) +
	// timestamp(1) + num_ops(1) = 10 bytes
	if len(data) < 10 {
		return nil, fmt.Errorf("%w: MOM header requires 10 bytes, got %d", ErrTooShort, len(data))
	}

	// messageSize is informational; we parse based on actual data length.
	_ = binary.BigEndian.Uint16(data[0:2])

	msg := &Message{
		ProtocolVersion: data[2],
		ASIndex:         data[3],
		MessageNumber:   data[4],
		DPIPIDIndex:     binary.BigEndian.Uint16(data[5:7]),
	}

	// data[7] = SCTE35 protocol version (skip)
	// data[8] = timestamp (skip)
	numOps := int(data[9])

	offset := 10
	for i := 0; i < numOps; i++ {
		// Each operation: opID(2) + data_length(2) + data[data_length]
		if offset+4 > len(data) {
			return nil, fmt.Errorf("%w: operation %d header at offset %d", ErrTooShort, i, offset)
		}

		opID := binary.BigEndian.Uint16(data[offset : offset+2])
		dataLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4

		if offset+dataLen > len(data) {
			return nil, fmt.Errorf("%w: operation %d data needs %d bytes at offset %d, have %d",
				ErrTooShort, i, dataLen, offset, len(data)-offset)
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
			AutoReturnFlag:   data[13] != 0,
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
// Wire format:
//
//	seg_event_id            uint32
//	segmentation_type_id    uint8   (bit 7 = cancel indicator)
//	seg_duration            uint40  (5 bytes, 90kHz ticks)
//	upid_type               uint8
//	upid_length             uint8
//	upid                    [upid_length]byte
//	seg_num                 uint8
//	segs_expected           uint8
func decodeSegmentationDescriptor(data []byte) (*SegmentationDescriptorRequest, error) {
	// Minimum: seg_event_id(4) + type/cancel(1) = 5 bytes
	if len(data) < 5 {
		return nil, fmt.Errorf("segmentation_descriptor_request requires at least 5 bytes, got %d", len(data))
	}

	sd := &SegmentationDescriptorRequest{
		SegEventID: binary.BigEndian.Uint32(data[0:4]),
	}

	typeField := data[4]

	// Check cancel indicator (bit 7).
	if typeField&0x80 != 0 {
		sd.CancelIndicator = true
		sd.SegmentationTypeID = typeField & 0x7F
		return sd, nil
	}

	sd.SegmentationTypeID = typeField

	// After the type byte: duration(5) + upid_type(1) + upid_length(1) = 7 more bytes minimum
	if len(data) < 12 {
		return nil, fmt.Errorf("segmentation_descriptor_request requires at least 12 bytes for non-cancel, got %d", len(data))
	}

	// 5-byte (40-bit) duration in 90kHz ticks, big-endian.
	sd.DurationTicks = uint64(data[5])<<32 |
		uint64(data[6])<<24 |
		uint64(data[7])<<16 |
		uint64(data[8])<<8 |
		uint64(data[9])

	sd.UPIDType = data[10]
	upidLen := int(data[11])

	expectedLen := 12 + upidLen + 2 // +2 for seg_num and segs_expected
	if len(data) < expectedLen {
		return nil, fmt.Errorf("segmentation_descriptor_request requires %d bytes, got %d", expectedLen, len(data))
	}

	if upidLen > 0 {
		sd.UPID = make([]byte, upidLen)
		copy(sd.UPID, data[12:12+upidLen])
	}

	sd.SegNum = data[12+upidLen]
	sd.SegExpected = data[12+upidLen+1]

	return sd, nil
}
