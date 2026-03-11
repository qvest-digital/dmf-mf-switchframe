package scte104

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/scte35"
)

// ---- ToCueMessage tests ----

func TestToCueMessage_NilMessage(t *testing.T) {
	_, err := ToCueMessage(nil)
	require.Error(t, err, "expected error for nil message")
}

func TestToCueMessage_EmptyOps(t *testing.T) {
	_, err := ToCueMessage(&Message{})
	require.Error(t, err, "expected error for empty operations")
}

func TestToCueMessage_SpliceNull(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{OpID: OpSpliceNull},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.Equal(t, uint8(scte35.CommandSpliceNull), cue.CommandType, "CommandType")
}

func TestToCueMessage_SpliceRequest_CueOut(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartImmediate,
					SpliceEventID:    100,
					UniqueProgramID:  50,
					BreakDuration:    300, // 300 * 100ms = 30s
					AvailNum:         1,
					AvailsExpected:   2,
					AutoReturnFlag:   true,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.Equal(t, uint8(scte35.CommandSpliceInsert), cue.CommandType, "CommandType")
	require.True(t, cue.IsOut, "IsOut should be true for cue-out")
	require.Equal(t, uint32(100), cue.EventID, "EventID")
	require.Equal(t, uint16(50), cue.UniqueProgramID, "UniqueProgramID")
	require.NotNil(t, cue.BreakDuration, "BreakDuration should not be nil")
	expected := 30 * time.Second
	require.Equal(t, expected, *cue.BreakDuration, "BreakDuration")
	require.Equal(t, uint8(1), cue.AvailNum, "AvailNum")
	require.Equal(t, uint8(2), cue.AvailsExpected, "AvailsExpected")
	require.True(t, cue.AutoReturn, "AutoReturn should be true")
	require.Equal(t, "immediate", cue.Timing, "Timing")
}

func TestToCueMessage_SpliceRequest_CueIn(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceEndImmediate,
					SpliceEventID:    100,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.False(t, cue.IsOut, "IsOut should be false for cue-in")
	require.False(t, cue.SpliceEventCancelIndicator, "SpliceEventCancelIndicator should be false")
	require.Equal(t, "immediate", cue.Timing, "Timing")
}

func TestToCueMessage_SpliceRequest_Scheduled(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartNormal,
					SpliceEventID:    200,
					PreRollTime:      5000,
					BreakDuration:    100,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.True(t, cue.IsOut, "IsOut should be true for SpliceStartNormal")
	require.Equal(t, "scheduled", cue.Timing, "Timing")
}

func TestToCueMessage_SpliceRequest_EndNormal(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceEndNormal,
					SpliceEventID:    300,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.False(t, cue.IsOut, "IsOut should be false for SpliceEndNormal")
	require.Equal(t, "scheduled", cue.Timing, "Timing")
}

func TestToCueMessage_SpliceRequest_Cancel(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceCancel,
					SpliceEventID:    100,
					BreakDuration:    300, // should be ignored for cancel
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.True(t, cue.SpliceEventCancelIndicator, "SpliceEventCancelIndicator should be true")
	require.Equal(t, uint32(100), cue.EventID, "EventID")
	require.Nil(t, cue.BreakDuration, "BreakDuration should be nil for cancel")
}

func TestToCueMessage_TimeSignal_WithDescriptor(t *testing.T) {
	upid := []byte("TEST-UPID")
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{PreRollTime: 2000},
			},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         500,
					SegmentationTypeID: 0x34,
					DurationTicks:      2700000,
					UPIDType:           0x09,
					UPID:               upid,
					SegNum:             1,
					SegExpected:        1,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.Equal(t, uint8(scte35.CommandTimeSignal), cue.CommandType, "CommandType")
	require.Len(t, cue.Descriptors, 1, "descriptor count")

	desc := cue.Descriptors[0]
	require.Equal(t, uint32(500), desc.SegEventID, "SegEventID")
	require.Equal(t, uint8(0x34), desc.SegmentationType, "SegmentationType")
	require.NotNil(t, desc.DurationTicks, "DurationTicks should not be nil")
	require.Equal(t, uint64(2700000), *desc.DurationTicks, "DurationTicks")
	require.Equal(t, uint8(0x09), desc.UPIDType, "UPIDType")
	require.Equal(t, upid, desc.UPID, "UPID")
}

