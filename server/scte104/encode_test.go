package scte104

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncode_NilMessage(t *testing.T) {
	_, err := Encode(nil)
	if err == nil {
		t.Fatal("expected error for nil message")
	}
}

func TestEncode_SpliceNull_RoundTrip(t *testing.T) {
	msg := &Message{
		ProtocolVersion: 0,
		ASIndex:         1,
		MessageNumber:   5,
		DPIPIDIndex:     42,
		Operations: []Operation{
			{OpID: OpSpliceNull},
		},
	}

	data, err := Encode(msg)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if decoded.ASIndex != msg.ASIndex {
		t.Errorf("ASIndex = %d, want %d", decoded.ASIndex, msg.ASIndex)
	}
	if decoded.MessageNumber != msg.MessageNumber {
		t.Errorf("MessageNumber = %d, want %d", decoded.MessageNumber, msg.MessageNumber)
	}
	if decoded.DPIPIDIndex != msg.DPIPIDIndex {
		t.Errorf("DPIPIDIndex = %d, want %d", decoded.DPIPIDIndex, msg.DPIPIDIndex)
	}
	if len(decoded.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(decoded.Operations))
	}
	if decoded.Operations[0].OpID != OpSpliceNull {
		t.Errorf("OpID = 0x%04X, want OpSpliceNull", decoded.Operations[0].OpID)
	}
}

func TestEncode_SpliceRequest_RoundTrip(t *testing.T) {
	original := &Message{
		ProtocolVersion: 0,
		ASIndex:         3,
		MessageNumber:   10,
		DPIPIDIndex:     500,
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartImmediate,
					SpliceEventID:    12345,
					UniqueProgramID:  200,
					PreRollTime:      8000,
					BreakDuration:    600,
					AvailNum:         1,
					AvailsExpected:   4,
					AutoReturnFlag:   true,
				},
			},
		},
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(decoded.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(decoded.Operations))
	}

	srd, ok := decoded.Operations[0].Data.(*SpliceRequestData)
	if !ok {
		t.Fatalf("expected *SpliceRequestData, got %T", decoded.Operations[0].Data)
	}

	orig := original.Operations[0].Data.(*SpliceRequestData)
	if srd.SpliceInsertType != orig.SpliceInsertType {
		t.Errorf("SpliceInsertType = %d, want %d", srd.SpliceInsertType, orig.SpliceInsertType)
	}
	if srd.SpliceEventID != orig.SpliceEventID {
		t.Errorf("SpliceEventID = %d, want %d", srd.SpliceEventID, orig.SpliceEventID)
	}
	if srd.UniqueProgramID != orig.UniqueProgramID {
		t.Errorf("UniqueProgramID = %d, want %d", srd.UniqueProgramID, orig.UniqueProgramID)
	}
	if srd.PreRollTime != orig.PreRollTime {
		t.Errorf("PreRollTime = %d, want %d", srd.PreRollTime, orig.PreRollTime)
	}
	if srd.BreakDuration != orig.BreakDuration {
		t.Errorf("BreakDuration = %d, want %d", srd.BreakDuration, orig.BreakDuration)
	}
	if srd.AvailNum != orig.AvailNum {
		t.Errorf("AvailNum = %d, want %d", srd.AvailNum, orig.AvailNum)
	}
	if srd.AvailsExpected != orig.AvailsExpected {
		t.Errorf("AvailsExpected = %d, want %d", srd.AvailsExpected, orig.AvailsExpected)
	}
	if srd.AutoReturnFlag != orig.AutoReturnFlag {
		t.Errorf("AutoReturnFlag = %v, want %v", srd.AutoReturnFlag, orig.AutoReturnFlag)
	}
}

func TestEncode_TimeSignalRequest_RoundTrip(t *testing.T) {
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{
					PreRollTime: 3000,
				},
			},
		},
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	tsr := decoded.Operations[0].Data.(*TimeSignalRequestData)
	if tsr.PreRollTime != 3000 {
		t.Errorf("PreRollTime = %d, want 3000", tsr.PreRollTime)
	}
}

