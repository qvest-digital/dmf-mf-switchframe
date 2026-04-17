package output

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/mpegts"
	"github.com/zsiec/switchframe/server/scte35"
)

// ---------------------------------------------------------------------------
// splice_insert tests
// ---------------------------------------------------------------------------

func TestMuxerCorrectness_SpliceInsert_Immediate_IDRAligned(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject an immediate cue-out with auto-return.
	msg := scte35.NewSpliceInsert(100, 30*time.Second, true, true)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Feed P-frames — SCTE-35 should be buffered (IDR-aligned), not flushed.
	h.feedPFrames(t, 5)

	// Clear output so we only see the next GOP's data.
	h.output = nil

	// Feed a full GOP to flush the SCTE-35 section.
	h.feedGOP(t, 30)

	// Verify structural integrity.
	assertSCTE35BeforeIDR(t, h.output)
	assertValidTSPackets(t, h.output)

	// Extract and verify the SCTE-35 section.
	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected at least 1 SCTE-35 section")

	sec := sections[0]
	require.Equal(t, uint8(scte35.CommandSpliceInsert), sec.msg.CommandType)
	require.True(t, sec.msg.IsOut, "expected IsOut=true for cue-out")
	require.True(t, sec.msg.AutoReturn, "expected AutoReturn=true")
	require.Equal(t, "immediate", sec.msg.Timing)
	require.Nil(t, sec.msg.SpliceTimePTS, "immediate splice must have nil SpliceTimePTS")
}

// ---------------------------------------------------------------------------
// splice_insert scheduled with splice_time PTS
// ---------------------------------------------------------------------------

func TestMuxerCorrectness_SpliceInsert_Scheduled_SpliceTimePTS(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	ptsBeforeSchedule := h.currentPTS.Load()

	// Schedule a cue-out with 5000ms pre-roll.
	msg := scte35.NewSpliceInsert(200, 60*time.Second, true, true)
	_, err := h.injector.ScheduleCue(msg, 5000)
	require.NoError(t, err)

	// Feed a GOP to flush the section.
	h.output = nil
	h.feedGOP(t, 30)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected at least 1 SCTE-35 section")

	sec := sections[0]
	require.Equal(t, "scheduled", sec.msg.Timing)
	require.NotNil(t, sec.msg.SpliceTimePTS, "scheduled splice must have SpliceTimePTS")

	// Expected PTS: currentPTS + 5000ms * 90 ticks/ms, masked to 33 bits.
	expectedPTS := (ptsBeforeSchedule + 5000*90) & 0x1FFFFFFFF
	require.Equal(t, expectedPTS, *sec.msg.SpliceTimePTS,
		"SpliceTimePTS should be currentPTS + preRoll*90, masked to 33 bits")
}

// ---------------------------------------------------------------------------
// time_signal tests
// ---------------------------------------------------------------------------

func TestMuxerCorrectness_TimeSignal_ProgramStart_Immediate(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject time_signal with program_start (0x10).
	msg := scte35.NewTimeSignal(0x10, 0, 0x01, []byte("SHOW001"))
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Flush with a GOP.
	h.output = nil
	h.feedGOP(t, 30)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected at least 1 SCTE-35 section")

	sec := sections[0]
	require.Equal(t, uint8(scte35.CommandTimeSignal), sec.msg.CommandType)
	require.GreaterOrEqual(t, len(sec.msg.Descriptors), 1, "expected at least 1 descriptor")

	desc := sec.msg.Descriptors[0]
	require.Equal(t, uint8(0x10), desc.SegmentationType, "segmentationType should be 0x10 (program_start)")
	require.Equal(t, uint8(0x01), desc.UPIDType)
	require.Equal(t, []byte("SHOW001"), desc.UPID)
}

func TestMuxerCorrectness_TimeSignal_ProgramEnd_Immediate(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject time_signal with program_end (0x11).
	msg := scte35.NewTimeSignal(0x11, 0, 0x01, []byte("SHOW001"))
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Flush with a GOP.
	h.output = nil
	h.feedGOP(t, 30)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1)

	sec := sections[0]
	require.Equal(t, uint8(scte35.CommandTimeSignal), sec.msg.CommandType)
	require.GreaterOrEqual(t, len(sec.msg.Descriptors), 1)

	desc := sec.msg.Descriptors[0]
	require.Equal(t, uint8(0x11), desc.SegmentationType, "segmentationType should be 0x11 (program_end)")
	require.Equal(t, uint8(0x01), desc.UPIDType)
	require.Equal(t, []byte("SHOW001"), desc.UPID)
}

func TestMuxerCorrectness_TimeSignal_BreakStart_WithDuration(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject time_signal with break_start (0x22) and 120s duration.
	msg := scte35.NewTimeSignal(0x22, 120*time.Second, 0x01, []byte("BRK01"))
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Flush with a GOP.
	h.output = nil
	h.feedGOP(t, 30)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1)

	sec := sections[0]
	require.Equal(t, uint8(scte35.CommandTimeSignal), sec.msg.CommandType)
	require.GreaterOrEqual(t, len(sec.msg.Descriptors), 1)

	desc := sec.msg.Descriptors[0]
	require.Equal(t, uint8(0x22), desc.SegmentationType, "segmentationType should be 0x22 (break_start)")
	require.NotNil(t, desc.DurationTicks, "break_start should have duration")
	// 120 seconds * 90000 ticks/second = 10,800,000 ticks.
	require.Equal(t, uint64(10_800_000), *desc.DurationTicks,
		"durationTicks should be 10800000 (120s * 90kHz)")
	require.Equal(t, []byte("BRK01"), desc.UPID)
}

