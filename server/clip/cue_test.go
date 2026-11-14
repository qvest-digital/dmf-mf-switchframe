package clip

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
)

// cueClip returns n pictures at 50 fps, each carrying its index as its
// only byte, and a AAC frames at 48 kHz, each carrying its index.
func cueClip(n, a int) ([]BufferedFrame, []BufferedAudioFrame) {
	frames := make([]BufferedFrame, n)
	for i := range frames {
		frames[i] = BufferedFrame{wireData: []byte{byte(i)}, pts: int64(i) * 1800, isKeyframe: i == 0}
	}
	audio := make([]BufferedAudioFrame, a)
	for i := range audio {
		audio[i] = BufferedAudioFrame{data: []byte{byte(i)}, pts: int64(i) * 1920, sampleRate: 48000, channels: 2}
	}
	return frames, audio
}

type outRec struct {
	id  byte
	pts int64
	at  int64 // TAI of the output
}

type cueRecorder struct {
	mu     sync.Mutex
	video  []outRec
	audio  []outRec
	starts []string
}

func (r *cueRecorder) config(frames []BufferedFrame, audio []BufferedAudioFrame) PlayerConfig {
	return PlayerConfig{
		Clip:         frames,
		AudioClip:    audio,
		OutputFPSNum: 50,
		OutputFPSDen: 1,
		Timed:        true,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			r.mu.Lock()
			r.video = append(r.video, outRec{yuv[0], pts, clock.TAINow()})
			r.mu.Unlock()
		},
		AudioOutput: func(data []byte, pts int64, sampleRate, channels int) {
			r.mu.Lock()
			r.audio = append(r.audio, outRec{data[0], pts, clock.TAINow()})
			r.mu.Unlock()
		},
		OnStart: func(media string, delay time.Duration, preroll string) {
			r.mu.Lock()
			r.starts = append(r.starts, media+" "+preroll)
			r.mu.Unlock()
		},
	}
}

func (r *cueRecorder) snapshot() ([]outRec, []outRec, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]outRec(nil), r.video...), append([]outRec(nil), r.audio...), append([]string(nil), r.starts...)
}

// A cued player shows its first picture and waits; released with no
// program tick to start on, it puts that picture and its audio on the
// read point of the next program tick, before that tick, so the tick
// shows the clip's first picture and plays its first audio. Without the
// cue the start decodes first and lands ticks later; on the arrival
// timeline it lands ReaderDelayGrains ticks later.
func TestCuedPlayerAirsItsFirstFrameAndAudioOnTheNextTick(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	period := clock.IndexToTimestamp(r, 1) - clock.IndexToTimestamp(r, 0)
	frames, audio := cueClip(50, 47)
	rec := &cueRecorder{}
	cfg := rec.config(frames, audio)
	cfg.Cued = true
	p := NewPlayer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer func() { p.Stop(); p.Wait() }()

	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) == 1 }, 2*time.Second, time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	v, a, _ := rec.snapshot()
	require.Len(t, v, 1, "a cued player shows its first picture once until a second has passed")
	require.Empty(t, a, "a cued player plays no audio")
	assert.Equal(t, byte(0), v[0].id)
	assert.LessOrEqual(t, v[0].pts, timelineRead(r, clock.TAINow()), "the still is due at the tick running")
	assert.Equal(t, StateLoaded, p.State())

	released := clock.TAINow()
	p.Release()
	assert.Equal(t, StatePlaying, p.State())
	require.Eventually(t, func() bool { v, a, _ := rec.snapshot(); return len(v) >= 4 && len(a) >= 3 }, 2*time.Second, time.Millisecond)
	v, a, starts := rec.snapshot()

	first := v[1]
	assert.Equal(t, byte(0), first.id)
	// The tick that reads the first frame: at most the one after next,
	// and only when the next was within half a period of the release.
	tick := clock.PTSIndex(r, first.pts) + clock.ReaderDelayGrains
	tickAt := clock.IndexToTimestamp(r, tick)
	assert.Equal(t, first.pts, clock.IndexPTS(r, tick-clock.ReaderDelayGrains), "the first frame is on a tick's read point")
	assert.Greater(t, tickAt, released, "the tick reading the first frame comes after the release")
	assert.LessOrEqual(t, tickAt, first.at+period+period/2, "the first frame is read by the next tick, or the one after when the next is too close")
	assert.Less(t, first.at, tickAt, "the first frame is there before its tick")
	assert.Equal(t, byte(1), v[2].id)
	assert.Equal(t, first.pts+1800, v[2].pts)

	// The audio starts with the first frame, and the audio the tick reads,
	// one period from the first frame's PTS, is there before the tick.
	assert.Equal(t, byte(0), a[0].id)
	assert.Equal(t, first.pts, a[0].pts)
	var covered int64
	for _, f := range a {
		if f.at < tickAt {
			covered = f.pts + 1920
		}
	}
	assert.GreaterOrEqual(t, covered, first.pts+1800, "the first tick's audio is buffered before it")
	assert.Equal(t, []string{"video ready", "audio ready"}, starts)
}

