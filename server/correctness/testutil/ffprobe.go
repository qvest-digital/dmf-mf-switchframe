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
// ffprobe outputs pts/dts as strings in older versions and integers in
// newer versions (8.x+). key_frame is always int 0/1.
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
// Uses json.RawMessage for fields that may be either strings or numbers
// depending on ffprobe version. Also handles the dts/pkt_dts field name
// variation between ffprobe versions.
type frameInfoRaw struct {
	MediaType   string          `json:"media_type"`
	StreamIndex int             `json:"stream_index"`
	KeyFrame    int             `json:"key_frame"`
	PTS         json.RawMessage `json:"pts"`
	DTS         json.RawMessage `json:"dts"`
	PktDTS      json.RawMessage `json:"pkt_dts"`
	PictType    string          `json:"pict_type"`
	PktSize     json.RawMessage `json:"pkt_size"`
}

// parseFlexibleInt64 parses a JSON value that may be a string, number, or "N/A".
// Returns 0, false for N/A or empty values.
func parseFlexibleInt64(raw json.RawMessage) (int64, bool, error) {
	if len(raw) == 0 {
		return 0, false, nil
	}

	// Try as number first (ffprobe 8.x).
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true, nil
	}

	// Try as string (older ffprobe).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" || s == "N/A" {
			return 0, false, nil
		}
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("parsing %q: %w", s, err)
		}
		return v, true, nil
	}

	return 0, false, fmt.Errorf("cannot parse %s as int64", string(raw))
}

// parseFlexibleInt parses a JSON value that may be a string or number.
func parseFlexibleInt(raw json.RawMessage) (int, bool, error) {
	v, ok, err := parseFlexibleInt64(raw)
	return int(v), ok, err
}

// UnmarshalJSON implements custom JSON unmarshaling to handle ffprobe's
// version-dependent output format. Older versions use string-encoded numbers;
// newer versions (8.x+) use native JSON numbers. The DTS field may be
// "dts" (older) or "pkt_dts" (newer).
func (f *FrameInfo) UnmarshalJSON(data []byte) error {
	var raw frameInfoRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	f.MediaType = raw.MediaType
	f.StreamIndex = raw.StreamIndex
	f.KeyFrame = raw.KeyFrame != 0
	f.PictType = raw.PictType

	// Parse PTS
	if pts, ok, err := parseFlexibleInt64(raw.PTS); err != nil {
		return fmt.Errorf("parsing pts: %w", err)
	} else if ok {
		f.PTS = pts
	}

	// Parse DTS — try "dts" first, fall back to "pkt_dts" (ffprobe 8.x).
	dtsRaw := raw.DTS
	if len(dtsRaw) == 0 {
		dtsRaw = raw.PktDTS
	}
	if dts, ok, err := parseFlexibleInt64(dtsRaw); err != nil {
		return fmt.Errorf("parsing dts: %w", err)
	} else if ok {
		f.DTS = dts
	}

	// Parse PktSize
	if size, ok, err := parseFlexibleInt(raw.PktSize); err != nil {
		return fmt.Errorf("parsing pkt_size: %w", err)
	} else if ok {
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
