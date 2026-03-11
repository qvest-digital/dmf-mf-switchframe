package scte104

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/scte35"
)

// TestPipeline_SpliceInsert_FullRoundTrip exercises the complete SCTE-104 pipeline:
// Build SCTE-104 splice_request -> Encode -> WrapST291 -> ParseST291 -> Decode ->
// ToCueMessage -> verify CueMessage fields -> FromCueMessage -> Encode -> WrapST291 ->
// ParseST291 -> Decode -> ToCueMessage -> verify fields are preserved.
func TestPipeline_SpliceInsert_FullRoundTrip(t *testing.T) {
	// Build a realistic SCTE-104 splice_request message (cue-out with auto-return).
	original := &Message{
		ProtocolVersion: 0,
		ASIndex:         1,
		MessageNumber:   42,
		DPIPIDIndex:     100,
		Operations: []Operation{
			{
				OpID: OpSpliceRequest,
				Data: &SpliceRequestData{
					SpliceInsertType: SpliceStartImmediate,
					SpliceEventID:    0x00ABCDEF,
					UniqueProgramID:  500,
					PreRollTime:      4000, // 4 seconds
					BreakDuration:    300,  // 300 * 100ms = 30s
					AvailNum:         1,
					AvailsExpected:   3,
					AutoReturnFlag:   true,
				},
			},
		},
	}

	// --- First pass: SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	// Encode SCTE-104 to binary.
	encoded1, err := Encode(original)
	require.NoError(t, err, "Encode (pass 1)")

	// Wrap in ST 291 VANC framing.
	vanc1, err := WrapST291(encoded1)
	require.NoError(t, err, "WrapST291 (pass 1)")

	// Parse ST 291 to extract SCTE-104 payload.
	payload1, err := ParseST291(vanc1)
	require.NoError(t, err, "ParseST291 (pass 1)")

	// Decode SCTE-104 binary back to message.
	decoded1, err := Decode(payload1)
	require.NoError(t, err, "Decode (pass 1)")

	// Translate SCTE-104 to SCTE-35 CueMessage.
	cue1, err := ToCueMessage(decoded1)
	require.NoError(t, err, "ToCueMessage (pass 1)")

	// Verify CueMessage fields after the first pass.
	require.Equal(t, uint8(scte35.CommandSpliceInsert), cue1.CommandType, "pass 1: CommandType")
	require.Equal(t, uint32(0x00ABCDEF), cue1.EventID, "pass 1: EventID")
	require.True(t, cue1.IsOut, "pass 1: IsOut should be true (cue-out)")
	require.Equal(t, "immediate", cue1.Timing, "pass 1: Timing")
	require.NotNil(t, cue1.BreakDuration, "pass 1: BreakDuration should not be nil")
	expectedDuration := 30 * time.Second
	require.Equal(t, expectedDuration, *cue1.BreakDuration, "pass 1: BreakDuration")
	require.True(t, cue1.AutoReturn, "pass 1: AutoReturn should be true")
	require.Equal(t, uint16(500), cue1.UniqueProgramID, "pass 1: UniqueProgramID")
	require.Equal(t, uint8(1), cue1.AvailNum, "pass 1: AvailNum")
	require.Equal(t, uint8(3), cue1.AvailsExpected, "pass 1: AvailsExpected")

	// --- Second pass: CueMessage -> SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	// Translate CueMessage back to SCTE-104.
	msg104_2, err := FromCueMessage(cue1)
	require.NoError(t, err, "FromCueMessage (pass 2)")

	// Encode SCTE-104 to binary.
	encoded2, err := Encode(msg104_2)
	require.NoError(t, err, "Encode (pass 2)")

	// Wrap in ST 291 VANC framing.
	vanc2, err := WrapST291(encoded2)
	require.NoError(t, err, "WrapST291 (pass 2)")

	// Parse ST 291 to extract SCTE-104 payload.
	payload2, err := ParseST291(vanc2)
	require.NoError(t, err, "ParseST291 (pass 2)")

	// Decode SCTE-104 binary back to message.
	decoded2, err := Decode(payload2)
	require.NoError(t, err, "Decode (pass 2)")

	// Translate SCTE-104 to SCTE-35 CueMessage.
	cue2, err := ToCueMessage(decoded2)
	require.NoError(t, err, "ToCueMessage (pass 2)")

	// Verify all fields are preserved after the full double round-trip.
	require.Equal(t, cue1.CommandType, cue2.CommandType, "round-trip: CommandType")
	require.Equal(t, cue1.EventID, cue2.EventID, "round-trip: EventID")
	require.Equal(t, cue1.IsOut, cue2.IsOut, "round-trip: IsOut")
	require.Equal(t, cue1.AutoReturn, cue2.AutoReturn, "round-trip: AutoReturn")
	require.NotNil(t, cue2.BreakDuration, "round-trip: BreakDuration should not be nil")
	require.Equal(t, *cue1.BreakDuration, *cue2.BreakDuration, "round-trip: BreakDuration")
	require.Equal(t, cue1.UniqueProgramID, cue2.UniqueProgramID, "round-trip: UniqueProgramID")
	require.Equal(t, cue1.AvailNum, cue2.AvailNum, "round-trip: AvailNum")
	require.Equal(t, cue1.AvailsExpected, cue2.AvailsExpected, "round-trip: AvailsExpected")
}

