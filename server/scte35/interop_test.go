package scte35

import (
	"testing"
	"time"

	asscte "github.com/as/scte"
	"github.com/stretchr/testify/require"
	untangled "github.com/untangledco/streaming/scte35"
)

// ---------------------------------------------------------------------------
// Our encode → untangledco decode
// ---------------------------------------------------------------------------

func TestInterop_OurEncode_UntangledDecode_SpliceInsertImmediateCueOut(t *testing.T) {
	t.Parallel()

	msg := NewSpliceInsert(5001, 30*time.Second, true, true)
	msg.UniqueProgramID = 7
	msg.AvailNum = 2
	msg.AvailsExpected = 5
	msg.Tier = 500

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	splice, err := untangled.Decode(encoded)
	require.NoError(t, err)
	require.NotNil(t, splice.Command)
	require.Equal(t, untangled.CommandType(0x05), splice.Command.Type)
	require.NotNil(t, splice.Command.Insert)

	ins := splice.Command.Insert
	require.Equal(t, uint32(5001), ins.ID)
	require.False(t, ins.Cancel)
	require.True(t, ins.OutOfNetwork)
	require.True(t, ins.Immediate)
	require.Nil(t, ins.SpliceTime)
	require.Equal(t, uint16(500), splice.Tier)

	// Note: untangledco has a known bug decoding immediate splice_insert with
	// break_duration. When Immediate=true, the library does not advance its
	// internal buffer past the flags byte before reading break_duration, so
	// Duration, ProgramID, AvailNum, and AvailExpected read from wrong offsets.
	// Verify these fields via the as/scte decoder instead (see below).
}

func TestInterop_OurEncode_UntangledDecode_SpliceInsertScheduled(t *testing.T) {
	t.Parallel()

	msg := NewSpliceInsert(6001, 15*time.Second, true, true)
	pts := int64(540000)
	msg.SpliceTimePTS = &pts

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	splice, err := untangled.Decode(encoded)
	require.NoError(t, err)
	require.NotNil(t, splice.Command)
	require.NotNil(t, splice.Command.Insert)

	ins := splice.Command.Insert
	require.Equal(t, uint32(6001), ins.ID)
	require.False(t, ins.Immediate)
	require.NotNil(t, ins.SpliceTime, "scheduled splice should have SpliceTime")
	require.Equal(t, uint64(540000), *ins.SpliceTime)
}

func TestInterop_OurEncode_UntangledDecode_SpliceInsertCancel(t *testing.T) {
	t.Parallel()

	msg := &CueMessage{
		CommandType:                CommandSpliceInsert,
		EventID:                    7001,
		SpliceEventCancelIndicator: true,
	}

	// Use verify=false because Comcast lib has a decoder bug for cancel messages.
	encoded, err := msg.Encode(false)
	require.NoError(t, err)

	splice, err := untangled.Decode(encoded)
	require.NoError(t, err)
	require.NotNil(t, splice.Command)
	require.NotNil(t, splice.Command.Insert)
	require.Equal(t, uint32(7001), splice.Command.Insert.ID)
	require.True(t, splice.Command.Insert.Cancel)
}

func TestInterop_OurEncode_UntangledDecode_TimeSignalProgramStart(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x10, 0, 0x01, []byte("SHOW001"))

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	splice, err := untangled.Decode(encoded)
	require.NoError(t, err)
	require.NotNil(t, splice.Command)
	require.Equal(t, untangled.CommandType(0x06), splice.Command.Type)

	require.Len(t, splice.Descriptors, 1)
	sd, ok := splice.Descriptors[0].(untangled.SegmentationDescriptor)
	require.True(t, ok, "expected SegmentationDescriptor, got %T", splice.Descriptors[0])
	require.Equal(t, uint8(0x10), sd.Type)
	require.Equal(t, untangled.UPIDType(0x01), sd.UPID.Type)
	require.Equal(t, []byte("SHOW001"), sd.UPID.Value)
}

