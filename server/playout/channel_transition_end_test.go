package playout

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The scheduler starts a transition at the natural end of a clip on the
// tick from which the clip, by the ticks the channel says it still covers,
// lasts through the tick the transition completes on: 400 ms at 50 fps
// blends 20 ticks and completes on the 21st. Where the item is not left
// at its end, or not through a transition, the clip plays out.
func TestScheduler_TransitionAtItemEndStartsBeforeTheEnd(t *testing.T) {
	future := time.Now().Add(time.Hour)
	cases := []struct {
		name  string
		setup func(s *Scheduler, a, b *PlaylistItem, pl *Playlist)
		want  bool
	}{
		{"transition", func(*Scheduler, *PlaylistItem, *PlaylistItem, *Playlist) {}, true},
		{"default transition", func(_ *Scheduler, _, b *PlaylistItem, pl *Playlist) {
			b.Transition, pl.DefaultTransition = nil, TransitionConfig{Type: "mix", DurationMs: 400}
		}, true},
		{"cut", func(_ *Scheduler, _, b *PlaylistItem, _ *Playlist) { b.Transition = nil }, false},
		{"hold", func(_ *Scheduler, a, _ *PlaylistItem, _ *Playlist) { a.Hold = true }, false},
		{"loop", func(_ *Scheduler, a, _ *PlaylistItem, _ *Playlist) { a.Loop = true }, false},
		{"loop released", func(s *Scheduler, a, _ *PlaylistItem, _ *Playlist) {
			a.Loop = true
			s.loopExit = true
		}, true},
		{"manual", func(s *Scheduler, _, _ *PlaylistItem, _ *Playlist) { s.TakeManual() }, false},
		{"next scheduled later", func(_ *Scheduler, _, b *PlaylistItem, _ *Playlist) { b.ScheduledTime = &future }, false},
		{"last item", func(_ *Scheduler, _, _ *PlaylistItem, pl *Playlist) { pl.Items = pl.Items[:1] }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScheduler()
			pl := schedPlaylist(schedClipItem("a", 0), schedClipItem("b", 0), schedClipItem("c", 0))
			pl.Items[1].Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
			pl.Items[2].Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
			now := time.Now()
			require.NoError(t, s.LoadPlaylist(pl))
			s.Start()
			require.Equal(t, "a", s.Tick(now).Item.ID)
			tc.setup(s, &pl.Items[0], &pl.Items[1], pl)

			// One tick more than the transition takes, then the 21 ticks it
			// takes.
			s.SignalTicksLeft("a", 22, 50)
			now = now.Add(20 * time.Millisecond)
			d := s.Tick(now)
			require.Nil(t, d.Transition, "the transition started with the clip lasting past it")
			require.NotNil(t, d.Item)
			require.Equal(t, "a", d.Item.ID)

			s.SignalTicksLeft("a", 21, 50)
			now = now.Add(20 * time.Millisecond)
			d = s.Tick(now)
			if !tc.want {
				require.Nil(t, d.Transition, "a transition started")
				require.NotNil(t, d.Item, "the clip did not play on")
				require.Equal(t, "a", d.Item.ID, "the clip did not play on")
				// Down to its last tick.
				s.SignalTicksLeft("a", 1, 50)
				now = now.Add(20 * time.Millisecond)
				d = s.Tick(now)
				require.Nil(t, d.Transition, "a transition started")
				require.NotNil(t, d.Item, "the clip did not play out")
				require.Equal(t, "a", d.Item.ID, "the clip did not play out")
				return
			}
			require.NotNil(t, d.Transition, "the transition did not start")
			require.Equal(t, 0.0, d.Transition.Progress)
			require.Equal(t, "b", d.Transition.Incoming.ID)

			// The incoming item does not inherit the count: it ends into the
			// transition after it only once the channel counts its own ticks.
			for range 21 {
				now = now.Add(20 * time.Millisecond)
				d = s.Tick(now)
			}
			require.Nil(t, d.Transition, "the incoming item took the outgoing one's count")
			require.NotNil(t, d.Item)
			require.Equal(t, "b", d.Item.ID)
			now = now.Add(20 * time.Millisecond)
			require.Nil(t, s.Tick(now).Transition, "the incoming item took the outgoing one's count")
		})
	}
}

