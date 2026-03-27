package scte35

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// splice_insert tests
// ---------------------------------------------------------------------------

func TestSpec_SpliceInsert_ImmediateCueOut(t *testing.T) {
	t.Parallel()

	msg := NewSpliceInsert(1001, 30*time.Second, true, true)
	msg.UniqueProgramID = 5
	msg.AvailNum = 1
	msg.AvailsExpected = 2

	// Encode with verification.
	data, err := msg.Encode(true)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// table_id must be 0xFC per SCTE-35.
	require.Equal(t, byte(0xFC), data[0], "table_id must be 0xFC")

	// Decode and verify all fields.
	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.Equal(t, uint32(1001), decoded.EventID)
	require.True(t, decoded.IsOut, "expected IsOut=true for cue-out")
	require.True(t, decoded.AutoReturn, "expected AutoReturn=true")
	require.Equal(t, "immediate", decoded.Timing)
	require.Nil(t, decoded.SpliceTimePTS, "immediate splice must have nil SpliceTimePTS")
	require.NotNil(t, decoded.BreakDuration)
	require.Equal(t, 30*time.Second, *decoded.BreakDuration)
	require.Equal(t, uint16(5), decoded.UniqueProgramID)
	require.Equal(t, uint8(1), decoded.AvailNum)
	require.Equal(t, uint8(2), decoded.AvailsExpected)
	require.Equal(t, uint16(0xFFF), decoded.Tier, "default tier must be 0xFFF")
	require.False(t, decoded.SpliceEventCancelIndicator)
}

func TestSpec_SpliceInsert_ImmediateCueIn(t *testing.T) {
	t.Parallel()

	msg := NewSpliceInsert(1001, 0, false, false)

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.False(t, decoded.IsOut, "expected IsOut=false for cue-in")
	require.Nil(t, decoded.BreakDuration, "cue-in must have no break duration")
	require.Equal(t, "immediate", decoded.Timing)
}

func TestSpec_SpliceInsert_ScheduledCueOut(t *testing.T) {
	t.Parallel()

	msg := NewSpliceInsert(2001, 60*time.Second, true, true)
	// Scheduled: set PTS = 90000 + 5000*90 = 540000 (6 seconds into stream).
	pts := int64(90000 + 5000*90)
	msg.SpliceTimePTS = &pts

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, "scheduled", decoded.Timing)
	require.NotNil(t, decoded.SpliceTimePTS)
	require.Equal(t, pts, *decoded.SpliceTimePTS)
	require.NotNil(t, decoded.BreakDuration)
	require.Equal(t, 60*time.Second, *decoded.BreakDuration)
}

func TestSpec_SpliceInsert_Cancel(t *testing.T) {
	t.Parallel()

	msg := &CueMessage{
		CommandType:                CommandSpliceInsert,
		EventID:                    3001,
		SpliceEventCancelIndicator: true,
	}

	// Encode with verify=false (library decode bug on cancel messages).
	data, err := msg.Encode(false)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.True(t, decoded.SpliceEventCancelIndicator)
	require.Equal(t, uint32(3001), decoded.EventID)
}

func TestSpec_SpliceInsert_Tier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tier     uint16
		expected uint16
	}{
		{"zero_means_unrestricted", 0, 0xFFF},
		{"tier_500", 500, 500},
		{"tier_max", 0xFFF, 0xFFF},
		{"tier_1", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := NewSpliceInsert(100, 10*time.Second, true, true)
			msg.Tier = tt.tier

			data, err := msg.Encode(true)
			require.NoError(t, err)

			decoded, err := Decode(data)
			require.NoError(t, err)

			require.Equal(t, tt.expected, decoded.Tier)
		})
	}
}

func TestSpec_SpliceInsert_PTSAdjustment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ptsAdj uint64
	}{
		{"zero", 0},
		{"mid_value", 183003},
		{"max_33bit", (1 << 33) - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := NewSpliceInsert(200, 10*time.Second, true, true)
			msg.PTSAdjustment = tt.ptsAdj

			data, err := msg.Encode(true)
			require.NoError(t, err)

			decoded, err := Decode(data)
			require.NoError(t, err)

			require.Equal(t, tt.ptsAdj, decoded.PTSAdjustment)
		})
	}
}

func TestSpec_SpliceInsert_AutoAssignEventID(t *testing.T) {
	t.Parallel()

	var captured []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		VerifyEncoding:    true,
	}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 15*time.Second, true, true) // EventID=0 → auto-assign
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.NotZero(t, eventID, "auto-assigned event ID must be non-zero")

	mu.Lock()
	dataCopy := append([]byte(nil), captured...)
	mu.Unlock()
	require.NotEmpty(t, dataCopy, "muxer sink must be called")

	decoded, err := Decode(dataCopy)
	require.NoError(t, err)
	require.Equal(t, eventID, decoded.EventID, "decoded event ID must match returned ID")
}

