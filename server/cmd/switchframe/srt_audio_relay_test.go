package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/metrics"
)

// stallingViewer is a browser viewer whose send stalls once, at the first
// audio frame it is sent at or after TAI from, until release is closed.
type stallingViewer struct {
	from    int64
	stalled chan struct{} // closed as the send stalls
	release chan struct{}
	once    sync.Once
}

func (v *stallingViewer) ID() string                     { return "stalling-viewer" }
func (v *stallingViewer) SendVideo(*media.VideoFrame)    {}
func (v *stallingViewer) SendCaptions(*ccx.CaptionFrame) {}
func (v *stallingViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{}
}

func (v *stallingViewer) SendAudio(*media.AudioFrame) {
	if clock.TAINow() < v.from {
		return
	}
	v.once.Do(func() {
		close(v.stalled)
		<-v.release
	})
}

// A browser viewer whose send stalls, as a slow connection's does, holds
// up an SRT source's relay, its AAC encode and broadcast, for 100 ms. The
// source's audio still reaches the mixer as it is decoded, and the program
// airs it without a break. Ingested by the goroutine that also encoded and
// broadcast it, each frame reached the mixer only after the relay had
// taken the frame before, so the stall held the program's audio back:
// its reads found it missing, and the decode callback dropped what the
// queue could not hold. At every program rate.
func TestSRTAudioIngestDoesNotWaitForTheRelay(t *testing.T) {
	forEachTimingRate(t, srtRelayStall)
}

func srtRelayStall(t *testing.T, r clock.Rate) {
	const job = 480 // 10 ms of audio a job
	const step = int64(10_000_000)
	const key = "srt:relay-stall"
	const warmup, stallFrom, end = 8 * time.Second, 9 * time.Second, 11 * time.Second
	const stall = 100 * time.Millisecond
	var now atomic.Int64
	scriptTAI(t, &now)
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel(key)
	m.SetLiveInput(key)
	m.SetActive(key, true)
	a := &App{mixer: m}

	start := now.Load()
	v := &stallingViewer{from: start + int64(stallFrom), stalled: make(chan struct{}), release: make(chan struct{})}
	relay := distribution.NewRelay()
	relay.AddViewer(v)
	var drops, relayDrops atomic.Int64
	rc := &srtRelayContext{
		key:     key,
		relay:   relay,
		state:   &srtSourceState{audioDrops: &drops, relayAudioDrops: &relayDrops},
		anchor:  &clock.TrackingAnchor{},
		audioCh: make(chan srtAudioJob, 8),
	}
	a.startSRTAudioRelay(rc)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(v.release) }) }
	t.Cleanup(func() {
		release()
		close(rc.audioCh)
	})

	// Job k's right channel is a level of its own, which the mixer's meter
	// shows once the job was pushed.
	level := func(k int) float32 { return float32(k+1) / 4096 }
	ingested := func(k int) bool {
		deadline := time.Now().Add(time.Second)
		want := audio.LinearToDBFS(float64(level(k)))
		for time.Now().Before(deadline) {
			if m.ChannelStates()[key].PeakR == want {
				return true
			}
			time.Sleep(20 * time.Microsecond)
		}
		return false
	}

	src0 := clock.PTS(start)
	tick := clock.TimestampToIndex(r, start) + 1
	before := int64(-1)
	var stalledAt int64
	for k := 0; int64(k+1)*step <= int64(end); k++ {
		at := start + int64(k+1)*step // job k has passed
		for ; clock.IndexToTimestamp(r, tick) < at; tick++ {
			ts := clock.IndexToTimestamp(r, tick)
			now.Store(ts)
			if before < 0 && ts-start >= int64(warmup) {
				before = m.ChannelBreaks()[key]
			}
			read, n := tickRead(r, tick)
			m.ProduceSamples(read, n, 0)
		}
		now.Store(at)
		if stalledAt != 0 && at-stalledAt >= int64(stall) {
			release()
		}
		pcm := make([]float32, 2*job)
		for i := 0; i < job; i++ {
			pcm[2*i], pcm[2*i+1] = 0.1, level(k)
		}
		select {
		case rc.audioCh <- srtAudioJob{pcm: pcm, pts: src0 + int64(k)*job*90000/48000, sampleRate: 48000, channels: 2}:
		default:
			drops.Add(1) // as the decode callback does
		}
		if !ingested(k) {
			t.Fatalf("audio decoded at %.2f s did not reach the mixer within a second", float64(at-start)/1e9)
		}
		if stalledAt == 0 && at-start >= int64(stallFrom)+4*step {
			// The relay has had four jobs since the stall's start to
			// broadcast one of them.
			select {
			case <-v.stalled:
				stalledAt = at
			case <-time.After(5 * time.Second):
				t.Fatal("the relay never broadcast to the stalling viewer: the case does not stall it")
			}
		}
	}
	require.NotZero(t, stalledAt, "the relay never stalled")
	require.Zero(t, drops.Load(), "decoded audio was dropped before the mixer")
	require.Zero(t, m.ChannelBreaks()[key]-before, "the program's audio of the source broke while its relay was stalled")
}

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
