package clip

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
)

// startFrameRates and startSampleRates are the formats a clip start is
// checked at.
var (
	startFrameRates = []clock.Rate{
		{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1},
		{Num: 30000, Den: 1001}, {Num: 30, Den: 1}, {Num: 50, Den: 1},
		{Num: 60000, Den: 1001}, {Num: 60, Den: 1}, {Num: 100, Den: 1},
		{Num: 120, Den: 1}, {Num: 240, Den: 1},
	}
	startSampleRates = []int{44100, 48000, 96000}
)

// fakeTAI stands the TAI clock still at the value it is set to, for the
// test's duration. Tests that use it do not run in parallel.
func fakeTAI(t *testing.T) *atomic.Int64 {
	t.Helper()
	clock.Epoch()
	now := &atomic.Int64{}
	now.Store(clock.TAINow())
	clock.SetTAISource(now.Load)
	t.Cleanup(func() { clock.SetTAISource(nil) })
	return now
}

// startRampCodec decodes every AAC frame to 1024 samples per channel
// that count the coded samples from 1, so a sample names its own place in
// the clip.
var startRampCodec = PCMCodec{NewDecoder: func(_, channels int) (PCMDecoder, error) {
	return &startRampDecoder{ch: channels}, nil
}}

type startRampDecoder struct{ n, ch int }

func (d *startRampDecoder) Decode([]byte) ([]float32, error) {
	out := make([]float32, 1024*d.ch)
	for i := range out {
		if i%d.ch == 0 {
			d.n++
		}
		out[i] = float32(d.n)
	}
	return out, nil
}
func (d *startRampDecoder) Channels() int    { return d.ch }
func (d *startRampDecoder) OutputDelay() int { return 0 }
func (d *startRampDecoder) Close() error     { return nil }

// startClip returns n pictures at rate r, or at 120 fps above it, the
// most a clip is taken to run at, each carrying its index, and stereo
// audio at sampleRate for as long.
func startClip(r clock.Rate, sampleRate, n int) ([]BufferedFrame, []BufferedAudioFrame) {
	if r.Num > 120*r.Den {
		r = clock.Rate{Num: 120, Den: 1}
	}
	frames := make([]BufferedFrame, n)
	for i := range frames {
		frames[i] = BufferedFrame{wireData: []byte{byte(i)}, pts: int64(i) * 90000 * r.Den / r.Num, isKeyframe: i == 0}
	}
	audio := make([]BufferedAudioFrame, int64(n)*int64(sampleRate)*r.Den/(1024*r.Num)+2)
	for i := range audio {
		audio[i] = BufferedAudioFrame{data: []byte{byte(i)}, pts: int64(i) * 1024 * 90000 / int64(sampleRate), sampleRate: sampleRate, channels: 2}
	}
	return frames, audio
}

// pictureAt returns the picture of frames that output slot k shows at
// rate r.
func pictureAt(frames []BufferedFrame, r clock.Rate, k int) int {
	srcNum, srcDen := clipRational(estimateFPSFromClipFrames(frames))
	cad := newCadence(int(r.Num), int(r.Den), srcNum, srcDen, 1)
	j := 0
	for cad.start(j+1) <= k {
		j++
	}
	return j
}

type startOut struct {
	id      byte
	pts     int64
	inStart bool // put out while StartAt ran
}

type startPCM struct {
	first   float32
	pts     int64
	samples int
	start   bool
	inStart bool
}

// goid returns the ID of the calling goroutine.
func goid() string {
	buf := make([]byte, 64)
	f := strings.Fields(string(buf[:runtime.Stack(buf, false)]))
	return f[1]
}

// startRec records what a player puts out, and whether the goroutine that
// called StartAt put it out. While shut, an output of any other goroutine
// waits until open, so what StartAt put out is told apart from what the
// playback goroutine put out after it.
type startRec struct {
	mu      sync.Mutex
	starter string // goroutine that calls StartAt
	gate    chan struct{}
	opened  sync.Once
	video   []startOut
	aac     []startOut
	pcm     []startPCM
}

