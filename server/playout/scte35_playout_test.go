package playout

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/scte35"
)

// captureHandler implements SecondaryEventHandler and records SCTE-35 cues.
type captureHandler struct {
	mu   sync.Mutex
	cues []*SCTE35EventData
}

func (h *captureHandler) OnSCTE35Cue(data *SCTE35EventData) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cues = append(h.cues, data)
	return nil
}

func (h *captureHandler) OnGraphicsOn(layer int, template string) error  { return nil }
func (h *captureHandler) OnGraphicsOff(layer int) error                  { return nil }
func (h *captureHandler) OnGraphicsUpdate(layer int, template string) error { return nil }
func (h *captureHandler) OnAudioLevel(sourceKey string, levelDB float64) error { return nil }
func (h *captureHandler) OnAudioMute(sourceKey string, muted bool) error { return nil }
func (h *captureHandler) OnAudioAFV(sourceKey string, afv bool) error    { return nil }
func (h *captureHandler) OnCaptionMode(mode string) error                { return nil }
func (h *captureHandler) OnCaptionText(text string) error                { return nil }

func (h *captureHandler) getCues() []*SCTE35EventData {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*SCTE35EventData, len(h.cues))
	copy(out, h.cues)
	return out
}

func TestPlayout_ProgramStartImmediate(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: 0,
			Anchor:   "start",
			SCTE35: &SCTE35EventData{
				Preset:      "program_start",
				CommandType: scte35.CommandTimeSignal,
				Timing:      "immediate",
				Descriptors: []SegDescriptor{
					{
						SegmentationType: 0x10,
						UPID:             "SHOW001",
						UPIDType:         0x01,
					},
				},
			},
		},
	}, 10000)

	sched.Tick(0)

	cues := h.getCues()
	require.Len(t, cues, 1)
	require.Equal(t, "program_start", cues[0].Preset)
	require.Equal(t, uint8(scte35.CommandTimeSignal), cues[0].CommandType)
	require.Equal(t, "immediate", cues[0].Timing)
	require.Len(t, cues[0].Descriptors, 1)
	require.Equal(t, uint8(0x10), cues[0].Descriptors[0].SegmentationType)
	require.Equal(t, "SHOW001", cues[0].Descriptors[0].UPID)
	require.Equal(t, uint8(0x01), cues[0].Descriptors[0].UPIDType)
}

func TestPlayout_ProgramStartWithPreRoll(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: 0,
			Anchor:   "start",
			SCTE35: &SCTE35EventData{
				Preset:      "program_start",
				CommandType: scte35.CommandTimeSignal,
				Timing:      "scheduled",
				Descriptors: []SegDescriptor{
					{
						SegmentationType: 0x10,
						UPID:             "SHOW001",
						UPIDType:         0x01,
					},
				},
			},
		},
	}, 10000)

	sched.Tick(0)

	cues := h.getCues()
	require.Len(t, cues, 1)
	require.Equal(t, "scheduled", cues[0].Timing)
	require.Equal(t, uint8(scte35.CommandTimeSignal), cues[0].CommandType)
}

func TestPlayout_ProgramEndImmediate(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: 0,
			Anchor:   "end",
			SCTE35: &SCTE35EventData{
				Preset:      "program_end",
				CommandType: scte35.CommandTimeSignal,
				Timing:      "immediate",
				Descriptors: []SegDescriptor{
					{SegmentationType: 0x11},
				},
			},
		},
	}, 10000)

	// Just before the end, nothing should fire.
	sched.Tick(9999)
	require.Len(t, h.getCues(), 0)

	// At the end, it fires.
	sched.Tick(10000)
	cues := h.getCues()
	require.Len(t, cues, 1)
	require.Equal(t, "program_end", cues[0].Preset)
	require.Equal(t, uint8(0x11), cues[0].Descriptors[0].SegmentationType)
}

func TestPlayout_ProgramEndWithPreRoll(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	// offset=-5000, anchor="end", durationMs=10000 → fire at 5000
	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: -5000,
			Anchor:   "end",
			SCTE35: &SCTE35EventData{
				Preset:      "program_end",
				CommandType: scte35.CommandTimeSignal,
				Timing:      "scheduled",
				Descriptors: []SegDescriptor{
					{SegmentationType: 0x11},
				},
			},
		},
	}, 10000)

	sched.Tick(4999)
	require.Len(t, h.getCues(), 0)

	sched.Tick(5000)
	cues := h.getCues()
	require.Len(t, cues, 1)
	require.Equal(t, "program_end", cues[0].Preset)
}

