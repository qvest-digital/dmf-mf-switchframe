package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// delayEncoder is an encoder whose frames lag their PCM by delay samples.
type delayEncoder struct {
	mockEncoder
	delay int
}

func (e *delayEncoder) Delay() int { return e.delay }

// The frame a tick encodes carries the mix of the encoder's delay before
// it, so its PTS is the tick's PTS less that delay. Stamped with the tick's,
// every output played the program audio late by it: 2048 samples, 42.7 ms,
// with FDK-AAC LC.
func TestTick_StampsFrameWithEncoderDelay(t *testing.T) {
	t.Parallel()

	newMixer := func(enc Encoder) *Mixer {
		m := NewMixer(MixerConfig{
			SampleRate:     48000,
			Channels:       2,
			Output:         func(*media.AudioFrame) {},
			DecoderFactory: func(int, int) (Decoder, error) { return &mockDecoder{}, nil },
			EncoderFactory: func(int, int) (Encoder, error) { return enc, nil },
			ClockDriven:    true,
		})
		t.Cleanup(func() { _ = m.Close() })
		m.AddChannel("cam1")
		m.SetActive("cam1", true)
		m.mu.Lock()
		m.outputPTS = 900000 // well past the delay, as on a seeded timeline
		m.mu.Unlock()
		return m
	}
	plain := newMixer(&mockEncoder{})
	delayed := newMixer(&delayEncoder{delay: 2048})

	for i := 0; i < 5; i++ {
		p, d := plain.tick(), delayed.tick()
		require.NotNil(t, p)
		require.NotNil(t, d)
		require.Equal(t, p.PTS-2048*90000/48000, d.PTS, "tick %d", i)
	}
}