func TestToCueMessage_TimeSignal_MultipleDescriptors(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{PreRollTime: 0},
			},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         1,
					SegmentationTypeID: 0x34,
				},
			},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         2,
					SegmentationTypeID: 0x36,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.Len(t, cue.Descriptors, 2, "descriptor count")
	require.Equal(t, uint32(1), cue.Descriptors[0].SegEventID, "descriptor[0].SegEventID")
	require.Equal(t, uint32(2), cue.Descriptors[1].SegEventID, "descriptor[1].SegEventID")
}

func TestToCueMessage_SegDescOnly_ImplicitTimeSignal(t *testing.T) {
	// Only segmentation descriptors, no explicit time_signal op.
	// Should create an implicit time_signal.
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         100,
					SegmentationTypeID: 0x30,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.Equal(t, uint8(scte35.CommandTimeSignal), cue.CommandType, "CommandType")
	require.Len(t, cue.Descriptors, 1, "descriptor count")
}

func TestToCueMessage_SegDescCancel(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{},
			},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:      777,
					CancelIndicator: true,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.Len(t, cue.Descriptors, 1, "descriptor count")
	require.True(t, cue.Descriptors[0].SegmentationEventCancelIndicator, "cancel indicator should be true")
}

// ---- PreRollMs tests ----

func TestPreRollMs_NilMessage(t *testing.T) {
	require.Equal(t, int64(0), PreRollMs(nil), "PreRollMs(nil)")
}

func TestPreRollMs_EmptyMessage(t *testing.T) {
	require.Equal(t, int64(0), PreRollMs(&Message{}), "PreRollMs(empty)")
}

func TestPreRollMs_SpliceStartNormal(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartNormal,
					PreRollTime:      5000,
				},
			},
		},
	}
	require.Equal(t, int64(5000), PreRollMs(msg), "PreRollMs")
}

func TestPreRollMs_SpliceEndNormal(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceEndNormal,
					PreRollTime:      3000,
				},
			},
		},
	}
	require.Equal(t, int64(3000), PreRollMs(msg), "PreRollMs")
}

func TestPreRollMs_SpliceStartImmediate_ReturnsZero(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartImmediate,
					PreRollTime:      5000, // should be ignored
				},
			},
		},
	}
	require.Equal(t, int64(0), PreRollMs(msg), "PreRollMs(SpliceStartImmediate)")
}

func TestPreRollMs_SpliceEndImmediate_ReturnsZero(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceEndImmediate,
					PreRollTime:      2000,
				},
			},
		},
	}
	require.Equal(t, int64(0), PreRollMs(msg), "PreRollMs(SpliceEndImmediate)")
}

func TestPreRollMs_SpliceCancel_ReturnsZero(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceCancel,
					PreRollTime:      1000,
				},
			},
		},
	}
	require.Equal(t, int64(0), PreRollMs(msg), "PreRollMs(SpliceCancel)")
}

func TestPreRollMs_TimeSignalRequest(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{
					PreRollTime: 4000,
				},
			},
		},
	}
	require.Equal(t, int64(4000), PreRollMs(msg), "PreRollMs(TimeSignalRequest)")
}

func TestPreRollMs_TimeSignalRequest_Zero(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{
					PreRollTime: 0,
				},
			},
		},
	}
	require.Equal(t, int64(0), PreRollMs(msg), "PreRollMs(TimeSignalRequest zero)")
}

func TestPreRollMs_SpliceNull_ReturnsZero(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{OpID: OpSpliceNull},
		},
	}
	require.Equal(t, int64(0), PreRollMs(msg), "PreRollMs(SpliceNull)")
}

func TestPreRollMs_BadDataType_ReturnsZero(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: "not a SpliceRequestData",
			},
		},
	}
	require.Equal(t, int64(0), PreRollMs(msg), "PreRollMs(bad data type)")
}

// ---- FromCueMessage tests ----

func TestFromCueMessage_NilMessage(t *testing.T) {
	_, err := FromCueMessage(nil)
	require.Error(t, err, "expected error for nil message")
}

func TestFromCueMessage_SpliceNull(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceNull,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1, "operation count")
	require.Equal(t, OpSpliceNull, msg.Operations[0].OpID, "OpID")
}

