//go:build cgo

package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
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
// queue could not hold. What the relay drops instead is counted. At every
// program rate.
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
	require.NotZero(t, relayDrops.Load(), "the stalled relay dropped nothing: its drops go uncounted")
	require.Zero(t, m.ChannelBreaks()[key]-before, "the program's audio of the source broke while its relay was stalled")
}

// ptsRecorder is a browser viewer that records the PTS of the audio frames
// it is sent.
type ptsRecorder struct {
	mu  sync.Mutex
	pts []int64
}

func (v *ptsRecorder) ID() string                     { return "pts-recorder" }
func (v *ptsRecorder) SendVideo(*media.VideoFrame)    {}
func (v *ptsRecorder) SendCaptions(*ccx.CaptionFrame) {}
func (v *ptsRecorder) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{}
}

func (v *ptsRecorder) SendAudio(f *media.AudioFrame) {
	v.mu.Lock()
	v.pts = append(v.pts, f.PTS)
	v.mu.Unlock()
}

// An SRT source whose decoder hands over audio in frames other than the
// 1024 samples the relay encodes, here 1152, has a job dropped before its
// relay. Each AAC frame the relay broadcasts after the gap is stamped with
// the PTS of its own first sample: carried on across the gap, the frames
// were stamped earlier than their audio by what was dropped, for as long
// as the connection lasted.
func TestSRTRelayAudioKeepsItsStampsAcrossDroppedFrames(t *testing.T) {
	const n, sr = 1152, 48000
	relay := distribution.NewRelay()
	rec := &ptsRecorder{}
	relay.AddViewer(rec)
	rc := &srtRelayContext{key: "srt:gap", relay: relay, state: &srtSourceState{}}
	jobs := make(chan srtAudioJob)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&App{}).relaySRTAudio(rc, jobs)
	}()
	const pts0 = int64(900_000)
	stamp := func(sample int64) int64 { return pts0 + sample*90000/sr }
	var want []int64 // the first sample of every AAC frame of the run after the gap
	for k := int64(0); k < 40; k++ {
		if k == 10 {
			continue // dropped
		}
		jobs <- srtAudioJob{pcm: make([]float32, 2*n), pts: stamp(k * n), sampleRate: sr, channels: 2}
	}
	close(jobs)
	<-done
	for s := int64(11 * n); s+1024 <= 40*n; s += 1024 {
		want = append(want, stamp(11*n)+(s-11*n)/1024*(1024*90000/sr))
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.GreaterOrEqual(t, len(rec.pts), len(want))
	got := rec.pts[len(rec.pts)-len(want):]
	require.Equal(t, want, got, "AAC frames after the gap are not stamped with their own audio")
}

// A 44.1 kHz stream's AAC frames, decoded to 48 kHz, are 1114 and 1115
// samples stamped 2089 or 2090 ticks apart, so a job continues the one
// before within a sample and the tick its stamp is rounded to, never
// exactly. The relay encodes it as one run: every AAC frame is stamped 1920
// ticks after the one before.
func TestSRTRelayAudioRunsOnAcrossA441StreamsStamps(t *testing.T) {
	relay := distribution.NewRelay()
	rec := &ptsRecorder{}
	relay.AddViewer(rec)
	rc := &srtRelayContext{key: "srt:441", relay: relay, state: &srtSourceState{}}
	jobs := make(chan srtAudioJob)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&App{}).relaySRTAudio(rc, jobs)
	}()
	samples := func(j int64) int64 { return j * 1024 * 48000 / 44100 }
	for j := int64(0); j < 200; j++ {
		n := samples(j+1) - samples(j)
		jobs <- srtAudioJob{pcm: make([]float32, 2*n), pts: 900_000 + j*1024*90000/44100, sampleRate: 48000, channels: 2}
	}
	close(jobs)
	<-done
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Greater(t, len(rec.pts), 150)
	for i := 1; i < len(rec.pts); i++ {
		require.Equal(t, int64(1920), rec.pts[i]-rec.pts[i-1], "AAC frame %d is not stamped on from the one before", i)
	}
}

// An encoder that stamps its PES packets on its capture clock moves each
// job's stamp up to tens of microseconds off the end of the job before.
// The relay encodes such a stream as one run: no audio is dropped at a
// jittered boundary, and every AAC frame is stamped 1920 ticks after the
// one before, give or take the jitter of a job's stamp it starts on.
func TestSRTRelayAudioRunsOnAcrossJitteredStamps(t *testing.T) {
	const n, jobs = 1152, 200
	relay := distribution.NewRelay()
	rec := &ptsRecorder{}
	relay.AddViewer(rec)
	rc := &srtRelayContext{key: "srt:jitter", relay: relay, state: &srtSourceState{}}
	ch := make(chan srtAudioJob)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&App{}).relaySRTAudio(rc, ch)
	}()
	for k := int64(0); k < jobs; k++ {
		jitter := k*7919%9 - 4 // up to 4 ticks, 44 us, either way
		ch <- srtAudioJob{pcm: make([]float32, 2*n), pts: 900_000 + k*n*90000/48000 + jitter, sampleRate: 48000, channels: 2}
	}
	close(ch)
	<-done
	rec.mu.Lock()
	defer rec.mu.Unlock()
	// The encoder holds back a frame or two of its own before its first
	// output.
	require.GreaterOrEqual(t, len(rec.pts), jobs*n/1024-2, "audio was dropped at a jittered boundary")
	for i := 1; i < len(rec.pts); i++ {
		require.InDelta(t, 1920, rec.pts[i]-rec.pts[i-1], 8, "AAC frame %d is not stamped on from the one before", i)
	}
}