// add records an output, after the gate for another goroutine than the
// starter's.
func (r *startRec) add(fn func(inStart bool)) {
	in := goid() == r.starter
	r.mu.Lock()
	gate := r.gate
	r.mu.Unlock()
	if gate != nil && !in {
		<-gate
	}
	r.mu.Lock()
	fn(in)
	r.mu.Unlock()
}

// shut has every output of another goroutine than the caller's wait until
// open, which runs at the end of t at the latest.
func (r *startRec) shut(t *testing.T) {
	r.mu.Lock()
	r.starter, r.gate = goid(), make(chan struct{})
	r.mu.Unlock()
	t.Cleanup(r.open)
}

func (r *startRec) open() { r.opened.Do(func() { close(r.gate) }) }

func (r *startRec) config(rate clock.Rate, sampleRate int, frames []BufferedFrame, audio []BufferedAudioFrame) PlayerConfig {
	return PlayerConfig{
		Clip:         frames,
		AudioClip:    audio,
		OutputFPSNum: int(rate.Num),
		OutputFPSDen: int(rate.Den),
		Timed:        true,
		Cued:         true,
		AudioRate:    sampleRate,
		PCMCodec:     startRampCodec,
		RawVideoOutput: func(yuv []byte, _, _ int, pts int64, _ bool) {
			r.add(func(in bool) { r.video = append(r.video, startOut{yuv[0], pts, in}) })
		},
		AudioOutput: func(data []byte, pts int64, _, _ int) {
			r.add(func(in bool) { r.aac = append(r.aac, startOut{data[0], pts, in}) })
		},
		PCMOutput: func(pcm []float32, pts int64, channels int, start bool) {
			r.add(func(in bool) { r.pcm = append(r.pcm, startPCM{pcm[0], pts, len(pcm) / channels, start, in}) })
		},
	}
}

func (r *startRec) snapshot() ([]startOut, []startOut, []startPCM) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]startOut(nil), r.video...), append([]startOut(nil), r.aac...), append([]startPCM(nil), r.pcm...)
}

// A cued player started on a program tick puts its first picture and its
// first audio out stamped with that tick's read point before the start
// returns, so the tick shows the clip's first picture and plays clip
// sample 0, however late in the period the tick runs. The goroutine goes
// on from the next slot and never puts the first one out again.
func TestCuedPlayerStartsOnTheTickItIsStartedOn(t *testing.T) {
	now := fakeTAI(t)
	for _, r := range startFrameRates {
		for _, sr := range startSampleRates {
			for _, phase := range []int64{0, 1, 32, 63} { // 64ths of a period into the tick
				t.Run(fmt.Sprintf("%d_%d/%dHz/%d_64", r.Num, r.Den, sr, phase), func(t *testing.T) {
					n := clock.TimestampToIndex(r, clock.TAINow()) + 8
					period := clock.IndexToTimestamp(r, n+1) - clock.IndexToTimestamp(r, n)
					now.Store(clock.IndexToTimestamp(r, n-1))
					frames, audio := startClip(r, sr, 8)
					rec := &startRec{}
					p := NewPlayer(rec.config(r, sr, frames, audio))
					ctx, cancel := context.WithCancel(context.Background())
					p.Start(ctx)
					t.Cleanup(func() { cancel(); p.Stop(); p.Wait() })
					require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) == 1 }, 2*time.Second, time.Millisecond)

					now.Store(clock.IndexToTimestamp(r, n) + phase*period/64)
					read := timelineRead(r, clock.IndexToTimestamp(r, n))
					rec.shut(t)
					require.True(t, p.StartAt(read))
					assert.Equal(t, StatePlaying, p.State())
					assert.False(t, p.StartAt(read), "a started player does not start again")
					p.Release()
					v, _, _ := rec.snapshot()
					require.Len(t, v, 2, "the start puts out its first picture before it returns")
					rec.open()

					require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) >= 4 }, 2*time.Second, time.Millisecond)
					v, aac, pcm := rec.snapshot()
					assert.False(t, v[0].inStart, "the still")
					require.Equal(t, startOut{0, read, true}, v[1], "the first picture goes out on the tick's read point as the tick starts it")
					step := ptsStep{num: r.Num, den: r.Den}
					want := read
					for k := 1; k < 3; k++ {
						want += step.next()
						require.Equal(t, startOut{byte(pictureAt(frames, r, k)), want, false}, v[1+k], "slot %d", k)
					}
					for _, f := range v[2:] {
						require.Greater(t, f.pts, read, "the first slot went out once")
					}

					require.NotEmpty(t, aac)
					assert.Equal(t, startOut{0, read, true}, aac[0], "the first AAC frame starts on the read point")

					// Clip sample 0 is stamped with the read point, and the
					// audio the tick reads is there before the tick reads it.
					require.NotEmpty(t, pcm)
					assert.True(t, pcm[0].start && pcm[0].inStart)
					assert.Equal(t, read, pcm[0].pts, "clip sample 0 is on the read point")
					ra := clock.Rate{Num: int64(sr), Den: 1}
					ri := clock.PTSIndex(r, read)
					tickSamples := clock.TimestampToIndex(ra, clock.IndexToTimestamp(r, ri+1)) - clock.TimestampToIndex(ra, clock.IndexToTimestamp(r, ri))
					var handed, put int
					for i, c := range pcm {
						if c.inStart {
							handed += c.samples
						}
						require.Equal(t, read+int64(put)*90000/int64(sr), c.pts, "chunk %d follows the one before", i)
						require.Equal(t, float32(put+1), c.first, "chunk %d starts with clip sample %d", i, put)
						assert.Equal(t, i == 0, c.start)
						put += c.samples
					}
					assert.GreaterOrEqual(t, handed, int(tickSamples), "the audio of the start tick is handed over with the start")
				})
			}
		}
	}
}

