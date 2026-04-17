package scte35

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scte35lib "github.com/Comcast/scte35-go/pkg/scte35"
	"github.com/stretchr/testify/require"
)

func TestAutoReturn_FiresOnPTSCrossing(t *testing.T) {
	t.Parallel()

	var currentPTS atomic.Int64
	currentPTS.Store(900000) // 10s into stream
	ptsFn := func() int64 { return currentPTS.Load() }

	var mu sync.Mutex
	var sinkCalls int
	sink := func(data []byte) {
		mu.Lock()
		sinkCalls++
		mu.Unlock()
	}

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(42, 5*time.Second, true, true)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	require.Len(t, inj.ActiveEventIDs(), 1, "event should be active before PTS crosses")

	time.Sleep(200 * time.Millisecond)
	require.Len(t, inj.ActiveEventIDs(), 1,
		"event should still be active: 200ms of wall clock elapsed but PTS has not advanced")

	currentPTS.Store(900000 + 5*90000 - 1) // 1 tick before scheduled
	time.Sleep(100 * time.Millisecond)
	require.Len(t, inj.ActiveEventIDs(), 1, "event should still be active: scheduledPTS not yet crossed")

	currentPTS.Store(900000 + 5*90000 + 1)
	require.Eventually(t, func() bool {
		return len(inj.ActiveEventIDs()) == 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"auto-return should fire when program PTS crosses the scheduled value")
}

func TestAutoReturn_PipelineStallDefersReturn(t *testing.T) {
	t.Parallel()

	var currentPTS atomic.Int64
	insertPTS := int64(1_000_000)
	currentPTS.Store(insertPTS)
	ptsFn := func() int64 { return currentPTS.Load() }

	sink := func(data []byte) {}

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	breakDur := 400 * time.Millisecond
	msg := NewSpliceInsert(7, breakDur, true, true)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	durTicks := int64(breakDur) * 90000 / int64(time.Second)

	// Advance PTS partway through the break.
	currentPTS.Store(insertPTS + durTicks/2)
	time.Sleep(100 * time.Millisecond)
	require.Len(t, inj.ActiveEventIDs(), 1, "event should be active mid-break")

	// Simulate a pipeline stall: wall clock keeps moving, PTS does not.
	time.Sleep(600 * time.Millisecond)
	require.Len(t, inj.ActiveEventIDs(), 1,
		"event should still be active after a 600ms wall-clock stall: "+
			"auto-return must be anchored to program PTS, not wall clock")

	// Pipeline resumes and catches up past the scheduled return.
	currentPTS.Store(insertPTS + durTicks + 10)
	require.Eventually(t, func() bool {
		return len(inj.ActiveEventIDs()) == 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"auto-return should fire promptly after PTS catches up past the scheduled value")
}

func TestAutoReturn_DoesNotFireEarlyWhenEngineRunsAhead(t *testing.T) {
	t.Parallel()

	var currentPTS atomic.Int64
	insertPTS := int64(500_000)
	currentPTS.Store(insertPTS)
	ptsFn := func() int64 { return currentPTS.Load() }

	sink := func(data []byte) {}
	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(11, 10*time.Second, true, true)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Advance wall clock fast, but only push PTS by 1s worth of ticks.
	currentPTS.Store(insertPTS + 90000)
	time.Sleep(200 * time.Millisecond)

	require.Len(t, inj.ActiveEventIDs(), 1,
		"event should still be active: PTS only advanced 1s but break is 10s")
}

func TestAutoReturn_HoldSuppressesPTSCrossingFire(t *testing.T) {
	t.Parallel()

	var currentPTS atomic.Int64
	insertPTS := int64(2_000_000)
	currentPTS.Store(insertPTS)
	ptsFn := func() int64 { return currentPTS.Load() }

	sink := func(data []byte) {}
	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(21, 300*time.Millisecond, true, true)
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	require.NoError(t, inj.HoldBreak(eventID))

	// Push PTS well past the original scheduled return.
	currentPTS.Store(insertPTS + 2*90000)
	time.Sleep(150 * time.Millisecond)

	require.Len(t, inj.ActiveEventIDs(), 1,
		"held event must not auto-return even after PTS crosses the original scheduled value")

	state := inj.State()
	ae, ok := state.ActiveEvents[eventID]
	require.True(t, ok)
	require.True(t, ae.Held)
}

func TestAutoReturn_ExtendReanchorsAgainstPTS(t *testing.T) {
	t.Parallel()

	var currentPTS atomic.Int64
	insertPTS := int64(3_000_000)
	currentPTS.Store(insertPTS)
	ptsFn := func() int64 { return currentPTS.Load() }

	sink := func(data []byte) {}
	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	origDurMs := int64(200)
	msg := NewSpliceInsert(33, time.Duration(origDurMs)*time.Millisecond, true, true)
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Extend to 1s total. Scheduled = insertPTS + 90000.
	require.NoError(t, inj.ExtendBreak(eventID, 1000))

	// Advance PTS to just past the original 200ms scheduled point.
	currentPTS.Store(insertPTS + 200*90 + 100)
	time.Sleep(150 * time.Millisecond)
	require.Len(t, inj.ActiveEventIDs(), 1,
		"extend must re-anchor auto-return; original scheduled PTS should no longer trigger")

	// Advance past the new scheduled PTS.
	currentPTS.Store(insertPTS + 90000 + 10)
	require.Eventually(t, func() bool {
		return len(inj.ActiveEventIDs()) == 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"auto-return should fire once PTS crosses the extended scheduled value")
}

func TestAutoReturn_PTSWrap33Bit(t *testing.T) {
	t.Parallel()

	// Start PTS near the 33-bit wrap boundary.
	nearMax := int64(1<<33 - 100_000) // 8589834592
	var currentPTS atomic.Int64
	currentPTS.Store(nearMax)
	ptsFn := func() int64 { return currentPTS.Load() }

	sink := func(data []byte) {}
	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Break duration 2s = 180000 ticks. Scheduled PTS wraps past 2^33.
	// (nearMax + 180000) & mask = 79408  (wrapped)
	msg := NewSpliceInsert(99, 2*time.Second, true, true)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Advance PTS to just before the wrapped scheduled value.
	currentPTS.Store(nearMax + 90_000) // midway
	time.Sleep(100 * time.Millisecond)
	require.Len(t, inj.ActiveEventIDs(), 1,
		"event should remain active: PTS has not reached the (wrapped) scheduled value")

	// Simulate the wrap: PTS wraps to a small positive value still before scheduled.
	currentPTS.Store(int64(50_000)) // wrapped; still before 79408
	time.Sleep(100 * time.Millisecond)
	require.Len(t, inj.ActiveEventIDs(), 1,
		"event should remain active across the PTS wrap boundary while before scheduled wrapped value")

	// Cross the wrapped scheduled value.
	currentPTS.Store(int64(100_000))
	require.Eventually(t, func() bool {
		return len(inj.ActiveEventIDs()) == 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"auto-return should fire once post-wrap PTS crosses the wrapped scheduled value")
}

func TestAutoReturn_TimeSignalFiresOnPTSCrossing(t *testing.T) {
	t.Parallel()

	var currentPTS atomic.Int64
	insertPTS := int64(4_500_000)
	currentPTS.Store(insertPTS)
	ptsFn := func() int64 { return currentPTS.Load() }

	sink := func(data []byte) {}
	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	ticks := scte35lib.DurationToTicks(400 * time.Millisecond)
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Timing:      "immediate",
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:       777,
				SegmentationType: 0x34,
				DurationTicks:    &ticks,
			},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)
	require.Len(t, inj.ActiveEventIDs(), 1,
		"time_signal with duration should not auto-return until PTS crosses scheduled value")

	currentPTS.Store(insertPTS + int64(ticks) + 100)
	require.Eventually(t, func() bool {
		return len(inj.ActiveEventIDs()) == 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"time_signal auto-return should fire once PTS crosses the scheduled value")
}