// Released before its first picture is decoded, a cued player starts as
// soon as it is, and says it waited.
func TestCuedPlayerReleasedEarlyReportsWaited(t *testing.T) {
	frames, audio := cueClip(10, 9)
	rec := &cueRecorder{}
	cfg := rec.config(frames, audio)
	cfg.Cued = true
	p := NewPlayer(cfg)
	p.Release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer func() { p.Stop(); p.Wait() }()
	require.Eventually(t, func() bool { _, _, s := rec.snapshot(); return len(s) == 2 }, 2*time.Second, time.Millisecond)
	v, _, s := rec.snapshot()
	assert.Equal(t, []string{"video waited", "audio waited"}, s)
	assert.Equal(t, byte(0), v[0].id, "no still before the start")
	assert.Equal(t, byte(1), v[1].id)
}

// A looping timed player's audio is one stream: every AAC frame follows
// the one before it without a gap or an overlap, across every loop, and
// each pass of it starts within half an AAC frame of the video's pass,
// although the clip's audio runs an AAC frame past its video. Placed by
// these PTS, the program audio of the clip has no discontinuity.
func TestTimedPlayerAudioRunsOnUnbrokenAcrossLoops(t *testing.T) {
	// 32 pictures at 50 fps are 0.64 s, 30 AAC frames at 48 kHz; the
	// clip carries 31.
	frames, audio := cueClip(32, 31)
	rec := &cueRecorder{}
	cfg := rec.config(frames, audio)
	cfg.Loop = true
	p := NewPlayer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer func() { p.Stop(); p.Wait() }()

	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) >= 3*32+4 }, 5*time.Second, 5*time.Millisecond)
	v, a, _ := rec.snapshot()

	for i := 1; i < len(v); i++ {
		require.Equal(t, v[i-1].pts+1800, v[i].pts, "video frame %d", i)
	}
	for i := 1; i < len(a); i++ {
		require.Equal(t, a[i-1].pts+1920, a[i].pts, "audio frame %d follows the one before", i)
	}
	var passes []int64
	for _, f := range v {
		if f.id == 0 {
			passes = append(passes, f.pts)
		}
	}
	var audioPasses []int64
	for _, f := range a {
		if f.id == 0 {
			audioPasses = append(audioPasses, f.pts)
		}
	}
	require.GreaterOrEqual(t, len(passes), 3)
	require.GreaterOrEqual(t, len(audioPasses), 3)
	for i := 0; i < 3; i++ {
		assert.InDelta(t, passes[i], audioPasses[i], 960, "pass %d", i)
	}
	// The audio is handed over ahead of the video it plays with.
	for _, f := range v[1:] {
		var ahead int64
		for _, af := range a {
			if af.at <= f.at {
				ahead = af.pts + 1920
			}
		}
		require.GreaterOrEqual(t, ahead, f.pts, "audio up to frame PTS %d was out with it", f.pts)
	}
}

