package scte104

import (
	"bytes"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/scte35"
)

// ---- ToCueMessage tests ----

func TestToCueMessage_NilMessage(t *testing.T) {
	_, err := ToCueMessage(nil)
	if err == nil {
		t.Fatal("expected error for nil message")
	}
}

func TestToCueMessage_EmptyOps(t *testing.T) {
	_, err := ToCueMessage(&Message{})
	if err == nil {
		t.Fatal("expected error for empty operations")
	}
}

func TestToCueMessage_SpliceNull(t *testing.T) {
	msg := &Message{
		Operations: []Operation{
			{OpID: OpSpliceNull},
		},
	}

	cue, err := ToCueMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cue.CommandType != scte35.CommandSpliceNull {
		t.Errorf("CommandType = 0x%02X, want CommandSpliceNull", cue.CommandType)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cue.CommandType != scte35.CommandSpliceInsert {
		t.Errorf("CommandType = 0x%02X, want CommandSpliceInsert", cue.CommandType)
	}
	if !cue.IsOut {
		t.Error("IsOut should be true for cue-out")
	}
	if cue.EventID != 100 {
		t.Errorf("EventID = %d, want 100", cue.EventID)
	}
	if cue.UniqueProgramID != 50 {
		t.Errorf("UniqueProgramID = %d, want 50", cue.UniqueProgramID)
	}
	if cue.BreakDuration == nil {
		t.Fatal("BreakDuration should not be nil")
	}
	expected := 30 * time.Second
	if *cue.BreakDuration != expected {
		t.Errorf("BreakDuration = %v, want %v", *cue.BreakDuration, expected)
	}
	if cue.AvailNum != 1 {
		t.Errorf("AvailNum = %d, want 1", cue.AvailNum)
	}
	if cue.AvailsExpected != 2 {
		t.Errorf("AvailsExpected = %d, want 2", cue.AvailsExpected)
	}
	if !cue.AutoReturn {
		t.Error("AutoReturn should be true")
	}
	if cue.Timing != "immediate" {
		t.Errorf("Timing = %q, want %q", cue.Timing, "immediate")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cue.IsOut {
		t.Error("IsOut should be false for cue-in")
	}
	if cue.SpliceEventCancelIndicator {
		t.Error("SpliceEventCancelIndicator should be false")
	}
	if cue.Timing != "immediate" {
		t.Errorf("Timing = %q, want %q", cue.Timing, "immediate")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cue.IsOut {
		t.Error("IsOut should be true for SpliceStartNormal")
	}
	if cue.Timing != "scheduled" {
		t.Errorf("Timing = %q, want %q", cue.Timing, "scheduled")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cue.IsOut {
		t.Error("IsOut should be false for SpliceEndNormal")
	}
	if cue.Timing != "scheduled" {
		t.Errorf("Timing = %q, want %q", cue.Timing, "scheduled")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cue.SpliceEventCancelIndicator {
		t.Error("SpliceEventCancelIndicator should be true")
	}
	if cue.EventID != 100 {
		t.Errorf("EventID = %d, want 100", cue.EventID)
	}
	if cue.BreakDuration != nil {
		t.Error("BreakDuration should be nil for cancel")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cue.CommandType != scte35.CommandTimeSignal {
		t.Errorf("CommandType = 0x%02X, want CommandTimeSignal", cue.CommandType)
	}
	if len(cue.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(cue.Descriptors))
	}

	desc := cue.Descriptors[0]
	if desc.SegEventID != 500 {
		t.Errorf("SegEventID = %d, want 500", desc.SegEventID)
	}
	if desc.SegmentationType != 0x34 {
		t.Errorf("SegmentationType = 0x%02X, want 0x34", desc.SegmentationType)
	}
	if desc.DurationTicks == nil {
		t.Fatal("DurationTicks should not be nil")
	}
	if *desc.DurationTicks != 2700000 {
		t.Errorf("DurationTicks = %d, want 2700000", *desc.DurationTicks)
	}
	if desc.UPIDType != 0x09 {
		t.Errorf("UPIDType = 0x%02X, want 0x09", desc.UPIDType)
	}
	if !bytes.Equal(desc.UPID, upid) {
		t.Errorf("UPID = %q, want %q", desc.UPID, upid)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cue.Descriptors) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(cue.Descriptors))
	}
	if cue.Descriptors[0].SegEventID != 1 {
		t.Errorf("descriptor[0].SegEventID = %d, want 1", cue.Descriptors[0].SegEventID)
	}
	if cue.Descriptors[1].SegEventID != 2 {
		t.Errorf("descriptor[1].SegEventID = %d, want 2", cue.Descriptors[1].SegEventID)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cue.CommandType != scte35.CommandTimeSignal {
		t.Errorf("CommandType = 0x%02X, want CommandTimeSignal", cue.CommandType)
	}
	if len(cue.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(cue.Descriptors))
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cue.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(cue.Descriptors))
	}
	if !cue.Descriptors[0].SegmentationEventCancelIndicator {
		t.Error("cancel indicator should be true")
	}
}

// ---- FromCueMessage tests ----

func TestFromCueMessage_NilMessage(t *testing.T) {
	_, err := FromCueMessage(nil)
	if err == nil {
		t.Fatal("expected error for nil message")
	}
}

func TestFromCueMessage_SpliceNull(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceNull,
	}

	msg, err := FromCueMessage(cue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}
	if msg.Operations[0].OpID != OpSpliceNull {
		t.Errorf("OpID = 0x%04X, want OpSpliceNull", msg.Operations[0].OpID)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(msg.Operations))
	}

	srd, ok := msg.Operations[0].Data.(*SpliceRequestData)
	if !ok {
		t.Fatalf("expected *SpliceRequestData, got %T", msg.Operations[0].Data)
	}

	if srd.SpliceInsertType != SpliceStartImmediate {
		t.Errorf("SpliceInsertType = %d, want %d (SpliceStartImmediate)", srd.SpliceInsertType, SpliceStartImmediate)
	}
	if srd.SpliceEventID != 100 {
		t.Errorf("SpliceEventID = %d, want 100", srd.SpliceEventID)
	}
	if srd.BreakDuration != 300 { // 30s / 100ms = 300
		t.Errorf("BreakDuration = %d, want 300", srd.BreakDuration)
	}
	if !srd.AutoReturnFlag {
		t.Error("AutoReturnFlag should be true")
	}
	if srd.UniqueProgramID != 50 {
		t.Errorf("UniqueProgramID = %d, want 50", srd.UniqueProgramID)
	}
	if srd.AvailNum != 1 {
		t.Errorf("AvailNum = %d, want 1", srd.AvailNum)
	}
	if srd.AvailsExpected != 2 {
		t.Errorf("AvailsExpected = %d, want 2", srd.AvailsExpected)
	}
}

