package scte104

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Encode serializes a Message into SCTE-104 binary format.
//
// The output is always a Multiple Operation Message (MOM) with OpID=0xFFFF,
// even for single operations. This simplifies downstream parsing and matches
// typical automation system behavior.
func Encode(msg *Message) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("scte104: cannot encode nil message")
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
		// auto_return_flag is a full 8-bit field with value 1 per SCTE 104
		// Table 8-5 (not a bit-7 flag).
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
// Wire format per SCTE 104 Table 8-29 (byte-aligned, all fields always
// present — including for cancel messages):
//
//	segmentation_event_id(4) + cancel_indicator(1) + duration(2, seconds) +
//	upid_type(1) + upid_length(1) + upid[N] + segmentation_type_id(1) +
//	segment_num(1) + segments_expected(1) + duration_extension_frames(1) +
//	delivery_not_restricted_flag(1) + web_delivery_allowed_flag(1) +
//	no_regional_blackout_flag(1) + archive_allowed_flag(1) +
//	device_restrictions(1) = 18 + N bytes
func encodeSegmentationDescriptor(data any) ([]byte, error) {
	sd, ok := data.(*SegmentationDescriptorRequest)
	if !ok {
		return nil, fmt.Errorf("segmentation_descriptor: expected *SegmentationDescriptorRequest, got %T", data)
	}

	upidLen := len(sd.UPID)
	if upidLen > 255 {
		return nil, fmt.Errorf("segmentation_descriptor: UPID length %d exceeds 255", upidLen)
	}

	bool01 := func(b bool) byte {
		if b {
			return 1
		}
		return 0
	}

	buf := make([]byte, 18+upidLen)

	binary.BigEndian.PutUint32(buf[0:4], sd.SegEventID)
	buf[4] = bool01(sd.CancelIndicator)
	binary.BigEndian.PutUint16(buf[5:7], sd.Duration) // seconds
	buf[7] = sd.UPIDType
	buf[8] = byte(upidLen)
	copy(buf[9:9+upidLen], sd.UPID)

	o := 9 + upidLen
	buf[o] = sd.SegmentationTypeID
	buf[o+1] = sd.SegNum
	buf[o+2] = sd.SegExpected
	buf[o+3] = sd.DurationExtensionFrames
	buf[o+4] = bool01(sd.DeliveryNotRestricted)
	buf[o+5] = bool01(sd.WebDeliveryAllowed)
	buf[o+6] = bool01(sd.NoRegionalBlackout)
	buf[o+7] = bool01(sd.ArchiveAllowed)
	buf[o+8] = sd.DeviceRestrictions

	return buf, nil
}