// Released first, a player is not started again on a tick: the start goes
// out once, the way Release put it.
func TestStartAtAfterReleaseLeavesTheStartAlone(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	frames, audio := startClip(r, 48000, 8)
	rec := &startRec{}
	p := NewPlayer(rec.config(r, 48000, frames, audio))
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	defer func() { cancel(); p.Stop(); p.Wait() }()
	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) == 1 }, 2*time.Second, time.Millisecond)

	p.Release()
	assert.False(t, p.StartAt(timelineRead(r, clock.TAINow())))
	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) >= 4 }, 2*time.Second, time.Millisecond)
	v, _, _ := rec.snapshot()
	for i, f := range v[1:] {
		assert.False(t, f.inStart)
		assert.Equal(t, byte(pictureAt(frames, r, i)), f.id, "slot %d", i)
	}
}

// A timed manager starts a slot's cue on the tick StartCuedAt names, and
// the slot plays it from then on, with the loop setting it holds.
func TestTimedManagerStartsACueOnTheTick(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := NewManager(store, ManagerConfig{
		DemuxFunc: func(string) ([]BufferedFrame, []BufferedAudioFrame, error) {
			f, a := cueClip(50, 47)
			return f, a, nil
		},
		OutputRate: func() (int, int) { return 50, 1 },
		Timed:      true,
	})
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	var mu sync.Mutex
	var video []startOut
	mgr.SetRawVideoOutput(func(_ string, yuv []byte, _, _ int, pts int64, _ bool) {
		mu.Lock()
		video = append(video, startOut{id: yuv[0], pts: pts})
		mu.Unlock()
	})
	got := func() []startOut {
		mu.Lock()
		defer mu.Unlock()
		return append([]startOut(nil), video...)
	}

	mgr.StartCuedAt(1, 0) // an empty slot starts nothing
	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.SetLoop(1, true))
	require.Eventually(t, func() bool { return len(got()) == 1 }, 2*time.Second, time.Millisecond)

	read := timelineRead(r, clock.TAINow())
	mgr.StartCuedAt(1, read)
	v := got()
	// The goroutine may put out the slot after once the start returns.
	require.GreaterOrEqual(t, len(v), 2, "the start puts the first picture out before it returns")
	assert.Equal(t, startOut{id: 0, pts: read}, v[1])
	require.Eventually(t, func() bool { return mgr.PlayerStates()[0].State == StatePlaying }, 2*time.Second, time.Millisecond)
	assert.True(t, mgr.PlayerStates()[0].Loop)
	assert.InDelta(t, 1.0, mgr.PlayerStates()[0].Speed, 0)

	mgr.StartCuedAt(1, read+1800) // playing: no second start
	require.Eventually(t, func() bool { return len(got()) >= 4 }, 2*time.Second, time.Millisecond)
	for _, f := range got()[2:] {
		assert.Greater(t, f.pts, read)
	}
}