// TestPipeline_TimeSignal_FullRoundTrip exercises the complete pipeline with
// time_signal + segmentation descriptors.
func TestPipeline_TimeSignal_FullRoundTrip(t *testing.T) {
	upid := []byte("SIGNAL:AD-BREAK-2026")

	// Build a SCTE-104 message with time_signal + segmentation descriptor.
	original := &Message{
		ProtocolVersion: 0,
		ASIndex:         2,
		MessageNumber:   7,
		DPIPIDIndex:     200,
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{
					PreRollTime: 3000, // 3 seconds
				},
			},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              0x0000BEEF,
					SegmentationTypeID:      0x34, // Provider Placement Opportunity Start
					DurationTicks:           2700000, // 30 seconds at 90kHz
					UPIDType:                0x09, // ADI
					UPID:                    upid,
					SegNum:                  1,
					SegExpected:             4,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	// --- First pass: SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	encoded1, err := Encode(original)
	require.NoError(t, err, "Encode (pass 1)")

	vanc1, err := WrapST291(encoded1)
	require.NoError(t, err, "WrapST291 (pass 1)")

	payload1, err := ParseST291(vanc1)
	require.NoError(t, err, "ParseST291 (pass 1)")

	decoded1, err := Decode(payload1)
	require.NoError(t, err, "Decode (pass 1)")

	cue1, err := ToCueMessage(decoded1)
	require.NoError(t, err, "ToCueMessage (pass 1)")

	// Verify CueMessage fields after the first pass.
	require.Equal(t, uint8(scte35.CommandTimeSignal), cue1.CommandType, "pass 1: CommandType")
	require.Len(t, cue1.Descriptors, 1, "pass 1: descriptor count")

	desc1 := cue1.Descriptors[0]
	require.Equal(t, uint32(0x0000BEEF), desc1.SegEventID, "pass 1: SegEventID")
	require.Equal(t, uint8(0x34), desc1.SegmentationType, "pass 1: SegmentationType")
	require.NotNil(t, desc1.DurationTicks, "pass 1: DurationTicks should not be nil")
	require.Equal(t, uint64(2700000), *desc1.DurationTicks, "pass 1: DurationTicks")
	require.Equal(t, uint8(0x09), desc1.UPIDType, "pass 1: UPIDType")
	require.Equal(t, upid, desc1.UPID, "pass 1: UPID")
	require.Equal(t, uint8(1), desc1.SegNum, "pass 1: SegNum")
	require.Equal(t, uint8(4), desc1.SegExpected, "pass 1: SegExpected")

	// --- Second pass: CueMessage -> SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	msg104_2, err := FromCueMessage(cue1)
	require.NoError(t, err, "FromCueMessage (pass 2)")

	encoded2, err := Encode(msg104_2)
	require.NoError(t, err, "Encode (pass 2)")

	vanc2, err := WrapST291(encoded2)
	require.NoError(t, err, "WrapST291 (pass 2)")

	payload2, err := ParseST291(vanc2)
	require.NoError(t, err, "ParseST291 (pass 2)")

	decoded2, err := Decode(payload2)
	require.NoError(t, err, "Decode (pass 2)")

	cue2, err := ToCueMessage(decoded2)
	require.NoError(t, err, "ToCueMessage (pass 2)")

	// Verify all fields are preserved after the full double round-trip.
	require.Equal(t, cue1.CommandType, cue2.CommandType, "round-trip: CommandType")
	require.Len(t, cue2.Descriptors, 1, "round-trip: descriptor count")

	desc2 := cue2.Descriptors[0]
	require.Equal(t, desc1.SegEventID, desc2.SegEventID, "round-trip: SegEventID")
	require.Equal(t, desc1.SegmentationType, desc2.SegmentationType, "round-trip: SegmentationType")
	require.NotNil(t, desc2.DurationTicks, "round-trip: DurationTicks should not be nil")
	require.Equal(t, *desc1.DurationTicks, *desc2.DurationTicks, "round-trip: DurationTicks")
	require.Equal(t, desc1.UPIDType, desc2.UPIDType, "round-trip: UPIDType")
	require.Equal(t, desc1.UPID, desc2.UPID, "round-trip: UPID")
	require.Equal(t, desc1.SegNum, desc2.SegNum, "round-trip: SegNum")
	require.Equal(t, desc1.SegExpected, desc2.SegExpected, "round-trip: SegExpected")
}