func TestEncode_SegmentationDescriptor_RoundTrip(t *testing.T) {
	upid := []byte("TEST-UPID-DATA")
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         99999,
					SegmentationTypeID: 0x34,
					DurationTicks:      2700000,
					UPIDType:           0x09,
					UPID:               upid,
					SegNum:             2,
					SegExpected:        5,
					ProgramSegmentationFlag: true,
					CancelIndicator:    false,
				},
			},
		},
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	orig := original.Operations[0].Data.(*SegmentationDescriptorRequest)

	if sd.SegEventID != orig.SegEventID {
		t.Errorf("SegEventID = %d, want %d", sd.SegEventID, orig.SegEventID)
	}
	if sd.SegmentationTypeID != orig.SegmentationTypeID {
		t.Errorf("SegmentationTypeID = 0x%02X, want 0x%02X", sd.SegmentationTypeID, orig.SegmentationTypeID)
	}
	if sd.DurationTicks != orig.DurationTicks {
		t.Errorf("DurationTicks = %d, want %d", sd.DurationTicks, orig.DurationTicks)
	}
	if sd.UPIDType != orig.UPIDType {
		t.Errorf("UPIDType = 0x%02X, want 0x%02X", sd.UPIDType, orig.UPIDType)
	}
	if !bytes.Equal(sd.UPID, orig.UPID) {
		t.Errorf("UPID = %q, want %q", sd.UPID, orig.UPID)
	}
	if sd.SegNum != orig.SegNum {
		t.Errorf("SegNum = %d, want %d", sd.SegNum, orig.SegNum)
	}
	if sd.SegExpected != orig.SegExpected {
		t.Errorf("SegExpected = %d, want %d", sd.SegExpected, orig.SegExpected)
	}
}

func TestEncode_SegmentationDescriptor_Cancel_RoundTrip(t *testing.T) {
	// Per SCTE 104 2021: cancel format is seg_event_id(4) + flags(1) = 5 bytes.
	// No type_id in cancel messages.
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         555,
					SegmentationTypeID: 0x35, // will be lost in cancel (not encoded)
					CancelIndicator:    true,
				},
			},
		},
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	if !sd.CancelIndicator {
		t.Error("CancelIndicator should be true")
	}
	if sd.SegEventID != 555 {
		t.Errorf("SegEventID = %d, want 555", sd.SegEventID)
	}
	// type_id is NOT encoded in cancel format per spec, so it's zero after decode.
	if sd.SegmentationTypeID != 0 {
		t.Errorf("SegmentationTypeID = 0x%02X, want 0x00 (not in cancel)", sd.SegmentationTypeID)
	}
}

func TestEncode_MultipleOps_RoundTrip(t *testing.T) {
	original := &Message{
		ProtocolVersion: 0,
		ASIndex:         7,
		MessageNumber:   22,
		DPIPIDIndex:     1500,
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{PreRollTime: 5000},
			},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         1001,
					SegmentationTypeID: 0x34,
					DurationTicks:      900000,
					UPIDType:           0x01,
					UPID:               []byte{0xAB, 0xCD},
					SegNum:             1,
					SegExpected:        1,
					ProgramSegmentationFlag: true,
				},
			},
			{OpID: OpSpliceNull},
		},
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(decoded.Operations) != 3 {
		t.Fatalf("expected 3 operations, got %d", len(decoded.Operations))
	}

	if decoded.Operations[0].OpID != OpTimeSignalRequest {
		t.Errorf("op[0].OpID = 0x%04X, want OpTimeSignalRequest", decoded.Operations[0].OpID)
	}
	if decoded.Operations[1].OpID != OpSegmentationDescriptorRequest {
		t.Errorf("op[1].OpID = 0x%04X, want OpSegmentationDescriptorRequest", decoded.Operations[1].OpID)
	}
	if decoded.Operations[2].OpID != OpSpliceNull {
		t.Errorf("op[2].OpID = 0x%04X, want OpSpliceNull", decoded.Operations[2].OpID)
	}
}

func TestEncode_UnsupportedOpID(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{OpID: 0x9999},
		},
	}

	_, err := Encode(msg)
	if err == nil {
		t.Fatal("expected error for unsupported OpID")
	}
}

