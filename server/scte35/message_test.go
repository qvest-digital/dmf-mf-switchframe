package scte35

import (
	"encoding/binary"
	"strings"
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

func TestDecode_MultiDescriptor_DeliveryRestrictions(t *testing.T) {
	// Build a time_signal with two descriptors having different delivery restrictions.
	dur1 := uint64(2700000)
	dur2 := uint64(900000)
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34,
				SegEventID:       100,
				DurationTicks:    &dur1,
				UPIDType:         0x0F,
				UPID:             []byte("https://example.com/1"),
				SegNum:           1,
				SegExpected:      1,
				DeliveryRestrictions: &DeliveryRestrictions{
					WebDeliveryAllowed: true,
					NoRegionalBlackout: false,
					ArchiveAllowed:     true,
					DeviceRestrictions: 0,
				},
			},
			{
				SegmentationType: 0x36,
				SegEventID:       200,
				DurationTicks:    &dur2,
				UPIDType:         0x0F,
				UPID:             []byte("https://example.com/2"),
				SegNum:           1,
				SegExpected:      1,
				DeliveryRestrictions: &DeliveryRestrictions{
					WebDeliveryAllowed: false,
					NoRegionalBlackout: true,
					ArchiveAllowed:     false,
					DeviceRestrictions: 3,
				},
			},
		},
	}

	encoded, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if len(decoded.Descriptors) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(decoded.Descriptors))
	}

	// Verify first descriptor's restrictions.
	dr1 := decoded.Descriptors[0].DeliveryRestrictions
	if dr1 == nil {
		t.Fatal("descriptor[0] DeliveryRestrictions is nil")
	}
	if !dr1.WebDeliveryAllowed {
		t.Error("descriptor[0] WebDeliveryAllowed should be true")
	}
	if dr1.NoRegionalBlackout {
		t.Error("descriptor[0] NoRegionalBlackout should be false")
	}
	if !dr1.ArchiveAllowed {
		t.Error("descriptor[0] ArchiveAllowed should be true")
	}

	// Verify second descriptor has DIFFERENT restrictions.
	dr2 := decoded.Descriptors[1].DeliveryRestrictions
	if dr2 == nil {
		t.Fatal("descriptor[1] DeliveryRestrictions is nil")
	}
	if dr2.WebDeliveryAllowed {
		t.Error("descriptor[1] WebDeliveryAllowed should be false")
	}
	if !dr2.NoRegionalBlackout {
		t.Error("descriptor[1] NoRegionalBlackout should be true")
	}
	if dr2.ArchiveAllowed {
		t.Error("descriptor[1] ArchiveAllowed should be false")
	}
	if dr2.DeviceRestrictions != 3 {
		t.Errorf("descriptor[1] DeviceRestrictions = %d, want 3", dr2.DeviceRestrictions)
	}

	// Top-level DeliveryRestrictions should match first descriptor (backward compat).
	if decoded.DeliveryRestrictions == nil {
		t.Fatal("top-level DeliveryRestrictions is nil")
	}
	if !decoded.DeliveryRestrictions.WebDeliveryAllowed {
		t.Error("top-level WebDeliveryAllowed should match first descriptor")
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

func TestEncode_TimeSignal_NilPTS(t *testing.T) {
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 1},
		},
		// SpliceTimePTS intentionally nil.
	}

	encoded, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	// When PTS is nil, the decoded message should also have nil PTS
	// (time_specified_flag=0), not PTS=0.
	if decoded.SpliceTimePTS != nil {
		t.Fatalf("expected nil SpliceTimePTS, got %d", *decoded.SpliceTimePTS)
	}
}

func TestEncode_SegNum_RoundTrip(t *testing.T) {
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34,
				SegEventID:       100,
				SegNum:           3,
				SegExpected:      5,
			},
		},
	}

	encoded, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if len(decoded.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(decoded.Descriptors))
	}
	d := decoded.Descriptors[0]
	if d.SegNum != 3 {
		t.Errorf("SegNum = %d, want 3", d.SegNum)
	}
	if d.SegExpected != 5 {
		t.Errorf("SegExpected = %d, want 5", d.SegExpected)
	}
}

