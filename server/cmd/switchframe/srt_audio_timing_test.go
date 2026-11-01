package main

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// scriptTAI makes clock.TAINow return now for one test. Goroutines that
// other tests of the package leave running read it too.
func scriptTAI(t *testing.T, now *atomic.Int64) {
	t.Helper()
	clock.Epoch()
	now.Store(clock.TAINow())
	clock.SetTAISource(now.Load)
	t.Cleanup(func() { clock.SetTAISource(nil) })
}

// An MPEG-TS muxer packs a stream's AAC frames into PES packets of several
// frames, so an SRT stream's audio reaches the decoder in bursts, here 180
// ms of audio every 180 ms, while its own PTS run on without a gap. Placed
// by those PTS no earlier than they arrive, the audio is on hand at every
// read of the program, two periods behind the clock; each burst placed at
// its arrival ends the moment it arrives, the read point runs out of it
// before the next burst, and the mixer splices the source on every burst.
func TestSRTAudioBurstsStayOnTheReadPoint(t *testing.T) {
	const n = 960 // one 20 ms period at 48 kHz
	const period = int64(20_000_000)
	const perBurst, bursts = 9, 40
	var now atomic.Int64
	scriptTAI(t, &now)
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	key := "srt:bursts"
	m.AddChannel(key)
	m.SetActive(key, true)
	a := &App{mixer: m}
	var anchor clock.TrackingAnchor

	rate := clock.Rate{Num: 48000, Den: 1}
	start := now.Load()
	srcPTS := int64(5_000_000)
	const warmup = 2 * perBurst
	var placed int64
	for i := 1; i <= perBurst*bursts; i++ {
		now.Store(start + int64(i)*period)
		if i%perBurst == 0 { // a burst once its last period has passed
			for k := 0; k < perBurst; k++ {
				pcm := make([]float32, n*2)
				for j := range pcm {
					pcm[j] = 0.1
				}
				a.ingestSRTAudio(key, &anchor, srtAudioJob{pcm: pcm, pts: srcPTS, sampleRate: 48000, channels: 2})
				srcPTS += 1800
			}
		}
		if i == warmup {
			placed = m.ChannelRealigns()[key]
		}
		m.ProduceSamples(clock.TimestampToIndex(rate, now.Load())-2*n, n, 0)
	}

	require.Zero(t, m.ChannelRealigns()[key]-placed,
		"SRT audio with continuous PTS must not be spliced back onto the read point")
}
