package main

import (
	"math"
	"strings"
	"sync"
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

// holdMixer holds m, as a read does, until the returned function is called
// or the test ends.
func holdMixer(t *testing.T, m *audio.Mixer) (release func()) {
	t.Helper()
	held, released := make(chan struct{}), make(chan struct{})
	var hold, done sync.Once
	m.SetRawAudioSink(func([]float32, int64, int, int) {
		hold.Do(func() {
			close(held)
			<-released
		})
	})
	release = func() { done.Do(func() { close(released) }) }
	t.Cleanup(release)
	go m.ProduceSamples(0, 480, 0)
	<-held
	return release
}

// How long an SRT source's decoded audio waited for the mixer's ingest and
// how long its push to the mixer took are reported apart, so a soak tells
// audio that reached the mixer late because the queue before it was long
// from audio held up by the mixer itself. A push that waits for the mixer,
// here for a read that holds it 40 ms, counts that wait.
func TestSRTAudioIngestReportsQueueWaitAndPush(t *testing.T) {
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	const key = "srt:delay"
	m.AddChannel(key)
	a := &App{mixer: m}
	queue := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "queue_delay_seconds"})
	push := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "push_duration_seconds"})
	var drops, relayDrops atomic.Int64
	rc := &srtRelayContext{
		key:          key,
		state:        &srtSourceState{audioDrops: &drops, relayAudioDrops: &relayDrops},
		anchor:       &clock.TrackingAnchor{},
		audioCh:      make(chan srtAudioJob, 1),
		queueDelay:   queue,
		pushDuration: push,
	}
	done := a.startSRTAudioRelay(rc)

	const late, hold = 40 * time.Millisecond, 40 * time.Millisecond
	release := holdMixer(t, m)
	time.AfterFunc(hold, release)
	rc.audioCh <- srtAudioJob{pcm: make([]float32, 2*480), pts: clock.NowPTS(), sampleRate: 48000, channels: 2, decoded: time.Now().Add(-late)}
	close(rc.audioCh)
	<-done

	sum := func(h prometheus.Histogram) (uint64, float64) {
		var out dto.Metric
		require.NoError(t, h.Write(&out))
		return out.GetHistogram().GetSampleCount(), out.GetHistogram().GetSampleSum()
	}
	n, waited := sum(queue)
	require.Equal(t, uint64(1), n)
	require.GreaterOrEqual(t, waited, late.Seconds())
	require.Less(t, waited, late.Seconds()+1)
	n, pushed := sum(push)
	require.Equal(t, uint64(1), n)
	require.GreaterOrEqual(t, pushed, (hold / 2).Seconds(), "the push's wait for the mixer was not counted")
}

// When an SRT connection ends, every frame of audio it had queued is
// pushed to the mixer before the ingest says it is done, here while a read
// holds the mixer for 50 ms, so a reset of the source's audio for a new
// connection, which comes after, is not followed by the old connection's
// audio placed by its old anchor.
func TestSRTAudioIngestIsDoneOnlyOnceItsQueueIsPushed(t *testing.T) {
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	const key = "srt:drain"
	m.AddChannel(key)
	m.SetActive(key, true)
	a := &App{mixer: m}
	var drops, relayDrops atomic.Int64
	rc := &srtRelayContext{
		key:     key,
		state:   &srtSourceState{audioDrops: &drops, relayAudioDrops: &relayDrops},
		anchor:  &clock.TrackingAnchor{},
		audioCh: make(chan srtAudioJob, 8),
	}
	pts := clock.NowPTS()
	for k := range 8 {
		pcm := make([]float32, 2*480)
		for i := range pcm {
			pcm[i] = 0.5
		}
		rc.audioCh <- srtAudioJob{pcm: pcm, pts: pts + int64(k)*900, sampleRate: 48000, channels: 2}
	}
	close(rc.audioCh)
	release := holdMixer(t, m)
	done := a.startSRTAudioRelay(rc)
	select {
	case <-done:
		t.Fatal("the ingest was done before the audio it had queued was pushed")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	<-done
	require.Equal(t, -6.0, math.Round(m.ChannelStates()[key].PeakL), "the last frame queued was not pushed")
	m.ResetChannelInput(key)
	time.Sleep(20 * time.Millisecond)
	read := clock.PTSIndex(clock.Rate{Num: 48000, Den: 1}, pts)
	for _, v := range m.ProduceSamples(read, 8*480, 0) {
		require.Zero(t, v, "audio of the ended connection was pushed after the reset")
	}
}

// A relay that falls behind drops the oldest audio it has queued, so once
// it catches up it goes on with the newest, not with audio up to its
// queue's length old.
func TestSRTRelayQueueDropsTheOldestAudio(t *testing.T) {
	ch := make(chan srtAudioJob, 2)
	var drops atomic.Int64
	for pts := range int64(4) {
		queueDroppingOldest(ch, srtAudioJob{pts: pts}, &drops)
	}
	require.Equal(t, int64(2), drops.Load())
	require.Equal(t, int64(2), (<-ch).pts)
	require.Equal(t, int64(3), (<-ch).pts)
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
