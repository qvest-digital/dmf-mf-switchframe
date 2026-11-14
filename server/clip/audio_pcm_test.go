//go:build cgo

package clip

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/aacfixture"
)

// readAll reads a stream to its end.
func readAll(s *PCMStream) []float32 {
	var out []float32
	for {
		pcm := s.Read(1000)
		if pcm == nil {
			return out
		}
		out = append(out, pcm...)
	}
}

// fixtureAudio writes c and returns its demuxed audio and its sound.
func fixtureAudio(t *testing.T, c aacfixture.Clip) ([]BufferedAudioFrame, []float32) {
	t.Helper()
	path, ref := aacfixture.Write(t, t.TempDir(), c)
	_, audio, err := DemuxFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, audio)
	return audio, ref
}

// An AAC stream starts with the encoder's priming, 1024 samples from the
// ffmpeg encoder. Its container says how long the priming is: an MP4 edit
// list or an iTunSMPB tag, and without either it is one frame. Sample 0
// of the stream is the sound's first, with every channel count the
// engine takes; played from the stream's first frame, the clip starts
// with the priming and the sound is late by it. iTunSMPB also says where
// the sound ends, and the stream ends there; ffmpeg's edit list runs to
// the end of the last frame.
func TestPCMStreamStartsAtTheSoundsFirstSample(t *testing.T) {
	cases := []struct {
		name  string
		clip  aacfixture.Clip
		exact bool // the container gives the sound's end
	}{
		{"ts", aacfixture.Clip{Ext: ".ts"}, false},
		{"mp4 edit list", aacfixture.Clip{Ext: ".mp4"}, false},
		{"mp4 no edit list", aacfixture.Clip{Ext: ".mp4", NoEditList: true}, false},
		// A priming of 2112 samples, as Apple's encoder writes: the
		// default of 1024 would play 1088 samples of it.
		{"mp4 iTunSMPB", aacfixture.Clip{Ext: ".mp4", Lead: 1088, ITunSMPB: true}, true},
	}
	for _, tc := range cases {
		for _, channels := range []int{1, 2, 6, 8} {
			if channels == 8 && tc.clip.Ext == ".ts" {
				// ffmpeg's 7.1 is channel configuration 12, which
				// ADTS cannot signal.
				continue
			}
			t.Run(fmt.Sprintf("%s/%dch", tc.name, channels), func(t *testing.T) {
				c := tc.clip
				c.Channels = channels
				audio, ref := fixtureAudio(t, c)
				s, err := NewPCMStream(audio, 48000, testPCMCodec)
				require.NoError(t, err)
				defer s.Close()
				require.Equal(t, channels, s.Channels())

				got := readAll(s)
				refLen := len(ref) / channels
				if tc.exact {
					assert.Equal(t, refLen, len(got)/channels, "samples presented")
				} else {
					// The padding stays: the stream ends on a frame.
					assert.GreaterOrEqual(t, len(got)/channels, refLen)
					assert.Less(t, len(got)/channels, refLen+2048)
				}
				// The centre is the third channel in ffmpeg's order and
				// in the decoder's; the 7.1 fixture's other channels are
				// in another order (see Mixer.downmixAAC).
				ch := 0
				if channels >= 6 {
					ch = 2
				}
				lag := aacfixture.Lag(got, ref, channels, ch, 3000, 20000)
				assert.Zero(t, lag, "sample 0 is %d samples off the sound's first", lag)
				assert.Less(t, aacfixture.RMS(got, ref, channels, ch, 0), 0.02)
			})
		}
	}
}

// A clip at 44.1 kHz is resampled to the mixer's 48 kHz, and the filter's
// delay is taken off: the sound starts on sample 0 within a sample.
func TestPCMStreamResampledStartsAtTheSoundsFirstSample(t *testing.T) {
	audio, _ := fixtureAudio(t, aacfixture.Clip{Ext: ".mp4", Channels: 2, Rate: 44100})
	_, ref := aacfixture.Write(t, t.TempDir(), aacfixture.Clip{Ext: ".mp4", Channels: 2, Rate: 48000})
	s, err := NewPCMStream(audio, 48000, testPCMCodec)
	require.NoError(t, err)
	defer s.Close()
	got := readAll(s)
	// The edit list runs to the end of the last frame at 44.1 kHz.
	assert.GreaterOrEqual(t, len(got)/2, len(ref)/2)
	assert.Less(t, len(got)/2, len(ref)/2+1024*48000/44100+2)
	lag := aacfixture.Lag(got, ref, 2, 0, 3000, 20000)
	assert.LessOrEqual(t, lag*lag, 1, "sample 0 is %d samples off the sound's first", lag)
}

// A loop plays pass after pass of the sound without a gap, an overlap or
// the priming between them: the stream is the sound, cut or filled with
// silence to the pass's length, over and over.
func TestPCMStreamLoopIsGapless(t *testing.T) {
	for _, tc := range []struct {
		name string
		clip aacfixture.Clip
		rate int
	}{
		{"mp4", aacfixture.Clip{Ext: ".mp4", Channels: 2}, 48000},
		{"ts", aacfixture.Clip{Ext: ".ts", Channels: 2}, 48000},
		{"mp4 44.1 kHz", aacfixture.Clip{Ext: ".mp4", Channels: 2, Rate: 44100}, 44100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audio, ref := fixtureAudio(t, tc.clip)
			s, err := NewPCMStream(audio, 48000, testPCMCodec)
			require.NoError(t, err)
			defer s.Close()
			// The pass of the pictures: the sound's length.
			pass := int64(len(ref) / 2)
			const passes = 4
			left := passes - 1
			s.Start(0, pass, func() (int64, bool) {
				if left == 0 {
					return 0, false
				}
				left--
				return pass, true
			})
			got := readAll(s)
			out := int(pass) * 48000 / tc.rate
			require.InDelta(t, passes*out, len(got)/2, 1)
			if tc.rate != 48000 {
				_, ref = aacfixture.Write(t, t.TempDir(), aacfixture.Clip{Ext: ".mp4", Channels: 2})
			}
			for p := 0; p < passes; p++ {
				seg := got[2*p*out : 2*min((p+1)*out, len(got)/2)]
				lag := aacfixture.Lag(seg, ref, 2, 0, 1500, 15000)
				assert.LessOrEqual(t, lag*lag, 1, "pass %d is %d samples off", p, lag)
				// Its end is the sound's end, up to the seam.
				tail := got[2*((p+1)*out-4000) : 2*((p+1)*out)]
				refTail := ref[len(ref)-2*4000:]
				assert.Less(t, aacfixture.RMS(tail, refTail, 2, 0, 0), 0.03, "pass %d ends short of the sound", p)
			}
		})
	}
}

// Seeking starts the stream at the sample asked for, and a pass that
// started there ends where the pass does.
func TestPCMStreamStartsAtAPosition(t *testing.T) {
	audio, ref := fixtureAudio(t, aacfixture.Clip{Ext: ".mp4", Channels: 2})
	s, err := NewPCMStream(audio, 48000, testPCMCodec)
	require.NoError(t, err)
	defer s.Close()
	const at = 12345
	s.Start(at, int64(len(ref)/2), nil)
	got := readAll(s)
	assert.Equal(t, len(ref)/2-at, len(got)/2)
	assert.Zero(t, aacfixture.Lag(got, ref[2*at:], 2, 0, 1500, 15000))
}
