package clip

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireFFmpeg checks that ffmpeg is available and skips the test if not.
func requireFFmpeg(t *testing.T) string {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available, skipping MP4 test")
	}
	return ffmpegPath
}

// generateTestMP4 uses ffmpeg to generate a valid MP4 file with H.264 video
// and AAC audio. numFrames determines the approximate number of video frames
// (via duration calculation at 30fps).
func generateTestMP4(t *testing.T, numFrames int) string {
	t.Helper()
	ffmpegPath := requireFFmpeg(t)

	duration := float64(numFrames) / 30.0
	if duration < 0.1 {
		duration = 0.1
	}

	mp4File := filepath.Join(t.TempDir(), "test.mp4")

	cmd := exec.Command(ffmpegPath,
		"-y",
		"-f", "lavfi", "-i", "color=c=red:size=320x240:rate=30:d="+formatFloat(duration),
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:d="+formatFloat(duration),
		"-c:v", "libx264", "-profile:v", "baseline", "-level", "3.0",
		"-pix_fmt", "yuv420p", "-g", "30", "-bf", "0",
		"-c:a", "aac", "-b:a", "128k",
		mp4File,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg MP4 generation failed: %v", err)
	}

	return mp4File
}

// generateTestMP4VideoOnly uses ffmpeg to generate a valid MP4 file with
// H.264 video only (no audio track).
func generateTestMP4VideoOnly(t *testing.T, numFrames int) string {
	t.Helper()
	ffmpegPath := requireFFmpeg(t)

	duration := float64(numFrames) / 30.0
	if duration < 0.1 {
		duration = 0.1
	}

	mp4File := filepath.Join(t.TempDir(), "test_video_only.mp4")

	cmd := exec.Command(ffmpegPath,
		"-y",
		"-f", "lavfi", "-i", "color=c=blue:size=320x240:rate=30:d="+formatFloat(duration),
		"-c:v", "libx264", "-profile:v", "baseline", "-level", "3.0",
		"-pix_fmt", "yuv420p", "-g", "30", "-bf", "0",
		"-an",
		mp4File,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg MP4 generation failed: %v", err)
	}

	return mp4File
}

// formatFloat formats a float64 to a string with 2 decimal places.
func formatFloat(f float64) string {
	return fmt.Sprintf("%.2f", f)
}

func TestDemuxMP4(t *testing.T) {
	mp4File := generateTestMP4(t, 30)

	frames, audioFrames, err := DemuxFile(mp4File)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("expected video frames from MP4")
	}
	if !frames[0].isKeyframe {
		t.Error("first frame should be keyframe")
	}
	// Keyframe should have SPS/PPS extracted from avcC.
	assert.NotEmpty(t, frames[0].sps, "keyframe should have SPS")
	assert.NotEmpty(t, frames[0].pps, "keyframe should have PPS")

	// Wire data should be valid AVC1 format.
	require.True(t, len(frames[0].wireData) > 4, "wireData should contain NALU data")

	_ = audioFrames
}

func TestDemuxMP4NoAudio(t *testing.T) {
	mp4File := generateTestMP4VideoOnly(t, 10)

	frames, audioFrames, err := DemuxFile(mp4File)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("expected video frames")
	}
	if len(audioFrames) != 0 {
		t.Errorf("expected no audio, got %d frames", len(audioFrames))
	}
}

func TestDemuxMP4_PTSOrder(t *testing.T) {
	mp4File := generateTestMP4(t, 30)

	frames, audioFrames, err := DemuxFile(mp4File)
	require.NoError(t, err)
	require.True(t, len(frames) > 1, "need at least 2 frames to check order")

	for i := 1; i < len(frames); i++ {
		if frames[i].pts <= frames[i-1].pts {
			t.Errorf("Video PTS not monotonic: frame[%d].pts=%d <= frame[%d].pts=%d",
				i, frames[i].pts, i-1, frames[i-1].pts)
		}
	}

	if len(audioFrames) > 1 {
		for i := 1; i < len(audioFrames); i++ {
			if audioFrames[i].pts < audioFrames[i-1].pts {
				t.Errorf("Audio PTS not monotonic: frame[%d].pts=%d < frame[%d].pts=%d",
					i, audioFrames[i].pts, i-1, audioFrames[i-1].pts)
			}
		}
	}
}

func TestDemuxMP4_AudioFrames(t *testing.T) {
	mp4File := generateTestMP4(t, 10)

	_, audioFrames, err := DemuxFile(mp4File)
	require.NoError(t, err)
	require.NotEmpty(t, audioFrames, "expected audio frames")

	for i, af := range audioFrames {
		assert.Greater(t, af.sampleRate, 0, "frame %d: sampleRate should be > 0", i)
		assert.Greater(t, af.channels, 0, "frame %d: channels should be > 0", i)
		assert.NotEmpty(t, af.data, "frame %d: data should not be empty", i)
	}
}

func TestDemuxMP4_M4VExtension(t *testing.T) {
	mp4File := generateTestMP4(t, 5)

	// Rename to .m4v to test extension routing.
	m4vFile := filepath.Join(t.TempDir(), "test.m4v")
	data, err := os.ReadFile(mp4File)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(m4vFile, data, 0o644))

	frames, _, err := DemuxFile(m4vFile)
	require.NoError(t, err)
	require.NotEmpty(t, frames, "expected video frames from .m4v")
}

func TestDemuxMP4_MOVExtension(t *testing.T) {
	mp4File := generateTestMP4(t, 5)

	// Rename to .mov to test extension routing.
	movFile := filepath.Join(t.TempDir(), "test.mov")
	data, err := os.ReadFile(mp4File)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(movFile, data, 0o644))

	frames, _, err := DemuxFile(movFile)
	require.NoError(t, err)
	require.NotEmpty(t, frames, "expected video frames from .mov")
}

func TestDemuxMP4_InvalidFile(t *testing.T) {
	tmpFile := writeTemp(t, []byte("not an mp4 file"), ".mp4")
	_, _, err := DemuxFile(tmpFile)
	assert.Error(t, err, "expected error for invalid MP4 file")
}

func TestDemuxMP4_EmptyFile(t *testing.T) {
	tmpFile := writeTemp(t, []byte{}, ".mp4")
	_, _, err := DemuxFile(tmpFile)
	assert.Error(t, err, "expected error for empty MP4 file")
}