// The PCM a cued player decodes ahead of its start was cut to the loop
// setting the cue had: with the setting changed since, the start decodes
// its PCM anew, and plays it from clip sample 0 on the read point all the
// same.
func TestCuedPlayerStartsItsSoundAnewAfterALoopChange(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	for _, loop := range []bool{false, true} {
		t.Run(fmt.Sprintf("loop_%v", loop), func(t *testing.T) {
			frames, audio := startClip(r, 48000, 8)
			rec := &startRec{}
			p := NewPlayer(rec.config(r, 48000, frames, audio))
			ctx, cancel := context.WithCancel(context.Background())
			p.Start(ctx)
			t.Cleanup(func() { cancel(); p.Stop(); p.Wait() })
			require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) == 1 }, 2*time.Second, time.Millisecond)
			p.SetLoop(loop)

			read := timelineRead(r, clock.TAINow())
			rec.shut(t)
			require.True(t, p.StartAt(read))
			_, _, pcm := rec.snapshot()
			rec.open()
			require.NotEmpty(t, pcm)
			assert.Equal(t, startPCM{first: 1, pts: read, samples: pcm[0].samples, start: true, inStart: true}, pcm[0])
			var n int
			for _, c := range pcm {
				n += c.samples
			}
			assert.GreaterOrEqual(t, n, 960, "the start tick's audio goes out with the start")
		})
	}
}

// onStarter counts the decoder calls a goroutine makes, once it is set.
type onStarter struct {
	id    atomic.Value // string, the goroutine's ID
	calls atomic.Int64
}

func (o *onStarter) note() {
	if id, _ := o.id.Load().(string); id != "" && id == goid() {
		o.calls.Add(1)
	}
}

// codec is startRampCodec with every decoder made and every frame decoded on
// the goroutine counted.
func (o *onStarter) codec() PCMCodec {
	return PCMCodec{NewDecoder: func(sr, channels int) (PCMDecoder, error) {
		o.note()
		d, err := startRampCodec.NewDecoder(sr, channels)
		return countingDecoder{d, o}, err
	}}
}

type countingDecoder struct {
	PCMDecoder
	o *onStarter
}

func (d countingDecoder) Decode(b []byte) ([]float32, error) {
	d.o.note()
	return d.PCMDecoder.Decode(b)
}

