package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// An MPEG-TS muxer packs a stream's AAC frames into PES packets of several
// frames, so an SRT stream's audio reaches the decoder in bursts, here 180
// ms of audio every 180 ms, while its own PTS run on without a gap. Placed
// by those PTS no earlier than they arrive, the audio is on hand at every
// read; each burst placed at its arrival ends at the moment it arrives, the
// program's read point runs out of it before the next burst, and the mixer
// splices the source back on every burst.
func TestSRTAudioBurstsStayOnTheReadPoint(t *testing.T) {
	const n = 960 // one 20 ms period at 48 kHz
	const period = 20 * time.Millisecond
	const perBurst, bursts = 9, 12
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	m.AddChannel("srt:a")
	m.SetActive("srt:a", true)

	a := &App{mixer: m}
	rc := &srtRelayContext{key: "srt:a", audioCh: make(chan srtAudioJob, perBurst*bursts)}
	a.startSRTAudioRelay(rc)

	start := time.Now()
	fed := make(chan struct{})
	go func() {
		defer close(fed)
		defer close(rc.audioCh)
		srcPTS := int64(5_000_000)
		for b := 0; b < bursts; b++ {
			// A burst is handed over once its last period has passed.
			time.Sleep(time.Until(start.Add(time.Duration((b+1)*perBurst) * period)))
			for k := 0; k < perBurst; k++ {
				pcm := make([]float32, n*2)
				for j := range pcm {
					pcm[j] = 0.1
				}
				rc.audioCh <- srtAudioJob{pcm: pcm, pts: srcPTS, sampleRate: 48000, channels: 2}
				srcPTS += 1800
			}
		}
	}()

	// Ticks read contiguous periods, as the program does, two periods
	// behind the clock, from the first burst on. The first ticks place the
	// source's ring on the read point.
	const warmup = 10
	rate := clock.Rate{Num: 48000, Den: 1}
	first := perBurst + 1
	read := clock.TimestampToIndex(rate, clock.TAINow()) + uint64(first-2)*n
	var placed int64
	for i := first; i < perBurst*bursts; i++ {
		if i == first+warmup {
			placed = m.ChannelRealigns()["srt:a"]
		}
		time.Sleep(time.Until(start.Add(time.Duration(i) * period)))
		m.ProduceSamples(read, n, 0)
		read += n
	}
	<-fed

	require.Zero(t, m.ChannelRealigns()["srt:a"]-placed,
		"SRT audio with continuous PTS must not be spliced back onto the read point")
}
