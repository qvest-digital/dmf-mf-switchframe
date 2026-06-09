package srt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPTSLinearizer_NormalProgression(t *testing.T) {
	// Normal frame progression should pass through unchanged.
	lin := NewPTSLinearizer()

	// Video at 24fps: 3750 ticks per frame at 90kHz.
	require.Equal(t, int64(0), lin.Linearize(0, StreamVideo))
	require.Equal(t, int64(3750), lin.Linearize(3750, StreamVideo))
	require.Equal(t, int64(7500), lin.Linearize(7500, StreamVideo))

	// Audio at 48kHz: 1920 ticks per frame.
	require.Equal(t, int64(1920), lin.Linearize(1920, StreamAudio))
	require.Equal(t, int64(3840), lin.Linearize(3840, StreamAudio))
}

func TestPTSLinearizer_VideoAudioPTSAlignedAfterJump(t *testing.T) {
	// THE BUG: After a PTS jump (SRT loop), video and audio PTS must stay
	// aligned. With separate offsets they diverge because video frameDur
	// (3750) differs from audio frameDur (1920).
	lin := NewPTSLinearizer()

	// Establish normal progression — video and audio interleaved.
	// Simulate a 10-second clip at 24fps video + 48kHz audio.
	videoPTS := int64(0)
	audioPTS := int64(0)
	const videoFrameDur = 3750  // 24fps in 90kHz
	const audioFrameDur = 1920  // 48kHz/1024 in 90kHz
	const clipDuration = 900000 // 10 seconds in 90kHz

	// Run through the clip.
	for videoPTS < clipDuration {
		lin.Linearize(videoPTS, StreamVideo)
		videoPTS += videoFrameDur
	}
	for audioPTS < clipDuration {
		lin.Linearize(audioPTS, StreamAudio)
		audioPTS += audioFrameDur
	}

	lastVideoBeforeJump := lin.Linearize(videoPTS, StreamVideo)
	lastAudioBeforeJump := lin.Linearize(audioPTS, StreamAudio)

	// PTS jump: source loops back to 0 (SRT demo clip restart).
	firstVideoAfterJump := lin.Linearize(0, StreamVideo)
	firstAudioAfterJump := lin.Linearize(0, StreamAudio)

	// Both should advance by approximately one frame duration from their
	// pre-jump values. The critical requirement: the relative offset between
	// video and audio PTS should be similar before and after the jump.
	preJumpDelta := lastVideoBeforeJump - lastAudioBeforeJump
	postJumpDelta := firstVideoAfterJump - firstAudioAfterJump

	// Allow up to 1 frame of drift (3750 ticks = ~41ms).
	// Without the fix, this diverges by thousands of ticks per jump.
	drift := abs64(postJumpDelta - preJumpDelta)
	require.Less(t, drift, int64(videoFrameDur),
		"video-audio PTS should not diverge after jump: pre=%d post=%d drift=%d",
		preJumpDelta, postJumpDelta, drift)
}

func TestPTSLinearizer_MultipleJumpsAccumulateDrift(t *testing.T) {
	// Multiple PTS jumps should not cause cumulative video-audio drift.
	lin := NewPTSLinearizer()

	const videoFrameDur = 3750
	const audioFrameDur = 1920
	const clipDuration = 900000 // 10 seconds

	for loop := 0; loop < 5; loop++ {
		// Run through one clip iteration.
		for pts := int64(0); pts < clipDuration; pts += videoFrameDur {
			lin.Linearize(pts, StreamVideo)
		}
		for pts := int64(0); pts < clipDuration; pts += audioFrameDur {
			lin.Linearize(pts, StreamAudio)
		}
	}

	// After 5 loops, get final PTS for both streams.
	finalVideo := lin.Linearize(0, StreamVideo)
	finalAudio := lin.Linearize(0, StreamAudio)

	// They should be within a few frames of each other, not hundreds of seconds apart.
	driftSeconds := float64(abs64(finalVideo-finalAudio)) / 90000.0
	require.Less(t, driftSeconds, 1.0,
		"after 5 loops, video-audio PTS drift should be <1 second, got %.1fs", driftSeconds)
}