// The count of ticks a clip still covers belongs to the item it was
// counted for and to the play-through it was counted in. A standby engine
// moved to an item by a snapshot used to take the count of the clip it
// left, and with it at that clip's end went straight on into the item
// after the one it was moved to.
func TestScheduler_TransitionAtItemEndCountsOnlyTheItemsOwnTicks(t *testing.T) {
	cases := []struct {
		name string
		move func(s *Scheduler)
		want string
	}{
		{"moved to the next item", func(s *Scheduler) {
			s.SignalTicksLeft("a", 3, 50)
			s.SetCursor(1, ModeAutomated, 0)
		}, "b"},
		{"moved back within the item", func(s *Scheduler) {
			s.SignalTicksLeft("a", 3, 50)
			s.SetCursor(0, ModeAutomated, 0)
		}, "a"},
		{"counted for the item left after the move", func(s *Scheduler) {
			s.SetCursor(1, ModeAutomated, 0)
			s.SignalTicksLeft("a", 3, 50)
		}, "b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScheduler()
			pl := schedPlaylist(schedClipItem("a", 0), schedClipItem("b", 0), schedClipItem("c", 0))
			pl.Items[1].Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
			pl.Items[2].Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
			require.NoError(t, s.LoadPlaylist(pl))
			s.Start()
			now := time.Now()
			require.Equal(t, "a", s.Tick(now).Item.ID)

			tc.move(s)
			now = now.Add(20 * time.Millisecond)
			d := s.Tick(now)
			require.Nil(t, d.Transition, "a transition started from a count of another play-through")
			require.NotNil(t, d.Item)
			require.Equal(t, tc.want, d.Item.ID)

			// Its own count still ends it into the transition.
			s.SignalTicksLeft(tc.want, 3, 50)
			now = now.Add(20 * time.Millisecond)
			d = s.Tick(now)
			require.NotNil(t, d.Transition, "the item's own count did not start the transition")
			require.Equal(t, tc.want, d.Transition.Outgoing.ID)
		})
	}
}

// A slate is left at its end through the transition into the next item
// as a clip is: the transition starts its length before the slate's
// duration is up, so the next item has the picture to itself once it is.
// The slate used to air its full duration and then the transition on top.
func TestPlayoutTransitionAtSlateEndCompletesOnItsDuration(t *testing.T) {
	const slateMs, durMs = 1000, 400
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3ffps", fps), func(t *testing.T) {
			s, b := slateItem("s", slateMs), clipItem("b")
			b.Transition = &TransitionConfig{Type: "mix", DurationMs: durMs}
			ch, tick, _ := pcmChannelAt(t, fps, pcmAssets{b.AssetURI: pcmClip(200, fps, 48000, 0, 1, math.MaxInt64)}, s, b)
			// The first tick on which the slate's duration is up.
			cut := int(math.Ceil(slateMs * fps / 1000))
			first, done := -1, -1
			for i := 0; i <= cut+int(math.Ceil(durMs*fps/1000))+2; i++ {
				tick(i)
				switch st := ch.scheduler.State(); {
				case st == SchedTransitioning && first < 0:
					first = i
				case st == SchedPlaying && first >= 0 && done < 0:
					done = i
				}
			}
			require.GreaterOrEqual(t, first, 0, "the transition never ran")
			require.GreaterOrEqual(t, done, 0, "the transition never completed")
			require.LessOrEqual(t, done-cut, 1, "the transition completes after the slate's duration")
			require.GreaterOrEqual(t, done, cut, "the transition completes before the slate's duration is up")
		})
	}
}

// layoutLevels is n channels at level 1.
func layoutLevels(n int) []float32 {
	l := make([]float32, n)
	for i := range l {
		l[i] = 1
	}
	return l
}

// laidOut lays chunks out as the mixer reads them, sample by sample: a
// chunk that continues the one before follows it, a start is placed by
// its PTS. Samples no chunk covers are silence.
func laidOut(chunks []pcmChunk, rate, channels int) []float32 {
	var out []float32
	var firstPTS int64
	next := math.NaN()
	for i, k := range chunks {
		at := len(out) / channels
		if i == 0 {
			firstPTS, next = k.pts, float64(k.pts)
		}
		if k.start || math.Abs(float64(k.pts)-next) > 2 {
			at = int(math.Round(float64(k.pts-firstPTS) * float64(rate) / 90000))
		}
		n := len(k.pcm) / channels
		for len(out) < (at+n)*channels {
			out = append(out, 0)
		}
		copy(out[at*channels:], k.pcm)
		next = float64(k.pts) + float64(n)*90000/float64(rate)
	}
	return out
}

