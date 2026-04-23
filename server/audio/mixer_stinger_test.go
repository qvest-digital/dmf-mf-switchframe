package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestMixer_SetStingerAudio_MonoUpmixesToStereo(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	const amp = 0.5
	monoBuf := make([]float32, 48000)
	for i := range monoBuf {
		monoBuf[i] = amp * float32(math.Sin(2*math.Pi*1000*float64(i)/48000))
	}

	m.SetStingerAudio(monoBuf, 48000, 1)

	m.mu.RLock()
	stinger := append([]float32(nil), m.stingerAudio...)
	chs := m.stingerChannels
	m.mu.RUnlock()

	require.Len(t, stinger, 2*len(monoBuf),
		"mono should upmix to stereo: interleaved length doubles")
	require.Equal(t, 2, chs, "stingerChannels should match mixer after upmix")
	require.Equal(t, monoBuf[0], stinger[0], "L sample 0 equals mono sample 0")
	require.Equal(t, monoBuf[0], stinger[1], "R sample 0 equals mono sample 0 (L==R)")
	for i := 0; i < len(monoBuf); i++ {
		require.Equal(t, monoBuf[i], stinger[2*i], "L[%d]", i)
		require.Equal(t, monoBuf[i], stinger[2*i+1], "R[%d]", i)
	}
}

func TestMixer_SetStingerAudio_SurroundDownmixesToStereo(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	const amp = float32(0.8)
	const frames = 100
	src := make([]float32, frames*6)
	for i := 0; i < frames; i++ {
		// Layout: L, R, C, LFE, Ls, Rs — center-only tone.
		src[i*6+2] = amp
	}

	m.SetStingerAudio(src, 48000, 6)

	m.mu.RLock()
	stinger := append([]float32(nil), m.stingerAudio...)
	chs := m.stingerChannels
	m.mu.RUnlock()

	require.Len(t, stinger, 2*frames, "6ch -> 2ch: interleaved length scales by 2/6")
	require.Equal(t, 2, chs, "stingerChannels should match mixer after downmix")

	const k = 0.7071067811865475
	expected := float32(amp * k)
	for i := 0; i < frames; i++ {
		require.InDelta(t, expected, stinger[i*2+0], 1e-5, "L frame %d", i)
		require.InDelta(t, expected, stinger[i*2+1], 1e-5, "R frame %d", i)
	}
}

func TestMixer_SetStingerAudio_DropsOnInvalidChannels(t *testing.T) {
	cases := []struct {
		name     string
		channels int
	}{
		{"zero", 0},
		{"seven", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMixer(MixerConfig{
				SampleRate: 48000,
				Channels:   2,
				Output:     func(frame *media.AudioFrame) {},
			})
			defer func() { _ = m.Close() }()

			audio := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
			m.SetStingerAudio(audio, 48000, tc.channels)

			m.mu.RLock()
			stinger := m.stingerAudio
			m.mu.RUnlock()
			require.Nil(t, stinger, "channels=%d must be rejected (out of [1,6])", tc.channels)
		})
	}
}