func TestInterop_OurEncode_UntangledDecode_TimeSignalBreakStartWithDuration(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x22, 120*time.Second, 0x01, []byte("BREAK01"))

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	splice, err := untangled.Decode(encoded)
	require.NoError(t, err)
	require.Len(t, splice.Descriptors, 1)

	sd, ok := splice.Descriptors[0].(untangled.SegmentationDescriptor)
	require.True(t, ok)
	require.Equal(t, uint8(0x22), sd.Type)
	require.NotNil(t, sd.Duration)
	// 120 seconds × 90000 = 10800000 ticks
	require.Equal(t, uint64(10800000), *sd.Duration)
}

func TestInterop_OurEncode_UntangledDecode_TimeSignalMultipleDescriptors(t *testing.T) {
	t.Parallel()

	descriptors := []SegmentationDescriptor{
		{
			SegmentationType: 0x22,
			SegEventID:       1,
			UPIDType:         0x01,
			UPID:             []byte("BREAK01"),
		},
		{
			SegmentationType: 0x30,
			SegEventID:       2,
			UPIDType:         0x01,
			UPID:             []byte("AD00001"),
		},
	}
	msg := NewTimeSignalMulti(descriptors)

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	splice, err := untangled.Decode(encoded)
	require.NoError(t, err)
	require.Len(t, splice.Descriptors, 2)

	sd0, ok := splice.Descriptors[0].(untangled.SegmentationDescriptor)
	require.True(t, ok)
	require.Equal(t, uint8(0x22), sd0.Type)
	require.Equal(t, uint32(1), sd0.EventID)

	sd1, ok := splice.Descriptors[1].(untangled.SegmentationDescriptor)
	require.True(t, ok)
	require.Equal(t, uint8(0x30), sd1.Type)
	require.Equal(t, uint32(2), sd1.EventID)
}

func TestInterop_OurEncode_UntangledDecode_SpliceNull(t *testing.T) {
	t.Parallel()

	msg := &CueMessage{
		CommandType: CommandSpliceNull,
	}

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	splice, err := untangled.Decode(encoded)
	require.NoError(t, err)
	require.NotNil(t, splice.Command)
	require.Equal(t, untangled.CommandType(0x00), splice.Command.Type)
	require.Nil(t, splice.Command.Insert)
	require.Nil(t, splice.Command.TimeSignal)
}

func TestInterop_OurEncode_UntangledDecode_TierAndPTSAdjustment(t *testing.T) {
	t.Parallel()

	msg := &CueMessage{
		CommandType:   CommandSpliceNull,
		Tier:          500,
		PTSAdjustment: 183003,
	}

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	splice, err := untangled.Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint16(500), splice.Tier)
	// The untangledco library has a known bug: it reads PTS adjustment bytes
	// into positions [1:5] of an 8-byte big-endian uint64, leaving 3 trailing
	// zero bytes. This results in the value being left-shifted by 24 bits.
	// Verify the raw bits are correct by shifting back.
	require.Equal(t, uint64(183003), splice.PTSAdjustment>>24)
}

// ---------------------------------------------------------------------------
// Our encode → as/scte decode
// ---------------------------------------------------------------------------

func TestInterop_OurEncode_AsScteDecode_SpliceInsertImmediateCueOut(t *testing.T) {
	t.Parallel()

	msg := NewSpliceInsert(5001, 30*time.Second, true, true)
	msg.UniqueProgramID = 7
	msg.AvailNum = 2
	msg.AvailsExpected = 5
	msg.Tier = 500

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	pkt, err := asscte.Parse(encoded)
	require.NoError(t, err)
	require.Equal(t, 0x05, pkt.CmdType)

	ins, ok := pkt.Cmd.(asscte.SpliceInsert)
	require.True(t, ok, "expected SpliceInsert, got %T", pkt.Cmd)
	require.Equal(t, 5001, ins.EventID)
	require.False(t, ins.Cancel)
	require.True(t, ins.OutOfNetwork)
	require.True(t, ins.Immediate)
	require.True(t, ins.AutoReturn)
	// 30s × 90000 = 2700000 ticks
	require.Equal(t, 2700000, ins.BreakDur)
	require.Equal(t, 7, ins.ProgID)
	require.Equal(t, 2, ins.Avail)
	require.Equal(t, 5, ins.AvailExp)
	require.Equal(t, 500, pkt.Tier)
}