// At an item's natural end the sound goes on without a gap or a step in
// every layout: through a mix, the sum of the outgoing and the incoming
// item at the same level stays at or above it, as the equal-power gains
// sum to 1 at least, and moves no faster than the gains do; through a
// cut, one item follows the other at full level. The mix used to drop
// the sound for a tick: the outgoing item had stopped at full level and
// the incoming one faded in from nothing.
func TestPlayoutItemEndCarriesTheSoundInEveryLayout(t *testing.T) {
	rates := channelRates
	if raceDetector {
		rates = []float64{25, 30000.0 / 1001, 60}
	}
	for _, typ := range []string{"mix", "cut"} {
		for _, n := range []int{1, 2, 6, 8} {
			for _, trim := range []bool{false, true} {
				for _, fps := range rates {
					name := fmt.Sprintf("%s/%dch/%.3ffps", typ, n, fps)
					if trim {
						name = "trimmed " + name
					}
					t.Run(name, func(t *testing.T) {
						checkItemEndLayout(t, typ, n, fps, trim)
					})
				}
			}
		}
	}
}

// checkItemEndLayout is TestPlayoutItemEndCarriesTheSoundInEveryLayout's
// case: an item at fps in n channels ends into the next through a typ
// transition, out of its whole clip or one trimmed to in and out points.
func checkItemEndLayout(t *testing.T, typ string, n int, fps float64, trim bool) {
	const rate, durMs = 48000, 400
	a, b := clipItem("a"), clipItem("b")
	if typ != "cut" {
		b.Transition = &TransitionConfig{Type: typ, DurationMs: durMs}
	}
	aPictures := int(math.Ceil(durMs*fps/1000)) + 10
	clipPictures := aPictures
	if trim {
		clipPictures = trimmedTo(&a, fps, trimLead(fps, rate), aPictures)
	}
	ch, tick, chunks := pcmChannelAt(t, fps, pcmAssets{
		a.AssetURI: withLayout(pcmClip(clipPictures, fps, rate, 0, 0, math.MaxInt64), layoutLevels(n)...),
		b.AssetURI: withLayout(pcmClip(3*aPictures, fps, rate, 0, 0, math.MaxInt64), layoutLevels(n)...),
	}, a, b)
	transitioned := false
	for i := 0; i < 2*aPictures; i++ {
		tick(i)
		transitioned = transitioned || ch.scheduler.State() == SchedTransitioning
	}
	require.Equal(t, typ != "cut", transitioned, "a transition ran")
	starts := 0
	for _, k := range *chunks {
		require.Equal(t, n, k.channels, "tick %d: the stream's layout", k.tick)
		if k.start {
			starts++
		}
	}
	// A cut starts the incoming item's sound afresh on its first
	// picture; a mix hands the stream on.
	wantStarts := 1
	if typ == "cut" {
		wantStarts = 2
	}
	require.Equal(t, wantStarts, starts, "the streams the sound goes out in")
	pcm := laidOut(*chunks, rate, n)
	require.Greater(t, len(pcm)/n, aPictures*rate/int(fps)+rate/10, "the incoming item's sound goes on")
	// The gains move along the curve at most pi/2 a transition,
	// over the samples of each tick at its progress, which is
	// counted in whole milliseconds, a millisecond a tick off.
	tickMs := 1000 / fps
	maxStep := 1.05*math.Pi/2*(tickMs+1)/durMs/(rate*tickMs/1000) + 1e-6
	peak := float32(0)
	for k := 0; k < len(pcm)/n; k++ {
		for c := range n {
			v := pcm[k*n+c]
			peak = max(peak, v)
			if typ == "cut" {
				if math.Abs(float64(v)-1) > 1e-6 {
					t.Fatalf("sample %d of %d, channel %d: %v, want the one item at full level", k, len(pcm)/n, c, v)
				}
				continue
			}
			if v < 1-1e-2 || v > math.Sqrt2+1e-3 {
				t.Fatalf("sample %d of %d, channel %d: %v, a dropout", k, len(pcm)/n, c, v)
			}
			if k > 0 && math.Abs(float64(v-pcm[(k-1)*n+c])) > maxStep {
				t.Fatalf("sample %d, channel %d: the sound steps from %v to %v, at most %v a sample", k, c, pcm[(k-1)*n+c], v, maxStep)
			}
		}
	}
	if typ != "cut" {
		require.Greater(t, peak, float32(1.3), "the two items never mixed")
	}
}

