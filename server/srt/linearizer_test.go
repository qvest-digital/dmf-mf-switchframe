package srt

import (
	"testing"

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

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