func TestInterop_OurEncode_AsScteDecode_SpliceInsertScheduled(t *testing.T) {
	t.Parallel()

	msg := NewSpliceInsert(6001, 15*time.Second, true, true)
	pts := int64(540000)
	msg.SpliceTimePTS = &pts

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	pkt, err := asscte.Parse(encoded)
	require.NoError(t, err)

	ins, ok := pkt.Cmd.(asscte.SpliceInsert)
	require.True(t, ok)
	require.Equal(t, 6001, ins.EventID)
	require.False(t, ins.Immediate)
	require.True(t, ins.Time.HasPTS)
	require.Equal(t, 540000, ins.Time.PTS)
}

func TestInterop_OurEncode_AsScteDecode_TimeSignalProgramStart(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x10, 0, 0x01, []byte("SHOW001"))

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	pkt, err := asscte.Parse(encoded)
	require.NoError(t, err)
	require.Equal(t, 0x06, pkt.CmdType)

	require.Len(t, pkt.Desc, 1)
	seg, ok := pkt.Desc[0].(asscte.DescSegment)
	require.True(t, ok, "expected DescSegment, got %T", pkt.Desc[0])
	require.Equal(t, byte(0x10), seg.SegType)
	require.Equal(t, []byte("SHOW001"), seg.UPID)
}

func TestInterop_OurEncode_AsScteDecode_TimeSignalBreakStart(t *testing.T) {
	t.Parallel()

	msg := NewTimeSignal(0x22, 120*time.Second, 0x01, []byte("BREAK01"))

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	pkt, err := asscte.Parse(encoded)
	require.NoError(t, err)

	require.Len(t, pkt.Desc, 1)
	seg, ok := pkt.Desc[0].(asscte.DescSegment)
	require.True(t, ok)
	require.Equal(t, byte(0x22), seg.SegType)
	require.True(t, seg.HasDuration)
	// 120 seconds × 90000 = 10800000 ticks
	require.Equal(t, 10800000, seg.Duration)
}

func TestInterop_OurEncode_AsScteDecode_SpliceNull(t *testing.T) {
	t.Parallel()

	msg := &CueMessage{
		CommandType: CommandSpliceNull,
	}

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	pkt, err := asscte.Parse(encoded)
	require.NoError(t, err)
	require.Equal(t, 0x00, pkt.CmdType)

	_, ok := pkt.Cmd.(asscte.SpliceNull)
	require.True(t, ok, "expected SpliceNull, got %T", pkt.Cmd)
}

func TestInterop_OurEncode_AsScteDecode_TierAndPTSAdjustment(t *testing.T) {
	t.Parallel()

	msg := &CueMessage{
		CommandType:   CommandSpliceNull,
		Tier:          500,
		PTSAdjustment: 183003,
	}

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	pkt, err := asscte.Parse(encoded)
	require.NoError(t, err)
	require.Equal(t, 500, pkt.Tier)
	require.Equal(t, int64(183003), pkt.PTSA)
}