// A timed player that was paused resumes on the clock: its frames and
// audio take the PTS of their new time rather than the old ones, which
// the program ticks have passed.
func TestTimedPlayerResumesOnTheClock(t *testing.T) {
	frames, audio := cueClip(200, 188)
	rec := &cueRecorder{}
	p := NewPlayer(rec.config(frames, audio))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer func() { p.Stop(); p.Wait() }()

	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) >= 3 }, 2*time.Second, time.Millisecond)
	p.Pause()
	time.Sleep(300 * time.Millisecond)
	n, _, _ := rec.snapshot()
	p.Resume()
	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) >= len(n)+2 }, 2*time.Second, time.Millisecond)
	v, a, _ := rec.snapshot()
	resumed := v[len(n)]
	assert.GreaterOrEqual(t, resumed.pts, clock.PTS(resumed.at)-2*1800, "the frame after the pause is due no earlier than its tick")
	last := a[len(a)-1]
	assert.InDelta(t, resumed.pts, last.pts, 2*1920+1800, "the audio moved with the video")
}

// With Timed, a slot's source exists from the load of its clip to its
// eject, so a cut can take it before it plays: the clip's first picture
// is out as a still at once, StartCued starts it from the cue with the
// loop setting the slot holds, and a stop cues it again rather than
// removing the source.
func TestTimedManagerCuesALoadedSlotAndStartsIt(t *testing.T) {
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
	var events []string
	var video []byte
	mgr.OnPlayerLifecycle(
		func(id int, key string) { mu.Lock(); events = append(events, "start "+key); mu.Unlock() },
		func(id int, key string) { mu.Lock(); events = append(events, "stop "+key); mu.Unlock() },
	)
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		mu.Lock()
		if len(events) == 0 {
			events = append(events, "frame before the source")
		}
		video = append(video, yuv[0])
		mu.Unlock()
	})
	got := func() ([]string, []byte) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), events...), append([]byte(nil), video...)
	}

	require.NoError(t, mgr.Load(2, c.ID))
	require.NoError(t, mgr.SetLoop(2, true))
	require.Eventually(t, func() bool { _, v := got(); return len(v) == 1 }, 2*time.Second, time.Millisecond)
	ev, _ := got()
	assert.Equal(t, []string{"start clip:2"}, ev, "the source exists once the clip is loaded")
	assert.Equal(t, StateLoaded, mgr.PlayerStates()[1].State)

	assert.False(t, mgr.StartCued(1), "an empty slot does not start")
	require.True(t, mgr.StartCued(2))
	st := mgr.PlayerStates()[1]
	assert.Equal(t, StatePlaying, st.State)
	assert.True(t, st.Loop, "the start keeps the loop setting the slot was loaded with")
	assert.False(t, mgr.StartCued(2), "a playing slot is not started again")
	require.Eventually(t, func() bool { _, v := got(); return len(v) >= 3 }, 2*time.Second, time.Millisecond)
	_, v := got()
	assert.Equal(t, []byte{0, 0, 1}, v[:3], "the still, then the clip from its first picture")

	require.NoError(t, mgr.Stop(2))
	assert.Equal(t, StateLoaded, mgr.PlayerStates()[1].State)
	n := len(v)
	require.Eventually(t, func() bool { _, v := got(); return len(v) > n }, 2*time.Second, time.Millisecond)
	_, v = got()
	assert.Equal(t, byte(0), v[len(v)-1], "stopped, the slot shows its first picture again")

	require.NoError(t, mgr.Eject(2))
	ev, _ = got()
	assert.Equal(t, []string{"start clip:2", "stop clip:2"}, ev)

	var m dto.Metric
	require.NoError(t, mgr.starts.starts.WithLabelValues("2", "ready").Write(&m))
	assert.Equal(t, 1.0, m.GetCounter().GetValue())
}