func TestSpec_SpliceInsert_ExplicitEventID(t *testing.T) {
	t.Parallel()

	var captured []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		VerifyEncoding:    true,
	}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(42, 15*time.Second, true, true)
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.Equal(t, uint32(42), eventID, "explicit event ID must be preserved")

	mu.Lock()
	dataCopy := append([]byte(nil), captured...)
	mu.Unlock()
	require.NotEmpty(t, dataCopy)

	decoded, err := Decode(dataCopy)
	require.NoError(t, err)
	require.Equal(t, uint32(42), decoded.EventID)
}

// ---------------------------------------------------------------------------
// splice_null test
// ---------------------------------------------------------------------------

func TestSpec_SpliceNull(t *testing.T) {
	t.Parallel()

	msg := &CueMessage{CommandType: CommandSpliceNull}

	data, err := msg.Encode(true)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// table_id must be 0xFC.
	require.Equal(t, byte(0xFC), data[0], "table_id must be 0xFC")

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceNull), decoded.CommandType)
}

// ---------------------------------------------------------------------------
// time_signal tests
// ---------------------------------------------------------------------------

func TestSpec_TimeSignal_ProgramStart(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x10, 0, 0x01, []byte("PROG001"))

	data, err := msg.Encode(true)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Equal(t, "immediate", decoded.Timing)
	require.Nil(t, decoded.SpliceTimePTS)
	require.Len(t, decoded.Descriptors, 1)

	d := decoded.Descriptors[0]
	require.Equal(t, uint8(0x10), d.SegmentationType)
	require.Equal(t, uint8(0x01), d.UPIDType)
	require.Equal(t, []byte("PROG001"), d.UPID)
	require.Nil(t, d.DurationTicks, "program_start must have no duration")
}

func TestSpec_TimeSignal_ProgramEnd(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x11, 0, 0x01, []byte("PROG001"))

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)
	require.Equal(t, uint8(0x11), decoded.Descriptors[0].SegmentationType)
}

func TestSpec_TimeSignal_BreakStartWithDuration(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x22, 120*time.Second, 0x01, []byte("BREAK01"))

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)

	d := decoded.Descriptors[0]
	require.Equal(t, uint8(0x22), d.SegmentationType)
	require.NotNil(t, d.DurationTicks)
	require.Equal(t, uint64(10800000), *d.DurationTicks, "120s * 90000 = 10800000 ticks")
	require.Equal(t, uint8(0x01), d.UPIDType)
	require.Equal(t, []byte("BREAK01"), d.UPID)
}

func TestSpec_TimeSignal_BreakEnd(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x23, 0, 0x01, []byte("BREAK01"))

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)

	d := decoded.Descriptors[0]
	require.Equal(t, uint8(0x23), d.SegmentationType)
	require.Nil(t, d.DurationTicks, "break_end must have no duration")
}

func TestSpec_TimeSignal_ProviderAdStart(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x30, 30*time.Second, 0x09, []byte("AD00001"))
	// Set seg_num and seg_expected on the descriptor.
	msg.Descriptors[0].SegNum = 1
	msg.Descriptors[0].SegExpected = 4

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)

	d := decoded.Descriptors[0]
	require.Equal(t, uint8(0x30), d.SegmentationType)
	require.Equal(t, uint8(0x09), d.UPIDType)
	require.Equal(t, []byte("AD00001"), d.UPID)
	require.NotNil(t, d.DurationTicks)
	require.Equal(t, uint64(2700000), *d.DurationTicks, "30s * 90000 = 2700000 ticks")
	require.Equal(t, uint8(1), d.SegNum)
	require.Equal(t, uint8(4), d.SegExpected)
}

func TestSpec_TimeSignal_DistributorAdStart(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x34, 30*time.Second, 0x09, []byte("AD00002"))

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)
	require.Equal(t, uint8(0x34), decoded.Descriptors[0].SegmentationType)
}

func TestSpec_TimeSignal_Scheduled(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x10, 0, 0x01, []byte("SCH001"))
	pts := int64(540000)
	msg.SpliceTimePTS = &pts

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Equal(t, "scheduled", decoded.Timing)
	require.NotNil(t, decoded.SpliceTimePTS)
	require.Equal(t, int64(540000), *decoded.SpliceTimePTS)
}

func TestSpec_TimeSignal_MultipleDescriptors(t *testing.T) {
	t.Parallel()

	dur1 := uint64(2700000)
	dur2 := uint64(2700000)
	descriptors := []SegmentationDescriptor{
		{
			SegmentationType: 0x22,
			SegEventID:       100,
			UPIDType:         0x01,
			UPID:             []byte("BRK01"),
			DurationTicks:    &dur1,
		},
		{
			SegmentationType: 0x30,
			SegEventID:       101,
			UPIDType:         0x09,
			UPID:             []byte("AD001"),
			SegNum:           1,
			SegExpected:      3,
			DurationTicks:    &dur2,
		},
	}

	msg := NewTimeSignalMulti(descriptors)

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 2)

	// First descriptor: break_start.
	d0 := decoded.Descriptors[0]
	require.Equal(t, uint8(0x22), d0.SegmentationType)
	require.Equal(t, uint32(100), d0.SegEventID)
	require.Equal(t, uint8(0x01), d0.UPIDType)
	require.Equal(t, []byte("BRK01"), d0.UPID)
	require.NotNil(t, d0.DurationTicks)
	require.Equal(t, uint64(2700000), *d0.DurationTicks)

	// Second descriptor: provider_ad_start.
	d1 := decoded.Descriptors[1]
	require.Equal(t, uint8(0x30), d1.SegmentationType)
	require.Equal(t, uint32(101), d1.SegEventID)
	require.Equal(t, uint8(0x09), d1.UPIDType)
	require.Equal(t, []byte("AD001"), d1.UPID)
	require.NotNil(t, d1.DurationTicks)
	require.Equal(t, uint64(2700000), *d1.DurationTicks)
	require.Equal(t, uint8(1), d1.SegNum)
	require.Equal(t, uint8(3), d1.SegExpected)
}

