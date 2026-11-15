package playout

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
)

// cueEvent is one thing a channel put out: its run placed on the engine
// timeline (place), a picture (video) or a chunk of PCM (pcm).
type cueEvent struct {
	kind   string
	pts    int64
	pic    int     // video: the picture's index in its clip
	sample float32 // pcm: the chunk's first sample
	start  bool    // pcm: a new stream
}

// cueRig is a manager whose channel 1 plays clips at fps, picture i of a
// clip carrying luma i+1 and its share of 48 kHz PCM numbered from 1, and
// records what the channel puts out.
type cueRig struct {
	m     *Manager
	fps   float64
	asRun *AsRunLogger

	mu     sync.Mutex
	events []cueEvent
	starts []string // onStart and onStop calls
	newEv  chan struct{}

	// startHook, when set before the call that offers or starts the
	// channel, runs once inside onStart before it is recorded.
	startHook func()
}

// cueClip is n pictures at fps with distinct pictures and 48 kHz stereo
// PCM whose sample k is k+1.
func cueClip(n int, fps float64) pcmIterator {
	const rate = 48000
	at := func(i int) int64 { return int64(float64(i)*rate/fps + 0.5) }
	frames := make([]DecodedFrame, n)
	for i := range frames {
		yuv := make([]byte, 16*16*3/2)
		yuv[0] = byte(i + 1)
		pcm := make([]float32, 2*(at(i+1)-at(i)))
		for k := 0; k < len(pcm); k += 2 {
			pcm[k] = float32(at(i) + int64(k/2) + 1)
			pcm[k+1] = pcm[k]
		}
		frames[i] = DecodedFrame{YUV: yuv, Width: 16, Height: 16, PTS: int64(float64(i)*90000/fps + 0.5), IsKeyframe: i == 0,
			AudioFrames: []AudioFrameData{{PCM: pcm, PTS: at(i) * 90000 / rate, SampleRate: rate, Channels: 2}}}
	}
	return pcmIterator{NewSliceIterator(frames), fps}
}

func newCueRig(t *testing.T, fps float64, preroll int, clips int) *cueRig {
	t.Helper()
	return newCueRigAt(t, fps, fps, preroll, clips)
}

// newCueRigAt is newCueRig with clips at clipFPS on a channel at fps.
func newCueRigAt(t *testing.T, fps, clipFPS float64, preroll int, clips int) *cueRig {
	t.Helper()
	assets := pcmAssets{}
	for _, id := range []string{"a", "b"} {
		assets["local:///clips/"+id+".ts"] = cueClip(clips, clipFPS)
	}
	r := &cueRig{fps: fps, newEv: make(chan struct{}, 1)}
	r.m = NewManager(ManagerConfig{AssetProvider: assets, AllowedLocalPrefixes: []string{"/"}, PrerollItems: preroll})
	t.Cleanup(r.m.Close)
	r.m.SetPipelineFormat(16, 16, fps)
	var err error
	r.asRun, err = NewAsRunLogger(AsRunLoggerConfig{Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.asRun.Close() })
	r.m.SetAsRunLogger(r.asRun)
	r.m.OnChannelLifecycle(
		func(_ int, key string) {
			if h := r.startHook; h != nil {
				r.startHook = nil
				h()
			}
			r.note(&r.starts, "start "+key)
		},
		func(_ int, key string) { r.note(&r.starts, "stop "+key) },
	)
	r.m.OnCuedStart(func(_ string, pts int64) {
		r.add(cueEvent{kind: "place", pts: pts})
	})
	r.m.SetRawVideoOutput(func(_ string, yuv []byte, _, _ int, pts int64, _ bool) {
		r.add(cueEvent{kind: "video", pts: pts, pic: int(yuv[0]) - 1})
	})
	r.m.SetPCMOutput(func(_ string, pcm []float32, pts int64, _ int, start bool) {
		r.add(cueEvent{kind: "pcm", pts: pts, sample: pcm[0], start: start})
	})
	return r
}

func (r *cueRig) note(to *[]string, s string) {
	r.mu.Lock()
	*to = append(*to, s)
	r.mu.Unlock()
}

func (r *cueRig) add(ev cueEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	select {
	case r.newEv <- struct{}{}:
	default:
	}
}

func (r *cueRig) snapshot() []cueEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]cueEvent(nil), r.events...)
}