// A timed clip that ends without a loop holds its last picture: each
// repeat is stamped with the read point of the tick running when it goes
// out, as the cue still is, so its PTS stays on the clock. Stamped a
// second on from the frame before, a repeat fell a second behind the
// clock at the first and further behind with each one late.
func TestTimedPlayerHoldsItsLastFrameOnTheTickRunning(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	frames, audio := cueClip(5, 4)
	rec := &cueRecorder{}
	p := NewPlayer(rec.config(frames, audio))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer func() { p.Stop(); p.Wait() }()

	require.Eventually(t, func() bool { v, _, _ := rec.snapshot(); return len(v) >= 7 }, 4*time.Second, 5*time.Millisecond)
	assert.Equal(t, StateHolding, p.State())
	v, _, _ := rec.snapshot()
	prev := v[4].pts
	for _, f := range v[5:7] {
		assert.Equal(t, byte(4), f.id, "the hold repeats the last picture")
		read := timelineRead(r, f.at)
		assert.LessOrEqual(t, f.pts, read, "a repeat is due at the tick running when it goes out")
		assert.Greater(t, f.pts, read-1800, "a repeat is on the clock, not a second behind it")
		assert.Greater(t, f.pts, prev, "a repeat is newer than the frame before it")
		prev = f.pts
	}
}

// A start on no program tick lands on the next tick's read point unless
// that tick starts within half a period, which leaves too little time to
// put out the first frame and audio before it; then it lands on the tick
// after. The margin is a share of the period, so it neither skips a tick
// at a high rate nor leaves a low one short.
func TestTimelineStartLeavesHalfAPeriodBeforeTheTick(t *testing.T) {
	const i = 1_000_000
	for _, r := range startFrameRates {
		tick := clock.IndexToTimestamp(r, i)
		period := tick - clock.IndexToTimestamp(r, i-1)
		for _, tc := range []struct {
			name   string
			before int64 // in 64ths of a period
			want   uint64
		}{
			{"just before a tick takes the one after", 1, i + 1},
			{"short of half a period before takes the one after", 31, i + 1},
			{"past half a period before takes it", 33, i},
			{"just after the tick before takes it", 63, i},
			{"on a tick takes the next", 0, i + 1},
		} {
			t.Run(fmt.Sprintf("%d_%d/%s", r.Num, r.Den, tc.name), func(t *testing.T) {
				got := timelineStart(r, tick-tc.before*period/64)
				// The read point of tick want, however far behind its
				// grain the program reads at rate r.
				want := timelineRead(r, clock.IndexToTimestamp(r, tc.want))
				assert.Equal(t, want, got)
			})
		}
	}
}

// Ejecting a playing timed slot stops its player before its source goes:
// a frame put out after the source was removed would find none.
func TestTimedManagerEjectStopsThePlayerBeforeTheSource(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := NewManager(store, ManagerConfig{
		DemuxFunc: func(string) ([]BufferedFrame, []BufferedAudioFrame, error) {
			f, a := cueClip(500, 470)
			return f, a, nil
		},
		OutputRate: func() (int, int) { return 50, 1 },
		Timed:      true,
	})
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	var mu sync.Mutex
	var frames, afterStop int
	stopped := false
	mgr.SetRawVideoOutput(func(string, []byte, int, int, int64, bool) {
		mu.Lock()
		frames++
		if stopped {
			afterStop++
		}
		mu.Unlock()
	})
	mgr.OnPlayerLifecycle(func(int, string) {}, func(int, string) {
		mu.Lock()
		stopped = true
		mu.Unlock()
		// A player still running puts out a frame every 20 ms.
		time.Sleep(100 * time.Millisecond)
	})

	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 1, true))
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return frames >= 5 }, 2*time.Second, time.Millisecond)
	require.NoError(t, mgr.Eject(1))
	mu.Lock()
	defer mu.Unlock()
	assert.True(t, stopped)
	assert.Zero(t, afterStop, "no frame after the source was removed")
}