func TestSpec_TimeSignal_MultipleUPIDs(t *testing.T) {
	t.Parallel()

	descriptors := []SegmentationDescriptor{
		{
			SegmentationType: 0x30,
			SegEventID:       200,
			UPIDType:         0x01,
			UPID:             []byte("PRIMARY"),
			AdditionalUPIDs: []AdditionalUPID{
				{Type: 0x09, Value: []byte("EXTRA1")},
				{Type: 0x01, Value: []byte("EXTRA2")},
			},
		},
	}

	msg := NewTimeSignalMulti(descriptors)

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.Equal(t, uint8(0x01), d.UPIDType)
	require.Equal(t, []byte("PRIMARY"), d.UPID)
	require.Len(t, d.AdditionalUPIDs, 2)
	require.Equal(t, uint8(0x09), d.AdditionalUPIDs[0].Type)
	require.Equal(t, []byte("EXTRA1"), d.AdditionalUPIDs[0].Value)
	require.Equal(t, uint8(0x01), d.AdditionalUPIDs[1].Type)
	require.Equal(t, []byte("EXTRA2"), d.AdditionalUPIDs[1].Value)
}

func TestSpec_TimeSignal_DeliveryRestrictions(t *testing.T) {
	t.Parallel()

	descriptors := []SegmentationDescriptor{
		{
			SegmentationType: 0x30,
			SegEventID:       300,
			UPIDType:         0x01,
			UPID:             []byte("RESTR1"),
			DeliveryRestrictions: &DeliveryRestrictions{
				WebDeliveryAllowed: true,
				NoRegionalBlackout: true,
				ArchiveAllowed:     false,
				DeviceRestrictions: 2,
			},
		},
	}

	msg := NewTimeSignalMulti(descriptors)

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.NotNil(t, d.DeliveryRestrictions)
	require.True(t, d.DeliveryRestrictions.WebDeliveryAllowed)
	require.True(t, d.DeliveryRestrictions.NoRegionalBlackout)
	require.False(t, d.DeliveryRestrictions.ArchiveAllowed)
	require.Equal(t, uint8(2), d.DeliveryRestrictions.DeviceRestrictions)
}

func TestSpec_TimeSignal_SubSegments(t *testing.T) {
	t.Parallel()

	descriptors := []SegmentationDescriptor{
		{
			SegmentationType:    0x30,
			SegEventID:          400,
			UPIDType:            0x01,
			UPID:                []byte("SUBSEG"),
			SegNum:              2,
			SegExpected:         4,
			SubSegmentNum:       1,
			SubSegmentsExpected: 3,
		},
	}

	msg := NewTimeSignalMulti(descriptors)

	data, err := msg.Encode(true)
	require.NoError(t, err)

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.Equal(t, uint8(2), d.SegNum)
	require.Equal(t, uint8(4), d.SegExpected)
	require.Equal(t, uint8(1), d.SubSegmentNum)
	require.Equal(t, uint8(3), d.SubSegmentsExpected)
}

// ---------------------------------------------------------------------------
// CRC-32 validation test
// ---------------------------------------------------------------------------

func TestSpec_CRC32_Validation(t *testing.T) {
	t.Parallel()

	msg := NewSpliceInsert(9999, 10*time.Second, true, true)

	data, err := msg.Encode(true)
	require.NoError(t, err)
	require.True(t, len(data) >= 4, "encoded data must be at least 4 bytes")

	// Corrupt the last byte (part of the CRC-32).
	corrupted := make([]byte, len(data))
	copy(corrupted, data)
	corrupted[len(corrupted)-1] ^= 0xFF

	_, err = Decode(corrupted)
	require.Error(t, err, "decoding with corrupted CRC must fail")
}

// ---------------------------------------------------------------------------
// table_id verification test
// ---------------------------------------------------------------------------

func TestSpec_TableID_0xFC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  *CueMessage
	}{
		{"splice_null", &CueMessage{CommandType: CommandSpliceNull}},
		{"splice_insert", NewSpliceInsert(1, 10*time.Second, true, true)},
		{"time_signal", NewTimeSignal(0x10, 0, 0x01, []byte("TBL01"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := tt.msg.Encode(true)
			require.NoError(t, err)
			require.NotEmpty(t, data)
			require.Equal(t, byte(0xFC), data[0], "table_id must be 0xFC for %s", tt.name)
		})
	}
}