func TestFromCueMessage_SpliceInsert_CueIn(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceInsert,
		EventID:     200,
		IsOut:       false,
	}

	msg, err := FromCueMessage(cue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	if srd.SpliceInsertType != SpliceEndImmediate {
		t.Errorf("SpliceInsertType = %d, want %d (SpliceEndImmediate)", srd.SpliceInsertType, SpliceEndImmediate)
	}
}

func TestFromCueMessage_SpliceInsert_Cancel(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType:                 scte35.CommandSpliceInsert,
		EventID:                    300,
		SpliceEventCancelIndicator: true,
	}

	msg, err := FromCueMessage(cue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srd := msg.Operations[0].Data.(*SpliceRequestData)
	if srd.SpliceInsertType != SpliceCancel {
		t.Errorf("SpliceInsertType = %d, want %d (SpliceCancel)", srd.SpliceInsertType, SpliceCancel)
	}
	if srd.SpliceEventID != 300 {
		t.Errorf("SpliceEventID = %d, want 300", srd.SpliceEventID)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msg.Operations) != 2 {
		t.Fatalf("expected 2 operations (time_signal + seg_desc), got %d", len(msg.Operations))
	}

	// First op: time_signal_request.
	if msg.Operations[0].OpID != OpTimeSignalRequest {
		t.Errorf("op[0].OpID = 0x%04X, want OpTimeSignalRequest", msg.Operations[0].OpID)
	}

	// Second op: segmentation_descriptor_request.
	if msg.Operations[1].OpID != OpSegmentationDescriptorRequest {
		t.Errorf("op[1].OpID = 0x%04X, want OpSegmentationDescriptorRequest", msg.Operations[1].OpID)
	}

	sd := msg.Operations[1].Data.(*SegmentationDescriptorRequest)
	if sd.SegEventID != 500 {
		t.Errorf("SegEventID = %d, want 500", sd.SegEventID)
	}
	if sd.SegmentationTypeID != 0x34 {
		t.Errorf("SegmentationTypeID = 0x%02X, want 0x34", sd.SegmentationTypeID)
	}
	if sd.DurationTicks != 2700000 {
		t.Errorf("DurationTicks = %d, want 2700000", sd.DurationTicks)
	}
	if sd.UPIDType != 0x09 {
		t.Errorf("UPIDType = 0x%02X, want 0x09", sd.UPIDType)
	}
	if !bytes.Equal(sd.UPID, []byte("TEST")) {
		t.Errorf("UPID = %q, want %q", sd.UPID, "TEST")
	}
}

