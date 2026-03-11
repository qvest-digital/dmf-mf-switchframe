package scte104

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncode_NilMessage(t *testing.T) {
	_, err := Encode(nil)
	require.Error(t, err)
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
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, msg.ASIndex, decoded.ASIndex)
	require.Equal(t, msg.MessageNumber, decoded.MessageNumber)
	require.Equal(t, msg.DPIPIDIndex, decoded.DPIPIDIndex)
	require.Len(t, decoded.Operations, 1)
	require.Equal(t, uint16(OpSpliceNull), decoded.Operations[0].OpID)
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
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, decoded.Operations, 1)

	require.IsType(t, &SpliceRequestData{}, decoded.Operations[0].Data)
	srd := decoded.Operations[0].Data.(*SpliceRequestData)

	orig := original.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, orig.SpliceInsertType, srd.SpliceInsertType)
	require.Equal(t, orig.SpliceEventID, srd.SpliceEventID)
	require.Equal(t, orig.UniqueProgramID, srd.UniqueProgramID)
	require.Equal(t, orig.PreRollTime, srd.PreRollTime)
	require.Equal(t, orig.BreakDuration, srd.BreakDuration)
	require.Equal(t, orig.AvailNum, srd.AvailNum)
	require.Equal(t, orig.AvailsExpected, srd.AvailsExpected)
	require.Equal(t, orig.AutoReturnFlag, srd.AutoReturnFlag)
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
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	tsr := decoded.Operations[0].Data.(*TimeSignalRequestData)
	require.Equal(t, uint16(3000), tsr.PreRollTime)
}

func TestEncode_SegmentationDescriptor_RoundTrip(t *testing.T) {
	upid := []byte("TEST-UPID-DATA")
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              99999,
					SegmentationTypeID:      0x34,
					DurationTicks:           2700000,
					UPIDType:                0x09,
					UPID:                    upid,
					SegNum:                  2,
					SegExpected:             5,
					ProgramSegmentationFlag: true,
					CancelIndicator:         false,
				},
			},
		},
	}

	data, err := Encode(original)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	orig := original.Operations[0].Data.(*SegmentationDescriptorRequest)

	require.Equal(t, orig.SegEventID, sd.SegEventID)
	require.Equal(t, orig.SegmentationTypeID, sd.SegmentationTypeID)
	require.Equal(t, orig.DurationTicks, sd.DurationTicks)
	require.Equal(t, orig.UPIDType, sd.UPIDType)
	require.Equal(t, orig.UPID, sd.UPID)
	require.Equal(t, orig.SegNum, sd.SegNum)
	require.Equal(t, orig.SegExpected, sd.SegExpected)
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
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.True(t, sd.CancelIndicator, "CancelIndicator should be true")
	require.Equal(t, uint32(555), sd.SegEventID)
	// type_id is NOT encoded in cancel format per spec, so it's zero after decode.
	require.Equal(t, uint8(0), sd.SegmentationTypeID)
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
					SegEventID:              1001,
					SegmentationTypeID:      0x34,
					DurationTicks:           900000,
					UPIDType:                0x01,
					UPID:                    []byte{0xAB, 0xCD},
					SegNum:                  1,
					SegExpected:             1,
					ProgramSegmentationFlag: true,
				},
			},
			{OpID: OpSpliceNull},
		},
	}

	data, err := Encode(original)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, decoded.Operations, 3)

	require.Equal(t, uint16(OpTimeSignalRequest), decoded.Operations[0].OpID)
	require.Equal(t, uint16(OpSegmentationDescriptorRequest), decoded.Operations[1].OpID)
	require.Equal(t, uint16(OpSpliceNull), decoded.Operations[2].OpID)
}

func TestEncode_UnsupportedOpID(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{OpID: 0x9999},
		},
	}

	_, err := Encode(msg)
	require.Error(t, err)
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
			require.Error(t, err)
		})
	}
}

func TestEncode_EmptyOperations(t *testing.T) {
	msg := &Message{
		Operations: []Operation{},
	}

	data, err := Encode(msg)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, decoded.Operations, 0)
}

