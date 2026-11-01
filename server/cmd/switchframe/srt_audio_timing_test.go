package main

import (
	"math"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/switcher"
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

// An SRT source that reconnects under the same key is placed by an anchor
// of its own connection. The new connection's PTS restart 1.5 s ahead of
// where the old ones stopped, as a restarted sender's may; an anchor that
// kept the old connection's lateness would place its audio more than a
// second ahead of its arrival, beyond what the mixer's ring holds, and the
// read point would get silence. Placed afresh, the new audio is read on
// its stamps from the first reads that reach it, with no realign.
func TestSRTReconnectPlacesTheNewConnectionsAudioAfresh(t *testing.T) {
	const n = 960 // one 20 ms period at 48 kHz
	const period = int64(20_000_000)
	const key = "srt:reconnect"
	var now atomic.Int64
	scriptTAI(t, &now)
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	a := &App{sw: sw, mixer: m, srtSources: map[string]*srtSourceState{}}
	t.Cleanup(a.stopSRTSources)

	rate := clock.Rate{Num: 48000, Den: 1}
	start := now.Load()
	step := 0
	// tick advances the clock one period, delivers pts's audio if pts is
	// not nil, and reads the mixer two periods behind the clock, as the
	// sync loop does. It reports whether every sample read carried audio.
	tick := func(anchor *clock.TrackingAnchor, pts *int64) (full bool) {
		step++
		now.Store(start + int64(step)*period)
		if pts != nil {
			pcm := make([]float32, n*2)
			for j := range pcm {
				pcm[j] = 0.1
			}
			a.ingestSRTAudio(key, anchor, srtAudioJob{pcm: pcm, pts: *pts, sampleRate: 48000, channels: 2})
		}
		out := m.ProduceSamples(clock.TimestampToIndex(rate, now.Load())-2*n, n, 0)
		full = len(out) == 2*n
		for _, v := range out {
			if math.Abs(float64(v)) < 0.01 {
				full = false
			}
		}
		return full
	}

	// Connection 1: two seconds.
	anchor := a.bindSRTSource(key)
	m.SetActive(key, true)
	srcPTS := int64(5_000_000)
	for i := range 100 {
		if full := tick(anchor, &srcPTS); i >= 4 && !full {
			t.Fatalf("connection 1 read %d is not full audio", i)
		}
		srcPTS += 1800
	}
	require.Zero(t, m.ChannelRealigns()[key], "connection 1 was realigned")

	// It drops for 300 ms. The read that finds the ring empty past its
	// end counts as one realign.
	a.releaseSRTConnection(key, srt.ModeListener)
	for range 15 {
		tick(nil, nil)
	}

	// Connection 2: its PTS 1.5 s ahead, for two seconds.
	anchor = a.bindSRTSource(key)
	before := m.ChannelRealigns()[key]
	srcPTS += 135000
	for i := range 100 {
		pts := srcPTS + newConnectionJitter(i)
		if full := tick(anchor, &pts); i >= 4 && !full {
			t.Fatalf("connection 2 read %d after the reconnect is not full audio", i)
		}
		srcPTS += 1800
	}
	require.Zero(t, m.ChannelRealigns()[key]-before, "the new connection's audio was realigned")
}

// newConnectionJitter offsets the reconnected source's i-th audio PTS.
var newConnectionJitter = func(int) int64 { return 0 }