func TestFromCueMessage_SpliceInsert_CueOut(t *testing.T) {
	dur := 30 * time.Second
	cue := &scte35.CueMessage{
		CommandType:     scte35.CommandSpliceInsert,
		EventID:         100,
		IsOut:           true,
		AutoReturn:      true,
		BreakDuration:   &dur,
		UniqueProgramID: 50,
		AvailNum:        1,
		AvailsExpected:  2,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 1, "operation count")

	srd, ok := msg.Operations[0].Data.(*SpliceRequestData)
	require.True(t, ok, "expected *SpliceRequestData, got %T", msg.Operations[0].Data)

	require.Equal(t, SpliceStartImmediate, srd.SpliceInsertType, "SpliceInsertType")
	require.Equal(t, uint32(100), srd.SpliceEventID, "SpliceEventID")
	require.Equal(t, uint16(300), srd.BreakDuration, "BreakDuration (30s / 100ms = 300)")
	require.True(t, srd.AutoReturnFlag, "AutoReturnFlag should be true")
	require.Equal(t, uint16(50), srd.UniqueProgramID, "UniqueProgramID")
	require.Equal(t, uint8(1), srd.AvailNum, "AvailNum")
	require.Equal(t, uint8(2), srd.AvailsExpected, "AvailsExpected")
}

func TestFromCueMessage_SpliceInsert_CueIn(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceInsert,
		EventID:     200,
		IsOut:       false,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, SpliceEndImmediate, srd.SpliceInsertType, "SpliceInsertType")
}

func TestFromCueMessage_SpliceInsert_Cancel(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType:                 scte35.CommandSpliceInsert,
		EventID:                    300,
		SpliceEventCancelIndicator: true,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, SpliceCancel, srd.SpliceInsertType, "SpliceInsertType")
	require.Equal(t, uint32(300), srd.SpliceEventID, "SpliceEventID")
}

func TestFromCueMessage_TimeSignal(t *testing.T) {
	ticks := uint64(2700000)
	cue := &scte35.CueMessage{
		CommandType: scte35.CommandTimeSignal,
		Descriptors: []scte35.SegmentationDescriptor{
			{
				SegEventID:       500,
				SegmentationType: 0x34,
				DurationTicks:    &ticks,
				UPIDType:         0x09,
				UPID:             []byte("TEST"),
			},
		},
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	require.Len(t, msg.Operations, 2, "expected 2 operations (time_signal + seg_desc)")

	// First op: time_signal_request.
	require.Equal(t, OpTimeSignalRequest, msg.Operations[0].OpID, "op[0].OpID")

	// Second op: segmentation_descriptor_request.
	require.Equal(t, OpSegmentationDescriptorRequest, msg.Operations[1].OpID, "op[1].OpID")

	sd := msg.Operations[1].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint32(500), sd.SegEventID, "SegEventID")
	require.Equal(t, uint8(0x34), sd.SegmentationTypeID, "SegmentationTypeID")
	require.Equal(t, uint64(2700000), sd.DurationTicks, "DurationTicks")
	require.Equal(t, uint8(0x09), sd.UPIDType, "UPIDType")
	require.Equal(t, []byte("TEST"), sd.UPID, "UPID")
}

func TestFromCueMessage_UnsupportedType(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType: 0xFF,
	}

	_, err := FromCueMessage(cue)
	require.Error(t, err, "expected error for unsupported command type")
}

// ---- Bidirectional round-trip tests ----

func TestRoundTrip_SpliceInsert_CueOut(t *testing.T) {
	dur := 30 * time.Second
	original := &scte35.CueMessage{
		CommandType:     scte35.CommandSpliceInsert,
		EventID:         42,
		IsOut:           true,
		AutoReturn:      true,
		BreakDuration:   &dur,
		UniqueProgramID: 100,
		AvailNum:        1,
		AvailsExpected:  3,
	}

	// CueMessage -> SCTE-104 -> CueMessage
	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	roundTripped, err := ToCueMessage(msg104)
	require.NoError(t, err, "ToCueMessage")

	require.Equal(t, original.CommandType, roundTripped.CommandType, "CommandType")
	require.Equal(t, original.EventID, roundTripped.EventID, "EventID")
	require.Equal(t, original.IsOut, roundTripped.IsOut, "IsOut")
	require.Equal(t, original.AutoReturn, roundTripped.AutoReturn, "AutoReturn")
	require.NotNil(t, roundTripped.BreakDuration, "BreakDuration should not be nil")
	require.Equal(t, *original.BreakDuration, *roundTripped.BreakDuration, "BreakDuration")
	require.Equal(t, original.UniqueProgramID, roundTripped.UniqueProgramID, "UniqueProgramID")
	require.Equal(t, original.AvailNum, roundTripped.AvailNum, "AvailNum")
	require.Equal(t, original.AvailsExpected, roundTripped.AvailsExpected, "AvailsExpected")
}

