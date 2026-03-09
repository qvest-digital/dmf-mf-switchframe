package scte35

import (
	"testing"
	"time"
)

func TestNewSpliceInsert_CueOut(t *testing.T) {
	dur := 30 * time.Second
	msg := NewSpliceInsert(42, dur, true, true)

	if msg.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", msg.CommandType)
	}
	if msg.EventID != 42 {
		t.Fatalf("expected event ID 42, got %d", msg.EventID)
	}
	if !msg.IsOut {
		t.Fatal("expected IsOut=true")
	}
	if !msg.AutoReturn {
		t.Fatal("expected AutoReturn=true")
	}
	if msg.BreakDuration == nil || *msg.BreakDuration != dur {
		t.Fatalf("expected 30s duration, got %v", msg.BreakDuration)
	}
}

func TestNewSpliceInsert_CueIn(t *testing.T) {
	msg := NewSpliceInsert(42, 0, false, false)

	if msg.IsOut {
		t.Fatal("expected IsOut=false for cue-in")
	}
	if msg.BreakDuration != nil {
		t.Fatalf("expected nil duration for cue-in, got %v", msg.BreakDuration)
	}
}

func TestNewTimeSignal(t *testing.T) {
	dur := 60 * time.Second
	upid := []byte("https://ads.example.com/avail/1")
	msg := NewTimeSignal(0x34, dur, 0x0F, upid)

	if msg.CommandType != CommandTimeSignal {
		t.Fatalf("expected time_signal, got %d", msg.CommandType)
	}
	if len(msg.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(msg.Descriptors))
	}
	d := msg.Descriptors[0]
	if d.SegmentationType != 0x34 {
		t.Fatalf("expected seg type 0x34, got 0x%02x", d.SegmentationType)
	}
	if d.UPIDType != 0x0F {
		t.Fatalf("expected UPID type 0x0F, got 0x%02x", d.UPIDType)
	}
}

func TestNewTimeSignalMulti(t *testing.T) {
	descs := []SegmentationDescriptor{
		{SegmentationType: 0x34, UPIDType: 0x0F, UPID: []byte("uri1")},
		{SegmentationType: 0x36, UPIDType: 0x09, UPID: []byte("adi1")},
	}
	msg := NewTimeSignalMulti(descs)

	if len(msg.Descriptors) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(msg.Descriptors))
	}
}

func TestEncode_SpliceInsert_RoundTrip(t *testing.T) {
	dur := 30 * time.Second
	msg := NewSpliceInsert(1, dur, true, true)

	encoded, err := msg.Encode(true) // verify=true
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded data is empty")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.CommandType != CommandSpliceInsert {
		t.Fatalf("round-trip: expected splice_insert, got %d", decoded.CommandType)
	}
	if decoded.EventID != 1 {
		t.Fatalf("round-trip: expected event ID 1, got %d", decoded.EventID)
	}
}

func TestEncode_TimeSignal_RoundTrip(t *testing.T) {
	dur := 60 * time.Second
	msg := NewTimeSignal(0x34, dur, 0x0F, []byte("https://example.com"))

	encoded, err := msg.Encode(true)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.CommandType != CommandTimeSignal {
		t.Fatalf("round-trip: expected time_signal, got %d", decoded.CommandType)
	}
	if len(decoded.Descriptors) != 1 {
		t.Fatalf("round-trip: expected 1 descriptor, got %d", len(decoded.Descriptors))
	}
}

func TestEncode_SpliceNull(t *testing.T) {
	msg := &CueMessage{CommandType: CommandSpliceNull}

	encoded, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode splice_null failed: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("splice_null encoded data is empty")
	}
}

func TestEncode_SpliceInsert_Cancel_RoundTrip(t *testing.T) {
	msg := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    99,
		SpliceEventCancelIndicator: true,
	}

	encoded, err := msg.Encode(true) // verify=true
	if err != nil {
		t.Fatalf("encode cancel failed: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded cancel data is empty")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode cancel failed: %v", err)
	}
	if decoded.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", decoded.CommandType)
	}
	if decoded.EventID != 99 {
		t.Fatalf("expected event ID 99, got %d", decoded.EventID)
	}
	if !decoded.SpliceEventCancelIndicator {
		t.Fatal("expected SpliceEventCancelIndicator=true after round-trip")
	}
	if decoded.IsOut {
		t.Fatal("expected IsOut=false for cancel message")
	}
}

func TestEncode_TimeSignal_CancelSegmentation_RoundTrip(t *testing.T) {
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:                      42,
				SegmentationEventCancelIndicator: true,
			},
		},
	}

	encoded, err := msg.Encode(true) // verify=true
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded data is empty")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.CommandType != CommandTimeSignal {
		t.Fatalf("round-trip: expected time_signal, got %d", decoded.CommandType)
	}
	if len(decoded.Descriptors) != 1 {
		t.Fatalf("round-trip: expected 1 descriptor, got %d", len(decoded.Descriptors))
	}
	d := decoded.Descriptors[0]
	if !d.SegmentationEventCancelIndicator {
		t.Fatal("round-trip: expected SegmentationEventCancelIndicator=true")
	}
	if d.SegEventID != 42 {
		t.Fatalf("round-trip: expected SegEventID=42, got %d", d.SegEventID)
	}
}