// TestPipeline_SpliceNull_FullRoundTrip exercises the complete heartbeat pipeline.
func TestPipeline_SpliceNull_FullRoundTrip(t *testing.T) {
	// Build a SCTE-104 splice_null heartbeat message.
	original := &Message{
		ProtocolVersion: 0,
		ASIndex:         5,
		MessageNumber:   99,
		DPIPIDIndex:     0,
		Operations: []Operation{
			{OpID: OpSpliceNull},
		},
	}

	// --- First pass: SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	encoded1, err := Encode(original)
	require.NoError(t, err, "Encode (pass 1)")

	vanc1, err := WrapST291(encoded1)
	require.NoError(t, err, "WrapST291 (pass 1)")

	payload1, err := ParseST291(vanc1)
	require.NoError(t, err, "ParseST291 (pass 1)")

	decoded1, err := Decode(payload1)
	require.NoError(t, err, "Decode (pass 1)")

	// Verify decoded SCTE-104 message header fields survived the wire round-trip.
	require.Equal(t, original.ASIndex, decoded1.ASIndex, "pass 1: ASIndex")
	require.Equal(t, original.MessageNumber, decoded1.MessageNumber, "pass 1: MessageNumber")
	require.Len(t, decoded1.Operations, 1, "pass 1: operation count")
	require.Equal(t, OpSpliceNull, decoded1.Operations[0].OpID, "pass 1: OpID")

	cue1, err := ToCueMessage(decoded1)
	require.NoError(t, err, "ToCueMessage (pass 1)")

	// Verify CueMessage is a splice_null.
	require.Equal(t, uint8(scte35.CommandSpliceNull), cue1.CommandType, "pass 1: CommandType")

	// --- Second pass: CueMessage -> SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	msg104_2, err := FromCueMessage(cue1)
	require.NoError(t, err, "FromCueMessage (pass 2)")

	encoded2, err := Encode(msg104_2)
	require.NoError(t, err, "Encode (pass 2)")

	vanc2, err := WrapST291(encoded2)
	require.NoError(t, err, "WrapST291 (pass 2)")

	payload2, err := ParseST291(vanc2)
	require.NoError(t, err, "ParseST291 (pass 2)")

	decoded2, err := Decode(payload2)
	require.NoError(t, err, "Decode (pass 2)")

	// Verify the decoded message still has a single splice_null operation.
	require.Len(t, decoded2.Operations, 1, "pass 2: operation count")
	require.Equal(t, OpSpliceNull, decoded2.Operations[0].OpID, "pass 2: OpID")

	cue2, err := ToCueMessage(decoded2)
	require.NoError(t, err, "ToCueMessage (pass 2)")

	// Verify CommandType is preserved after the full double round-trip.
	require.Equal(t, uint8(scte35.CommandSpliceNull), cue2.CommandType, "round-trip: CommandType")
}