func TestFromCueMessage_UnsupportedType(t *testing.T) {
	cue := &scte35.CueMessage{
		CommandType: 0xFF,
	}

	_, err := FromCueMessage(cue)
	if err == nil {
		t.Fatal("expected error for unsupported command type")
	}
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
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	roundTripped, err := ToCueMessage(msg104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if roundTripped.CommandType != original.CommandType {
		t.Errorf("CommandType = 0x%02X, want 0x%02X", roundTripped.CommandType, original.CommandType)
	}
	if roundTripped.EventID != original.EventID {
		t.Errorf("EventID = %d, want %d", roundTripped.EventID, original.EventID)
	}
	if roundTripped.IsOut != original.IsOut {
		t.Errorf("IsOut = %v, want %v", roundTripped.IsOut, original.IsOut)
	}
	if roundTripped.AutoReturn != original.AutoReturn {
		t.Errorf("AutoReturn = %v, want %v", roundTripped.AutoReturn, original.AutoReturn)
	}
	if roundTripped.BreakDuration == nil {
		t.Fatal("BreakDuration should not be nil")
	}
	if *roundTripped.BreakDuration != *original.BreakDuration {
		t.Errorf("BreakDuration = %v, want %v", *roundTripped.BreakDuration, *original.BreakDuration)
	}
	if roundTripped.UniqueProgramID != original.UniqueProgramID {
		t.Errorf("UniqueProgramID = %d, want %d", roundTripped.UniqueProgramID, original.UniqueProgramID)
	}
	if roundTripped.AvailNum != original.AvailNum {
		t.Errorf("AvailNum = %d, want %d", roundTripped.AvailNum, original.AvailNum)
	}
	if roundTripped.AvailsExpected != original.AvailsExpected {
		t.Errorf("AvailsExpected = %d, want %d", roundTripped.AvailsExpected, original.AvailsExpected)
	}
}

func TestRoundTrip_SpliceInsert_CueIn(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceInsert,
		EventID:     55,
		IsOut:       false,
	}

	msg104, err := FromCueMessage(original)
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	roundTripped, err := ToCueMessage(msg104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if roundTripped.IsOut != false {
		t.Error("IsOut should be false after round-trip")
	}
	if roundTripped.SpliceEventCancelIndicator {
		t.Error("cancel should be false after round-trip")
	}
}

func TestRoundTrip_SpliceInsert_Cancel(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType:                 scte35.CommandSpliceInsert,
		EventID:                    77,
		SpliceEventCancelIndicator: true,
	}

	msg104, err := FromCueMessage(original)
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	roundTripped, err := ToCueMessage(msg104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if !roundTripped.SpliceEventCancelIndicator {
		t.Error("SpliceEventCancelIndicator should be true after round-trip")
	}
	if roundTripped.EventID != 77 {
		t.Errorf("EventID = %d, want 77", roundTripped.EventID)
	}
}

func TestRoundTrip_SpliceNull(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceNull,
	}

	msg104, err := FromCueMessage(original)
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	roundTripped, err := ToCueMessage(msg104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if roundTripped.CommandType != scte35.CommandSpliceNull {
		t.Errorf("CommandType = 0x%02X, want CommandSpliceNull", roundTripped.CommandType)
	}
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
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	roundTripped, err := ToCueMessage(msg104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if roundTripped.CommandType != scte35.CommandTimeSignal {
		t.Errorf("CommandType = 0x%02X, want CommandTimeSignal", roundTripped.CommandType)
	}
	if len(roundTripped.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(roundTripped.Descriptors))
	}

	desc := roundTripped.Descriptors[0]
	origDesc := original.Descriptors[0]

	if desc.SegEventID != origDesc.SegEventID {
		t.Errorf("SegEventID = %d, want %d", desc.SegEventID, origDesc.SegEventID)
	}
	if desc.SegmentationType != origDesc.SegmentationType {
		t.Errorf("SegmentationType = 0x%02X, want 0x%02X", desc.SegmentationType, origDesc.SegmentationType)
	}
	if desc.DurationTicks == nil {
		t.Fatal("DurationTicks should not be nil")
	}
	if *desc.DurationTicks != *origDesc.DurationTicks {
		t.Errorf("DurationTicks = %d, want %d", *desc.DurationTicks, *origDesc.DurationTicks)
	}
	if desc.UPIDType != origDesc.UPIDType {
		t.Errorf("UPIDType = 0x%02X, want 0x%02X", desc.UPIDType, origDesc.UPIDType)
	}
	if !bytes.Equal(desc.UPID, origDesc.UPID) {
		t.Errorf("UPID = %q, want %q", desc.UPID, origDesc.UPID)
	}
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
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	roundTripped, err := ToCueMessage(msg104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if len(roundTripped.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(roundTripped.Descriptors))
	}
	if !roundTripped.Descriptors[0].SegmentationEventCancelIndicator {
		t.Error("cancel indicator should be true after round-trip")
	}
	if roundTripped.Descriptors[0].SegEventID != 888 {
		t.Errorf("SegEventID = %d, want 888", roundTripped.Descriptors[0].SegEventID)
	}
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
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	// SCTE-104 Message -> binary
	wireData, err := Encode(msg104)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// binary -> SCTE-104 Message
	decoded104, err := Decode(wireData)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// SCTE-104 Message -> CueMessage
	result, err := ToCueMessage(decoded104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if result.CommandType != original.CommandType {
		t.Errorf("CommandType = 0x%02X, want 0x%02X", result.CommandType, original.CommandType)
	}
	if result.EventID != original.EventID {
		t.Errorf("EventID = %d, want %d", result.EventID, original.EventID)
	}
	if result.IsOut != original.IsOut {
		t.Errorf("IsOut = %v, want %v", result.IsOut, original.IsOut)
	}
	if *result.BreakDuration != *original.BreakDuration {
		t.Errorf("BreakDuration = %v, want %v", *result.BreakDuration, *original.BreakDuration)
	}
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
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	wireData, err := Encode(msg104)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	decoded104, err := Decode(wireData)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	result, err := ToCueMessage(decoded104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if result.CommandType != scte35.CommandTimeSignal {
		t.Errorf("CommandType = 0x%02X, want CommandTimeSignal", result.CommandType)
	}
	if len(result.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(result.Descriptors))
	}
	if *result.Descriptors[0].DurationTicks != ticks {
		t.Errorf("DurationTicks = %d, want %d", *result.Descriptors[0].DurationTicks, ticks)
	}
	if !bytes.Equal(result.Descriptors[0].UPID, upid) {
		t.Errorf("UPID = %q, want %q", result.Descriptors[0].UPID, upid)
	}
}

func TestFullWireRoundTrip_SpliceNull(t *testing.T) {
	original := &scte35.CueMessage{
		CommandType: scte35.CommandSpliceNull,
	}

	msg104, err := FromCueMessage(original)
	if err != nil {
		t.Fatalf("FromCueMessage error: %v", err)
	}

	wireData, err := Encode(msg104)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	decoded104, err := Decode(wireData)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	result, err := ToCueMessage(decoded104)
	if err != nil {
		t.Fatalf("ToCueMessage error: %v", err)
	}

	if result.CommandType != scte35.CommandSpliceNull {
		t.Errorf("CommandType = 0x%02X, want CommandSpliceNull", result.CommandType)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cue.BreakDuration != nil {
		t.Errorf("BreakDuration should be nil for zero duration, got %v", *cue.BreakDuration)
	}
}