// A slot is loaded without a loop, and its loop is set while it is cued,
// as an operator's loop tick or a peer engine's sync sets it. The start on
// a program tick then hands over the PCM the cue decoded, in the loop
// mode the start has, and decodes nothing on the tick: decoding there
// opened a decoder and a resampler, which at a high rate costs the tick
// more than its period. The start tick's PCM reaches the slot after
// however its PTS rounds.
func TestCuedPlayerStartsInTheLoopModeSetAfterItsCue(t *testing.T) {
	now := fakeTAI(t)
	for _, r := range startFrameRates {
		for _, sr := range startSampleRates {
			for _, loop := range []bool{true, false} {
				t.Run(fmt.Sprintf("%d_%d/%dHz/loop_%v", r.Num, r.Den, sr, loop), func(t *testing.T) {
					n := clock.TimestampToIndex(r, clock.TAINow()) + 8
					now.Store(clock.IndexToTimestamp(r, n-1))
					frames, audio := startClip(r, sr, 8)
					rec := &startRec{}
					cfg := rec.config(r, sr, frames, audio)
					cfg.Loop = !loop
					on := &onStarter{}
					cfg.PCMCodec = on.codec()
					// Where the audio's first pass ends, as the start hands
					// over its PCM: the goroutine waits for the start then.
					var p *Player
					var audioEnd, passLen, want int64
					pcmOut := cfg.PCMOutput
					cfg.PCMOutput = func(pcm []float32, pts int64, channels int, start bool) {
						if start && on.calls.Load() == 0 {
							if id, _ := on.id.Load().(string); id == goid() {
								audioEnd, passLen, want = p.pcmAudioEnd, p.pcm.passLen, p.pcm.Samples()
								if loop {
									want = p.pcmVideoEnd
								}
							}
						}
						pcmOut(pcm, pts, channels, start)
					}
					p = NewPlayer(cfg)
					ctx, cancel := context.WithCancel(context.Background())
					p.Start(ctx)
					t.Cleanup(func() { cancel(); p.Stop(); p.Wait() })
					require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) == 1 }, 2*time.Second, time.Millisecond)

					p.SetLoop(loop)
					now.Store(clock.IndexToTimestamp(r, n))
					read := timelineRead(r, clock.IndexToTimestamp(r, n))
					rec.shut(t)
					on.id.Store(goid())
					require.True(t, p.StartAt(read))
					assert.Zero(t, on.calls.Load(), "the start decodes nothing on the tick")
					_, _, pcm := rec.snapshot()
					rec.open()

					require.NotEmpty(t, pcm, "the start hands over the start tick's PCM")
					assert.Equal(t, startPCM{first: 1, pts: read, samples: pcm[0].samples, start: true, inStart: true}, pcm[0])
					var handed int64
					for _, c := range pcm {
						handed += int64(c.samples)
					}
					period := int64(90000) * r.Den / r.Num
					assert.Equal(t, (period+1)*int64(sr)/90000+2, handed, "the start hands over the cue's span")
					ra := clock.Rate{Num: int64(sr), Den: 1}
					next := clock.TimestampToIndex(ra, clock.TimestampOf(read+period+1))
					assert.Greater(t, handed, int64(next-clock.TimestampToIndex(ra, clock.TimestampOf(read))), "the PCM reaches the slot after")

					require.NotZero(t, want)
					assert.Equal(t, want, audioEnd, "the audio's pass ends where the loop mode has it end")
					assert.Equal(t, want, passLen)
				})
			}
		}
	}
}

// A clip shorter than about a period, cued without a loop and started
// with one, has its cue read past where the looping pass ends. The start
// then hands over no PCM and still decodes nothing on the tick: the
// goroutine starts the clip's sound on the start's read point instead.
func TestCuedPlayerOfAShortClipStartsItsSoundOffTheTick(t *testing.T) {
	now := fakeTAI(t)
	r := clock.Rate{Num: 24, Den: 1}
	n := clock.TimestampToIndex(r, clock.TAINow()) + 8
	now.Store(clock.IndexToTimestamp(r, n-1))
	frames, audio := startClip(clock.Rate{Num: 30, Den: 1}, 48000, 1)
	rec := &startRec{}
	cfg := rec.config(r, 48000, frames, audio)
	on := &onStarter{}
	cfg.PCMCodec = on.codec()
	p := NewPlayer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	t.Cleanup(func() { cancel(); p.Stop(); p.Wait() })
	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) == 1 }, 2*time.Second, time.Millisecond)

	p.SetLoop(true)
	now.Store(clock.IndexToTimestamp(r, n))
	read := timelineRead(r, clock.IndexToTimestamp(r, n))
	rec.shut(t)
	on.id.Store(goid())
	require.True(t, p.StartAt(read))
	assert.Zero(t, on.calls.Load(), "the start decodes nothing on the tick")
	_, _, pcm := rec.snapshot()
	assert.Empty(t, pcm, "a cue read past the looping pass is not handed over")
	rec.open()

	require.Eventually(t, func() bool { _, _, pcm := rec.snapshot(); return len(pcm) > 0 }, 2*time.Second, time.Millisecond)
	_, _, pcm = rec.snapshot()
	assert.Equal(t, startPCM{first: 1, pts: read, samples: pcm[0].samples, start: true, inStart: false}, pcm[0])
}