// TestPipeline_PreRollMs_SurvivesRoundTrip verifies that PreRollMs is preserved
// through encode->decode round-trips for both splice_request and time_signal.
func TestPipeline_PreRollMs_SurvivesRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		msg    *Message
		wantMs int64
	}{
		{
			name: "splice_request_scheduled",
			msg: &Message{
				Operations: []Operation{
					{
						OpID: OpSpliceRequest,
						Data: &SpliceRequestData{
							SpliceInsertType: SpliceStartNormal,
							SpliceEventID:    100,
							PreRollTime:      4000,
							BreakDuration:    300,
						},
					},
				},
			},
			wantMs: 4000,
		},
		{
			name: "time_signal_request",
			msg: &Message{
				Operations: []Operation{
					{
						OpID: OpTimeSignalRequest,
						Data: &TimeSignalRequestData{
							PreRollTime: 3000,
						},
					},
					{
						OpID: OpSegmentationDescriptorRequest,
						Data: &SegmentationDescriptorRequest{
							SegEventID:              500,
							SegmentationTypeID:      0x34,
							ProgramSegmentationFlag: true,
						},
					},
				},
			},
			wantMs: 3000,
		},
		{
			name: "splice_request_immediate_zero",
			msg: &Message{
				Operations: []Operation{
					{
						OpID: OpSpliceRequest,
						Data: &SpliceRequestData{
							SpliceInsertType: SpliceStartImmediate,
							SpliceEventID:    200,
							PreRollTime:      5000, // should be ignored
						},
					},
				},
			},
			wantMs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode -> Decode round-trip.
			encoded, err := Encode(tt.msg)
			require.NoError(t, err, "Encode")
			decoded, err := Decode(encoded)
			require.NoError(t, err, "Decode")

			got := PreRollMs(decoded)
			require.Equal(t, tt.wantMs, got, "PreRollMs after encode->decode")

			// Also test with ST 291 wrapping.
			vanc, err := WrapST291(encoded)
			require.NoError(t, err, "WrapST291")
			payload, err := ParseST291(vanc)
			require.NoError(t, err, "ParseST291")
			decoded2, err := Decode(payload)
			require.NoError(t, err, "Decode after ST291")

			got2 := PreRollMs(decoded2)
			require.Equal(t, tt.wantMs, got2, "PreRollMs after ST291 round-trip")
		})
	}
}