func TestPTSLinearizer_OneSidedAudioGapDoesNotShiftVideo(t *testing.T) {
	// Finding #76: an audio-only PTS gap (>0.5s SRT loss burst or audio
	// decode errors while video keeps flowing) is NOT a shared-timeline jump.
	// It must not rewrite the shared offset, or the video output PTS jumps
	// backward by the gap length even though video's raw deltas are normal.
	lin := NewPTSLinearizer()

	const videoFrameDur = 3750 // 24fps in 90kHz
	const audioFrameDur = 1920 // 48kHz/1024 in 90kHz
	const gapStart = 180000    // 2s in
	const gapLen = 90000       // 1s audio-only dropout
	const end = 360000         // 4s total

	var lastVideoOut int64 = -1
	feedVideo := func(raw int64) {
		out := lin.Linearize(raw, StreamVideo)
		if lastVideoOut >= 0 {
			require.Equal(t, int64(videoFrameDur), out-lastVideoOut,
				"video output PTS must keep advancing by one frame (raw video deltas are normal); raw=%d", raw)
		}
		lastVideoOut = out
	}
	var lastAudioOut int64 = -1
	feedAudio := func(raw int64) {
		out := lin.Linearize(raw, StreamAudio)
		if lastAudioOut >= 0 {
			require.GreaterOrEqual(t, out, lastAudioOut,
				"audio output PTS must stay monotonic; raw=%d", raw)
		}
		lastAudioOut = out
	}

	// Interleave both streams in timestamp order, skipping the audio frames
	// that fall inside the dropout window.
	nextVideo, nextAudio := int64(0), int64(0)
	for nextVideo < end || nextAudio < end {
		if nextAudio <= nextVideo && nextAudio < end {
			if nextAudio < gapStart || nextAudio >= gapStart+gapLen {
				feedAudio(nextAudio)
			}
			nextAudio += audioFrameDur
		} else {
			feedVideo(nextVideo)
			nextVideo += videoFrameDur
		}
	}
}

func TestPTSLinearizer_SharedJumpStillCorrectedAfterOneSidedGap(t *testing.T) {
	// After a one-sided audio gap, a subsequent genuine shared jump (source
	// loop back to 0) must still be corrected exactly once and keep both
	// outputs monotonic and aligned.
	lin := NewPTSLinearizer()

	const videoFrameDur = 3750
	const audioFrameDur = 1920
	const gapStart = 90000
	const gapLen = 90000
	const loopAt = 360000 // both streams restart at 0 here

	var lastVideoOut, lastAudioOut int64 = -1, -1
	feed := func(raw int64, st StreamType) int64 {
		out := lin.Linearize(raw, st)
		if st == StreamVideo {
			if lastVideoOut >= 0 {
				require.Greater(t, out, lastVideoOut, "video output must stay monotonic; raw=%d", raw)
			}
			lastVideoOut = out
		} else {
			if lastAudioOut >= 0 {
				require.GreaterOrEqual(t, out, lastAudioOut, "audio output must stay monotonic; raw=%d", raw)
			}
			lastAudioOut = out
		}
		return out
	}

	run := func(from, to int64) {
		nextVideo, nextAudio := from, from
		for nextVideo < to || nextAudio < to {
			if nextAudio <= nextVideo && nextAudio < to {
				if nextAudio < gapStart || nextAudio >= gapStart+gapLen {
					feed(nextAudio, StreamAudio)
				}
				nextAudio += audioFrameDur
			} else {
				feed(nextVideo, StreamVideo)
				nextVideo += videoFrameDur
			}
		}
	}

	run(0, loopAt)
	preDelta := lastVideoOut - lastAudioOut

	// Source loops: both streams restart from 0.
	run(0, 90000)
	postDelta := lastVideoOut - lastAudioOut

	// Video/audio alignment must survive the loop (within a frame or two).
	require.Less(t, abs64(postDelta-preDelta), int64(2*videoFrameDur),
		"video-audio alignment must survive a shared jump after a one-sided gap: pre=%d post=%d", preDelta, postDelta)
}

