package scte104

import (
	"bytes"
	"testing"
	"time"

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
	if err != nil {
		t.Fatalf("Encode (pass 1): %v", err)
	}

	// Wrap in ST 291 VANC framing.
	vanc1, err := WrapST291(encoded1)
	if err != nil {
		t.Fatalf("WrapST291 (pass 1): %v", err)
	}

	// Parse ST 291 to extract SCTE-104 payload.
	payload1, err := ParseST291(vanc1)
	if err != nil {
		t.Fatalf("ParseST291 (pass 1): %v", err)
	}

	// Decode SCTE-104 binary back to message.
	decoded1, err := Decode(payload1)
	if err != nil {
		t.Fatalf("Decode (pass 1): %v", err)
	}

	// Translate SCTE-104 to SCTE-35 CueMessage.
	cue1, err := ToCueMessage(decoded1)
	if err != nil {
		t.Fatalf("ToCueMessage (pass 1): %v", err)
	}

	// Verify CueMessage fields after the first pass.
	if cue1.CommandType != scte35.CommandSpliceInsert {
		t.Errorf("pass 1: CommandType = 0x%02X, want 0x%02X", cue1.CommandType, scte35.CommandSpliceInsert)
	}
	if cue1.EventID != 0x00ABCDEF {
		t.Errorf("pass 1: EventID = 0x%08X, want 0x00ABCDEF", cue1.EventID)
	}
	if !cue1.IsOut {
		t.Error("pass 1: IsOut should be true (cue-out)")
	}
	if cue1.Timing != "immediate" {
		t.Errorf("pass 1: Timing = %q, want %q", cue1.Timing, "immediate")
	}
	if cue1.BreakDuration == nil {
		t.Fatal("pass 1: BreakDuration should not be nil")
	}
	expectedDuration := 30 * time.Second
	if *cue1.BreakDuration != expectedDuration {
		t.Errorf("pass 1: BreakDuration = %v, want %v", *cue1.BreakDuration, expectedDuration)
	}
	if !cue1.AutoReturn {
		t.Error("pass 1: AutoReturn should be true")
	}
	if cue1.UniqueProgramID != 500 {
		t.Errorf("pass 1: UniqueProgramID = %d, want 500", cue1.UniqueProgramID)
	}
	if cue1.AvailNum != 1 {
		t.Errorf("pass 1: AvailNum = %d, want 1", cue1.AvailNum)
	}
	if cue1.AvailsExpected != 3 {
		t.Errorf("pass 1: AvailsExpected = %d, want 3", cue1.AvailsExpected)
	}

	// --- Second pass: CueMessage -> SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	// Translate CueMessage back to SCTE-104.
	msg104_2, err := FromCueMessage(cue1)
	if err != nil {
		t.Fatalf("FromCueMessage (pass 2): %v", err)
	}

	// Encode SCTE-104 to binary.
	encoded2, err := Encode(msg104_2)
	if err != nil {
		t.Fatalf("Encode (pass 2): %v", err)
	}

	// Wrap in ST 291 VANC framing.
	vanc2, err := WrapST291(encoded2)
	if err != nil {
		t.Fatalf("WrapST291 (pass 2): %v", err)
	}

	// Parse ST 291 to extract SCTE-104 payload.
	payload2, err := ParseST291(vanc2)
	if err != nil {
		t.Fatalf("ParseST291 (pass 2): %v", err)
	}

	// Decode SCTE-104 binary back to message.
	decoded2, err := Decode(payload2)
	if err != nil {
		t.Fatalf("Decode (pass 2): %v", err)
	}

	// Translate SCTE-104 to SCTE-35 CueMessage.
	cue2, err := ToCueMessage(decoded2)
	if err != nil {
		t.Fatalf("ToCueMessage (pass 2): %v", err)
	}

	// Verify all fields are preserved after the full double round-trip.
	if cue2.CommandType != cue1.CommandType {
		t.Errorf("round-trip: CommandType = 0x%02X, want 0x%02X", cue2.CommandType, cue1.CommandType)
	}
	if cue2.EventID != cue1.EventID {
		t.Errorf("round-trip: EventID = 0x%08X, want 0x%08X", cue2.EventID, cue1.EventID)
	}
	if cue2.IsOut != cue1.IsOut {
		t.Errorf("round-trip: IsOut = %v, want %v", cue2.IsOut, cue1.IsOut)
	}
	if cue2.AutoReturn != cue1.AutoReturn {
		t.Errorf("round-trip: AutoReturn = %v, want %v", cue2.AutoReturn, cue1.AutoReturn)
	}
	if cue2.BreakDuration == nil {
		t.Fatal("round-trip: BreakDuration should not be nil")
	}
	if *cue2.BreakDuration != *cue1.BreakDuration {
		t.Errorf("round-trip: BreakDuration = %v, want %v", *cue2.BreakDuration, *cue1.BreakDuration)
	}
	if cue2.UniqueProgramID != cue1.UniqueProgramID {
		t.Errorf("round-trip: UniqueProgramID = %d, want %d", cue2.UniqueProgramID, cue1.UniqueProgramID)
	}
	if cue2.AvailNum != cue1.AvailNum {
		t.Errorf("round-trip: AvailNum = %d, want %d", cue2.AvailNum, cue1.AvailNum)
	}
	if cue2.AvailsExpected != cue1.AvailsExpected {
		t.Errorf("round-trip: AvailsExpected = %d, want %d", cue2.AvailsExpected, cue1.AvailsExpected)
	}
}

