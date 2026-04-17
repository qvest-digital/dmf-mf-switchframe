package output

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal/mpegts"
	"github.com/zsiec/switchframe/server/scte35"
)

// ---------------------------------------------------------------------------
// Broadcast edge case tests for SCTE-35 IDR alignment
// ---------------------------------------------------------------------------

// TestBroadcast_LongStream_CueMidStream verifies that a cue injected after
// a long stream of GOPs is correctly IDR-aligned and the output remains
// structurally valid across many GOPs.
func TestBroadcast_LongStream_CueMidStream(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Feed 5 GOPs of 30 frames each to simulate a long-running stream.
	for i := 0; i < 5; i++ {
		h.feedGOP(t, 30)
	}

	// Inject a cue-out mid-stream.
	msg := scte35.NewSpliceInsert(1, 30*time.Second, true, true)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Feed P-frames (no IDR) — cue should remain buffered.
	h.feedPFrames(t, 10)

	// Clear output, feed a new GOP to flush the cue.
	h.output = nil
	h.feedGOP(t, 30)

	assertSCTE35BeforeIDR(t, h.output)
	assertValidTSPackets(t, h.output)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected at least 1 SCTE-35 section")
	require.True(t, sections[0].msg.IsOut, "expected IsOut=true for cue-out")

	// Feed 4 more GOPs and verify the full output remains valid.
	for i := 0; i < 4; i++ {
		h.feedGOP(t, 30)
	}
	assertValidTSPackets(t, h.output)
}

// TestBroadcast_BackToBackCues verifies that a cue-out immediately followed
// by a cue-in produces both sections in the correct order on the next IDR.
func TestBroadcast_BackToBackCues(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject cue-out then immediate cue-in with same event ID.
	msgOut := scte35.NewSpliceInsert(100, 30*time.Second, true, false)
	_, err := h.injector.InjectCue(msgOut)
	require.NoError(t, err)

	msgIn := scte35.NewSpliceInsert(100, 0, false, false)
	_, err = h.injector.InjectCue(msgIn)
	require.NoError(t, err)

	// Flush with a GOP.
	h.output = nil
	h.feedGOP(t, 30)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 2,
		"expected at least 2 SCTE-35 sections (cue-out + cue-in)")

	// First section should be cue-out, second should be cue-in.
	require.True(t, sections[0].msg.IsOut, "first section should be cue-out")
	require.False(t, sections[1].msg.IsOut, "second section should be cue-in")
}

// TestBroadcast_CueDuringPFrameRun verifies that SCTE-35 sections are NOT
// flushed on P-frames and only appear at the next IDR boundary.
func TestBroadcast_CueDuringPFrameRun(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Start a GOP with just the IDR, then stream P-frames.
	h.feedGOP(t, 1)
	h.feedPFrames(t, 15)

	// Inject cue mid-P-frame run.
	msg := scte35.NewSpliceInsert(200, 30*time.Second, true, true)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Feed more P-frames — no IDR, so cue must stay buffered.
	h.output = nil
	h.feedPFrames(t, 14)

	require.Equal(t, 0, countSCTE35Packets(h.output, mpegts.SCTE35PID),
		"SCTE-35 must NOT flush on P-frames")

	// Now feed a full GOP — the cue should flush before the IDR.
	h.output = nil
	h.feedGOP(t, 30)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected SCTE-35 section after IDR")
	assertSCTE35BeforeIDR(t, h.output)
}

// TestBroadcast_MultipleCuesSameGOP verifies that multiple cues injected
// between IDRs are all flushed at the next keyframe (up to the muxer cap).
func TestBroadcast_MultipleCuesSameGOP(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject 3 time_signal cues with no keyframe between them.
	for i := 0; i < 3; i++ {
		msg := scte35.NewTimeSignal(0x10, 0, 0x01, []byte("SIG"))
		_, err := h.injector.InjectCue(msg)
		require.NoError(t, err)
	}

	// Flush with a single GOP.
	h.output = nil
	h.feedGOP(t, 30)

	assertSCTE35BeforeIDR(t, h.output)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 3,
		"expected at least 3 SCTE-35 sections (one per cue)")
}

// TestBroadcast_VariableGOPSize verifies SCTE-35 IDR alignment works
// correctly across a range of GOP sizes from short to long.
func TestBroadcast_VariableGOPSize(t *testing.T) {
	gopSizes := []int{15, 30, 60, 90}

	for _, size := range gopSizes {
		t.Run("", func(t *testing.T) {
			h := newTestHarness(t)
			defer h.close()
			h.initInjector(t)
			h.initMuxer(t)

			msg := scte35.NewSpliceInsert(300, 30*time.Second, true, true)
			_, err := h.injector.InjectCue(msg)
			require.NoError(t, err)

			h.output = nil
			h.feedGOP(t, size)

			assertSCTE35BeforeIDR(t, h.output)

			sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
			require.GreaterOrEqual(t, len(sections), 1,
				"expected SCTE-35 section for GOP size %d", size)
		})
	}
}