func TestRoundTrip_SpliceInsert_CueIn(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceInsert,
		EventID:     55,
		IsOut:       false,
	}

	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	roundTripped, err := ToCueMessage(msg104)
	require.NoError(t, err, "ToCueMessage")

	require.False(t, roundTripped.IsOut, "IsOut should be false after round-trip")
	require.False(t, roundTripped.SpliceEventCancelIndicator, "cancel should be false after round-trip")
}

func TestRoundTrip_SpliceInsert_Cancel(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType:                 scte35.CommandSpliceInsert,
		EventID:                    77,
		SpliceEventCancelIndicator: true,
	}

	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	roundTripped, err := ToCueMessage(msg104)
	require.NoError(t, err, "ToCueMessage")

	require.True(t, roundTripped.SpliceEventCancelIndicator, "SpliceEventCancelIndicator should be true after round-trip")
	require.Equal(t, uint32(77), roundTripped.EventID, "EventID")
}

func TestRoundTrip_SpliceNull(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceNull,
	}

	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	roundTripped, err := ToCueMessage(msg104)
	require.NoError(t, err, "ToCueMessage")

	require.Equal(t, uint8(scte35.CommandSpliceNull), roundTripped.CommandType, "CommandType")
}

func TestRoundTrip_TimeSignal(t *testing.T) {
	ticks := uint64(900000) // 10 seconds at 90kHz
	original := &scte35.CueMessage{
		CommandType: scte35.CommandTimeSignal,
		Descriptors: []scte35.SegmentationDescriptor{
			{
				SegEventID:       1001,
				SegmentationType: 0x34,
				DurationTicks:    &ticks,
				UPIDType:         0x09,
				UPID:             []byte("AD-12345"),
			},
		},
	}

	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	roundTripped, err := ToCueMessage(msg104)
	require.NoError(t, err, "ToCueMessage")

	require.Equal(t, uint8(scte35.CommandTimeSignal), roundTripped.CommandType, "CommandType")
	require.Len(t, roundTripped.Descriptors, 1, "descriptor count")

	desc := roundTripped.Descriptors[0]
	origDesc := original.Descriptors[0]

	require.Equal(t, origDesc.SegEventID, desc.SegEventID, "SegEventID")
	require.Equal(t, origDesc.SegmentationType, desc.SegmentationType, "SegmentationType")
	require.NotNil(t, desc.DurationTicks, "DurationTicks should not be nil")
	require.Equal(t, *origDesc.DurationTicks, *desc.DurationTicks, "DurationTicks")
	require.Equal(t, origDesc.UPIDType, desc.UPIDType, "UPIDType")
	require.Equal(t, origDesc.UPID, desc.UPID, "UPID")
}

func TestRoundTrip_TimeSignal_CancelDescriptor(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType: scte35.CommandTimeSignal,
		Descriptors: []scte35.SegmentationDescriptor{
			{
				SegEventID:                       888,
				SegmentationEventCancelIndicator: true,
			},
		},
	}

	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	roundTripped, err := ToCueMessage(msg104)
	require.NoError(t, err, "ToCueMessage")

	require.Len(t, roundTripped.Descriptors, 1, "descriptor count")
	require.True(t, roundTripped.Descriptors[0].SegmentationEventCancelIndicator, "cancel indicator should be true after round-trip")
	require.Equal(t, uint32(888), roundTripped.Descriptors[0].SegEventID, "SegEventID")
}

// ---- Full wire round-trip: CueMessage -> SCTE-104 -> binary -> SCTE-104 -> CueMessage ----