func TestPlayout_BreakStartImmediate(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	breakDur := int64(120000)
	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: 15000,
			Anchor:   "start",
			SCTE35: &SCTE35EventData{
				Preset:          "cue_out",
				CommandType:     scte35.CommandSpliceInsert,
				IsOut:           true,
				BreakDurationMs: &breakDur,
				AutoReturn:      true,
				Timing:          "immediate",
			},
		},
	}, 60000)

	sched.Tick(14999)
	require.Len(t, h.getCues(), 0)

	sched.Tick(15000)
	cues := h.getCues()
	require.Len(t, cues, 1)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), cues[0].CommandType)
	require.True(t, cues[0].IsOut)
	require.NotNil(t, cues[0].BreakDurationMs)
	require.Equal(t, int64(120000), *cues[0].BreakDurationMs)
	require.True(t, cues[0].AutoReturn)
}

func TestPlayout_BreakStartWithPreRoll(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	breakDur := int64(120000)
	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: 10000,
			Anchor:   "start",
			SCTE35: &SCTE35EventData{
				Preset:          "cue_out",
				CommandType:     scte35.CommandSpliceInsert,
				IsOut:           true,
				BreakDurationMs: &breakDur,
				AutoReturn:      true,
				Timing:          "scheduled",
			},
		},
	}, 60000)

	sched.Tick(9999)
	require.Len(t, h.getCues(), 0)

	sched.Tick(10000)
	cues := h.getCues()
	require.Len(t, cues, 1)
	require.Equal(t, "scheduled", cues[0].Timing)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), cues[0].CommandType)
}

func TestPlayout_MultipleEventsOrdered(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	eventID := uint32(42)
	breakDur := int64(120000)
	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: 0,
			Anchor:   "start",
			SCTE35: &SCTE35EventData{
				Preset:      "program_start",
				CommandType: scte35.CommandTimeSignal,
				Timing:      "immediate",
				Descriptors: []SegDescriptor{
					{SegmentationType: 0x10},
				},
			},
		},
		{
			Type:     "scte35_cue",
			OffsetMs: 30000,
			Anchor:   "start",
			SCTE35: &SCTE35EventData{
				Preset:          "cue_out",
				CommandType:     scte35.CommandSpliceInsert,
				EventID:         &eventID,
				IsOut:           true,
				BreakDurationMs: &breakDur,
				AutoReturn:      true,
				Timing:          "immediate",
			},
		},
		{
			Type:     "scte35_cue",
			OffsetMs: 0,
			Anchor:   "end",
			SCTE35: &SCTE35EventData{
				Preset:      "program_end",
				CommandType: scte35.CommandTimeSignal,
				Timing:      "immediate",
				Descriptors: []SegDescriptor{
					{SegmentationType: 0x11},
				},
			},
		},
	}, 60000)

	// Tick 0 → program_start fires.
	sched.Tick(0)
	cues := h.getCues()
	require.Len(t, cues, 1)
	require.Equal(t, uint8(0x10), cues[0].Descriptors[0].SegmentationType)

	// Tick 30000 → splice_insert fires.
	sched.Tick(30000)
	cues = h.getCues()
	require.Len(t, cues, 2)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), cues[1].CommandType)
	require.True(t, cues[1].IsOut)

	// Tick 60000 → program_end fires.
	sched.Tick(60000)
	cues = h.getCues()
	require.Len(t, cues, 3)
	require.Equal(t, uint8(0x11), cues[2].Descriptors[0].SegmentationType)
}

func TestPlayout_NoReFire(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: 1000,
			Anchor:   "start",
			SCTE35: &SCTE35EventData{
				Preset:      "program_start",
				CommandType: scte35.CommandTimeSignal,
				Timing:      "immediate",
				Descriptors: []SegDescriptor{
					{SegmentationType: 0x10},
				},
			},
		},
	}, 10000)

	sched.Tick(1000)
	require.Len(t, h.getCues(), 1)

	// Tick well past the fire time again.
	sched.Tick(2000)
	sched.Tick(5000)
	sched.Tick(10000)
	require.Len(t, h.getCues(), 1)
}

