// Package testutil provides shared test utilities for correctness tests.
// These utilities include ffprobe wrappers, test signal generators,
// spec-aware comparison helpers, YUV frame analysis, and TS packet parsing.
package testutil

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// FFprobeResult is the top-level container for ffprobe JSON output.
type FFprobeResult struct {
	Streams []StreamInfo `json:"streams"`
	Format  FormatInfo   `json:"format"`
}

// VideoStream returns the first video stream, or nil if none found.
func (r *FFprobeResult) VideoStream() *StreamInfo {
	for i := range r.Streams {
		if r.Streams[i].CodecType == "video" {
			return &r.Streams[i]
		}
	}
	return nil
}

// AudioStream returns the first audio stream, or nil if none found.
func (r *FFprobeResult) AudioStream() *StreamInfo {
	for i := range r.Streams {
		if r.Streams[i].CodecType == "audio" {
			return &r.Streams[i]
		}
	}
	return nil
}

// StreamInfo represents a single stream from ffprobe output.
type StreamInfo struct {
	CodecName      string `json:"codec_name"`
	CodecType      string `json:"codec_type"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	ColorPrimaries string `json:"color_primaries"`
	ColorTransfer  string `json:"color_transfer"`
	ColorSpace     string `json:"color_space"`
	ColorRange     string `json:"color_range"`
	RFrameRate     string `json:"r_frame_rate"`
	NbFrames       string `json:"nb_frames"`
	SampleRate     string `json:"sample_rate"`
	Channels       int    `json:"channels"`
}

// FormatInfo represents the format section from ffprobe output.
type FormatInfo struct {
	Filename   string `json:"filename"`
	FormatName string `json:"format_name"`
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	BitRate    string `json:"bit_rate"`
}

// FrameInfo represents a single frame from ffprobe -show_frames output.
// ffprobe outputs pts/dts as strings and key_frame as int 0/1.
type FrameInfo struct {
	MediaType   string `json:"media_type"`
	StreamIndex int    `json:"stream_index"`
	KeyFrame    bool
	PTS         int64
	DTS         int64
	PictType    string `json:"pict_type"`
	PktSize     int
}

// frameInfoRaw is the raw JSON representation from ffprobe.
type frameInfoRaw struct {
	MediaType   string `json:"media_type"`
	StreamIndex int    `json:"stream_index"`
	KeyFrame    int    `json:"key_frame"`
	PTS         string `json:"pts"`
	DTS         string `json:"dts"`
	PictType    string `json:"pict_type"`
	PktSize     string `json:"pkt_size"`
}

// UnmarshalJSON implements custom JSON unmarshaling to handle ffprobe's
// string-encoded numbers and int-encoded booleans.
func (f *FrameInfo) UnmarshalJSON(data []byte) error {
	var raw frameInfoRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	f.MediaType = raw.MediaType
	f.StreamIndex = raw.StreamIndex
	f.KeyFrame = raw.KeyFrame != 0
	f.PictType = raw.PictType

	// Parse PTS - may be "N/A" or a numeric string
	if raw.PTS != "" && raw.PTS != "N/A" {
		pts, err := strconv.ParseInt(raw.PTS, 10, 64)
		if err != nil {
			return fmt.Errorf("parsing pts %q: %w", raw.PTS, err)
		}
		f.PTS = pts
	}

	// Parse DTS
	if raw.DTS != "" && raw.DTS != "N/A" {
		dts, err := strconv.ParseInt(raw.DTS, 10, 64)
		if err != nil {
			return fmt.Errorf("parsing dts %q: %w", raw.DTS, err)
		}
		f.DTS = dts
	}

	// Parse PktSize
	if raw.PktSize != "" && raw.PktSize != "N/A" {
		size, err := strconv.Atoi(raw.PktSize)
		if err != nil {
			return fmt.Errorf("parsing pkt_size %q: %w", raw.PktSize, err)
		}
		f.PktSize = size
	}

	return nil
}

// ProbeFile runs ffprobe on the given file and returns structured results.
func ProbeFile(t *testing.T, path string) *FFprobeResult {
	t.Helper()

	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)

	out, err := cmd.Output()
	require.NoError(t, err, "ffprobe failed on %s", path)

	var result FFprobeResult
	require.NoError(t, json.Unmarshal(out, &result), "failed to parse ffprobe output")

	return &result
}

// ProbeFrames runs ffprobe -show_frames and returns all frames.
func ProbeFrames(t *testing.T, path string) []FrameInfo {
	t.Helper()

	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_frames",
		path,
	)

	out, err := cmd.Output()
	require.NoError(t, err, "ffprobe -show_frames failed on %s", path)

	var wrapper struct {
		Frames []FrameInfo `json:"frames"`
	}
	require.NoError(t, json.Unmarshal(out, &wrapper), "failed to parse ffprobe frames output")

	return wrapper.Frames
}

// ProbeVideoFrames returns only video frames from ffprobe output.
func ProbeVideoFrames(t *testing.T, path string) []FrameInfo {
	t.Helper()

	frames := ProbeFrames(t, path)
	var video []FrameInfo
	for _, f := range frames {
		if f.MediaType == "video" {
			video = append(video, f)
		}
	}
	return video
}

// ProbeAudioFrames returns only audio frames from ffprobe output.
func ProbeAudioFrames(t *testing.T, path string) []FrameInfo {
	t.Helper()

	frames := ProbeFrames(t, path)
	var audio []FrameInfo
	for _, f := range frames {
		if f.MediaType == "audio" {
			audio = append(audio, f)
		}
	}
	return audio
}

// RequireNoErrors runs ffprobe on the file and asserts it exits successfully.
func RequireNoErrors(t *testing.T, path string) {
	t.Helper()

	cmd := exec.Command("ffprobe",
		"-v", "error",
		path,
	)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffprobe reported errors on %s: %s", path, string(out))
}