func TestEncode_TimeSignal_WithPTS(t *testing.T) {
	// Create a time_signal with SpliceTimePTS set to 8100000 (90s at 90kHz).
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34,
				UPIDType:         0x0F,
				UPID:             []byte("https://ads.example.com/avail/1"),
			},
		},
	}
	pts := int64(8100000) // 90 seconds at 90kHz
	msg.SpliceTimePTS = &pts

	encoded, err := msg.Encode(true) // verify=true
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded data is empty")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.CommandType != CommandTimeSignal {
		t.Fatalf("expected time_signal, got %d", decoded.CommandType)
	}
	if decoded.SpliceTimePTS == nil {
		t.Fatal("expected SpliceTimePTS to be set after round-trip")
	}
	if *decoded.SpliceTimePTS != 8100000 {
		t.Fatalf("expected SpliceTimePTS=8100000, got %d", *decoded.SpliceTimePTS)
	}
	if decoded.Timing != "scheduled" {
		t.Fatalf("expected Timing=scheduled, got %s", decoded.Timing)
	}
}

func TestEncode_SpliceInsert_Scheduled_RoundTrip(t *testing.T) {
	dur := 30 * time.Second
	pts := int64(8100000) // 90s at 90kHz
	msg := &CueMessage{
		CommandType:   CommandSpliceInsert,
		EventID:       5,
		IsOut:         true,
		AutoReturn:    true,
		BreakDuration: &dur,
		SpliceTimePTS: &pts,
		Timing:        "scheduled",
	}

	encoded, err := msg.Encode(true) // verify=true
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded data is empty")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", decoded.CommandType)
	}
	if decoded.Timing != "scheduled" {
		t.Fatalf("expected timing=scheduled, got %q", decoded.Timing)
	}
	if decoded.SpliceTimePTS == nil {
		t.Fatal("expected SpliceTimePTS to be set")
	}
	if *decoded.SpliceTimePTS != 8100000 {
		t.Fatalf("expected SpliceTimePTS=8100000, got %d", *decoded.SpliceTimePTS)
	}
	if decoded.EventID != 5 {
		t.Fatalf("expected EventID=5, got %d", decoded.EventID)
	}
	if !decoded.IsOut {
		t.Fatal("expected IsOut=true")
	}
}

func TestEncode_SpliceInsert_Immediate_NoSpliceTime(t *testing.T) {
	dur := 30 * time.Second
	msg := &CueMessage{
		CommandType:   CommandSpliceInsert,
		EventID:       10,
		IsOut:         true,
		AutoReturn:    true,
		BreakDuration: &dur,
		// SpliceTimePTS is nil — immediate mode
	}

	encoded, err := msg.Encode(true) // verify=true
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded data is empty")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", decoded.CommandType)
	}
	if decoded.Timing != "immediate" {
		t.Fatalf("expected timing=immediate, got %q", decoded.Timing)
	}
	if decoded.SpliceTimePTS != nil {
		t.Fatalf("expected SpliceTimePTS=nil, got %d", *decoded.SpliceTimePTS)
	}
}

func TestEncode_SpliceInsert_AvailFields_RoundTrip(t *testing.T) {
	dur := 30 * time.Second
	msg := &CueMessage{
		CommandType:     CommandSpliceInsert,
		EventID:         10,
		IsOut:           true,
		AutoReturn:      true,
		BreakDuration:   &dur,
		Timing:          "immediate",
		UniqueProgramID: 1234,
		AvailNum:        2,
		AvailsExpected:  4,
	}

	encoded, err := msg.Encode(true) // verify=true
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded data is empty")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", decoded.CommandType)
	}
	if decoded.EventID != 10 {
		t.Fatalf("expected event ID 10, got %d", decoded.EventID)
	}
	if !decoded.IsOut {
		t.Fatal("expected IsOut=true")
	}
	if decoded.UniqueProgramID != 1234 {
		t.Fatalf("expected UniqueProgramID=1234, got %d", decoded.UniqueProgramID)
	}
	if decoded.AvailNum != 2 {
		t.Fatalf("expected AvailNum=2, got %d", decoded.AvailNum)
	}
	if decoded.AvailsExpected != 4 {
		t.Fatalf("expected AvailsExpected=4, got %d", decoded.AvailsExpected)
	}
}

func TestDecode_InvalidCRC(t *testing.T) {
	dur := 30 * time.Second
	msg := NewSpliceInsert(1, dur, true, true)
	encoded, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Corrupt the last byte (CRC-32).
	encoded[len(encoded)-1] ^= 0xFF

	_, err = Decode(encoded)
	if err == nil {
		t.Fatal("expected CRC error on corrupt data")
	}
}