// A staged picture (StageVideo) is copied while the player waits: the
// start on a program tick hands it over stamped with the tick's read
// point and copies nothing, and every still is staged too, so the start
// never waits on a still's copy. Stills staged and not put out are
// dropped.
func TestCuedPlayerHandsItsStagedPictureToTheTick(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	frames, audio := startClip(r, 48000, 8)
	rec := &startRec{}
	cfg := rec.config(r, 48000, frames, audio)
	var mu sync.Mutex
	var starter string
	var copiedOnTick, staged, dropped int
	var put []int64
	raw := cfg.RawVideoOutput
	cfg.RawVideoOutput = func(yuv []byte, w, h int, pts int64, kf bool) {
		mu.Lock()
		if goid() == starter {
			copiedOnTick++
		}
		mu.Unlock()
		raw(yuv, w, h, pts, kf)
	}
	cfg.StageVideo = func(yuv []byte, _, _ int, _ bool) (func(int64), func()) {
		mu.Lock()
		defer mu.Unlock()
		if goid() == starter {
			copiedOnTick++
		}
		staged++
		return func(pts int64) {
				mu.Lock()
				put = append(put, pts)
				mu.Unlock()
			}, func() {
				mu.Lock()
				dropped++
				mu.Unlock()
			}
	}
	p := NewPlayer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	t.Cleanup(func() { cancel(); p.Stop(); p.Wait() })
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(put) == 1 }, 2*time.Second, time.Millisecond, "the still goes out staged")

	read := timelineRead(r, clock.TAINow())
	mu.Lock()
	starter = goid()
	mu.Unlock()
	require.True(t, p.StartAt(read))
	mu.Lock()
	assert.Zero(t, copiedOnTick, "the start copies no picture on the tick")
	require.Len(t, put, 2)
	assert.Equal(t, read, put[1], "the staged picture goes out on the read point")
	mu.Unlock()
	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) >= 2 }, 2*time.Second, time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, staged, len(put)+dropped, "every staged picture is put out or dropped")
}

// A program tick starts a slot's cue before the manager makes it the
// slot's player (playCue): a stop, a pause or a seek in between finds
// the clip playing, and the slot reads as playing.
func TestTimedManagerTakesACueTheTickStarted(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := NewManager(store, ManagerConfig{
		DemuxFunc: func(string) ([]BufferedFrame, []BufferedAudioFrame, error) {
			f, a := cueClip(50, 47)
			return f, a, nil
		},
		OutputRate: func() (int, int) { return 50, 1 },
		Timed:      true,
	})
	defer mgr.Close()
	c := addTestClip(t, store, dir)
	var stills atomic.Int64
	mgr.SetRawVideoOutput(func(string, []byte, int, int, int64, bool) { stills.Add(1) })
	require.NoError(t, mgr.Load(1, c.ID))
	require.Eventually(t, func() bool { return stills.Load() > 0 }, 2*time.Second, time.Millisecond)

	// The start StartCuedAt makes, without the playCue it leaves to run.
	cue := mgr.cue[0].Load()
	require.NotNil(t, cue)
	require.True(t, cue.StartAt(timelineRead(clock.Rate{Num: 50, Den: 1}, clock.TAINow())))
	assert.Equal(t, StatePlaying, mgr.PlayerStates()[0].State, "the slot reads as playing")
	require.NoError(t, mgr.Pause(1), "the clip the tick started pauses")
	require.NoError(t, mgr.Stop(1), "and stops")
	assert.Equal(t, StateLoaded, mgr.PlayerStates()[0].State)
}

// stagedCount counts the put and drop calls of each frame a player
// staged (StageVideo).
type stagedCount struct {
	mu    sync.Mutex
	calls []int
}

func (s *stagedCount) stage(on func()) func([]byte, int, int, bool) (func(int64), func()) {
	return func([]byte, int, int, bool) (func(int64), func()) {
		s.mu.Lock()
		k := len(s.calls)
		s.calls = append(s.calls, 0)
		s.mu.Unlock()
		if k == 0 && on != nil {
			on()
		}
		done := func() { s.mu.Lock(); s.calls[k]++; s.mu.Unlock() }
		return func(int64) { done() }, done
	}
}

