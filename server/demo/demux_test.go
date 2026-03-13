package demo

import (
	"os"
	"path/filepath"
	"testing"

	astits "github.com/asticode/go-astits"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/codec"
)

func TestParseAudioFrames_UsesRealSampleRate(t *testing.T) {
	t.Parallel()

	// Build a synthetic PES packet with 44100Hz stereo ADTS audio.
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01}
	adts := append(codec.BuildADTS(44100, 2, len(payload)), payload...)

	d := &astits.DemuxerData{
		PES: &astits.PESData{
			Data: adts,
			Header: &astits.PESHeader{
				OptionalHeader: &astits.PESOptionalHeader{
					PTS: &astits.ClockReference{Base: 90000},
				},
			},
		},
	}

	frames := parseAudioFrames(d, d.PES.Header.OptionalHeader)
	require.Len(t, frames, 1)
	require.Equal(t, 44100, frames[0].SampleRate)
	require.Equal(t, 2, frames[0].Channels)
	require.Equal(t, int64(90000), frames[0].PTS)
}

func TestParseAudioFrames_MultipleFrames_44100Hz(t *testing.T) {
	t.Parallel()

	// Build two concatenated 44100Hz ADTS frames.
	p1 := []byte{0x01, 0x02, 0x03}
	p2 := []byte{0x04, 0x05, 0x06}
	adts1 := append(codec.BuildADTS(44100, 2, len(p1)), p1...)
	adts2 := append(codec.BuildADTS(44100, 2, len(p2)), p2...)
	data := append(adts1, adts2...)

	d := &astits.DemuxerData{
		PES: &astits.PESData{
			Data: data,
			Header: &astits.PESHeader{
				OptionalHeader: &astits.PESOptionalHeader{
					PTS: &astits.ClockReference{Base: 0},
				},
			},
		},
	}

	frames := parseAudioFrames(d, d.PES.Header.OptionalHeader)
	require.Len(t, frames, 2)

	// For 44100Hz: 1024 * 90000 / 44100 = 2089 ticks (integer division).
	expectedDelta := int64(1024 * 90000 / 44100)
	require.Equal(t, int64(0), frames[0].PTS)
	require.Equal(t, expectedDelta, frames[1].PTS)
	require.Equal(t, 44100, frames[0].SampleRate)
	require.Equal(t, 44100, frames[1].SampleRate)
}

func TestParseAudioFrames_48kHz(t *testing.T) {
	t.Parallel()

	// Demux a clip known to have 48kHz audio.
	clipsDir := filepath.Join("..", "..", "test", "clips")
	path := filepath.Join(clipsDir, "tears_of_steel.ts")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("test clip not available:", path)
	}

	result, err := demuxTSFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, result.Audio, "expected audio frames")

	for i, af := range result.Audio {
		require.Equal(t, 48000, af.SampleRate, "frame %d sample rate", i)
		require.Equal(t, 2, af.Channels, "frame %d channels", i)
	}
}

func TestParseAudioFrames_FallbackOnNonADTS(t *testing.T) {
	t.Parallel()

	// Non-ADTS data should fall back to 48000/2.
	d := &astits.DemuxerData{
		PES: &astits.PESData{
			Data: []byte{0x00, 0x01, 0x02, 0x03},
			Header: &astits.PESHeader{
				OptionalHeader: &astits.PESOptionalHeader{
					PTS: &astits.ClockReference{Base: 0},
				},
			},
		},
	}

	frames := parseAudioFrames(d, d.PES.Header.OptionalHeader)
	require.Len(t, frames, 1)
	require.Equal(t, 48000, frames[0].SampleRate)
	require.Equal(t, 2, frames[0].Channels)
}

func TestDemuxTS_BBB_ADTSParsed(t *testing.T) {
	t.Parallel()

	// bbb.ts has 48kHz audio. This test verifies the ADTS parser extracts
	// the actual sample rate (not hardcoded) and PTS spacing is correct.
	clipsDir := filepath.Join("..", "..", "test", "clips")
	path := filepath.Join(clipsDir, "bbb.ts")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("test clip not available:", path)
	}

	result, err := demuxTSFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, result.Audio, "expected audio frames")

	for i, af := range result.Audio {
		require.Equal(t, 48000, af.SampleRate, "frame %d: expected 48000Hz, got %d", i, af.SampleRate)
		require.Equal(t, 2, af.Channels, "frame %d channels", i)
	}

	// Verify PTS spacing reflects 48kHz (1024*90000/48000 = 1920 ticks per AAC frame).
	if len(result.Audio) >= 2 {
		delta := result.Audio[1].PTS - result.Audio[0].PTS
		require.Greater(t, delta, int64(1800), "PTS delta too small")
		require.Less(t, delta, int64(100000), "PTS delta unreasonably large")
	}
}

func TestDemuxTS_VideoFrames(t *testing.T) {
	t.Parallel()

	clipsDir := filepath.Join("..", "..", "test", "clips")
	path := filepath.Join(clipsDir, "bbb.ts")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("test clip not available:", path)
	}

	result, err := demuxTSFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, result.Video, "expected video frames")

	// At least one keyframe must exist.
	hasKeyframe := false
	for _, vf := range result.Video {
		if vf.IsKeyframe {
			hasKeyframe = true
			break
		}
	}
	require.True(t, hasKeyframe, "expected at least one keyframe")
}

func TestDemuxTS_AllClips(t *testing.T) {
	t.Parallel()

	clipsDir := filepath.Join("..", "..", "test", "clips")
	for _, filename := range clipFiles {
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(clipsDir, filename)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Skip("test clip not available:", path)
			}

			result, err := demuxTSFile(path)
			require.NoError(t, err)
			require.NotEmpty(t, result.Video, "expected video frames")
			require.NotEmpty(t, result.Audio, "expected audio frames")

			// Verify consistent sample rate across all audio frames.
			sr := result.Audio[0].SampleRate
			for i, af := range result.Audio {
				require.Equal(t, sr, af.SampleRate, "frame %d: inconsistent sample rate", i)
			}

			// Verify audio PTS is non-decreasing.
			var lastPTS int64
			for i, af := range result.Audio {
				if i > 0 {
					require.GreaterOrEqual(t, af.PTS, lastPTS,
						"frame %d: audio PTS decreased (%d < %d)", i, af.PTS, lastPTS)
				}
				lastPTS = af.PTS
			}
		})
	}
}

