package main

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/switcher"
)

// programLumaRecorder records the PTS, first luma byte and arrival time of
// every raw program frame.
type programLumaRecorder struct {
	mu   sync.Mutex
	pts  []int64
	luma []byte
	at   []time.Time
}

func (r *programLumaRecorder) ID() string { return "luma-recorder" }
func (r *programLumaRecorder) SendVideo(f *media.VideoFrame) {
	if len(f.WireData) == 0 {
		return
	}
	r.mu.Lock()
	r.pts = append(r.pts, f.PTS)
	r.luma = append(r.luma, f.WireData[0])
	r.at = append(r.at, time.Now())
	r.mu.Unlock()
}
func (r *programLumaRecorder) SendAudio(*media.AudioFrame)    {}
func (r *programLumaRecorder) SendCaptions(*ccx.CaptionFrame) {}
func (r *programLumaRecorder) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{}
}

// gpuRemovals is a GPU source manager that records the sources removed
// from it and, in order, what was done to its sources: "ingest <key>",
// "stop-preview <key>" and "remove <key>", a run of one entry noted once.
type gpuRemovals struct {
	mu      sync.Mutex
	removed []string
	log     []string
}

func (g *gpuRemovals) note(e string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n := len(g.log); n == 0 || g.log[n-1] != e {
		g.log = append(g.log, e)
	}
}

// logged returns the log's entries for key.
func (g *gpuRemovals) logged(key string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, e := range g.log {
		if strings.HasSuffix(e, " "+key) && (len(out) == 0 || out[len(out)-1] != e) {
			out = append(out, e)
		}
	}
	return out
}

func (g *gpuRemovals) IngestYUV(key string, _ []byte, _, _ int, _ int64) { g.note("ingest " + key) }
func (g *gpuRemovals) StopPreview(key string)                            { g.note("stop-preview " + key) }
func (g *gpuRemovals) RemoveSource(key string) {
	g.note("remove " + key)
	g.mu.Lock()
	g.removed = append(g.removed, key)
	g.mu.Unlock()
}

