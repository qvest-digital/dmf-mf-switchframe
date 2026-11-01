//go:build cgo

package main

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// A playout channel hands each clip frame's AAC frames over with it, on
// the channel's frame ticker, under PTS that run on without a gap: a 24 fps
// clip shown at 50 fps hands over a clip frame's 41.7 ms of audio when that
// frame is first shown, on every second or third tick. Placed on the
// timeline by those PTS, the channel's audio stays on the program read
// point, two periods behind the clock, over a long playout and through a
// ticker that jitters by up to 8 ms; each AAC frame placed at its arrival
// lands up to a period off the read point and the mixer keeps splicing the
// channel back.
func TestPlayoutAudioStaysOnTheReadPoint(t *testing.T) {
	const n = 960 // one 20 ms period at 48 kHz
	const period = int64(20_000_000)
	const ticks = 3 * 60 * 50 // three minutes
	var now atomic.Int64
	scriptTAI(t, &now)
	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000, Channels: 2, DecoderFactory: audioDecoderFactory(),
	})
	defer func() { _ = m.Close() }()
	key := "playout:timing"
	m.AddChannel(key)
	m.SetActive(key, true)
	a := &App{mixer: m}
	enc, err := audio.NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	rate := clock.Rate{Num: 48000, Den: 1}
	pcm := make([]float32, 1024*2)
	for j := range pcm {
		pcm[j] = 0.1
	}
	start := now.Load()
	audioPTS := int64(1_000_000)
	samples := int64(0) // channel audio handed over so far
	const warmup = 50
	var placed int64
	for i := 1; i <= ticks; i++ {
		// The channel's ticker fires for tick i, up to 8 ms late, and hands
		// over the AAC frames that start within the clip frame it shows.
		jitter := int64((i*37)%9) * 1_000_000
		now.Store(start + int64(i)*period + jitter)
		clipFrame := int64(i) * 24 / 50
		for samples < (clipFrame+1)*48000/24 {
			aac, err := enc.Encode(pcm)
			require.NoError(t, err)
			samples += 1024
			if len(aac) == 0 {
				continue
			}
			a.ingestPlayoutAudio(key, &media.AudioFrame{Data: aac, PTS: audioPTS, SampleRate: 48000, Channels: 2})
			audioPTS += 1920
		}
		now.Store(start + int64(i)*period)
		if i == warmup {
			placed = m.ChannelRealigns()[key]
		}
		m.ProduceSamples(clock.TimestampToIndex(rate, now.Load())-2*n, n, 0)
	}

	require.Zero(t, m.ChannelRealigns()[key]-placed,
		"playout audio with continuous PTS must not be spliced back onto the read point")
}