func TestFullWireRoundTrip_SpliceInsert(t *testing.T) {
	dur := 60 * time.Second
	original := &scte35.CueMessage{
		CommandType:     scte35.CommandSpliceInsert,
		EventID:         9999,
		IsOut:           true,
		AutoReturn:      true,
		BreakDuration:   &dur,
		UniqueProgramID: 42,
		AvailNum:        2,
		AvailsExpected:  5,
	}

	// CueMessage -> SCTE-104 Message
	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	// SCTE-104 Message -> binary
	wireData, err := Encode(msg104)
	require.NoError(t, err, "Encode")

	// binary -> SCTE-104 Message
	decoded104, err := Decode(wireData)
	require.NoError(t, err, "Decode")

	// SCTE-104 Message -> CueMessage
	result, err := ToCueMessage(decoded104)
	require.NoError(t, err, "ToCueMessage")

	require.Equal(t, original.CommandType, result.CommandType, "CommandType")
	require.Equal(t, original.EventID, result.EventID, "EventID")
	require.Equal(t, original.IsOut, result.IsOut, "IsOut")
	require.Equal(t, *original.BreakDuration, *result.BreakDuration, "BreakDuration")
}

func TestFullWireRoundTrip_TimeSignal(t *testing.T) {
	ticks := uint64(4500000)
	upid := []byte("FULL-WIRE-TEST")
	original := &scte35.CueMessage{
		CommandType: scte35.CommandTimeSignal,
		Descriptors: []scte35.SegmentationDescriptor{
			{
				SegEventID:       12345,
				SegmentationType: 0x36,
				DurationTicks:    &ticks,
				UPIDType:         0x09,
				UPID:             upid,
			},
		},
	}

	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	wireData, err := Encode(msg104)
	require.NoError(t, err, "Encode")

	decoded104, err := Decode(wireData)
	require.NoError(t, err, "Decode")

	result, err := ToCueMessage(decoded104)
	require.NoError(t, err, "ToCueMessage")

	require.Equal(t, uint8(scte35.CommandTimeSignal), result.CommandType, "CommandType")
	require.Len(t, result.Descriptors, 1, "descriptor count")
	require.Equal(t, ticks, *result.Descriptors[0].DurationTicks, "DurationTicks")
	require.Equal(t, upid, result.Descriptors[0].UPID, "UPID")
}

func TestFullWireRoundTrip_SpliceNull(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceNull,
	}

	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	wireData, err := Encode(msg104)
	require.NoError(t, err, "Encode")

	decoded104, err := Decode(wireData)
	require.NoError(t, err, "Decode")

	result, err := ToCueMessage(decoded104)
	require.NoError(t, err, "ToCueMessage")

	require.Equal(t, uint8(scte35.CommandSpliceNull), result.CommandType, "CommandType")
}

func TestToCueMessage_SpliceRequest_ZeroBreakDuration(t *testing.T) {
	// BreakDuration=0 should result in nil BreakDuration in CueMessage.
	msg := &Message{
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartImmediate,
					SpliceEventID:    1,
					BreakDuration:    0,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)

	require.Nil(t, cue.BreakDuration, "BreakDuration should be nil for zero duration")
}

func TestFromCueMessage_BreakDuration_Rounding(t *testing.T) {
	// 350ms should round to 4 (x100ms = 400ms) with rounding,
	// not 3 (300ms) with truncation.
	dur := 350 * time.Millisecond
	cue := &scte35.CueMessage{
		CommandType:   scte35.CommandSpliceInsert,
		EventID:       1,
		IsOut:         true,
		BreakDuration: &dur,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, uint16(4), srd.BreakDuration, "BreakDuration should be 4 (rounded from 350ms)")
}

// ---- FromCueMessage scheduled timing tests ----

func TestFromCueMessage_SpliceInsert_ScheduledCueOut(t *testing.T) {
	dur := 30 * time.Second
	cue := &scte35.CueMessage{
		CommandType:   scte35.CommandSpliceInsert,
		EventID:       400,
		IsOut:         true,
		Timing:        "scheduled",
		BreakDuration: &dur,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, SpliceStartNormal, srd.SpliceInsertType, "SpliceInsertType")
}

func TestFromCueMessage_SpliceInsert_ScheduledCueIn(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceInsert,
		EventID:     401,
		IsOut:       false,
		Timing:      "scheduled",
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, SpliceEndNormal, srd.SpliceInsertType, "SpliceInsertType")
}

func TestFromCueMessage_SpliceInsert_ImmediateCueOut_Regression(t *testing.T) {
	// Regression: immediate cue-out must still produce SpliceStartImmediate.
	dur := 10 * time.Second
	cue := &scte35.CueMessage{
		CommandType:   scte35.CommandSpliceInsert,
		EventID:       402,
		IsOut:         true,
		Timing:        "immediate",
		BreakDuration: &dur,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, SpliceStartImmediate, srd.SpliceInsertType, "SpliceInsertType")
}