func TestInterop_OurEncode_AsScteDecode_TimeSignalMultipleDescriptors(t *testing.T) {
	t.Parallel()

	descriptors := []SegmentationDescriptor{
		{
			SegmentationType: 0x22,
			SegEventID:       1,
			UPIDType:         0x01,
			UPID:             []byte("BREAK01"),
		},
		{
			SegmentationType: 0x30,
			SegEventID:       2,
			UPIDType:         0x01,
			UPID:             []byte("AD00001"),
		},
	}
	msg := NewTimeSignalMulti(descriptors)

	encoded, err := msg.Encode(true)
	require.NoError(t, err)

	pkt, err := asscte.Parse(encoded)
	require.NoError(t, err)
	require.Len(t, pkt.Desc, 2)

	seg0, ok := pkt.Desc[0].(asscte.DescSegment)
	require.True(t, ok)
	require.Equal(t, byte(0x22), seg0.SegType)
	require.Equal(t, 1, seg0.EventID)

	seg1, ok := pkt.Desc[1].(asscte.DescSegment)
	require.True(t, ok)
	require.Equal(t, byte(0x30), seg1.SegType)
	require.Equal(t, 2, seg1.EventID)
}

// ---------------------------------------------------------------------------
// untangledco encode → our decode
// ---------------------------------------------------------------------------

func TestInterop_UntangledEncode_OurDecode_SpliceInsert(t *testing.T) {
	t.Parallel()

	splice := &untangled.Splice{
		Tier: 0x0fff,
		Command: &untangled.Command{
			Type: untangled.SpliceInsert,
			Insert: &untangled.Insert{
				ID:            8001,
				OutOfNetwork:  true,
				Immediate:     true,
				ProgramID:     3,
				AvailNum:      1,
				AvailExpected: 2,
				Duration: &untangled.BreakDuration{
					AutoReturn: true,
					Duration:   4500000, // 50 seconds
				},
			},
		},
	}

	encoded, err := untangled.Encode(splice)
	require.NoError(t, err)

	msg, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), msg.CommandType)
	require.Equal(t, uint32(8001), msg.EventID)
	require.True(t, msg.IsOut)
	require.True(t, msg.AutoReturn)
	require.NotNil(t, msg.BreakDuration)
	require.Equal(t, 50*time.Second, *msg.BreakDuration)
	require.Equal(t, uint16(3), msg.UniqueProgramID)
	require.Equal(t, uint8(1), msg.AvailNum)
	require.Equal(t, uint8(2), msg.AvailsExpected)
}

func TestInterop_UntangledEncode_OurDecode_TimeSignalProgramStart(t *testing.T) {
	t.Parallel()

	pts := uint64(900000) // 10 seconds at 90kHz
	splice := &untangled.Splice{
		Tier: 0x0fff,
		Command: &untangled.Command{
			Type:       untangled.TimeSignal,
			TimeSignal: &pts,
		},
		Descriptors: []untangled.SpliceDescriptor{
			untangled.SegmentationDescriptor{
				EventID: 100,
				Type:    0x10, // ProgramStart
				UPID: untangled.UPID{
					Type:  untangled.UPIDAdID,
					Value: []byte("PROG001"),
				},
			},
		},
	}

	encoded, err := untangled.Encode(splice)
	require.NoError(t, err)

	msg, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandTimeSignal), msg.CommandType)
	require.Len(t, msg.Descriptors, 1)
	require.Equal(t, uint8(0x10), msg.Descriptors[0].SegmentationType)
	require.Equal(t, uint32(100), msg.Descriptors[0].SegEventID)
	// untangled.UPIDAdID == 3 (SCTE-35 Table 22: Ad-ID)
	require.Equal(t, uint8(3), msg.Descriptors[0].UPIDType)
	require.Equal(t, []byte("PROG001"), msg.Descriptors[0].UPID)
}

func TestInterop_UntangledEncode_OurDecode_SpliceNull(t *testing.T) {
	t.Parallel()

	splice := &untangled.Splice{
		Tier: 0x0fff,
		Command: &untangled.Command{
			Type: untangled.SpliceNull,
		},
	}

	encoded, err := untangled.Encode(splice)
	require.NoError(t, err)

	msg, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceNull), msg.CommandType)
}
