package main

import (
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

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

// timingRates are the program frame rates the timing tests run at. The
// SRT decoder hands the mixer 48 kHz whatever the stream carries, so the
// mixer side runs at 48 kHz only.
var timingRates = []clock.Rate{
	{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1}, {Num: 30000, Den: 1001},
	{Num: 30, Den: 1}, {Num: 50, Den: 1}, {Num: 60000, Den: 1001}, {Num: 60, Den: 1},
	{Num: 100, Den: 1}, {Num: 120000, Den: 1001}, {Num: 120, Den: 1},
	{Num: 240000, Den: 1001}, {Num: 240, Den: 1},
}

// forEachTimingRate runs f at every program rate.
func forEachTimingRate(t *testing.T, f func(t *testing.T, r clock.Rate)) {
	for _, r := range timingRates {
		t.Run(fmt.Sprintf("%d_%dfps", r.Num, r.Den), func(t *testing.T) { f(t, r) })
	}
}

// tickRead returns the 48 kHz samples the tick at program index n reads
// at rate r, as the sync loop does: the period clock.ReadDelay(r) periods
// behind it.
func tickRead(r clock.Rate, n uint64) (start uint64, samples int) {
	sr := clock.Rate{Num: 48000, Den: 1}
	d := clock.ReadDelay(r)
	start = clock.TimestampToIndex(sr, clock.IndexToTimestamp(r, n-d))
	return start, int(clock.TimestampToIndex(sr, clock.IndexToTimestamp(r, n-d+1)) - start)
}

// An MPEG-TS muxer packs a stream's AAC frames into PES packets of several
// frames, so an SRT stream's audio reaches the decoder in bursts, here 180
// ms of audio every 180 ms, while its own PTS run on without a gap. Placed
// by those PTS no earlier than they arrive, the audio is on hand at every
// read of the program, clock.ReadDelay periods behind the clock at every
// program rate; each burst placed at its arrival ends the moment it
// arrives, the read point runs out of it before the next burst, and the
// mixer splices the source on every burst.
func TestSRTAudioBurstsStayOnTheReadPoint(t *testing.T) {
	forEachTimingRate(t, srtAudioBursts)
}

func srtAudioBursts(t *testing.T, r clock.Rate) {
	const n = 960 // 20 ms of audio a job
	const job = int64(20_000_000)
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

	start := now.Load()
	srcPTS := int64(5_000_000)
	warmup := start + 2*perBurst*job
	var placed int64
	burst := int64(1) // a burst once its last job has passed
	for tick := clock.TimestampToIndex(r, start) + 1; ; tick++ {
		ts := clock.IndexToTimestamp(r, tick)
		if ts > start+bursts*perBurst*job {
			break
		}
		for ; start+burst*perBurst*job <= ts; burst++ {
			now.Store(start + burst*perBurst*job)
			for k := 0; k < perBurst; k++ {
				pcm := make([]float32, n*2)
				for j := range pcm {
					pcm[j] = 0.1
				}
				a.ingestSRTAudio(key, &anchor, srtAudioJob{pcm: pcm, pts: srcPTS, sampleRate: 48000, channels: 2})
				srcPTS += 1800
			}
		}
		now.Store(ts)
		if warmup != 0 && ts >= warmup {
			placed, warmup = m.ChannelRealigns()[key], 0
		}
		read, count := tickRead(r, tick)
		m.ProduceSamples(read, count, 0)
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
// its stamps from the first reads that reach it, with no realign, at
// every program rate.
func TestSRTReconnectPlacesTheNewConnectionsAudioAfresh(t *testing.T) {
	forEachTimingRate(t, srtReconnect)
}

func srtReconnect(t *testing.T, r clock.Rate) {
	const n = 960 // 20 ms of audio a job
	const job = int64(20_000_000)
	const key = "srt:reconnect"
	var now atomic.Int64
	scriptTAI(t, &now)
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	a := &App{sw: sw, mixer: m, srtSources: map[string]*srtSourceState{}}
	t.Cleanup(func() {
		a.stopSRTSources()
		a.cancelSRTReleases()
	})

	rate := clock.Rate{Num: 48000, Den: 1}
	start := now.Load()
	tick := clock.TimestampToIndex(r, start) + 1
	// readUntil reads the mixer at every tick up to ts, as the sync loop
	// does. It reports whether every sample read from the timestamp from
	// on carried audio.
	readUntil := func(ts, from int64) (full bool) {
		full = true
		for ; clock.IndexToTimestamp(r, tick) <= ts; tick++ {
			now.Store(clock.IndexToTimestamp(r, tick))
			read, count := tickRead(r, tick)
			out := m.ProduceSamples(read, count, 0)
			if clock.IndexToTimestamp(rate, read) < from {
				continue
			}
			if len(out) != 2*count {
				full = false
			}
			for _, v := range out {
				if math.Abs(float64(v)) < 0.01 {
					full = false
				}
			}
		}
		return full
	}
	// connect delivers 100 jobs of 20 ms through anchor, each as its last
	// sample has passed and the i-th stamped pts(i), and fails on a read
	// from 60 ms into the connection on that is not full audio.
	at := start
	connect := func(name string, anchor *clock.TrackingAnchor, pts func(i int) int64) {
		from := at + 60_000_000
		for i := range 100 {
			at += job
			if !readUntil(at-1, from) {
				t.Fatalf("%s read before job %d is not full audio", name, i)
			}
			now.Store(at)
			pcm := make([]float32, n*2)
			for j := range pcm {
				pcm[j] = 0.1
			}
			a.ingestSRTAudio(key, anchor, srtAudioJob{pcm: pcm, pts: pts(i), sampleRate: 48000, channels: 2})
		}
		if !readUntil(at, from) {
			t.Fatalf("%s last read is not full audio", name)
		}
	}

	// Connection 1: two seconds.
	anchor := a.bindSRTSource(key)
	m.SetActive(key, true)
	srcPTS := int64(5_000_000)
	connect("connection 1", anchor, func(i int) int64 { return srcPTS + int64(i)*1800 })
	require.Zero(t, m.ChannelRealigns()[key], "connection 1 was realigned")

	// It drops for 300 ms. The read that finds the ring empty past its
	// end counts as one realign.
	a.releaseSRTConnection(key, srt.ModeListener)
	at += 300_000_000
	readUntil(at, math.MaxInt64)

	// Connection 2: its PTS 1.5 s ahead, for two seconds.
	anchor = a.bindSRTSource(key)
	before := m.ChannelRealigns()[key]
	srcPTS += 100*1800 + 135000
	connect("connection 2", anchor, func(i int) int64 { return srcPTS + int64(i)*1800 + newConnectionJitter(i) })
	require.Zero(t, m.ChannelRealigns()[key]-before, "the new connection's audio was realigned")
}

// newConnectionJitter offsets the reconnected source's i-th audio PTS.
var newConnectionJitter = func(int) int64 { return 0 }

// An SRT connection's first audio arrives later than the rest, as a
// backlog the socket built before the first frame was decoded: 10 ms, or
// half a period at a program rate whose period is not longer than that.
// The anchor places it at that lateness, and the mixer's first read puts
// it on those stamps; the windows after the first arrive on time, and the
// anchor settles that much earlier. That is less than the period the
// mixer realigns at, so without placing the audio again on its stamps
// once the anchor settles, it would stay that much later than its video
// for the rest of the connection.
func TestSRTAudioLandsOnItsStampsOnceTheAnchorSettles(t *testing.T) {
	forEachTimingRate(t, srtAudioSettles)
}

func srtAudioSettles(t *testing.T, r clock.Rate) {
	const job = 480 // 10 ms of audio per delivery
	const step = int64(10_000_000)
	const key = "srt:settle"
	var now atomic.Int64
	scriptTAI(t, &now)
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel(key)
	m.SetActive(key, true)
	a := &App{mixer: m}
	var anchor clock.TrackingAnchor

	rate := clock.Rate{Num: 48000, Den: 1}
	start := now.Load()
	// Job k's own PTS is the engine PTS of step k, so on time it lands on
	// sample index startIdx+k*job, and every 20th job is loud. The index
	// is the one that PTS stands for: start is off the sample grid, and
	// its PTS, a 90 kHz tick, is up to half a tick off start.
	startIdx := clock.PTSIndex(rate, clock.PTS(start))
	deliver := func(k int) {
		pcm := make([]float32, job*2)
		level := float32(0.1)
		if k%20 == 0 {
			level = 0.5
		}
		for j := range pcm {
			pcm[j] = level
		}
		pts := clock.PTS(start + int64(k)*step)
		a.ingestSRTAudio(key, &anchor, srtAudioJob{pcm: pcm, pts: pts, sampleRate: 48000, channels: 2})
	}

	// The jobs before 1.9 s arrive late, the rest on time. The program
	// reads at its own ticks, each after the jobs that arrived before it.
	const lateUntil, jobs = 190, 1000
	late := 10 * time.Millisecond
	if period := time.Duration(clock.IndexToTimestamp(r, 1)); period <= late {
		late = period / 2
	}
	settled := clock.TrackingSettle * clock.TrackingWindow / 900
	var prev float32
	var onsets int
	tick := clock.TimestampToIndex(r, start) + 1
	for k := 0; k <= jobs; k++ {
		at := start + int64(k)*step
		if k < lateUntil {
			at += int64(late)
		}
		for ; clock.IndexToTimestamp(r, tick) < at; tick++ {
			ts := clock.IndexToTimestamp(r, tick)
			read, n := tickRead(r, tick)
			out := m.ProduceSamples(read, n, 0)
			for i := 0; i < n; i++ {
				v := out[2*i]
				if v > 0.3 && prev <= 0.3 && ts > start+int64(settled+4)*step {
					onsets++
					off := int64(read+uint64(i)-startIdx) % (20 * job)
					if off > 10*job {
						off -= 20 * job
					}
					require.Zerof(t, off, "a loud job lands %d samples off its stamps after the anchor settled", off)
				}
				prev = v
			}
		}
		now.Store(at)
		deliver(k)
	}
	require.NotZero(t, onsets, "no loud job was read after the anchor settled")
	require.Zero(t, m.ChannelRealigns()[key], "the source was realigned")
}

// Once its anchor settled, an SRT source's frames arrive later for good,
// as a longer network path delivers them. Under the anchor's slack its
// audio is still read on time where it is, and the anchor leaves it there.
// A little more, still inside the audio budget, ReadDelay-1 periods,
// raises the anchor: its video moves that much later, and its audio, which
// the mixer realigns only past a period, has to be placed on its stamps
// again, or the two sit apart until the offset comes down a minute later.
// Past the budget, its audio misses reads until the anchor raises once. In
// every case the audio lands where its video is once the lateness lasts,
// at every program rate and for every audio frame length.
func TestSRTAudioStaysOnItsVideoThroughALastingLateness(t *testing.T) {
	rates := []clock.Rate{
		{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1}, {Num: 30000, Den: 1001}, {Num: 30, Den: 1},
		{Num: 50, Den: 1}, {Num: 60000, Den: 1001}, {Num: 60, Den: 1}, {Num: 100, Den: 1},
		{Num: 120000, Den: 1001}, {Num: 120, Den: 1}, {Num: 240000, Den: 1001}, {Num: 240, Den: 1},
	}
	for _, r := range rates {
		for _, frame := range []int{1024, 1536} {
			for _, c := range []struct {
				name   string
				late   func(slack, budget int64) int64
				raises int64
			}{
				{"under-slack", func(slack, _ int64) int64 { return slack - 10 }, 0},
				{"past-slack", func(slack, _ int64) int64 { return slack + clock.TrackingDriftMargin/2 }, 1},
				{"past-budget", func(_, budget int64) int64 { return budget + 45 }, 1},
			} {
				t.Run(fmt.Sprintf("%d/%d/%d/%s", r.Num, r.Den, frame, c.name), func(t *testing.T) {
					srtLastingLateness(t, r, frame, c.late, c.raises)
				})
			}
		}
	}
}

// srtLastingLateness runs one SRT source at program rate r, in audio
// frames of frame samples, that settles on time and from 8 s on arrives
// late(slack, budget) ticks later for good, slack being the anchor's and
// budget the audio's, ReadDelay-1 periods, and checks the anchor raised
// raises times and the audio lands on its video from 16 s on.
func srtLastingLateness(t *testing.T, r clock.Rate, frame int, lateness func(int64, int64) int64, raises int64) {
	const key = "srt:later"
	var now atomic.Int64
	scriptTAI(t, &now)
	clock.SetProgramRate(r)
	t.Cleanup(func() { clock.SetProgramRate(clock.Rate{Num: 50, Den: 1}) })
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel(key)
	m.SetActive(key, true)
	a := &App{mixer: m}
	var anchor clock.TrackingAnchor

	period := clock.ProgramPeriod()
	d := clock.ReadDelay(r)
	late := lateness(clock.TrackingSlack(), (int64(d)-1)*period)
	sr := clock.Rate{Num: 48000, Den: 1}
	start := now.Load()
	step := int64(frame) * 90000 / 48000 // frame length in 90 kHz ticks
	src0 := clock.PTS(start)
	const lateFrom, checkFrom, end = 8_000_000_000, 16_000_000_000, 26_000_000_000
	// Job k carries the samples from src0+k*step and arrives as its last
	// sample has passed, later by late from lateFrom on. Every 10th job is
	// loud, and the video frame of its time, mapped through the same
	// anchor as it arrives, names the index the loud audio belongs at.
	video := map[int64]bool{}
	arrival := func(k int) int64 {
		at := clock.TimestampOf(src0+int64(k+1)*step) - start
		if at >= lateFrom {
			at += late * 100_000 / 9
		}
		return start + at
	}
	deliver := func(k int) {
		pcm := make([]float32, frame*2)
		level := float32(0.1)
		if k%10 == 0 {
			level = 0.5
		}
		for j := range pcm {
			pcm[j] = level
		}
		pts := src0 + int64(k)*step
		a.ingestSRTAudio(key, &anchor, srtAudioJob{pcm: pcm, pts: pts, sampleRate: 48000, channels: 2})
		if k%10 == 0 {
			video[int64(clock.PTSIndex(sr, anchor.PTS(pts)))] = true
		}
	}

	next := 0
	var prev float32
	var onsets int
	var realigns, underruns int64
	g0 := clock.TimestampToIndex(r, start) + 3
	for g := g0; clock.IndexToTimestamp(r, g)-start < end; g++ {
		at := clock.IndexToTimestamp(r, g)
		for arrival(next) <= at {
			now.Store(arrival(next))
			deliver(next)
			next++
		}
		now.Store(at)
		read := clock.TimestampToIndex(sr, clock.IndexToTimestamp(r, g-d))
		n := int(clock.TimestampToIndex(sr, clock.IndexToTimestamp(r, g-d+1)) - read)
		if at-start < checkFrom {
			realigns, underruns = m.ChannelRealigns()[key], m.ChannelUnderruns()[key]
		}
		out := m.ProduceSamples(read, n, 0)
		for i := 0; i < n; i++ {
			v := out[2*i]
			if v > 0.3 && prev <= 0.3 && at-start >= checkFrom {
				onsets++
				idx := int64(read) + int64(i)
				for vi := range video {
					if off := idx - vi; off > -5*int64(frame) && off < 5*int64(frame) {
						require.Zerof(t, off, "late %d ticks, period %d: a loud frame lands %d samples off its video", late, period, off)
					}
				}
			}
			prev = v
		}
	}
	require.NotZero(t, onsets, "no loud frame was read once the lateness lasted")
	require.Equal(t, realigns, m.ChannelRealigns()[key], "late %d ticks, period %d: realigned once the lateness lasted", late, period)
	require.Equal(t, underruns, m.ChannelUnderruns()[key], "late %d ticks, period %d: ran dry once the lateness lasted", late, period)
	require.Equal(t, raises, anchor.Stats().Raises, "late %d ticks, period %d: anchor raises", late, period)
}