// ---------------------------------------------------------------------------
// OnSpliceOut callback tests
// ---------------------------------------------------------------------------

func TestMuxerCorrectness_OnSpliceOut_FiredOnCueOut(t *testing.T) {
	var fired atomic.Bool

	h := newTestHarnessWithConfig(t, scte35.InjectorConfig{
		SCTE35PID: mpegts.SCTE35PID,
		OnSpliceOut: func() {
			fired.Store(true)
		},
	})
	defer h.close()
	h.initMuxer(t)

	// Inject a cue-out.
	msg := scte35.NewSpliceInsert(300, 30*time.Second, true, true)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	require.True(t, fired.Load(), "OnSpliceOut should fire for cue-out")
}

func TestMuxerCorrectness_OnSpliceOut_NotFiredForCueIn(t *testing.T) {
	var fired atomic.Bool

	h := newTestHarnessWithConfig(t, scte35.InjectorConfig{
		SCTE35PID: mpegts.SCTE35PID,
		OnSpliceOut: func() {
			fired.Store(true)
		},
	})
	defer h.close()
	h.initMuxer(t)

	// Inject a cue-in (IsOut=false).
	msg := scte35.NewSpliceInsert(301, 0, false, false)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	require.False(t, fired.Load(), "OnSpliceOut should NOT fire for cue-in")
}

// ---------------------------------------------------------------------------
// Auto-return generates cue-in
// ---------------------------------------------------------------------------

func TestMuxerCorrectness_AutoReturn_GeneratesCueIn(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject cue-out with 100ms auto-return duration.
	msg := scte35.NewSpliceInsert(400, 100*time.Millisecond, true, true)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Feed a GOP to flush the cue-out.
	h.output = nil
	h.feedGOP(t, 30)

	// Verify the cue-out was flushed.
	cueOutSections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(cueOutSections), 1, "expected cue-out section")

	// Wait for auto-return timer to fire (100ms break + margin).
	time.Sleep(200 * time.Millisecond)

	// Feed another GOP to flush the auto-return cue-in.
	h.output = nil
	h.feedGOP(t, 30)

	cueInSections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(cueInSections), 1, "expected auto-return cue-in section")

	// The auto-return should be a cue-in (IsOut=false).
	found := false
	for _, sec := range cueInSections {
		if sec.msg.CommandType == scte35.CommandSpliceInsert && !sec.msg.IsOut {
			found = true
			break
		}
	}
	require.True(t, found, "expected at least one cue-in splice_insert from auto-return")
}

// ---------------------------------------------------------------------------
// HoldBreak prevents auto-return
// ---------------------------------------------------------------------------

func TestMuxerCorrectness_HoldBreak_PreventsAutoReturn(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject cue-out with 100ms auto-return.
	msg := scte35.NewSpliceInsert(500, 100*time.Millisecond, true, true)
	eventID, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Hold the break immediately to prevent auto-return.
	err = h.injector.HoldBreak(eventID)
	require.NoError(t, err)

	// Wait past the auto-return duration.
	time.Sleep(200 * time.Millisecond)

	// Feed a GOP to flush anything pending.
	h.output = nil
	h.feedGOP(t, 30)

	// There should be no cue-in sections — the hold should have prevented it.
	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	for _, sec := range sections {
		if sec.msg.CommandType == scte35.CommandSpliceInsert && !sec.msg.IsOut {
			t.Fatal("found unexpected cue-in section — HoldBreak should have prevented auto-return")
		}
	}
}

// ---------------------------------------------------------------------------
// TS packet structure verification
// ---------------------------------------------------------------------------

func TestMuxerCorrectness_TSPacketStructure(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject a splice_insert.
	msg := scte35.NewSpliceInsert(600, 30*time.Second, true, true)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Flush with a GOP.
	h.output = nil
	h.feedGOP(t, 30)

	// Verify TS packet structure.
	assertValidTSPackets(t, h.output)

	// Find the first SCTE-35 packet and verify its structure.
	const pktSize = 188
	found := false
	for i := 0; i+pktSize <= len(h.output); i += pktSize {
		pkt := h.output[i : i+pktSize]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid != mpegts.SCTE35PID {
			continue
		}

		found = true

		// Verify sync byte.
		require.Equal(t, byte(0x47), pkt[0], "sync byte must be 0x47")

		// Verify PUSI (Payload Unit Start Indicator) is set.
		pusi := (pkt[1] & 0x40) != 0
		require.True(t, pusi, "PUSI must be set for SCTE-35 section start")

		// Determine adaptation field control.
		afc := (pkt[3] >> 4) & 0x03
		payloadStart := 4
		if afc == 0x03 { // both adaptation field and payload
			afLen := int(pkt[4])
			payloadStart = 5 + afLen
		}
		// afc=0x01 means payload only (most common for SCTE-35).
		require.Less(t, payloadStart, pktSize, "payload must fit within packet")

		// Check pointer_field = 0x00 (section starts immediately after pointer).
		require.Equal(t, byte(0x00), pkt[payloadStart],
			"pointer_field should be 0x00 for new section")

		// Check table_id = 0xFC (SCTE-35 splice_info_section).
		sectionStart := payloadStart + 1
		require.Less(t, sectionStart, pktSize, "section start must be within packet")
		require.Equal(t, byte(0xFC), pkt[sectionStart],
			"table_id must be 0xFC for SCTE-35")

		break
	}

	require.True(t, found, "no SCTE-35 TS packet found in output")
}