func TestPlayout_Reanchor(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	// End-anchored at offset=-2000, durationMs=10000 → fires at 8000.
	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: -2000,
			Anchor:   "end",
			SCTE35: &SCTE35EventData{
				Preset:      "program_end",
				CommandType: scte35.CommandTimeSignal,
				Timing:      "immediate",
				Descriptors: []SegDescriptor{
					{SegmentationType: 0x11},
				},
			},
		},
	}, 10000)

	// Nothing fires before original time.
	sched.Tick(7999)
	require.Len(t, h.getCues(), 0)

	// Reanchor to 15000 → new fire time = 15000 + (-2000) = 13000.
	sched.Reanchor(15000)

	// Old fire time (8000) should not fire since reanchored.
	sched.Tick(8000)
	require.Len(t, h.getCues(), 0)

	sched.Tick(12999)
	require.Len(t, h.getCues(), 0)

	// New fire time fires.
	sched.Tick(13000)
	cues := h.getCues()
	require.Len(t, cues, 1)
	require.Equal(t, "program_end", cues[0].Preset)
}

func TestPlayout_AllFieldsPreserved(t *testing.T) {
	h := &captureHandler{}
	sched := NewSecondaryEventScheduler(h)

	eventID := uint32(9999)
	breakDur := int64(90000)
	spliceTime := int64(2700000) // 30s in 90kHz
	segDur := int64(60000)

	sched.BeginItem([]SecondaryEvent{
		{
			Type:     "scte35_cue",
			OffsetMs: 0,
			Anchor:   "start",
			SCTE35: &SCTE35EventData{
				Preset:          "custom",
				CommandType:     scte35.CommandSpliceInsert,
				EventID:         &eventID,
				IsOut:           true,
				BreakDurationMs: &breakDur,
				AutoReturn:      true,
				Timing:          "scheduled",
				SpliceTimePTS:   &spliceTime,
				PTSAdjustment:   12345,
				UniqueProgramID: 42,
				AvailNum:        3,
				AvailsExpected:  5,
				Tier:            100,
				Descriptors: []SegDescriptor{
					{
						SegmentationType: 0x34,
						SegEventID:       77777,
						DurationMs:       &segDur,
						UPIDType:         0x09,
						UPID:             "SIGNAL:abcdef0123456789",
					},
					{
						SegmentationType:      0x36,
						SegEventID:            88888,
						UPIDType:              0x01,
						UPID:                  "SHOW002",
						DeliveryNotRestricted: true,
						WebDeliveryAllowed:    true,
						NoRegionalBlackout:    true,
						ArchiveAllowed:        true,
						DeviceRestrictions:    0x03,
						Cancel:                false,
					},
				},
			},
		},
	}, 10000)

	sched.Tick(0)

	cues := h.getCues()
	require.Len(t, cues, 1)

	c := cues[0]
	require.Equal(t, "custom", c.Preset)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), c.CommandType)
	require.NotNil(t, c.EventID)
	require.Equal(t, uint32(9999), *c.EventID)
	require.True(t, c.IsOut)
	require.NotNil(t, c.BreakDurationMs)
	require.Equal(t, int64(90000), *c.BreakDurationMs)
	require.True(t, c.AutoReturn)
	require.Equal(t, "scheduled", c.Timing)
	require.NotNil(t, c.SpliceTimePTS)
	require.Equal(t, int64(2700000), *c.SpliceTimePTS)
	require.Equal(t, int64(12345), c.PTSAdjustment)
	require.Equal(t, uint16(42), c.UniqueProgramID)
	require.Equal(t, uint8(3), c.AvailNum)
	require.Equal(t, uint8(5), c.AvailsExpected)
	require.Equal(t, uint16(100), c.Tier)

	require.Len(t, c.Descriptors, 2)

	d0 := c.Descriptors[0]
	require.Equal(t, uint8(0x34), d0.SegmentationType)
	require.Equal(t, uint32(77777), d0.SegEventID)
	require.NotNil(t, d0.DurationMs)
	require.Equal(t, int64(60000), *d0.DurationMs)
	require.Equal(t, uint8(0x09), d0.UPIDType)
	require.Equal(t, "SIGNAL:abcdef0123456789", d0.UPID)

	d1 := c.Descriptors[1]
	require.Equal(t, uint8(0x36), d1.SegmentationType)
	require.Equal(t, uint32(88888), d1.SegEventID)
	require.Equal(t, uint8(0x01), d1.UPIDType)
	require.Equal(t, "SHOW002", d1.UPID)
	require.True(t, d1.DeliveryNotRestricted)
	require.True(t, d1.WebDeliveryAllowed)
	require.True(t, d1.NoRegionalBlackout)
	require.True(t, d1.ArchiveAllowed)
	require.Equal(t, uint8(0x03), d1.DeviceRestrictions)
	require.False(t, d1.Cancel)
}