// A frame a player stages is a pool buffer: it goes to the pipeline (put)
// or back (drop), once, however the start comes, a release or a tick
// landing while the cue still stages its picture included. Released
// then, the cue took the start without a still and kept the staged
// buffer for good.
func TestCuedPlayerPutsOrDropsEveryStagedFrameOnce(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	for _, tc := range []struct {
		name    string
		staging func(p *Player) // runs as the cue stages its picture
		after   func(p *Player) // runs once the cue is out
	}{
		{"release while staging", func(p *Player) { p.Release() }, nil},
		{"tick while staging", func(p *Player) { p.StartAt(timelineRead(r, clock.TAINow())) }, nil},
		{"release after the cue", nil, func(p *Player) { p.Release() }},
		{"tick after the cue", nil, func(p *Player) { p.StartAt(timelineRead(r, clock.TAINow())) }},
		{"stop while cued", nil, func(*Player) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames, audio := startClip(r, 48000, 8)
			rec := &startRec{}
			cfg := rec.config(r, 48000, frames, audio)
			var p *Player
			sc := &stagedCount{}
			cfg.StageVideo = sc.stage(func() {
				if tc.staging != nil {
					tc.staging(p)
				}
			})
			p = NewPlayer(cfg)
			ctx, cancel := context.WithCancel(context.Background())
			p.Start(ctx)
			if tc.staging == nil {
				// The still goes out staged: the first frame staged is the
				// one held for the start, the second the still's.
				require.Eventually(t, func() bool {
					sc.mu.Lock()
					defer sc.mu.Unlock()
					return len(sc.calls) > 1 && sc.calls[1] > 0
				}, 2*time.Second, time.Millisecond)
				tc.after(p)
			}
			if tc.name != "stop while cued" {
				// The playback goes on through RawVideoOutput.
				require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) >= 2 }, 2*time.Second, time.Millisecond)
			}
			cancel()
			p.Stop()
			p.Wait()

			sc.mu.Lock()
			defer sc.mu.Unlock()
			require.NotEmpty(t, sc.calls)
			for k, n := range sc.calls {
				assert.Equal(t, 1, n, "staged frame %d is put out or dropped once", k)
			}
		})
	}
}

// The start on a program tick reports itself (OnStart) from the player's
// goroutine, as it goes on, not on the tick: a report logs and updates
// metrics. The delay each report carries is still the one from the
// release to the tick that started the clip, not to the report.
func TestCuedPlayerReportsATickStartOffTheTick(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	frames, audio := startClip(r, 48000, 8)
	rec := &startRec{}
	cfg := rec.config(r, 48000, frames, audio)
	const slow = 30 * time.Millisecond
	type report struct {
		media   string
		delay   time.Duration
		onStart bool // on the goroutine that called StartAt
	}
	var mu sync.Mutex
	var starter string
	var reports []report
	cfg.OnStart = func(media string, delay time.Duration, _ string) {
		mu.Lock()
		reports = append(reports, report{media, delay, goid() == starter})
		first := len(reports) == 1
		mu.Unlock()
		if first {
			// A report measured as it is made would carry this.
			time.Sleep(slow)
		}
	}
	p := NewPlayer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	t.Cleanup(func() { cancel(); p.Stop(); p.Wait() })
	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) == 1 }, 2*time.Second, time.Millisecond)

	mu.Lock()
	starter = goid()
	mu.Unlock()
	t0 := time.Now()
	require.True(t, p.StartAt(timelineRead(r, clock.TAINow())))
	onTick := time.Since(t0)
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(reports) == 2 }, 2*time.Second, time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for _, rp := range reports {
		assert.False(t, rp.onStart, "%s is reported on the tick", rp.media)
		assert.LessOrEqual(t, rp.delay, onTick, "%s delay runs to the tick, not to the report", rp.media)
	}
	assert.Equal(t, "video", reports[0].media)
	assert.Equal(t, "audio", reports[1].media)
}