// TestPipeline_TimeSignal_FullRoundTrip exercises the complete pipeline with
// time_signal + segmentation descriptors:
// Build SCTE-104 time_signal + seg_desc -> Encode -> WrapST291 -> ParseST291 ->
// Decode -> ToCueMessage -> verify -> FromCueMessage -> Encode -> WrapST291 ->
// ParseST291 -> Decode -> ToCueMessage -> verify fields are preserved.
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
	if err != nil {
		t.Fatalf("Encode (pass 1): %v", err)
	}

	vanc1, err := WrapST291(encoded1)
	if err != nil {
		t.Fatalf("WrapST291 (pass 1): %v", err)
	}

	payload1, err := ParseST291(vanc1)
	if err != nil {
		t.Fatalf("ParseST291 (pass 1): %v", err)
	}

	decoded1, err := Decode(payload1)
	if err != nil {
		t.Fatalf("Decode (pass 1): %v", err)
	}

	cue1, err := ToCueMessage(decoded1)
	if err != nil {
		t.Fatalf("ToCueMessage (pass 1): %v", err)
	}

	// Verify CueMessage fields after the first pass.
	if cue1.CommandType != scte35.CommandTimeSignal {
		t.Errorf("pass 1: CommandType = 0x%02X, want 0x%02X (time_signal)",
			cue1.CommandType, scte35.CommandTimeSignal)
	}
	if len(cue1.Descriptors) != 1 {
		t.Fatalf("pass 1: expected 1 descriptor, got %d", len(cue1.Descriptors))
	}

	desc1 := cue1.Descriptors[0]
	if desc1.SegEventID != 0x0000BEEF {
		t.Errorf("pass 1: SegEventID = 0x%08X, want 0x0000BEEF", desc1.SegEventID)
	}
	if desc1.SegmentationType != 0x34 {
		t.Errorf("pass 1: SegmentationType = 0x%02X, want 0x34", desc1.SegmentationType)
	}
	if desc1.DurationTicks == nil {
		t.Fatal("pass 1: DurationTicks should not be nil")
	}
	if *desc1.DurationTicks != 2700000 {
		t.Errorf("pass 1: DurationTicks = %d, want 2700000", *desc1.DurationTicks)
	}
	if desc1.UPIDType != 0x09 {
		t.Errorf("pass 1: UPIDType = 0x%02X, want 0x09", desc1.UPIDType)
	}
	if !bytes.Equal(desc1.UPID, upid) {
		t.Errorf("pass 1: UPID = %q, want %q", desc1.UPID, upid)
	}
	if desc1.SegNum != 1 {
		t.Errorf("pass 1: SegNum = %d, want 1", desc1.SegNum)
	}
	if desc1.SegExpected != 4 {
		t.Errorf("pass 1: SegExpected = %d, want 4", desc1.SegExpected)
	}

	// --- Second pass: CueMessage -> SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	msg104_2, err := FromCueMessage(cue1)
	if err != nil {
		t.Fatalf("FromCueMessage (pass 2): %v", err)
	}

	encoded2, err := Encode(msg104_2)
	if err != nil {
		t.Fatalf("Encode (pass 2): %v", err)
	}

	vanc2, err := WrapST291(encoded2)
	if err != nil {
		t.Fatalf("WrapST291 (pass 2): %v", err)
	}

	payload2, err := ParseST291(vanc2)
	if err != nil {
		t.Fatalf("ParseST291 (pass 2): %v", err)
	}

	decoded2, err := Decode(payload2)
	if err != nil {
		t.Fatalf("Decode (pass 2): %v", err)
	}

	cue2, err := ToCueMessage(decoded2)
	if err != nil {
		t.Fatalf("ToCueMessage (pass 2): %v", err)
	}

	// Verify all fields are preserved after the full double round-trip.
	if cue2.CommandType != cue1.CommandType {
		t.Errorf("round-trip: CommandType = 0x%02X, want 0x%02X", cue2.CommandType, cue1.CommandType)
	}
	if len(cue2.Descriptors) != 1 {
		t.Fatalf("round-trip: expected 1 descriptor, got %d", len(cue2.Descriptors))
	}

	desc2 := cue2.Descriptors[0]
	if desc2.SegEventID != desc1.SegEventID {
		t.Errorf("round-trip: SegEventID = 0x%08X, want 0x%08X", desc2.SegEventID, desc1.SegEventID)
	}
	if desc2.SegmentationType != desc1.SegmentationType {
		t.Errorf("round-trip: SegmentationType = 0x%02X, want 0x%02X",
			desc2.SegmentationType, desc1.SegmentationType)
	}
	if desc2.DurationTicks == nil {
		t.Fatal("round-trip: DurationTicks should not be nil")
	}
	if *desc2.DurationTicks != *desc1.DurationTicks {
		t.Errorf("round-trip: DurationTicks = %d, want %d", *desc2.DurationTicks, *desc1.DurationTicks)
	}
	if desc2.UPIDType != desc1.UPIDType {
		t.Errorf("round-trip: UPIDType = 0x%02X, want 0x%02X", desc2.UPIDType, desc1.UPIDType)
	}
	if !bytes.Equal(desc2.UPID, desc1.UPID) {
		t.Errorf("round-trip: UPID = %q, want %q", desc2.UPID, desc1.UPID)
	}
	if desc2.SegNum != desc1.SegNum {
		t.Errorf("round-trip: SegNum = %d, want %d", desc2.SegNum, desc1.SegNum)
	}
	if desc2.SegExpected != desc1.SegExpected {
		t.Errorf("round-trip: SegExpected = %d, want %d", desc2.SegExpected, desc1.SegExpected)
	}
}