func TestPTSLinearizer_SharedBackwardRebaseStaysMonotonic(t *testing.T) {
	// Encoder restart rebasing BOTH streams 0.8s backward — smaller than
	// crossStreamPTSTolerance (1s). The first stream to see the jump finds
	// its new raw PTS "consistent" with the other stream's still-pre-jump
	// lastInput; without the backward-jump guard it passed through and
	// emitted output PTS 0.8s lower than the previous output. Both outputs
	// must stay monotonic and A/V alignment must be preserved, regardless
	// of which stream sees the rebase first.
	const videoFrameDur = 3750 // 24fps in 90kHz
	const audioFrameDur = 1920 // 48kHz/1024 in 90kHz
	const rebaseAt = 360000    // 4s in
	const rebase = 72000       // 0.8s backward, within crossStreamPTSTolerance

	for _, first := range []StreamType{StreamVideo, StreamAudio} {
		name := "videoSeesRebaseFirst"
		if first == StreamAudio {
			name = "audioSeesRebaseFirst"
		}
		t.Run(name, func(t *testing.T) {
			lin := NewPTSLinearizer()

			var lastVideoOut, lastAudioOut int64 = -1, -1
			feed := func(raw int64, st StreamType) {
				out := lin.Linearize(raw, st)
				if st == StreamVideo {
					if lastVideoOut >= 0 {
						require.Greater(t, out, lastVideoOut,
							"video output PTS must stay monotonic; raw=%d", raw)
					}
					lastVideoOut = out
				} else {
					if lastAudioOut >= 0 {
						require.Greater(t, out, lastAudioOut,
							"audio output PTS must stay monotonic; raw=%d", raw)
					}
					lastAudioOut = out
				}
			}

			// Interleaved normal progression up to the rebase point.
			nextVideo, nextAudio := int64(0), int64(0)
			for nextVideo < rebaseAt || nextAudio < rebaseAt {
				if nextAudio <= nextVideo && nextAudio < rebaseAt {
					feed(nextAudio, StreamAudio)
					nextAudio += audioFrameDur
				} else {
					feed(nextVideo, StreamVideo)
					nextVideo += videoFrameDur
				}
			}
			preDelta := lastVideoOut - lastAudioOut

			// Encoder restart: both timelines continue 0.8s lower than they
			// would have. `first` delivers the rebased PTS first.
			vRaw, aRaw := nextVideo-rebase, nextAudio-rebase
			if first == StreamVideo {
				feed(vRaw, StreamVideo)
				feed(aRaw, StreamAudio)
			} else {
				feed(aRaw, StreamAudio)
				feed(vRaw, StreamVideo)
			}
			vRaw += videoFrameDur
			aRaw += audioFrameDur

			// Continue interleaved for 1s after the rebase.
			endV, endA := vRaw+90000, aRaw+90000
			for vRaw < endV || aRaw < endA {
				if aRaw <= vRaw && aRaw < endA {
					feed(aRaw, StreamAudio)
					aRaw += audioFrameDur
				} else {
					feed(vRaw, StreamVideo)
					vRaw += videoFrameDur
				}
			}

			// A/V relative alignment must survive the rebase (within a
			// frame or two — the correction continues at lastOutput+frameDur).
			postDelta := lastVideoOut - lastAudioOut
			require.Less(t, abs64(postDelta-preDelta), int64(2*videoFrameDur),
				"video-audio alignment must survive a shared backward rebase: pre=%d post=%d",
				preDelta, postDelta)
		})
	}
}

func TestPTSLinearizer_StaleOtherStreamFallsBackToJumpCorrection(t *testing.T) {
	// If the other stream died long ago, its last raw PTS cannot vouch for
	// the shared timeline. A jump on the live stream must then fall back to
	// the regular shared-offset correction so its output stays monotonic.
	lin := NewPTSLinearizer()

	const videoFrameDur = 3750
	now := time.Unix(1000, 0)
	lin.now = func() time.Time { return now }

	// Audio delivers a couple of frames near PTS 0, then dies.
	lin.Linearize(0, StreamAudio)
	lin.Linearize(1920, StreamAudio)

	// Video runs for 60s of frames while the clock advances.
	var lastOut int64 = -1
	for pts := int64(0); pts < 60*90000; pts += videoFrameDur {
		now = now.Add(time.Second / 24)
		out := lin.Linearize(pts, StreamVideo)
		if lastOut >= 0 {
			require.Greater(t, out, lastOut)
		}
		lastOut = out
	}

	// Video loops back to ~0 — numerically close to the dead audio stream's
	// last raw PTS, but audio is long stale and must not be trusted.
	now = now.Add(time.Second / 24)
	out := lin.Linearize(0, StreamVideo)
	require.Greater(t, out, lastOut,
		"video output must stay monotonic across a loop even when the other stream is stale")
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