func TestEncode_WrongDataType(t *testing.T) {
	tests := []struct {
		name string
		op   Operation
	}{
		{
			name: "splice_request with wrong type",
			op: Operation{
				OpID: OpSpliceRequest,
				Data: &TimeSignalRequestData{},
			},
		},
		{
			name: "time_signal with wrong type",
			op: Operation{
				OpID: OpTimeSignalRequest,
				Data: &SpliceRequestData{},
			},
		},
		{
			name: "seg_descriptor with wrong type",
			op: Operation{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SpliceRequestData{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{
				Operations: []Operation{tt.op},
			}
			_, err := Encode(msg)
			if err == nil {
				t.Fatal("expected error for wrong data type")
			}
		})
	}
}

func TestEncode_EmptyOperations(t *testing.T) {
	msg := &Message{
		Operations: []Operation{},
	}

	data, err := Encode(msg)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(decoded.Operations) != 0 {
		t.Errorf("expected 0 operations, got %d", len(decoded.Operations))
	}
}

func TestEncode_SegmentationDescriptor_EmptyUPID_RoundTrip(t *testing.T) {
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         42,
					SegmentationTypeID: 0x30,
					DurationTicks:      0,
					UPIDType:           0x00,
					UPID:               nil,
					SegNum:             0,
					SegExpected:        0,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	if sd.SegEventID != 42 {
		t.Errorf("SegEventID = %d, want 42", sd.SegEventID)
	}
	if len(sd.UPID) != 0 {
		t.Errorf("UPID length = %d, want 0", len(sd.UPID))
	}
}

func TestEncode_TooManyOperations(t *testing.T) {
	ops := make([]Operation, 256)
	for i := range ops {
		ops[i] = Operation{OpID: OpSpliceNull}
	}
	msg := &Message{Operations: ops}

	_, err := Encode(msg)
	if err == nil {
		t.Fatal("expected error for >255 operations")
	}
}

func TestEncode_DurationTicksExceeds40Bit(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         1,
					SegmentationTypeID: 0x34,
					DurationTicks:      0x10000000000, // exceeds 40-bit
					UPIDType:           0,
					SegNum:             0,
					SegExpected:        0,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	_, err := Encode(msg)
	if err == nil {
		t.Fatal("expected error for DurationTicks exceeding 40-bit maximum")
	}
}

func TestEncode_SegmentationDescriptor_FullTypeRange(t *testing.T) {
	// Per SCTE 104 2021, type_id is a full 8-bit field (not packed with cancel).
	// Verify all values 0x00-0xFF are accepted.
	for _, typeID := range []uint8{0x00, 0x34, 0x7F, 0x80, 0xFE, 0xFF} {
		msg := &Message{
			Operations: []Operation{
				{
					OpID: OpSegmentationDescriptorRequest,
					Data: &SegmentationDescriptorRequest{
						SegEventID:         1,
						SegmentationTypeID: typeID,
						UPIDType:           0,
						SegNum:             0,
						SegExpected:        0,
					ProgramSegmentationFlag: true,
					},
				},
			},
		}

		data, err := Encode(msg)
		if err != nil {
			t.Fatalf("encode error for type_id 0x%02X: %v", typeID, err)
		}

		decoded, err := Decode(data)
		if err != nil {
			t.Fatalf("decode error for type_id 0x%02X: %v", typeID, err)
		}

		sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
		if sd.SegmentationTypeID != typeID {
			t.Errorf("type_id 0x%02X round-trip: got 0x%02X", typeID, sd.SegmentationTypeID)
		}
	}
}

func TestEncode_LargeDurationTicks(t *testing.T) {
	// Test a 40-bit duration value close to the maximum.
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         1,
					SegmentationTypeID: 0x34,
					DurationTicks:      0xFFFFFFFFFF, // max 40-bit value
					UPIDType:           0,
					SegNum:             0,
					SegExpected:        0,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	if sd.DurationTicks != 0xFFFFFFFFFF {
		t.Errorf("DurationTicks = 0x%X, want 0xFFFFFFFFFF", sd.DurationTicks)
	}
}

func TestEncode_MessageSize_ExcludesSelf(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{OpID: OpSpliceNull},
		},
	}

	data, err := Encode(msg)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	// Wire: OpID(2) + messageSize(2) + fields(8) + ops
	// messageSize should equal total - 4 (excludes OpID + messageSize itself).
	if len(data) < 4 {
		t.Fatalf("encoded data too short: %d", len(data))
	}
	messageSize := binary.BigEndian.Uint16(data[2:4])
	expectedSize := uint16(len(data) - 4) // everything after messageSize
	if messageSize != expectedSize {
		t.Errorf("messageSize = %d, want %d (total %d - 4)", messageSize, expectedSize, len(data))
	}
}