// TestBroadcast_RapidCueOutInCycle verifies that rapid cue-out/cue-in
// cycling produces valid TS output. The muxer caps pending IDR sections
// at 4, so only the most recent 4 survive.
func TestBroadcast_RapidCueOutInCycle(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// 5 rapid cue-out/cue-in cycles (10 total injections).
	for i := 0; i < 5; i++ {
		eventID := uint32(1000 + i*2)

		msgOut := scte35.NewSpliceInsert(eventID, 30*time.Second, true, false)
		_, err := h.injector.InjectCue(msgOut)
		require.NoError(t, err)

		msgIn := scte35.NewSpliceInsert(eventID, 0, false, false)
		_, err = h.injector.InjectCue(msgIn)
		require.NoError(t, err)
	}

	// Flush with a GOP.
	h.output = nil
	h.feedGOP(t, 30)

	assertValidTSPackets(t, h.output)

	// maxPendingIDRSCTE35 = 4, so only the last 4 sections are kept.
	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 4,
		"expected at least 4 SCTE-35 sections (muxer cap)")
}

// TestBroadcast_PTSWrap33Bit verifies that scheduled cues near the 33-bit
// PTS wrap boundary produce a valid SpliceTimePTS.
func TestBroadcast_PTSWrap33Bit(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	h.currentPTS.Store((1 << 33) - 90000)

	// Schedule with 5000ms pre-roll — the splice PTS will wrap past 2^33.
	msg := scte35.NewSpliceInsert(700, 60*time.Second, true, true)
	_, err := h.injector.ScheduleCue(msg, 5000)
	require.NoError(t, err)

	// Flush with a GOP.
	h.output = nil
	h.feedGOP(t, 30)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected at least 1 SCTE-35 section")

	sec := sections[0]
	require.NotNil(t, sec.msg.SpliceTimePTS, "scheduled splice must have SpliceTimePTS")
	require.Greater(t, *sec.msg.SpliceTimePTS, int64(0),
		"SpliceTimePTS should be a valid positive value after 33-bit wrap")
}

// TestBroadcast_AutoReturn_CueInAppearsInTS verifies that an auto-return
// cue-out generates a cue-in in the TS output after the break duration expires.
func TestBroadcast_AutoReturn_CueInAppearsInTS(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject cue-out with 200ms duration + auto-return.
	msg := scte35.NewSpliceInsert(800, 200*time.Millisecond, true, true)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Flush the cue-out.
	h.output = nil
	h.feedGOP(t, 30)

	cueOutSections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(cueOutSections), 1, "expected cue-out section")

	// Verify the cue-out.
	foundOut := false
	for _, sec := range cueOutSections {
		if sec.msg.CommandType == scte35.CommandSpliceInsert && sec.msg.IsOut {
			foundOut = true
			break
		}
	}
	require.True(t, foundOut, "expected cue-out splice_insert")

	// Wait for auto-return to fire (200ms break + 100ms margin).
	time.Sleep(300 * time.Millisecond)

	// Flush the auto-return cue-in.
	h.output = nil
	h.feedGOP(t, 30)

	cueInSections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(cueInSections), 1, "expected auto-return cue-in section")

	// Find the cue-in.
	foundIn := false
	for _, sec := range cueInSections {
		if sec.msg.CommandType == scte35.CommandSpliceInsert && !sec.msg.IsOut {
			foundIn = true
			break
		}
	}
	require.True(t, foundIn, "expected cue-in splice_insert from auto-return")
}

// TestBroadcast_InterleavedAudioVideo verifies that SCTE-35 sections are
// correctly IDR-aligned even when audio frames are interleaved with video.
func TestBroadcast_InterleavedAudioVideo(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Inject a cue.
	msg := scte35.NewSpliceInsert(900, 30*time.Second, true, true)
	_, err := h.injector.InjectCue(msg)
	require.NoError(t, err)

	// Clear output. Write an audio frame, then a GOP.
	h.output = nil

	err = h.muxer.WriteAudio(&media.AudioFrame{
		PTS:        h.currentPTS.Load() + 1500,
		Data:       []byte{0xDE, 0x04, 0x00, 0x26, 0x20, 0x54, 0xE5, 0x00},
		SampleRate: 48000,
		Channels:   2,
	})
	require.NoError(t, err)

	h.feedGOP(t, 30)

	assertValidTSPackets(t, h.output)
	assertSCTE35BeforeIDR(t, h.output)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected SCTE-35 section with interleaved audio")
}

// TestBroadcast_PreRollRepetition verifies that SCTE-67 pre-roll repetition
// re-emits the cue on subsequent GOPs during the pre-roll window.
func TestBroadcast_PreRollRepetition(t *testing.T) {
	h := newTestHarness(t)
	defer h.close()
	h.initInjector(t)
	h.initMuxer(t)

	// Schedule with 3000ms pre-roll (SCTE-67 repeats every 1s).
	msg := scte35.NewSpliceInsert(1000, 60*time.Second, true, true)
	_, err := h.injector.ScheduleCue(msg, 3000)
	require.NoError(t, err)

	// First flush — the initial cue should appear.
	h.output = nil
	h.feedGOP(t, 30)

	sections := extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected initial pre-roll cue")

	// Wait for at least one repetition cycle (1s repeat, wait 1.5s).
	time.Sleep(1500 * time.Millisecond)

	// Second flush — the repeated cue should appear.
	h.output = nil
	h.feedGOP(t, 30)

	sections = extractSCTE35Sections(t, h.output, mpegts.SCTE35PID)
	require.GreaterOrEqual(t, len(sections), 1, "expected repeated pre-roll cue")
}