// TestPipeline_SubSegments_FullRoundTrip exercises the complete pipeline with
// sub-segment fields (sub_segment_num, sub_segments_expected) per SCTE 104 2021.
func TestPipeline_SubSegments_FullRoundTrip(t *testing.T) {
	upid := []byte("SUBSEG-TEST")

	original := &Message{
		ProtocolVersion: 0,
		ASIndex:         1,
		MessageNumber:   10,
		DPIPIDIndex:     100,
		Operations: []Operation{
			{
				OpID: OpTimeSignalRequest,
				Data: &TimeSignalRequestData{PreRollTime: 2000},
			},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              0x00001234,
					SegmentationTypeID:      0x34,
					DurationTicks:           5400000, // 60 seconds
					UPIDType:                0x09,
					UPID:                    upid,
					SegNum:                  1,
					SegExpected:             4,
					SubSegmentNum:           2,
					SubSegmentsExpected:     3,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	// --- Pass 1: SCTE-104 -> wire -> SCTE-104 -> CueMessage ---
	encoded1, err := Encode(original)
	require.NoError(t, err, "Encode (pass 1)")

	vanc1, err := WrapST291(encoded1)
	require.NoError(t, err, "WrapST291 (pass 1)")

	payload1, err := ParseST291(vanc1)
	require.NoError(t, err, "ParseST291 (pass 1)")

	decoded1, err := Decode(payload1)
	require.NoError(t, err, "Decode (pass 1)")

	cue1, err := ToCueMessage(decoded1)
	require.NoError(t, err, "ToCueMessage (pass 1)")

	require.Len(t, cue1.Descriptors, 1, "pass 1: descriptor count")
	desc1 := cue1.Descriptors[0]
	require.Equal(t, uint8(2), desc1.SubSegmentNum, "pass 1: SubSegmentNum")
	require.Equal(t, uint8(3), desc1.SubSegmentsExpected, "pass 1: SubSegmentsExpected")

	// --- Pass 2: CueMessage -> SCTE-104 -> wire -> SCTE-104 -> CueMessage ---
	msg104_2, err := FromCueMessage(cue1)
	require.NoError(t, err, "FromCueMessage (pass 2)")

	encoded2, err := Encode(msg104_2)
	require.NoError(t, err, "Encode (pass 2)")

	vanc2, err := WrapST291(encoded2)
	require.NoError(t, err, "WrapST291 (pass 2)")

	payload2, err := ParseST291(vanc2)
	require.NoError(t, err, "ParseST291 (pass 2)")

	decoded2, err := Decode(payload2)
	require.NoError(t, err, "Decode (pass 2)")

	cue2, err := ToCueMessage(decoded2)
	require.NoError(t, err, "ToCueMessage (pass 2)")

	require.Len(t, cue2.Descriptors, 1, "round-trip: descriptor count")
	desc2 := cue2.Descriptors[0]
	require.Equal(t, desc1.SubSegmentNum, desc2.SubSegmentNum, "round-trip: SubSegmentNum")
	require.Equal(t, desc1.SubSegmentsExpected, desc2.SubSegmentsExpected, "round-trip: SubSegmentsExpected")
}

// TestPipeline_SubSegments_NotParsedForNonSubSegmentTypes verifies that
// sub_segment_num and sub_segments_expected are NOT parsed for segmentation
// types that don't carry them per SCTE-35 Table 22.
func TestPipeline_SubSegments_NotParsedForNonSubSegmentTypes(t *testing.T) {
	// Use type 0x34 (Provider Placement Opportunity Start) which HAS sub-segments.
	msgWith := &Message{
		Operations: []Operation{
			{OpID: OpTimeSignalRequest, Data: &TimeSignalRequestData{PreRollTime: 0}},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              0x00005678,
					SegmentationTypeID:      0x34,
					UPIDType:                0x09,
					UPID:                    []byte("X"),
					SegNum:                  1,
					SegExpected:             2,
					SubSegmentNum:           3,
					SubSegmentsExpected:     4,
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	encoded, err := Encode(msgWith)
	require.NoError(t, err, "Encode (sub-seg type)")
	decoded, err := Decode(encoded)
	require.NoError(t, err, "Decode (sub-seg type)")
	sd := decoded.Operations[1].Data.(*SegmentationDescriptorRequest)
	require.Equal(t, uint8(3), sd.SubSegmentNum, "type 0x34: SubSegmentNum")
	require.Equal(t, uint8(4), sd.SubSegmentsExpected, "type 0x34: SubSegmentsExpected")

	// Now use type 0x30 (Provider Advertisement Start) which does NOT have sub-segments.
	msgWithout := &Message{
		Operations: []Operation{
			{OpID: OpTimeSignalRequest, Data: &TimeSignalRequestData{PreRollTime: 0}},
			{
				OpID: OpSegmentationDescriptorRequest,
				Data: &SegmentationDescriptorRequest{
					SegEventID:              0x00009ABC,
					SegmentationTypeID:      0x30,
					UPIDType:                0x09,
					UPID:                    []byte("Y"),
					SegNum:                  1,
					SegExpected:             2,
					SubSegmentNum:           3, // Should NOT be encoded for 0x30
					SubSegmentsExpected:     4, // Should NOT be encoded for 0x30
					ProgramSegmentationFlag: true,
				},
			},
		},
	}

	encoded2, err := Encode(msgWithout)
	require.NoError(t, err, "Encode (non-sub-seg type)")
	decoded2, err := Decode(encoded2)
	require.NoError(t, err, "Decode (non-sub-seg type)")
	sd2 := decoded2.Operations[1].Data.(*SegmentationDescriptorRequest)
	// For 0x30, sub-segment fields should be zero (not parsed).
	require.Equal(t, uint8(0), sd2.SubSegmentNum, "type 0x30: SubSegmentNum should not be parsed")
	require.Equal(t, uint8(0), sd2.SubSegmentsExpected, "type 0x30: SubSegmentsExpected should not be parsed")
}

// TestPipeline_HasSubSegmentFields verifies the hasSubSegmentFields function
// correctly classifies segmentation types per SCTE-35 Table 22.
func TestPipeline_HasSubSegmentFields(t *testing.T) {
	// Types that SHOULD have sub-segment fields.
	subSegTypes := []uint8{0x34, 0x36, 0x38, 0x3A, 0x44, 0x46}
	for _, typeID := range subSegTypes {
		require.True(t, hasSubSegmentFields(typeID), "hasSubSegmentFields(0x%02X) should be true", typeID)
	}

	// Types that should NOT have sub-segment fields.
	nonSubSegTypes := []uint8{0x22, 0x30, 0x32, 0x3C, 0x3E, 0x40, 0x42, 0x50, 0x00, 0xFF}
	for _, typeID := range nonSubSegTypes {
		require.False(t, hasSubSegmentFields(typeID), "hasSubSegmentFields(0x%02X) should be false", typeID)
	}
}