func TestEncode_SegmentationDescriptor_ComponentLevelRejected(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              1,
					SegmentationTypeID:      0x34,
					ProgramSegmentationFlag: false, // component-level
				},
			},
		},
	}

	_, err := Encode(msg)
	if err == nil {
		t.Fatal("expected error for component-level segmentation encoding")
	}
}

func TestEncode_AutoReturnFlag_Bit7(t *testing.T) {
	// Per SCTE-104 spec, auto_return_flag is bit 7 (MSB) of byte 13
	// in splice_request_data. Verify encoding writes 0x80, not 0x01.
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartImmediate,
					SpliceEventID:    1,
					AutoReturnFlag:   true,
				},
			},
		},
	}

	data, err := Encode(msg)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	// MOM wire format: OpID(2) + messageSize(2) + fields(8) + num_ops(1=included in fields)
	// Then operation: opID(2) + data_length(2) + splice_request_data(14)
	// splice_request_data starts at offset 16 (after MOM header 12 + op header 4).
	// auto_return_flag is byte 13 within the 14-byte splice_request_data.
	spliceDataOffset := 12 + 4 // MOM header(12) + op header(4)
	autoReturnByte := data[spliceDataOffset+13]

	if autoReturnByte != 0x80 {
		t.Errorf("auto_return_flag byte = 0x%02X, want 0x80 (bit 7)", autoReturnByte)
	}

	// Also verify that when AutoReturnFlag is false, the byte is 0x00.
	msg.Operations[0].Data.(*SpliceRequestData).AutoReturnFlag = false
	data2, err := Encode(msg)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	if data2[spliceDataOffset+13] != 0x00 {
		t.Errorf("auto_return_flag byte (false) = 0x%02X, want 0x00", data2[spliceDataOffset+13])
	}
}

func TestEncode_SegmentationDescriptor_SubSegments_RoundTrip(t *testing.T) {
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              42,
					SegmentationTypeID:      0x34,
					DurationTicks:           2700000,
					UPIDType:                0x09,
					UPID:                    []byte("TEST"),
					SegNum:                  1,
					SegExpected:             4,
					SubSegmentNum:           2,
					SubSegmentsExpected:     3,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	if sd.SubSegmentNum != 2 {
		t.Errorf("SubSegmentNum = %d, want 2", sd.SubSegmentNum)
	}
	if sd.SubSegmentsExpected != 3 {
		t.Errorf("SubSegmentsExpected = %d, want 3", sd.SubSegmentsExpected)
	}
	// Verify other fields survived.
	if sd.SegEventID != 42 {
		t.Errorf("SegEventID = %d, want 42", sd.SegEventID)
	}
	if sd.SegNum != 1 {
		t.Errorf("SegNum = %d, want 1", sd.SegNum)
	}
	if sd.SegExpected != 4 {
		t.Errorf("SegExpected = %d, want 4", sd.SegExpected)
	}
}

func TestEncode_SegmentationDescriptor_ZeroSubSegments_NotEncoded(t *testing.T) {
	// When SubSegmentNum and SubSegmentsExpected are both 0,
	// the extra 2 bytes should NOT be encoded (backward compat).
	withSub := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              1,
					SegmentationTypeID:      0x34,
					SubSegmentNum:           1,
					SubSegmentsExpected:     2,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}
	withoutSub := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              1,
					SegmentationTypeID:      0x34,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	dataWith, err := Encode(withSub)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	dataWithout, err := Encode(withoutSub)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	// With sub-segments should be 2 bytes longer.
	if len(dataWith) != len(dataWithout)+2 {
		t.Errorf("len(withSub)=%d, len(withoutSub)=%d, expected difference of 2",
			len(dataWith), len(dataWithout))
	}
}
