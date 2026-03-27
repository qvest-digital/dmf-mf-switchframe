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