// TestPipeline_SpliceNull_FullRoundTrip exercises the complete heartbeat pipeline:
// Build splice_null -> Encode -> WrapST291 -> ParseST291 -> Decode -> ToCueMessage ->
// verify CommandType is splice_null -> FromCueMessage -> Encode -> WrapST291 ->
// ParseST291 -> Decode -> ToCueMessage -> verify fields are preserved.
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
	if err != nil {
		t.Fatalf("Encode (pass 1): %v", err)
	}

	vanc1, err := WrapST291(encoded1)
	if err != nil {
		t.Fatalf("WrapST291 (pass 1): %v", err)
	}

	payload1, err := ParseST291(vanc1)
	if err != nil {
		t.Fatalf("ParseST291 (pass 1): %v", err)
	}

	decoded1, err := Decode(payload1)
	if err != nil {
		t.Fatalf("Decode (pass 1): %v", err)
	}

	// Verify decoded SCTE-104 message header fields survived the wire round-trip.
	if decoded1.ASIndex != original.ASIndex {
		t.Errorf("pass 1: ASIndex = %d, want %d", decoded1.ASIndex, original.ASIndex)
	}
	if decoded1.MessageNumber != original.MessageNumber {
		t.Errorf("pass 1: MessageNumber = %d, want %d", decoded1.MessageNumber, original.MessageNumber)
	}
	if len(decoded1.Operations) != 1 {
		t.Fatalf("pass 1: expected 1 operation, got %d", len(decoded1.Operations))
	}
	if decoded1.Operations[0].OpID != OpSpliceNull {
		t.Errorf("pass 1: OpID = 0x%04X, want OpSpliceNull (0x%04X)",
			decoded1.Operations[0].OpID, OpSpliceNull)
	}

	cue1, err := ToCueMessage(decoded1)
	if err != nil {
		t.Fatalf("ToCueMessage (pass 1): %v", err)
	}

	// Verify CueMessage is a splice_null.
	if cue1.CommandType != scte35.CommandSpliceNull {
		t.Errorf("pass 1: CommandType = 0x%02X, want 0x%02X (splice_null)",
			cue1.CommandType, scte35.CommandSpliceNull)
	}

	// --- Second pass: CueMessage -> SCTE-104 -> wire -> SCTE-104 -> CueMessage ---

	msg104_2, err := FromCueMessage(cue1)
	if err != nil {
		t.Fatalf("FromCueMessage (pass 2): %v", err)
	}

	encoded2, err := Encode(msg104_2)
	if err != nil {
		t.Fatalf("Encode (pass 2): %v", err)
	}

	vanc2, err := WrapST291(encoded2)
	if err != nil {
		t.Fatalf("WrapST291 (pass 2): %v", err)
	}

	payload2, err := ParseST291(vanc2)
	if err != nil {
		t.Fatalf("ParseST291 (pass 2): %v", err)
	}

	decoded2, err := Decode(payload2)
	if err != nil {
		t.Fatalf("Decode (pass 2): %v", err)
	}

	// Verify the decoded message still has a single splice_null operation.
	if len(decoded2.Operations) != 1 {
		t.Fatalf("pass 2: expected 1 operation, got %d", len(decoded2.Operations))
	}
	if decoded2.Operations[0].OpID != OpSpliceNull {
		t.Errorf("pass 2: OpID = 0x%04X, want OpSpliceNull (0x%04X)",
			decoded2.Operations[0].OpID, OpSpliceNull)
	}

	cue2, err := ToCueMessage(decoded2)
	if err != nil {
		t.Fatalf("ToCueMessage (pass 2): %v", err)
	}

	// Verify CommandType is preserved after the full double round-trip.
	if cue2.CommandType != scte35.CommandSpliceNull {
		t.Errorf("round-trip: CommandType = 0x%02X, want 0x%02X (splice_null)",
			cue2.CommandType, scte35.CommandSpliceNull)
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
	if err != nil {
		t.Fatalf("Encode (pass 1): %v", err)
	}

	vanc1, err := WrapST291(encoded1)
	if err != nil {
		t.Fatalf("WrapST291 (pass 1): %v", err)
	}

	payload1, err := ParseST291(vanc1)
	if err != nil {
		t.Fatalf("ParseST291 (pass 1): %v", err)
	}

	decoded1, err := Decode(payload1)
	if err != nil {
		t.Fatalf("Decode (pass 1): %v", err)
	}

	cue1, err := ToCueMessage(decoded1)
	if err != nil {
		t.Fatalf("ToCueMessage (pass 1): %v", err)
	}

	if len(cue1.Descriptors) != 1 {
		t.Fatalf("pass 1: expected 1 descriptor, got %d", len(cue1.Descriptors))
	}
	desc1 := cue1.Descriptors[0]
	if desc1.SubSegmentNum != 2 {
		t.Errorf("pass 1: SubSegmentNum = %d, want 2", desc1.SubSegmentNum)
	}
	if desc1.SubSegmentsExpected != 3 {
		t.Errorf("pass 1: SubSegmentsExpected = %d, want 3", desc1.SubSegmentsExpected)
	}

	// --- Pass 2: CueMessage -> SCTE-104 -> wire -> SCTE-104 -> CueMessage ---
	msg104_2, err := FromCueMessage(cue1)
	if err != nil {
		t.Fatalf("FromCueMessage (pass 2): %v", err)
	}

	encoded2, err := Encode(msg104_2)
	if err != nil {
		t.Fatalf("Encode (pass 2): %v", err)
	}

	vanc2, err := WrapST291(encoded2)
	if err != nil {
		t.Fatalf("WrapST291 (pass 2): %v", err)
	}

	payload2, err := ParseST291(vanc2)
	if err != nil {
		t.Fatalf("ParseST291 (pass 2): %v", err)
	}

	decoded2, err := Decode(payload2)
	if err != nil {
		t.Fatalf("Decode (pass 2): %v", err)
	}

	cue2, err := ToCueMessage(decoded2)
	if err != nil {
		t.Fatalf("ToCueMessage (pass 2): %v", err)
	}

	if len(cue2.Descriptors) != 1 {
		t.Fatalf("round-trip: expected 1 descriptor, got %d", len(cue2.Descriptors))
	}
	desc2 := cue2.Descriptors[0]
	if desc2.SubSegmentNum != desc1.SubSegmentNum {
		t.Errorf("round-trip: SubSegmentNum = %d, want %d", desc2.SubSegmentNum, desc1.SubSegmentNum)
	}
	if desc2.SubSegmentsExpected != desc1.SubSegmentsExpected {
		t.Errorf("round-trip: SubSegmentsExpected = %d, want %d",
			desc2.SubSegmentsExpected, desc1.SubSegmentsExpected)
	}
}