func TestEncode_SegmentationDescriptor_EmptyUPID_RoundTrip(t *testing.T) {
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              42,
					SegmentationTypeID:      0x30,
					DurationTicks:           0,
					UPIDType:                0x00,
					UPID:                    nil,
					SegNum:                  0,
					SegExpected:             0,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	data, err := Encode(original)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(42), sd.SegEventID)
	require.Len(t, sd.UPID, 0)
}

func TestEncode_TooManyOperations(t *testing.T) {
	ops := make([]Operation, 256)
	for i := range ops {
		ops[i] = Operation{OpID: OpSpliceNull}
	}
	msg := &Message{Operations: ops}

	_, err := Encode(msg)
	require.Error(t, err)
}

func TestEncode_DurationTicksExceeds40Bit(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              1,
					SegmentationTypeID:      0x34,
					DurationTicks:           0x10000000000, // exceeds 40-bit
					UPIDType:                0,
					SegNum:                  0,
					SegExpected:             0,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	_, err := Encode(msg)
	require.Error(t, err)
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
						SegEventID:              1,
						SegmentationTypeID:      typeID,
						UPIDType:                0,
						SegNum:                  0,
						SegExpected:             0,
						ProgramSegmentationFlag: true,
					},
				},
			},
		}

		data, err := Encode(msg)
		require.NoError(t, err, "encode error for type_id 0x%02X", typeID)

		decoded, err := Decode(data)
		require.NoError(t, err, "decode error for type_id 0x%02X", typeID)

		sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
		require.Equal(t, typeID, sd.SegmentationTypeID)
	}
}

func TestEncode_LargeDurationTicks(t *testing.T) {
	// Test a 40-bit duration value close to the maximum.
	original := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              1,
					SegmentationTypeID:      0x34,
					DurationTicks:           0xFFFFFFFFFF, // max 40-bit value
					UPIDType:                0,
					SegNum:                  0,
					SegExpected:             0,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	data, err := Encode(original)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint64(0xFFFFFFFFFF), sd.DurationTicks)
}

func TestEncode_MessageSize_ExcludesSelf(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{OpID: OpSpliceNull},
		},
	}

	data, err := Encode(msg)
	require.NoError(t, err)

	// Wire: OpID(2) + messageSize(2) + fields(8) + ops
	// messageSize should equal total - 4 (excludes OpID + messageSize itself).
	require.True(t, len(data) >= 4, "encoded data too short: %d", len(data))
	messageSize := binary.BigEndian.Uint16(data[2:4])
	expectedSize := uint16(len(data) - 4) // everything after messageSize
	require.Equal(t, expectedSize, messageSize)
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
	require.Error(t, err)
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
	require.NoError(t, err)

	// MOM wire format: OpID(2) + messageSize(2) + fields(8) + num_ops(1=included in fields)
	// Then operation: opID(2) + data_length(2) + splice_request_data(14)
	// splice_request_data starts at offset 16 (after MOM header 12 + op header 4).
	// auto_return_flag is byte 13 within the 14-byte splice_request_data.
	spliceDataOffset := 12 + 4 // MOM header(12) + op header(4)
	autoReturnByte := data[spliceDataOffset+13]

	require.Equal(t, byte(0x80), autoReturnByte)

	// Also verify that when AutoReturnFlag is false, the byte is 0x00.
	msg.Operations[0].Data.(*SpliceRequestData).AutoReturnFlag = false
	data2, err := Encode(msg)
	require.NoError(t, err)
	require.Equal(t, byte(0x00), data2[spliceDataOffset+13])
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
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	sd := decoded.Operations[0].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint8(2), sd.SubSegmentNum)
	require.Equal(t, uint8(3), sd.SubSegmentsExpected)
	// Verify other fields survived.
	require.Equal(t, uint32(42), sd.SegEventID)
	require.Equal(t, uint8(1), sd.SegNum)
	require.Equal(t, uint8(4), sd.SegExpected)
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
	require.NoError(t, err)
	dataWithout, err := Encode(withoutSub)
	require.NoError(t, err)

	// With sub-segments should be 2 bytes longer.
	require.Equal(t, len(dataWithout)+2, len(dataWith))
}