// A loop released with EndLoop is left at the end of its pass through the
// transition into the next item as any clip is at its end: the transition
// completes on the pass's last tick, over the pass's own pictures and
// sound. It used to start after the pass, on its last picture held and
// its sound stopped.
func TestPlayoutTransitionAfterEndLoopOverlapsTheLastPass(t *testing.T) {
	const rate, durMs = 48000, 400
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3ffps", fps), func(t *testing.T) {
			a, b := clipItem("a"), clipItem("b")
			a.Loop = true
			b.Transition = &TransitionConfig{Type: "mix", DurationMs: durMs}
			pass := int(math.Ceil(durMs*fps/1000)) + 10
			ch, tick, chunks := pcmChannelAt(t, fps, pcmAssets{
				a.AssetURI: pcmClip(pass, fps, rate, 1, 1, math.MaxInt64),
				b.AssetURI: pcmClip(3*pass, fps, rate, 1, 1, math.MaxInt64),
			}, a, b)
			done := -1
			for i := 0; i < 4*pass; i++ {
				if i == pass+2 {
					require.NoError(t, ch.EndLoop())
				}
				was := ch.scheduler.State()
				tick(i)
				if was == SchedTransitioning && ch.scheduler.State() == SchedPlaying {
					done = i
				}
			}
			require.Equal(t, 2*pass-1, done, "the transition completes on the second pass's last tick")
			pcm := laidOut(*chunks, rate, 2)
			require.Greater(t, len(pcm)/2, 2*pass*rate/int(fps), "the incoming item's sound goes on")
			for k := 0; k < len(pcm)/2; k++ {
				if pcm[2*k] < 1-1e-2 {
					t.Fatalf("sample %d of %d: %v, a dropout", k, len(pcm)/2, pcm[2*k])
				}
			}
		})
	}
}

// The incoming item's sound starts on its first picture, which opens a
// transition at an item's end: its first sample is at that picture's
// place in the stream, as the stream runs on after the transition.
func TestPlayoutTransitionAtItemEndKeepsTheIncomingSoundOnItsPictures(t *testing.T) {
	const rate, durMs = 48000, 400
	const unit = 1e-6 // the level of the incoming item's sample i is (i+1)*unit
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3ffps", fps), func(t *testing.T) {
			a, b := clipItem("a"), clipItem("b")
			b.Transition = &TransitionConfig{Type: "mix", DurationMs: durMs}
			aPictures := int(math.Ceil(durMs*fps/1000)) + 10
			in := pcmClip(3*aPictures, fps, rate, 0, 0, math.MaxInt64)
			for i := range in.frames {
				first := int(math.Round(float64(i) * rate / fps))
				for _, af := range in.frames[i].AudioFrames {
					for k := 1; k < len(af.PCM); k += 2 {
						af.PCM[k] = float32(first+k/2+1) * unit
					}
				}
			}
			ch, tick, chunks := pcmChannelAt(t, fps, pcmAssets{
				a.AssetURI: pcmClip(aPictures, fps, rate, 1, 0, math.MaxInt64),
				b.AssetURI: in,
			}, a, b)
			first := -1
			for i := 0; i < 2*aPictures; i++ {
				tick(i)
				if first < 0 && ch.scheduler.State() == SchedTransitioning {
					first = i
				}
			}
			require.Positive(t, first, "the transition never ran")
			pcm := laidOut(*chunks, rate, 2)
			// The stream starts on the outgoing item's first picture, tick 0.
			p0 := int(math.Round(float64(first) * rate / fps))
			n := len(pcm) / 2
			require.Greater(t, n-rate/10, p0+(durMs+100)*rate/1000, "the stream ends in the transition")
			for k := n - rate/10; k < n; k++ {
				if got := float64(pcm[2*k+1])/unit - 1; math.Abs(got-float64(k-p0)) > 0.5 {
					t.Fatalf("stream sample %d is the incoming item's sample %v, want %d", k, got, k-p0)
				}
			}
		})
	}
}