func TestDecodeSpliceInsertCancel_ValidCRC(t *testing.T) {
	// Encode a cancel message — the library produces correct CRC-32.
	msg := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    12345,
		SpliceEventCancelIndicator: true,
	}
	data, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Call the fallback decoder directly.
	decoded, err := decodeSpliceInsertCancel(data)
	if err != nil {
		t.Fatalf("decodeSpliceInsertCancel failed: %v", err)
	}
	if decoded.EventID != 12345 {
		t.Fatalf("expected EventID=12345, got %d", decoded.EventID)
	}
	if !decoded.SpliceEventCancelIndicator {
		t.Fatal("expected SpliceEventCancelIndicator=true")
	}
}

func TestDecodeSpliceInsertCancel_CorruptCRC(t *testing.T) {
	// Encode a cancel message with correct CRC.
	msg := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    12345,
		SpliceEventCancelIndicator: true,
	}
	data, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Corrupt a byte in the middle of the payload.
	data[10] ^= 0xFF

	_, err = decodeSpliceInsertCancel(data)
	if err == nil {
		t.Fatal("expected CRC error on corrupt data")
	}
	if !strings.Contains(err.Error(), "CRC-32 mismatch") {
		t.Fatalf("expected CRC-32 mismatch error, got: %v", err)
	}
}

func TestDecodeSpliceInsertCancel_SectionLengthExceedsData(t *testing.T) {
	// Construct minimal data where section_length claims more than available.
	data := make([]byte, 25)
	data[0] = 0xFC                // table_id
	data[13] = CommandSpliceInsert // splice_command_type
	// Set section_length to 255 (way more than 22 remaining bytes).
	data[1] = 0x00
	data[2] = 0xFF

	_, err := decodeSpliceInsertCancel(data)
	if err == nil {
		t.Fatal("expected error for section length exceeding data")
	}
	if !strings.Contains(err.Error(), "section length exceeds data") {
		t.Fatalf("expected 'section length exceeds data' error, got: %v", err)
	}
}

func TestCrc32MPEG2(t *testing.T) {
	// Known test vector: empty input should return 0xFFFFFFFF (initial CRC, no data).
	// Actually for MPEG-2 CRC, the initial value is 0xFFFFFFFF with no final XOR.
	// For zero-length data the result is 0xFFFFFFFF.
	crc := crc32MPEG2(nil)
	if crc != 0xFFFFFFFF {
		t.Fatalf("expected 0xFFFFFFFF for empty data, got 0x%08X", crc)
	}

	// Test with a known SCTE-35 section: encode a message and verify
	// the CRC of all-but-last-4-bytes matches the last 4 bytes.
	msg := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    42,
		SpliceEventCancelIndicator: true,
	}
	data, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	sectionLen := 3 + (int(data[1]&0x0F)<<8 | int(data[2]))
	crcData := data[:sectionLen-4]
	expectedCRC := binary.BigEndian.Uint32(data[sectionLen-4 : sectionLen])
	computedCRC := crc32MPEG2(crcData)
	if computedCRC != expectedCRC {
		t.Fatalf("CRC mismatch: computed 0x%08X, expected 0x%08X", computedCRC, expectedCRC)
	}
}