// An SRT camera on program whose connection drops and comes back is the
// same source: the program holds its last frame across the gap and goes on
// with the new connection's first frame, with no black in between. The
// source stays on program, its audio channel stays on air, and its GPU
// entry stays, so the freeze has a frame to show. The new connection's
// timestamps restart 1.5 s ahead of the old ones, as a restarted sender's
// may: an anchor that kept the old connection's lateness would place them
// that far ahead of their arrival. Placed by an anchor of their own they
// are shown as they arrive.
func TestSRTReconnectFreezesThroughOnProgram(t *testing.T) {
	const key = "srt:cam"
	const w, h = 320, 240
	const samples = 1600 // one 29.97 fps frame of 48 kHz audio, near enough

	relay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, relay)
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	sw.SetMixer(m)
	gpuSources := &gpuRemovals{}
	sw.SetGPUSourceManager(gpuSources)
	a := &App{sw: sw, mixer: m, srtSources: map[string]*srtSourceState{}}
	rec := &programLumaRecorder{}
	relay.AddViewer(rec)

	anchor := a.bindSRTSource(key)
	if err := sw.Cut(context.Background(), key); err != nil {
		t.Fatal(err)
	}

	// The mixer is read on the clock, two periods behind, as the sync loop
	// reads it; loud records whether a read carried the source's audio. A
	// host that cannot keep the reads within half a second of the clock
	// cannot run the test: catching up from further behind takes it longer
	// than the clock moves, and the reads fall behind without end.
	rate := clock.Rate{Num: 48000, Den: 1}
	next := clock.TimestampToIndex(rate, clock.TAINow()) - 3*samples
	read := func() (loud bool) {
		target := clock.TimestampToIndex(rate, clock.TAINow()) - 2*samples
		if lag := target - min(next, target); lag > 24000 {
			t.Fatalf("mixer reads fell %d samples behind the clock, want at most 24000: host too slow", lag)
		}
		for ; next+samples <= target; next += samples {
			for _, v := range m.ProduceSamples(next, samples, 0) {
				if math.Abs(float64(v)) > 0.01 {
					loud = true
				}
			}
		}
		return loud
	}

	// A connection feeds the source through the app's ingest paths; send
	// gives it n frames of luma y with their audio, one per frame period,
	// from source PTS srcPTS on, and returns when the program first carried
	// the audio.
	type conn struct {
		video chan srtVideoJob
		audio chan srtAudioJob
		done  sync.WaitGroup
	}
	open := func(anchor *clock.TrackingAnchor) *conn {
		c := &conn{video: make(chan srtVideoJob, 4), audio: make(chan srtAudioJob, 8)}
		a.startSRTPipelineIngest(key, anchor, c.video, nil)
		c.done.Go(func() {
			for job := range c.audio {
				a.ingestSRTAudio(key, anchor, job)
			}
		})
		return c
	}
	hangUp := func(c *conn) {
		close(c.video)
		close(c.audio)
		drained := make(chan struct{})
		go func() { c.done.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(2 * time.Second):
			t.Fatal("audio ingest did not drain within 2s of the hang-up")
		}
	}
	period := 1001 * time.Millisecond / 30
	send := func(c *conn, n int, y byte, srcPTS int64) (heard time.Time) {
		for i := range n {
			yuv := make([]byte, w*h*3/2)
			for j := range yuv[:w*h] {
				yuv[j] = y
			}
			select {
			case c.video <- srtVideoJob{yuv: yuv, w: w, h: h, pts: srcPTS + int64(i)*3003}:
			case <-time.After(time.Second):
				t.Fatalf("video ingest took no frame for 1s at frame %d", i)
			}
			pcm := make([]float32, samples*2)
			for j := range pcm {
				pcm[j] = 0.1
			}
			select {
			case c.audio <- srtAudioJob{pcm: pcm, pts: srcPTS + int64(i)*3003, sampleRate: 48000, channels: 2}:
			case <-time.After(time.Second):
				t.Fatalf("audio ingest took no frame for 1s at frame %d", i)
			}
			time.Sleep(period)
			if read() && heard.IsZero() {
				heard = time.Now()
			}
		}
		return heard
	}

	// Connection 1: half a second of luma 50.
	c1 := open(anchor)
	srcPTS := int64(20 * 90000)
	if send(c1, 15, 50, srcPTS).IsZero() {
		t.Fatal("the source's audio never reached the program before the drop")
	}
	// The reconnect may cost one realign, and one more on a host whose
	// scheduling jitter realigned the first connection too. No more: a
	// first connection noisy enough to realign many times must not lift
	// the bound over a storm of them.
	jitter := m.ChannelRealigns()[key]
	allowed := 1 + min(jitter, 1)
	srcPTS += 15 * 3003

	// The connection drops: OnStopped's cleanup, then 300 ms with nothing.
	hangUp(c1)
	a.releaseSRTConnection(key, srt.ModeListener)
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(period) {
		read()
	}

	// Connection 2 under the same key, its PTS 1.5 s ahead.
	anchor = a.bindSRTSource(key)
	if got := sw.ProgramSource(); got != key {
		t.Fatalf("program is %q after the reconnect, want %q", got, key)
	}
	if !m.IsChannelActive(key) {
		t.Fatal("the source's audio channel is off air after the reconnect")
	}
	before := m.ChannelRealigns()[key]
	c2 := open(anchor)
	reconnected := time.Now()
	heard := send(c2, 15, 90, srcPTS+135000)
	hangUp(c2)

	if got := sw.ProgramSource(); got != key {
		t.Fatalf("program is %q after the new connection's frames, want %q", got, key)
	}
	if heard.IsZero() {
		t.Fatal("the new connection's audio never reached the program")
	}
	if heard.Sub(reconnected) > 400*time.Millisecond {
		t.Fatalf("the new connection's audio reached the program %v after it started, want within 400ms",
			heard.Sub(reconnected))
	}
	if n := m.ChannelRealigns()[key] - before; n > allowed {
		t.Fatalf("the source's audio was realigned %d times after the reconnect, %d before it, want at most %d",
			n, jitter, allowed)
	}
	gpuSources.mu.Lock()
	removed := gpuSources.removed
	gpuSources.mu.Unlock()
	if len(removed) != 0 {
		t.Fatalf("GPU entries removed across the drop and reconnect: %v, want none", removed)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	first50, first90 := -1, -1
	for i, y := range rec.luma {
		if y == 50 && first50 < 0 {
			first50 = i
		}
		if y == 90 {
			first90 = i
			break
		}
	}
	if first50 < 0 || first90 < 0 {
		t.Fatalf("program never showed both connections (first 50 at %d, first 90 at %d)", first50, first90)
	}
	for i := first50; i < first90; i++ {
		if rec.luma[i] != 50 {
			t.Fatalf("program frame %d between the drop and the new connection has luma %d, want the held 50",
				i, rec.luma[i])
		}
	}
	if d := rec.at[first90].Sub(reconnected); d > 400*time.Millisecond {
		t.Fatalf("the new connection's first frame reached the program %v after it was sent, want within 400ms", d)
	}
	for i := 1; i < len(rec.pts); i++ {
		if rec.pts[i] <= rec.pts[i-1] {
			t.Fatalf("program PTS steps back at frame %d: %d after %d", i, rec.pts[i], rec.pts[i-1])
		}
	}
}
