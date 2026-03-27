package testutil

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFFprobeResultParsing(t *testing.T) {
	// Test that our types correctly parse ffprobe JSON output.
	// ffprobe outputs numbers as strings, and key_frame as int 0/1.
	sampleJSON := `{
		"streams": [
			{
				"codec_name": "h264",
				"codec_type": "video",
				"width": 1920,
				"height": 1080,
				"r_frame_rate": "30/1",
				"nb_frames": "300",
				"color_primaries": "bt709",
				"color_transfer": "bt709",
				"color_space": "bt709",
				"color_range": "tv",
				"sample_rate": "",
				"channels": 0
			},
			{
				"codec_name": "aac",
				"codec_type": "audio",
				"width": 0,
				"height": 0,
				"r_frame_rate": "0/0",
				"nb_frames": "500",
				"sample_rate": "48000",
				"channels": 2
			}
		],
		"format": {
			"filename": "test.ts",
			"format_name": "mpegts",
			"duration": "10.000000",
			"size": "1234567",
			"bit_rate": "987654"
		}
	}`

	var result FFprobeResult
	err := json.Unmarshal([]byte(sampleJSON), &result)
	require.NoError(t, err)

	require.Len(t, result.Streams, 2)

	// Video stream
	vs := result.Streams[0]
	require.Equal(t, "h264", vs.CodecName)
	require.Equal(t, "video", vs.CodecType)
	require.Equal(t, 1920, vs.Width)
	require.Equal(t, 1080, vs.Height)
	require.Equal(t, "30/1", vs.RFrameRate)
	require.Equal(t, "300", vs.NbFrames)
	require.Equal(t, "bt709", vs.ColorPrimaries)
	require.Equal(t, "bt709", vs.ColorTransfer)
	require.Equal(t, "bt709", vs.ColorSpace)
	require.Equal(t, "tv", vs.ColorRange)

	// Audio stream
	as := result.Streams[1]
	require.Equal(t, "aac", as.CodecName)
	require.Equal(t, "audio", as.CodecType)
	require.Equal(t, "48000", as.SampleRate)
	require.Equal(t, 2, as.Channels)

	// Format
	require.Equal(t, "test.ts", result.Format.Filename)
	require.Equal(t, "mpegts", result.Format.FormatName)
	require.Equal(t, "10.000000", result.Format.Duration)
}

func TestFrameInfoParsing(t *testing.T) {
	// ffprobe frame output: pts is a string, key_frame is int 0/1
	sampleJSON := `{
		"frames": [
			{
				"media_type": "video",
				"stream_index": 0,
				"key_frame": 1,
				"pts": "90000",
				"dts": "90000",
				"pict_type": "I",
				"pkt_size": "50000"
			},
			{
				"media_type": "video",
				"stream_index": 0,
				"key_frame": 0,
				"pts": "93000",
				"dts": "93000",
				"pict_type": "P",
				"pkt_size": "5000"
			},
			{
				"media_type": "audio",
				"stream_index": 1,
				"key_frame": 1,
				"pts": "90000",
				"dts": "90000",
				"pict_type": "",
				"pkt_size": "1024"
			}
		]
	}`

	var wrapper struct {
		Frames []FrameInfo `json:"frames"`
	}
	err := json.Unmarshal([]byte(sampleJSON), &wrapper)
	require.NoError(t, err)
	require.Len(t, wrapper.Frames, 3)

	// Video I-frame
	f0 := wrapper.Frames[0]
	require.Equal(t, "video", f0.MediaType)
	require.Equal(t, 0, f0.StreamIndex)
	require.True(t, f0.KeyFrame)
	require.Equal(t, int64(90000), f0.PTS)
	require.Equal(t, int64(90000), f0.DTS)
	require.Equal(t, "I", f0.PictType)
	require.Equal(t, 50000, f0.PktSize)

	// Video P-frame
	f1 := wrapper.Frames[1]
	require.False(t, f1.KeyFrame)
	require.Equal(t, int64(93000), f1.PTS)
	require.Equal(t, "P", f1.PictType)

	// Audio frame
	f2 := wrapper.Frames[2]
	require.Equal(t, "audio", f2.MediaType)
	require.Equal(t, 1, f2.StreamIndex)
}

func TestFrameInfoParsing_MissingPTS(t *testing.T) {
	// Sometimes PTS can be "N/A" or missing
	sampleJSON := `{
		"frames": [
			{
				"media_type": "video",
				"stream_index": 0,
				"key_frame": 0,
				"pts": "N/A",
				"dts": "N/A",
				"pict_type": "P",
				"pkt_size": "1000"
			}
		]
	}`

	var wrapper struct {
		Frames []FrameInfo `json:"frames"`
	}
	err := json.Unmarshal([]byte(sampleJSON), &wrapper)
	require.NoError(t, err)
	require.Len(t, wrapper.Frames, 1)

	// N/A should parse to 0 (or some sentinel)
	f := wrapper.Frames[0]
	require.Equal(t, int64(0), f.PTS)
	require.Equal(t, int64(0), f.DTS)
}

func TestVideoStream_Helper(t *testing.T) {
	result := &FFprobeResult{
		Streams: []StreamInfo{
			{CodecName: "aac", CodecType: "audio"},
			{CodecName: "h264", CodecType: "video", Width: 1920, Height: 1080},
		},
	}

	vs := result.VideoStream()
	require.NotNil(t, vs)
	require.Equal(t, "h264", vs.CodecName)
	require.Equal(t, 1920, vs.Width)
}

func TestAudioStream_Helper(t *testing.T) {
	result := &FFprobeResult{
		Streams: []StreamInfo{
			{CodecName: "h264", CodecType: "video"},
			{CodecName: "aac", CodecType: "audio", SampleRate: "48000", Channels: 2},
		},
	}

	as := result.AudioStream()
	require.NotNil(t, as)
	require.Equal(t, "aac", as.CodecName)
	require.Equal(t, "48000", as.SampleRate)
}

func TestNoVideoStream(t *testing.T) {
	result := &FFprobeResult{
		Streams: []StreamInfo{
			{CodecName: "aac", CodecType: "audio"},
		},
	}

	vs := result.VideoStream()
	require.Nil(t, vs)
}