// waitEvents waits until cond holds for what the channel put out.
func (r *cueRig) waitEvents(t *testing.T, what string, cond func([]cueEvent) bool) []cueEvent {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		if evs := r.snapshot(); cond(evs) {
			return evs
		}
		select {
		case <-r.newEv:
		case <-timeout:
			t.Fatalf("no %s", what)
		}
	}
}

func (r *cueRig) load(t *testing.T, ids ...string) {
	t.Helper()
	items := make([]PlaylistItem, len(ids))
	for i, id := range ids {
		items[i] = clipItem(id)
	}
	require.NoError(t, r.m.LoadPlaylist(1, testPlaylist(items...)))
}

// waitCue waits until channel 1 holds the first picture of the item it is
// cued on.
func (r *cueRig) waitCue(t *testing.T) {
	t.Helper()
	ch := r.m.cueChannels[0].Load()
	waitFor(t, 10*time.Second, func() bool { return ch.preroll.cue.Load() != nil })
}

// cut runs StartCuedAt as a program tick reading at readPTS does and
// returns what the channel put out before it returned. A run it starts
// puts nothing out before then: it waits for the cue the tick holds.
func (r *cueRig) cut(readPTS int64) []cueEvent {
	n := len(r.snapshot())
	r.m.StartCuedAt(1, readPTS)
	return r.snapshot()[n:]
}

func (r *cueRig) state() string { return r.m.ChannelStates()[0].State }

func (r *cueRig) itemStarts() []string {
	var ids []string
	one := 1
	for _, ev := range r.asRun.Query(AsRunQuery{Type: AsRunItemStart, ChannelID: &one}) {
		ids = append(ids, ev.ItemID)
	}
	return ids
}

// freezeWall stops channel 1's wall clock, so its run puts out the
// pictures due when it starts and no more.
func (r *cueRig) freezeWall() {
	at := time.Now()
	r.m.channels[0].wallNow = func() time.Time { return at }
}

// dueAtStart is how many pictures after the first a run started from a
// cue taken at readPTS puts out at once: those due up to a period ahead
// of the clock, the lead a run keeps.
func dueAtStart(readPTS int64, fps float64) int {
	lag := time.Duration(clock.NowPTS()-readPTS) * time.Second / 90000
	period := time.Duration(float64(time.Second) / fps)
	return int((lag + period) / period)
}

// scriptClock holds the engine clock two periods past readPTS, as a program
// tick reading at readPTS runs, and returns readPTS.
func scriptClock(t *testing.T, fps float64) int64 {
	t.Helper()
	clock.Epoch()
	now := clock.TAINow()
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	return clock.NowPTS() - int64(2*90000/fps)
}

