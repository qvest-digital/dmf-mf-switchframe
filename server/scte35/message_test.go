package scte35

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewSpliceInsert_CueOut(t *testing.T) {
	t.Parallel()
	dur := 30 * time.Second
	msg := NewSpliceInsert(42, dur, true, true)

	require.Equal(t, uint8(CommandSpliceInsert), msg.CommandType)
	require.Equal(t, uint32(42), msg.EventID)
	require.True(t, msg.IsOut, "expected IsOut=true")
	require.True(t, msg.AutoReturn, "expected AutoReturn=true")
	require.NotNil(t, msg.BreakDuration)
	require.Equal(t, dur, *msg.BreakDuration)
}

func TestNewSpliceInsert_CueIn(t *testing.T) {
	t.Parallel()
	msg := NewSpliceInsert(42, 0, false, false)

	require.False(t, msg.IsOut, "expected IsOut=false for cue-in")
	require.Nil(t, msg.BreakDuration)
}

func TestNewTimeSignal(t *testing.T) {
	t.Parallel()
	dur := 60 * time.Second
	upid := []byte("https://ads.example.com/avail/1")
	msg := NewTimeSignal(0x34, dur, 0x0F, upid)

	require.Equal(t, uint8(CommandTimeSignal), msg.CommandType)
	require.Len(t, msg.Descriptors, 1)
	d := msg.Descriptors[0]
	require.Equal(t, uint8(0x34), d.SegmentationType)
	require.Equal(t, uint8(0x0F), d.UPIDType)
}

func TestNewTimeSignalMulti(t *testing.T) {
	t.Parallel()
	descs := []SegmentationDescriptor{
		{SegmentationType: 0x34, UPIDType: 0x0F, UPID: []byte("uri1")},
		{SegmentationType: 0x36, UPIDType: 0x09, UPID: []byte("adi1")},
	}
	msg := NewTimeSignalMulti(descs)

	require.Len(t, msg.Descriptors, 2)
}

func TestEncode_SpliceInsert_RoundTrip(t *testing.T) {
	t.Parallel()
	dur := 30 * time.Second
	msg := NewSpliceInsert(1, dur, true, true)

	encoded, err := msg.Encode(true) // verify=true
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.Equal(t, uint32(1), decoded.EventID)
}

func TestEncode_TimeSignal_RoundTrip(t *testing.T) {
	t.Parallel()
	dur := 60 * time.Second
	msg := NewTimeSignal(0x34, dur, 0x0F, []byte("https://example.com"))

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)
}

func TestEncode_SpliceNull(t *testing.T) {
	t.Parallel()
	msg := &CueMessage{CommandType: CommandSpliceNull}

	encoded, err := msg.Encode(false)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)
}

func TestEncode_SpliceInsert_Cancel_RoundTrip(t *testing.T) {
	t.Parallel()
	msg := &CueMessage{
		CommandType:                CommandSpliceInsert,
		EventID:                    99,
		SpliceEventCancelIndicator: true,
	}

	encoded, err := msg.Encode(true) // verify=true
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.Equal(t, uint32(99), decoded.EventID)
	require.True(t, decoded.SpliceEventCancelIndicator, "expected SpliceEventCancelIndicator=true after round-trip")
	require.False(t, decoded.IsOut, "expected IsOut=false for cancel message")
}

func TestEncode_TimeSignal_CancelSegmentation_RoundTrip(t *testing.T) {
	t.Parallel()
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:                       42,
				SegmentationEventCancelIndicator: true,
			},
		},
	}

	encoded, err := msg.Encode(true) // verify=true
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.True(t, d.SegmentationEventCancelIndicator, "round-trip: expected SegmentationEventCancelIndicator=true")
	require.Equal(t, uint32(42), d.SegEventID)
}

func TestEncode_TimeSignal_WithPTS(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.NotNil(t, decoded.SpliceTimePTS)
	require.Equal(t, int64(8100000), *decoded.SpliceTimePTS)
	require.Equal(t, "scheduled", decoded.Timing)
}

func TestEncode_SpliceInsert_Scheduled_RoundTrip(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.Equal(t, "scheduled", decoded.Timing)
	require.NotNil(t, decoded.SpliceTimePTS)
	require.Equal(t, int64(8100000), *decoded.SpliceTimePTS)
	require.Equal(t, uint32(5), decoded.EventID)
	require.True(t, decoded.IsOut, "expected IsOut=true")
}

func TestEncode_SpliceInsert_Immediate_NoSpliceTime(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.Equal(t, "immediate", decoded.Timing)
	require.Nil(t, decoded.SpliceTimePTS)
}

