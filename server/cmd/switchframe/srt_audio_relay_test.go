package main

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/metrics"
)

// The time from an SRT source's decoder handing over a frame of audio to
// its push to the mixer is reported, so a soak tells audio that broke
// because it reached the mixer late from audio that arrived late.
func TestSRTAudioIngestReportsItsDelay(t *testing.T) {
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	const key = "srt:delay"
	m.AddChannel(key)
	a := &App{mixer: m}
	h := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "ingest_delay_seconds"})
	var drops, relayDrops atomic.Int64
	rc := &srtRelayContext{
		key:         key,
		state:       &srtSourceState{audioDrops: &drops, relayAudioDrops: &relayDrops},
		anchor:      &clock.TrackingAnchor{},
		audioCh:     make(chan srtAudioJob, 1),
		ingestDelay: h,
	}
	a.startSRTAudioRelay(rc)
	const late = 40 * time.Millisecond
	rc.audioCh <- srtAudioJob{pcm: make([]float32, 2*480), pts: clock.NowPTS(), sampleRate: 48000, channels: 2, decoded: time.Now().Add(-late)}
	close(rc.audioCh)

	var out atomic.Pointer[dto.Metric]
	require.Eventually(t, func() bool {
		m := &dto.Metric{}
		if h.Write(m) != nil || m.GetHistogram().GetSampleCount() != 1 {
			return false
		}
		out.Store(m)
		return true
	}, 5*time.Second, time.Millisecond)
	sum := out.Load().GetHistogram().GetSampleSum()
	require.GreaterOrEqual(t, sum, late.Seconds())
	require.Less(t, sum, late.Seconds()+1)
}

// A connected SRT source's audio dropped before the mixer, which the
// program misses, and before its relay, which only its browser preview
// and replay miss, are counted apart, in the debug snapshot and as
// metrics.
func TestSRTAudioDropsAreReported(t *testing.T) {
	var drops, relayDrops atomic.Int64
	drops.Add(2)
	relayDrops.Add(5)
	a := &App{srtSources: map[string]*srtSourceState{
		"srt:cam": {audioDrops: &drops, relayAudioDrops: &relayDrops},
	}}

	snap := (&relayDropProvider{app: a}).DebugSnapshot()["sources"].(map[string]any)["srt:cam"].(map[string]int64)
	require.Equal(t, int64(2), snap["programAudioDrops"])
	require.Equal(t, int64(5), snap["audioDrops"])

	label := metrics.SourceLabel("srt:cam")
	want := `
# HELP switchframe_srt_audio_drops_total Decoded audio frames of a connected SRT source dropped before the mixer, because its ingest had fallen behind the decoder: missing from the program. Starts at 0 on every connection.
# TYPE switchframe_srt_audio_drops_total counter
switchframe_srt_audio_drops_total{source="` + label + `"} 2
# HELP switchframe_srt_relay_audio_drops_total Decoded audio frames of a connected SRT source dropped before the browser relay's AAC encode, because the relay had fallen behind the mixer's ingest: missing from the source's browser preview and replay only. Starts at 0 on every connection.
# TYPE switchframe_srt_relay_audio_drops_total counter
switchframe_srt_relay_audio_drops_total{source="` + label + `"} 5
`
	require.NoError(t, testutil.CollectAndCompare(srtAudioDropCollector{app: a}, strings.NewReader(want)))
	problems, err := testutil.CollectAndLint(srtAudioDropCollector{app: a})
	require.NoError(t, err)
	require.Empty(t, problems)
}