func TestMultipleUPIDs_RoundTrip(t *testing.T) {
	// A segmentation descriptor with 2 UPIDs should survive encode→decode.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34, // Provider Placement Opportunity Start
				SegEventID:       500,
				UPIDType:         0x09, // ADI (first UPID)
				UPID:             []byte("SIGNAL:first-upid"),
				AdditionalUPIDs: []AdditionalUPID{
					{Type: 0x0F, Value: []byte("https://example.com/second")},
				},
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
		t.Fatalf("expected time_signal, got %d", decoded.CommandType)
	}
	if len(decoded.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(decoded.Descriptors))
	}

	d := decoded.Descriptors[0]

	// First UPID preserved in backward-compatible fields.
	if d.UPIDType != 0x09 {
		t.Fatalf("first UPID type: expected 0x09, got 0x%02x", d.UPIDType)
	}
	if string(d.UPID) != "SIGNAL:first-upid" {
		t.Fatalf("first UPID value: expected %q, got %q", "SIGNAL:first-upid", string(d.UPID))
	}

	// Second UPID preserved in AdditionalUPIDs.
	if len(d.AdditionalUPIDs) != 1 {
		t.Fatalf("expected 1 additional UPID, got %d", len(d.AdditionalUPIDs))
	}
	if d.AdditionalUPIDs[0].Type != 0x0F {
		t.Fatalf("additional UPID type: expected 0x0F, got 0x%02x", d.AdditionalUPIDs[0].Type)
	}
	if string(d.AdditionalUPIDs[0].Value) != "https://example.com/second" {
		t.Fatalf("additional UPID value: expected %q, got %q",
			"https://example.com/second", string(d.AdditionalUPIDs[0].Value))
	}
}

func TestMultipleUPIDs_ThreeUPIDs_RoundTrip(t *testing.T) {
	// A segmentation descriptor with 3 UPIDs.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34,
				SegEventID:       501,
				UPIDType:         0x09,
				UPID:             []byte("first"),
				AdditionalUPIDs: []AdditionalUPID{
					{Type: 0x09, Value: []byte("second")},
					{Type: 0x0F, Value: []byte("third")},
				},
			},
		},
	}

	encoded, err := msg.Encode(true)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	d := decoded.Descriptors[0]
	if d.UPIDType != 0x09 {
		t.Fatalf("first UPID type: expected 0x09, got 0x%02x", d.UPIDType)
	}
	if string(d.UPID) != "first" {
		t.Fatalf("first UPID value: expected %q, got %q", "first", string(d.UPID))
	}
	if len(d.AdditionalUPIDs) != 2 {
		t.Fatalf("expected 2 additional UPIDs, got %d", len(d.AdditionalUPIDs))
	}
	if string(d.AdditionalUPIDs[0].Value) != "second" {
		t.Fatalf("second UPID: expected %q, got %q", "second", string(d.AdditionalUPIDs[0].Value))
	}
	if string(d.AdditionalUPIDs[1].Value) != "third" {
		t.Fatalf("third UPID: expected %q, got %q", "third", string(d.AdditionalUPIDs[1].Value))
	}
}

func TestSingleUPID_BackwardCompatible(t *testing.T) {
	// A descriptor with only 1 UPID should have no AdditionalUPIDs.
	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://example.com"))

	encoded, err := msg.Encode(true)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	d := decoded.Descriptors[0]
	if d.UPIDType != 0x0F {
		t.Fatalf("UPID type: expected 0x0F, got 0x%02x", d.UPIDType)
	}
	if len(d.AdditionalUPIDs) != 0 {
		t.Fatalf("expected 0 additional UPIDs, got %d", len(d.AdditionalUPIDs))
	}
}

func TestEncode_SubSegment_RoundTrip(t *testing.T) {
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType:    0x34,
				SegEventID:          200,
				SubSegmentNum:       2,
				SubSegmentsExpected: 4,
			},
		},
	}

	encoded, err := msg.Encode(false)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if len(decoded.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(decoded.Descriptors))
	}
	d := decoded.Descriptors[0]
	if d.SubSegmentNum != 2 {
		t.Errorf("SubSegmentNum = %d, want 2", d.SubSegmentNum)
	}
	if d.SubSegmentsExpected != 4 {
		t.Errorf("SubSegmentsExpected = %d, want 4", d.SubSegmentsExpected)
	}
}