func TestEncode_SpliceInsert_AvailFields_RoundTrip(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.Equal(t, uint32(10), decoded.EventID)
	require.True(t, decoded.IsOut, "expected IsOut=true")
	require.Equal(t, uint16(1234), decoded.UniqueProgramID)
	require.Equal(t, uint8(2), decoded.AvailNum)
	require.Equal(t, uint8(4), decoded.AvailsExpected)
}

func TestDecode_MultiDescriptor_DeliveryRestrictions(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)

	require.Len(t, decoded.Descriptors, 2)

	// Verify first descriptor's restrictions.
	dr1 := decoded.Descriptors[0].DeliveryRestrictions
	require.NotNil(t, dr1, "descriptor[0] DeliveryRestrictions is nil")
	require.True(t, dr1.WebDeliveryAllowed, "descriptor[0] WebDeliveryAllowed should be true")
	require.False(t, dr1.NoRegionalBlackout, "descriptor[0] NoRegionalBlackout should be false")
	require.True(t, dr1.ArchiveAllowed, "descriptor[0] ArchiveAllowed should be true")

	// Verify second descriptor has DIFFERENT restrictions.
	dr2 := decoded.Descriptors[1].DeliveryRestrictions
	require.NotNil(t, dr2, "descriptor[1] DeliveryRestrictions is nil")
	require.False(t, dr2.WebDeliveryAllowed, "descriptor[1] WebDeliveryAllowed should be false")
	require.True(t, dr2.NoRegionalBlackout, "descriptor[1] NoRegionalBlackout should be true")
	require.False(t, dr2.ArchiveAllowed, "descriptor[1] ArchiveAllowed should be false")
	require.Equal(t, uint8(3), dr2.DeviceRestrictions)

	// Top-level DeliveryRestrictions should match first descriptor (backward compat).
	require.NotNil(t, decoded.DeliveryRestrictions, "top-level DeliveryRestrictions is nil")
	require.True(t, decoded.DeliveryRestrictions.WebDeliveryAllowed, "top-level WebDeliveryAllowed should match first descriptor")
}

func TestDecode_InvalidCRC(t *testing.T) {
	t.Parallel()
	dur := 30 * time.Second
	msg := NewSpliceInsert(1, dur, true, true)
	encoded, err := msg.Encode(false)
	require.NoError(t, err)

	// Corrupt the last byte (CRC-32).
	encoded[len(encoded)-1] ^= 0xFF

	_, err = Decode(encoded)
	require.Error(t, err, "expected CRC error on corrupt data")
}

func TestEncode_TimeSignal_NilPTS(t *testing.T) {
	t.Parallel()
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 1},
		},
		// SpliceTimePTS intentionally nil.
	}

	encoded, err := msg.Encode(false)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)

	// When PTS is nil, the decoded message should also have nil PTS
	// (time_specified_flag=0), not PTS=0.
	require.Nil(t, decoded.SpliceTimePTS)
}

func TestEncode_SegNum_RoundTrip(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)

	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.Equal(t, uint8(3), d.SegNum)
	require.Equal(t, uint8(5), d.SegExpected)
}

func TestDecodeSpliceInsertCancel_ValidCRC(t *testing.T) {
	t.Parallel()
	// Encode a cancel message — the library produces correct CRC-32.
	msg := &CueMessage{
		CommandType:                CommandSpliceInsert,
		EventID:                    12345,
		SpliceEventCancelIndicator: true,
	}
	data, err := msg.Encode(false)
	require.NoError(t, err)

	// Call the fallback decoder directly.
	decoded, err := decodeSpliceInsertCancel(data)
	require.NoError(t, err)
	require.Equal(t, uint32(12345), decoded.EventID)
	require.True(t, decoded.SpliceEventCancelIndicator, "expected SpliceEventCancelIndicator=true")
}

func TestDecodeSpliceInsertCancel_CorruptCRC(t *testing.T) {
	t.Parallel()
	// Encode a cancel message with correct CRC.
	msg := &CueMessage{
		CommandType:                CommandSpliceInsert,
		EventID:                    12345,
		SpliceEventCancelIndicator: true,
	}
	data, err := msg.Encode(false)
	require.NoError(t, err)

	// Corrupt a byte in the middle of the payload.
	data[10] ^= 0xFF

	_, err = decodeSpliceInsertCancel(data)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "CRC-32 mismatch"), "expected CRC-32 mismatch error, got: %v", err)
}

func TestDecodeSpliceInsertCancel_SectionLengthExceedsData(t *testing.T) {
	t.Parallel()
	// Construct minimal data where section_length claims more than available.
	data := make([]byte, 25)
	data[0] = 0xFC                 // table_id
	data[13] = CommandSpliceInsert // splice_command_type
	// Set section_length to 255 (way more than 22 remaining bytes).
	data[1] = 0x00
	data[2] = 0xFF

	_, err := decodeSpliceInsertCancel(data)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "section length exceeds data"), "expected 'section length exceeds data' error, got: %v", err)
}