// A cut or a mix to a cued channel starts it from the program tick that
// shows its source: before StartCuedAt returns, the item's first picture
// is out stamped with the tick's read point, and its sound from sample 0
// as a new stream stamped the same, so that tick shows and plays them. The
// run goes on from the picture after it, one period on, and the sound
// with no break; the first picture does not go out twice, and the item
// starts once in the as-run log. Started from the cut as Start starts it,
// the first picture came a period or more after the tick, stamped
// wherever the channel's clock had reached.
func TestCutStartsACuedChannelOnTheCutTick(t *testing.T) {
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3ffps", fps), func(t *testing.T) {
			readPTS := scriptClock(t, fps)
			r := newCueRig(t, fps, 1, 40)
			r.freezeWall()
			r.load(t, "a", "b")
			r.waitCue(t)
			require.Equal(t, "cued", r.state())
			require.Empty(t, r.snapshot(), "a cued channel puts nothing out")

			evs := r.cut(readPTS)
			require.GreaterOrEqual(t, len(evs), 3, "what the tick put out")
			require.Equal(t, cueEvent{kind: "place", pts: readPTS}, evs[0],
				"the run is placed on the read point before its first picture")
			require.Equal(t, cueEvent{kind: "video", pts: readPTS, pic: 0}, evs[1],
				"picture 0 goes out on the tick, stamped with its read point")
			require.Equal(t, cueEvent{kind: "pcm", pts: readPTS, sample: 1, start: true}, evs[2],
				"sample 0 goes out on the tick, a new stream stamped with its read point")

			// The run puts the pictures due out at once, with the wall
			// clock standing still: the read point is behind the clock,
			// and a run keeps a period's lead on it.
			due := dueAtStart(readPTS, fps)
			require.GreaterOrEqual(t, due, 2)
			evs = r.waitEvents(t, "the pictures due", func(evs []cueEvent) bool {
				return countKind(evs, "video") >= 1+due && countKind(evs, "pcm") >= 2
			})
			step, frac := (&Channel{config: ChannelConfig{FPS: fps}}).videoStep()
			next, _ := advancePTS(readPTS, 0, step, frac)
			var videos, pcms []cueEvent
			for _, ev := range evs {
				switch ev.kind {
				case "video":
					videos = append(videos, ev)
				case "pcm":
					pcms = append(pcms, ev)
				}
			}
			require.Equal(t, 1, countKind(evs, "place"), "the run is placed once")
			require.Equal(t, cueEvent{kind: "video", pts: next, pic: 1}, videos[1], "the run goes on with picture 1, a period on")
			pts, f := readPTS, int64(0)
			for k := 1; k <= due; k++ {
				pts, f = advancePTS(pts, f, step, frac)
				require.Equal(t, cueEvent{kind: "video", pts: pts, pic: k}, videos[k], "picture %d of the %d due", k, due)
			}
			first := int64(float64(48000)/fps + 0.5)
			require.Equal(t, cueEvent{kind: "pcm", pts: readPTS + first*90000/48000, sample: float32(first + 1)}, pcms[1],
				"the sound goes on from the sample after the first picture's")
			// Item a's pictures go out once each, in order; a slow host may
			// have reached item b, whose first picture is picture 0 again.
			for i, v := range videos[:min(len(videos), 40)] {
				require.Equal(t, i, v.pic, "picture %d of item a", i)
			}
			waitFor(t, 5*time.Second, func() bool { return len(r.itemStarts()) > 0 })
			require.Equal(t, "a", r.itemStarts()[0], "item a starts first")
			require.NotContains(t, r.itemStarts()[1:], "a", "item a starts once")
		})
	}
}

func countKind(evs []cueEvent, kind string) int {
	n := 0
	for _, ev := range evs {
		if ev.kind == kind {
			n++
		}
	}
	return n
}

// A channel cued on another item than its first, by JumpTo, starts on that
// item's first picture.
func TestCutStartsTheItemTheChannelIsCuedOn(t *testing.T) {
	readPTS := scriptClock(t, 50)
	r := newCueRig(t, 50, 1, 40)
	r.load(t, "a", "b")
	require.NoError(t, r.m.JumpTo(1, "b"))
	r.waitCue(t)
	evs := r.cut(readPTS)
	require.GreaterOrEqual(t, len(evs), 2)
	require.Equal(t, cueEvent{kind: "video", pts: readPTS, pic: 0}, evs[1])
	waitFor(t, 5*time.Second, func() bool { return len(r.itemStarts()) > 0 })
	require.Equal(t, []string{"b"}, r.itemStarts())
}

// An operator's Start that comes after the tick took the cue, before the
// start the cut makes, goes on from the cue as that one would: the first
// picture goes out once, on the tick.
func TestStartAfterTheCutGoesOnFromTheCue(t *testing.T) {
	readPTS := scriptClock(t, 50)
	r := newCueRig(t, 50, 1, 40)
	r.load(t, "a")
	r.waitCue(t)
	r.cut(readPTS)
	if err := r.m.Start(1); err != nil {
		require.ErrorIs(t, err, ErrAlreadyOnAir)
	}
	evs := r.waitEvents(t, "picture 2", func(evs []cueEvent) bool { return countKind(evs, "video") >= 3 })
	var pics []int
	for _, ev := range evs {
		if ev.kind == "video" {
			pics = append(pics, ev.pic)
		}
	}
	require.Equal(t, []int{0, 1, 2}, pics[:3])
	require.Equal(t, 1, countKind(evs, "place"))
	require.Equal(t, []string{"a"}, r.itemStarts())
}