func TestFromCueMessage_SpliceInsert_ImmediateCueIn_Regression(t *testing.T) {
	// Regression: immediate cue-in must still produce SpliceEndImmediate.
	cue := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceInsert,
		EventID:     403,
		IsOut:       false,
		Timing:      "immediate",
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, SpliceEndImmediate, srd.SpliceInsertType, "SpliceInsertType")
}

func TestRoundTrip_ScheduledPreservation(t *testing.T) {
	// Round-trip: scheduled cue-out should preserve timing through
	// CueMessage -> SCTE-104 -> CueMessage.
	dur := 20 * time.Second
	original := &scte35.CueMessage{
		CommandType:     scte35.CommandSpliceInsert,
		EventID:         500,
		IsOut:           true,
		Timing:          "scheduled",
		AutoReturn:      true,
		BreakDuration:   &dur,
		UniqueProgramID: 10,
	}

	msg104, err := FromCueMessage(original)
	require.NoError(t, err, "FromCueMessage")

	// Verify intermediate SCTE-104 uses SpliceStartNormal.
	srd := msg104.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, SpliceStartNormal, srd.SpliceInsertType, "intermediate SpliceInsertType")

	roundTripped, err := ToCueMessage(msg104)
	require.NoError(t, err, "ToCueMessage")

	require.Equal(t, "scheduled", roundTripped.Timing, "Timing")
	require.True(t, roundTripped.IsOut, "IsOut should be true after round-trip")
	require.Equal(t, original.EventID, roundTripped.EventID, "EventID")
}

func TestFromCueMessage_BreakDuration_OverflowClamp(t *testing.T) {
	// 2 hours = 7200s = 72000 x 100ms units -- exceeds uint16 max (65535).
	// Must be clamped to 65535, not silently wrap around.
	dur := 2 * time.Hour
	cue := &scte35.CueMessage{
		CommandType:   scte35.CommandSpliceInsert,
		EventID:       1,
		IsOut:         true,
		BreakDuration: &dur,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	require.Equal(t, uint16(65535), srd.BreakDuration, "BreakDuration should be clamped from 2h")
}

func TestFromCueMessage_BreakDuration_JustUnderMax(t *testing.T) {
	// 109 minutes = 6540s = 65400 x 100ms units -- fits in uint16, must NOT be clamped.
	dur := 109 * time.Minute
	cue := &scte35.CueMessage{
		CommandType:   scte35.CommandSpliceInsert,
		EventID:       2,
		IsOut:         true,
		BreakDuration: &dur,
	}

	msg, err := FromCueMessage(cue)
	require.NoError(t, err)

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	// 109 min = 6540s -> 65400 units of 100ms
	require.Equal(t, uint16(65400), srd.BreakDuration, "BreakDuration should be 65400 (109 min, not clamped)")
}

func TestRoundTrip_SegNum(t *testing.T) {
	// SCTE-104 -> SCTE-35 -> SCTE-104 should preserve SegNum/SegExpected.
	msg := &Message{
		Operations: []Operation{
			{OpID: OpTimeSignalRequest, Data: &TimeSignalRequestData{}},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:         99,
					SegmentationTypeID: 0x34,
					SegNum:             2,
					SegExpected:        5,
				},
			},
		},
	}

	cue, err := ToCueMessage(msg)
	require.NoError(t, err, "ToCueMessage")
	require.Len(t, cue.Descriptors, 1, "descriptor count")
	require.Equal(t, uint8(2), cue.Descriptors[0].SegNum, "SegNum")
	require.Equal(t, uint8(5), cue.Descriptors[0].SegExpected, "SegExpected")

	msg2, err := FromCueMessage(cue)
	require.NoError(t, err, "FromCueMessage")

	// Find the segmentation descriptor operation.
	var sd *SegmentationDescriptorRequest
	for _, op := range msg2.Operations {
		if op.OpID == OpSegmentationDescriptorRequest {
			sd = op.Data.(*SegmentationDescriptorRequest)
			break
		}
	}
	require.NotNil(t, sd, "no segmentation descriptor in round-trip result")
	require.Equal(t, uint8(2), sd.SegNum, "round-trip SegNum")
	require.Equal(t, uint8(5), sd.SegExpected, "round-trip SegExpected")
}