func TestCrc32MPEG2(t *testing.T) {
	t.Parallel()
	// Known test vector: empty input should return 0xFFFFFFFF (initial CRC, no data).
	// Actually for MPEG-2 CRC, the initial value is 0xFFFFFFFF with no final XOR.
	// For zero-length data the result is 0xFFFFFFFF.
	crc := crc32MPEG2(nil)
	require.Equal(t, uint32(0xFFFFFFFF), crc)

	// Test with a known SCTE-35 section: encode a message and verify
	// the CRC of all-but-last-4-bytes matches the last 4 bytes.
	msg := &CueMessage{
		CommandType:                CommandSpliceInsert,
		EventID:                    42,
		SpliceEventCancelIndicator: true,
	}
	data, err := msg.Encode(false)
	require.NoError(t, err)

	sectionLen := 3 + (int(data[1]&0x0F)<<8 | int(data[2]))
	crcData := data[:sectionLen-4]
	expectedCRC := binary.BigEndian.Uint32(data[sectionLen-4 : sectionLen])
	computedCRC := crc32MPEG2(crcData)
	require.Equal(t, expectedCRC, computedCRC)
}

func TestMultipleUPIDs_RoundTrip(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decoded, err := Decode(encoded)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)

	d := decoded.Descriptors[0]

	// First UPID preserved in backward-compatible fields.
	require.Equal(t, uint8(0x09), d.UPIDType)
	require.Equal(t, "SIGNAL:first-upid", string(d.UPID))

	// Second UPID preserved in AdditionalUPIDs.
	require.Len(t, d.AdditionalUPIDs, 1)
	require.Equal(t, uint8(0x0F), d.AdditionalUPIDs[0].Type)
	require.Equal(t, "https://example.com/second", string(d.AdditionalUPIDs[0].Value))
}

func TestMultipleUPIDs_ThreeUPIDs_RoundTrip(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)

	d := decoded.Descriptors[0]
	require.Equal(t, uint8(0x09), d.UPIDType)
	require.Equal(t, "first", string(d.UPID))
	require.Len(t, d.AdditionalUPIDs, 2)
	require.Equal(t, "second", string(d.AdditionalUPIDs[0].Value))
	require.Equal(t, "third", string(d.AdditionalUPIDs[1].Value))
}

func TestSingleUPID_BackwardCompatible(t *testing.T) {
	t.Parallel()
	// A descriptor with only 1 UPID should have no AdditionalUPIDs.
	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://example.com"))

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)

	d := decoded.Descriptors[0]
	require.Equal(t, uint8(0x0F), d.UPIDType)
	require.Len(t, d.AdditionalUPIDs, 0)
}

func TestEncode_Tier_RoundTrip(t *testing.T) {
	t.Parallel()
	dur := 30 * time.Second
	msg := NewSpliceInsert(1, dur, true, true)
	msg.Tier = 500 // restricted tier

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint16(500), decoded.Tier)
}

func TestEncode_Tier_DefaultsTo4095(t *testing.T) {
	t.Parallel()
	// Tier=0 should default to 4095 (unrestricted) in the encoded output.
	msg := &CueMessage{CommandType: CommandSpliceNull}

	encoded, err := msg.Encode(false)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint16(4095), decoded.Tier)
}

func TestEncode_PTSAdjustment_RoundTrip(t *testing.T) {
	t.Parallel()
	dur := 30 * time.Second
	msg := NewSpliceInsert(1, dur, true, true)
	msg.PTSAdjustment = 183003 // example: PTS shifted by ~2 seconds

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint64(183003), decoded.PTSAdjustment)
}

func TestEncode_PTSAdjustment_DefaultZero(t *testing.T) {
	t.Parallel()
	// Default PTSAdjustment should be 0 when not explicitly set.
	msg := NewSpliceInsert(1, 30*time.Second, true, true)

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint64(0), decoded.PTSAdjustment)
}

func TestEncode_TierAndPTSAdjustment_Combined(t *testing.T) {
	t.Parallel()
	// Both tier and PTSAdjustment should survive round-trip together.
	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("test"))
	msg.Tier = 100
	msg.PTSAdjustment = 8100000 // 90s

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint16(100), decoded.Tier)
	require.Equal(t, uint64(8100000), decoded.PTSAdjustment)
}

func TestEncode_SubSegment_RoundTrip(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)

	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.Equal(t, uint8(2), d.SubSegmentNum)
	require.Equal(t, uint8(4), d.SubSegmentsExpected)
}