// A cut to a cued channel whose first picture is not prepared, with no
// pre-roll or before the preparation is done, starts it as Start does:
// program does not stay black, though the first picture comes after the
// tick.
func TestCutStartsACuedChannelWithoutAPreparedPicture(t *testing.T) {
	readPTS := scriptClock(t, 50)
	r := newCueRig(t, 50, 0, 40)
	r.load(t, "a")
	r.cut(readPTS)
	waitFor(t, 5*time.Second, func() bool { return r.state() == "on_air" })
	// The run starts on the clock as Start starts it: nothing went out
	// stamped with the read point, and the run is not placed on it.
	for _, ev := range r.waitEvents(t, "picture 0", func(evs []cueEvent) bool { return countKind(evs, "video") >= 1 }) {
		require.False(t, ev.kind == "place" || ev.pts == readPTS, "%s put out on the tick", ev.kind)
	}
}

// A cut starts only a cued channel. One on air goes on as it was, one
// without a playlist, stopped or completed stays so; none puts anything
// out on the tick.
func TestCutLeavesAChannelThatIsNotCuedAlone(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, r *cueRig)
		state string
	}{
		{"on air", func(t *testing.T, r *cueRig) {
			r.load(t, "a")
			require.NoError(t, r.m.Start(1))
			r.waitEvents(t, "the run", func(evs []cueEvent) bool { return countKind(evs, "video") >= 1 })
		}, "on_air"},
		{"empty", func(*testing.T, *cueRig) {}, "stopped"},
		{"stopped", func(t *testing.T, r *cueRig) {
			r.load(t, "a")
			r.waitCue(t)
			require.NoError(t, r.m.Start(1))
			r.waitEvents(t, "the run", func(evs []cueEvent) bool { return countKind(evs, "video") >= 1 })
			require.NoError(t, r.m.Stop(1))
		}, "stopped"},
		{"stopped cue", func(t *testing.T, r *cueRig) {
			r.load(t, "a")
			r.waitCue(t)
			require.NoError(t, r.m.Stop(1))
		}, "stopped"},
		{"completed", func(t *testing.T, r *cueRig) {
			r.load(t, "a")
			require.NoError(t, r.m.Start(1))
			waitFor(t, 10*time.Second, func() bool { return r.state() == "completed" })
		}, "completed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readPTS := scriptClock(t, 50)
			r := newCueRig(t, 50, 1, 5)
			tc.setup(t, r)
			before := r.itemStarts()
			// A run on air puts its own pictures out meanwhile, never one
			// stamped with the read point.
			r.cut(readPTS)
			for _, ev := range r.snapshot() {
				require.False(t, ev.kind == "place" || ev.pts == readPTS, "the tick puts %s out", ev.kind)
			}
			r.m.cutStarts.Wait()
			require.Equal(t, tc.state, r.state())
			require.Equal(t, before, r.itemStarts(), "no item started")
		})
	}
}

// A cued channel is a source, so a cut or a mix can reach it: it is
// offered from its cue, by a playlist load or a jump while off air, and
// withdrawn when it stops.
func TestCuedChannelIsASource(t *testing.T) {
	r := newCueRig(t, 50, 1, 40)
	r.load(t, "a", "b")
	require.Equal(t, []string{"start playout:1"}, r.lifecycle(), "a loaded channel is offered")
	require.NoError(t, r.m.Stop(1))
	require.Equal(t, []string{"start playout:1", "stop playout:1"}, r.lifecycle(), "a stopped one is withdrawn")
	require.NoError(t, r.m.JumpTo(1, "b"))
	require.Equal(t, []string{"start playout:1", "stop playout:1", "start playout:1"}, r.lifecycle(), "a jump cues and offers it")
}

func (r *cueRig) lifecycle() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.starts...)
}

// The program tick takes the cue once and waits for nothing: not for the
// locks a start or a state read holds, which the start the tick launches
// waits for instead, and not for a cue a Start is retiring, which it
// leaves alone. A second tick that shows the channel before the start
// ran puts nothing out again.
func TestTheCutTickTakesTheCueOnceWithoutWaiting(t *testing.T) {
	readPTS := scriptClock(t, 50)
	r := newCueRig(t, 50, 1, 40)
	r.load(t, "a")
	r.waitCue(t)
	ch := r.m.cueChannels[0].Load()
	ch.lifecycleMu.Lock()
	ch.mu.Lock()
	r.m.mu.Lock()
	done := make(chan []cueEvent, 1)
	go func() {
		evs := r.cut(readPTS)
		done <- append(evs, r.cut(readPTS+1800)...)
	}()
	var evs []cueEvent
	select {
	case evs = <-done:
	case <-time.After(10 * time.Second):
	}
	r.m.mu.Unlock()
	ch.mu.Unlock()
	ch.lifecycleMu.Unlock()
	require.Equal(t, []cueEvent{
		{kind: "place", pts: readPTS},
		{kind: "video", pts: readPTS, pic: 0},
		{kind: "pcm", pts: readPTS, sample: 1, start: true},
	}, evs, "the two ticks put the cue out once, without waiting")
	waitFor(t, 5*time.Second, func() bool { return r.state() == "on_air" })
}

