package scte104

import (
	"encoding/binary"
	"fmt"
)

// Encode serializes a Message into SCTE-104 binary format.
//
// The output is always a Multiple Operation Message (MOM) with OpID=0xFFFF,
// even for single operations. This simplifies downstream parsing and matches
// typical automation system behavior.
func Encode(msg *Message) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("scte104: cannot encode nil message")
	}
	if len(msg.Operations) > 255 {
		return nil, fmt.Errorf("scte104: too many operations: %d (max 255)", len(msg.Operations))
	}

	// Serialize all operations first to compute total size.
	var opsData []byte
	for i, op := range msg.Operations {
		opBytes, err := encodeOperation(op)
		if err != nil {
			return nil, fmt.Errorf("scte104 encode operation %d: %w", i, err)
		}
		opsData = append(opsData, opBytes...)
	}

	// MOM fields after messageSize: protocolVersion(1) + AS_index(1) +
	// message_number(1) + DPI_PID_index(2) + SCTE35_protocol_version(1) +
	// timestamp(1) + num_ops(1) = 8 bytes.
	// messageSize counts the remaining bytes after itself (excludes its own 2 bytes).
	const fieldsAfterMsgSize = 8
	messageSize := uint16(fieldsAfterMsgSize + len(opsData))

	// Total wire size: OpID(2) + messageSize(2) + fieldsAfterMsgSize + ops
	buf := make([]byte, 2+2+int(messageSize))

	// OpID = 0xFFFF (MOM)
	binary.BigEndian.PutUint16(buf[0:2], OpMultipleOperationMessage)

	// messageSize
	binary.BigEndian.PutUint16(buf[2:4], messageSize)

	// protocolVersion
	buf[4] = msg.ProtocolVersion

	// AS_index
	buf[5] = msg.ASIndex

	// message_number
	buf[6] = msg.MessageNumber

	// DPI_PID_index
	binary.BigEndian.PutUint16(buf[7:9], msg.DPIPIDIndex)

	// SCTE35_protocol_version (always 0)
	buf[9] = 0

	// timestamp: SCTE-104 defines a multi-byte timestamp structure, but many
	// implementations use a single zero byte (no timestamp). This simplification
	// is widely accepted by downstream splicer equipment.
	buf[10] = 0

	// num_ops
	buf[11] = uint8(len(msg.Operations))

	// Append serialized operations.
	copy(buf[12:], opsData)

	return buf, nil
}

// encodeOperation serializes a single operation to its wire format:
// opID(2) + data_length(2) + data[data_length].
func encodeOperation(op Operation) ([]byte, error) {
	var payload []byte
	var err error

	switch op.OpID {
	case OpSpliceRequest:
		payload, err = encodeSpliceRequest(op.Data)
	case OpSpliceNull:
		payload = nil // No payload.
	case OpTimeSignalRequest:
		payload, err = encodeTimeSignalRequest(op.Data)
	case OpSegmentationDescriptorRequest:
		payload, err = encodeSegmentationDescriptor(op.Data)
	default:
		return nil, fmt.Errorf("unsupported operation ID: 0x%04X", op.OpID)
	}
	if err != nil {
		return nil, err
	}

	// opID(2) + data_length(2) + payload
	buf := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint16(buf[0:2], op.OpID)
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(payload)))
	copy(buf[4:], payload)

	return buf, nil
}

// encodeSpliceRequest serializes SpliceRequestData to 14 bytes.
func encodeSpliceRequest(data any) ([]byte, error) {
	srd, ok := data.(*SpliceRequestData)
	if !ok {
		return nil, fmt.Errorf("splice_request: expected *SpliceRequestData, got %T", data)
	}

	buf := make([]byte, 14)
	buf[0] = srd.SpliceInsertType
	binary.BigEndian.PutUint32(buf[1:5], srd.SpliceEventID)
	binary.BigEndian.PutUint16(buf[5:7], srd.UniqueProgramID)
	binary.BigEndian.PutUint16(buf[7:9], srd.PreRollTime)
	binary.BigEndian.PutUint16(buf[9:11], srd.BreakDuration)
	buf[11] = srd.AvailNum
	buf[12] = srd.AvailsExpected
	if srd.AutoReturnFlag {
		buf[13] = 1
	}

	return buf, nil
}

// encodeTimeSignalRequest serializes TimeSignalRequestData to 2 bytes.
func encodeTimeSignalRequest(data any) ([]byte, error) {
	tsr, ok := data.(*TimeSignalRequestData)
	if !ok {
		return nil, fmt.Errorf("time_signal_request: expected *TimeSignalRequestData, got %T", data)
	}

	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf[0:2], tsr.PreRollTime)

	return buf, nil
}

// encodeSegmentationDescriptor serializes a SegmentationDescriptorRequest.
//
// Wire format per SCTE 104 2021 Table 8-29:
//
//	Cancel:     seg_event_id(4) + flags_byte(1) = 5 bytes
//	Non-cancel: seg_event_id(4) + flags_byte(1) + duration(5) + upid_type(1) +
//	            upid_length(1) + upid[N] + type_id(1) + seg_num(1) + segs_expected(1)
func encodeSegmentationDescriptor(data any) ([]byte, error) {
	sd, ok := data.(*SegmentationDescriptorRequest)
	if !ok {
		return nil, fmt.Errorf("segmentation_descriptor: expected *SegmentationDescriptorRequest, got %T", data)
	}

	if sd.CancelIndicator {
		// Cancel: seg_event_id(4) + flags_byte(1) = 5 bytes
		buf := make([]byte, 5)
		binary.BigEndian.PutUint32(buf[0:4], sd.SegEventID)
		buf[4] = 0x80 // cancel=1, reserved=0, program_seg_flag=0
		return buf, nil
	}

	// Non-cancel: seg_event_id(4) + flags(1) + duration(5) + upid_type(1) +
	// upid_length(1) + upid[N] + type_id(1) + seg_num(1) + segs_expected(1) = 15 + N
	upidLen := len(sd.UPID)
	buf := make([]byte, 15+upidLen)

	binary.BigEndian.PutUint32(buf[0:4], sd.SegEventID)
	// flags_byte: cancel=0, reserved=0, program_segmentation_flag=1 (always program-level)
	buf[4] = 0x01

	// 5-byte (40-bit) duration in 90kHz ticks, big-endian.
	if sd.DurationTicks > 0xFFFFFFFFFF {
		return nil, fmt.Errorf("segmentation_descriptor: DurationTicks 0x%X exceeds 40-bit maximum", sd.DurationTicks)
	}
	buf[5] = byte(sd.DurationTicks >> 32)
	buf[6] = byte(sd.DurationTicks >> 24)
	buf[7] = byte(sd.DurationTicks >> 16)
	buf[8] = byte(sd.DurationTicks >> 8)
	buf[9] = byte(sd.DurationTicks)

	buf[10] = sd.UPIDType
	buf[11] = byte(upidLen)

	copy(buf[12:12+upidLen], sd.UPID)

	buf[12+upidLen] = sd.SegmentationTypeID
	buf[12+upidLen+1] = sd.SegNum
	buf[12+upidLen+2] = sd.SegExpected

	return buf, nil
}