// A cue a Start is retiring is left alone by the tick, which does not wait
// for it; the Start goes on as Start does.
func TestTheCutTickLeavesACueBeingRetiredAlone(t *testing.T) {
	readPTS := scriptClock(t, 50)
	r := newCueRig(t, 50, 1, 40)
	r.load(t, "a")
	r.waitCue(t)
	cs := r.m.cueChannels[0].Load().preroll.cue.Load()
	cs.mu.Lock()
	done := make(chan []cueEvent, 1)
	go func() { done <- r.cut(readPTS) }()
	var evs []cueEvent
	select {
	case evs = <-done:
	case <-time.After(10 * time.Second):
		evs = []cueEvent{{kind: "the tick waited"}}
	}
	cs.mu.Unlock()
	require.Empty(t, evs, "the tick puts nothing out and does not wait")
	waitFor(t, 5*time.Second, func() bool { return r.state() == "on_air" })
}

// A clip slower than the channel starts from its cue the same way: the
// tick puts picture 0 and its sound, two channel periods of it, out; the
// run shows picture 0 for the next period, with no sound of its own, and
// picture 1 with its sound continuing the stream.
func TestCutStartsACuedChannelOfASlowerClip(t *testing.T) {
	readPTS := scriptClock(t, 50)
	r := newCueRigAt(t, 50, 25, 1, 40)
	r.freezeWall()
	r.load(t, "a")
	r.waitCue(t)
	r.cut(readPTS)
	due := dueAtStart(readPTS, 50)
	require.Equal(t, 3, due)
	evs := r.waitEvents(t, "the pictures due", func(evs []cueEvent) bool { return countKind(evs, "video") >= 1+due })
	var videos, pcms []cueEvent
	for _, ev := range evs {
		switch ev.kind {
		case "video":
			videos = append(videos, ev)
		case "pcm":
			pcms = append(pcms, ev)
		}
	}
	require.Equal(t, []cueEvent{
		{kind: "video", pts: readPTS, pic: 0},
		{kind: "video", pts: readPTS + 1800, pic: 0},
		{kind: "video", pts: readPTS + 3600, pic: 1},
		{kind: "video", pts: readPTS + 5400, pic: 1},
	}, videos[:4])
	require.Equal(t, []cueEvent{
		{kind: "pcm", pts: readPTS, sample: 1, start: true},
		{kind: "pcm", pts: readPTS + 3600, sample: 1921},
	}, pcms[:2])
}

// A tick the sync loop runs after the manager closed starts nothing.
func TestAClosedManagerStartsNothingOnACut(t *testing.T) {
	readPTS := scriptClock(t, 50)
	r := newCueRig(t, 50, 1, 40)
	r.m.Close()
	r.load(t, "a")
	r.waitCue(t)
	require.Empty(t, r.cut(readPTS))
	r.m.cutStarts.Wait()
	require.Equal(t, "cued", r.state())
}

// A Stop that runs while the cue is being offered as a source ends with
// the source withdrawn: offered after the Stop withdrew it, a stopped
// channel stayed a source.
func TestAStopWhileTheCueIsOfferedWithdrawsIt(t *testing.T) {
	r := newCueRig(t, 50, 1, 40)
	entered, release := make(chan struct{}), make(chan struct{})
	r.startHook = func() { close(entered); <-release }
	loaded := make(chan error, 1)
	go func() { loaded <- r.m.LoadPlaylist(1, testPlaylist(clipItem("a"))) }()
	<-entered
	require.NoError(t, r.m.Stop(1))
	close(release)
	require.NoError(t, <-loaded)
	lc := r.lifecycle()
	require.Equal(t, "stop playout:1", lc[len(lc)-1], "lifecycle %v", lc)
}
