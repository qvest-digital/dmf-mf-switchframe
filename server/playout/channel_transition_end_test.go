package playout

import (
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
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

// The secondary events of an item run on through the transition at its
// end until the incoming item takes over: a transition overlapping the
// item's last ticks used to stop them there, and the events due in that
// time, as one anchored a little before the end, never fired.
func TestPlayoutSecondaryEventsFireInTheTransitionAtItemEnd(t *testing.T) {
	const durMs = 400
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3ffps", fps), func(t *testing.T) {
			n := int(math.Round(2 * fps))
			// The clip's length as the channel reanchors its events to it.
			endMs := int64(float64(n) * 1000 / fps)
			a, b := clipItem("a"), clipItem("b")
			a.SecondaryEvents = []SecondaryEvent{
				{Type: "graphics_off", Layer: 1, OffsetMs: -100, Anchor: "end"},
				{Type: "graphics_off", Layer: 2, OffsetMs: int(endMs) - 200, Anchor: "start"},
			}
			b.Transition = &TransitionConfig{Type: "mix", DurationMs: durMs}
			ch, tick, _ := pcmChannelAt(t, fps, pcmAssets{
				a.AssetURI: pcmClip(n, fps, 48000, 1, 0, math.MaxInt64),
				b.AssetURI: pcmClip(2*n, fps, 48000, 0, 1, math.MaxInt64),
			}, a, b)
			h := &mockHandler{}
			ch.SetSecondaryHandler(h)

			// The first tick at which each event's time is up.
			due := func(ms int64) int {
				i := 0
				for time.Duration(math.Round(float64(i)*1e9/fps)).Milliseconds() < ms {
					i++
				}
				return i
			}
			want := map[string]int{"layer=1": due(endMs - 100), "layer=2": due(endMs - 200)}
			got := map[string]int{}
			first := -1
			for i := 0; i < n+10; i++ {
				tick(i)
				if first < 0 && ch.scheduler.State() == SchedTransitioning {
					first = i
				}
				for _, c := range h.getCalls() {
					if _, ok := got[c.Args]; !ok {
						got[c.Args] = i
					}
				}
			}
			require.GreaterOrEqual(t, first, 0, "the transition never ran")
			for _, w := range want {
				require.Greater(t, w, first, "the event is not due inside the transition")
			}
			require.Equal(t, want, got, "the ticks the events fired on")
		})
	}
}

// A transition that goes out as a cut does not overlap the clip's end:
// the incoming item takes over on the tick after the clip's last, where it
// would without the transition. Overlapping, the cut came on the tick the
// transition started, a transition's length before the clip's end.
func TestPlayoutTransitionThatCutsLetsTheClipPlayOut(t *testing.T) {
	cases := []struct {
		name     string
		incoming PlaylistItem
		typ      string
		setup    func(ch *Channel)
	}{
		{"into a live source aliased on the GPU", liveItem("b", "cam"), "mix", func(ch *Channel) {
			ch.SetGPUAlias(func(string) bool { return true })
		}},
		{"of a type the compositor does not blend", clipItem("b"), "push", func(ch *Channel) {
			ch.compositor = NewCompositor(16, 16, nil)
		}},
	}
	for _, tc := range cases {
		for _, fps := range channelRates {
			t.Run(fmt.Sprintf("%s/%.3ffps", tc.name, fps), func(t *testing.T) {
				n := int(math.Round(2 * fps))
				a, b := clipItem("a"), tc.incoming
				b.Transition = &TransitionConfig{Type: tc.typ, DurationMs: 400}
				ch, tick, chunks := pcmChannelAt(t, fps, pcmAssets{
					a.AssetURI:             pcmClip(n, fps, 48000, 1, 0, math.MaxInt64),
					clipItem("b").AssetURI: pcmClip(2*n, fps, 48000, 0, 1, math.MaxInt64),
				}, a, b)
				tc.setup(ch)
				ch.SetGPUSourceTap(func(string) *DecodedFrame {
					return &DecodedFrame{YUV: make([]byte, video.YUV420FrameSize(16, 16)), Width: 16, Height: 16}
				})
				for i := 0; i < n+10; i++ {
					tick(i)
				}
				// The clip's samples, all of them, went out by the tick
				// after its last.
				samples, last := 0, -1
				for _, c := range *chunks {
					for k := 0; k < len(c.pcm); k += c.channels {
						if c.pcm[k] > 0 {
							samples++
							last = c.tick
						}
					}
				}
				want := int(math.Round(float64(n) * 48000 / fps))
				require.Equal(t, want, samples, "the clip's samples that went out, its last on tick %d of %d", last, n)
				require.Less(t, last, n+1, "the clip's sound past its end")
			})
		}
	}
}

// An event anchored to an item's end fires once where the item ends at
// its natural end, on the tick the next item takes over. Ending into a
// transition, the item hands over on its last tick, which starts before
// that end, so the event used to be dropped there; a cut hands over on
// the tick after the last, where the item's end has come, unless the clip
// plays fewer pictures than it counts, as where a decoder drops some. An
// item left part-way by Next has not reached its end, and the event does
// not fire.
func TestPlayoutEndAnchoredEventFiresOnceWhereTheItemEnds(t *testing.T) {
	mix := &TransitionConfig{Type: "mix", DurationMs: 400}
	cases := []struct {
		name       string
		transition *TransitionConfig
		extra      int // pictures the clip counts and does not play
		nextAt     int // tick Next is pressed on, -1 for none
		fires      bool
	}{
		{"into a transition", mix, 0, -1, true},
		{"into a cut", nil, 0, -1, true},
		{"into a cut from a clip short of its count", nil, 5, -1, true},
		{"left mid-item by a cut", nil, 0, 10, false},
		{"left mid-item through a transition", mix, 0, 10, false},
	}
	for _, tc := range cases {
		for _, fps := range channelRates {
			t.Run(fmt.Sprintf("%s/%.3ffps", tc.name, fps), func(t *testing.T) {
				n := int(math.Round(2 * fps))
				a, b := clipItem("a"), clipItem("b")
				a.SecondaryEvents = []SecondaryEvent{{Type: "graphics_off", Layer: 1, OffsetMs: 0, Anchor: "end"}}
				b.Transition = tc.transition
				ch, tick, _ := pcmChannelAt(t, fps, overcountAssets{pcmAssets{
					a.AssetURI: pcmClip(n, fps, 48000, 1, 0, math.MaxInt64),
					b.AssetURI: pcmClip(2*n, fps, 48000, 0, 1, math.MaxInt64),
				}, tc.extra}, a, b)
				h := &mockHandler{}
				ch.SetSecondaryHandler(h)

				fired, takeover := []int{}, -1
				seen := 0
				for i := 0; i < n+20; i++ {
					if i == tc.nextAt {
						require.NoError(t, ch.Next())
					}
					tick(i)
					if takeover < 0 && ch.scheduler.State() == SchedPlaying && ch.scheduler.CurrentItem().ID == "b" {
						takeover = i
					}
					calls := h.getCalls()
					for range calls[seen:] {
						fired = append(fired, i)
					}
					seen = len(calls)
				}
				require.GreaterOrEqual(t, takeover, 0, "b never took over")
				if !tc.fires {
					require.Empty(t, fired, "the end-anchored event fired for an item left part-way")
					return
				}
				require.Equal(t, []int{takeover}, fired, "the ticks the end-anchored event fired on; b took over on %d", takeover)
			})
		}
	}
}

// overcountAssets opens the clips of pcmAssets reporting extra more
// pictures than they play.
type overcountAssets struct {
	pcmAssets
	extra int
}

func (o overcountAssets) Open(uri string) (FrameIterator, error) {
	it, err := o.pcmAssets.Open(uri)
	if err != nil {
		return nil, err
	}
	return overcounted{it.(pcmIterator), o.extra}, nil
}

type overcounted struct {
	pcmIterator
	extra int
}

func (o overcounted) TotalFrames() int { return o.pcmIterator.TotalFrames() + o.extra }

// A cued channel started on the tick that cuts to it ends its first item
// into a transition as any run does: the count of ticks left takes in the
// first picture the cut tick put out, which the run's first tick shows
// again, so the transition commits on the item's last picture. The run
// starts a few ticks behind the clock and runs them at once, so the item's
// time, which its secondary events follow, runs those ticks behind its
// pictures; the events due by its end still fire, once, on the commit.
func TestCuedRunEndsItsFirstItemIntoATransition(t *testing.T) {
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3ffps", fps), func(t *testing.T) {
			n := int(math.Round(fps)) // a second
			readPTS := scriptClock(t, fps)
			r := newCueRig(t, fps, 1, n)
			ch := r.m.channels[0]
			var wall atomic.Int64
			wall.Store(time.Now().UnixNano())
			ch.wallNow = func() time.Time { return time.Unix(0, wall.Load()) }
			h := &mockHandler{}
			r.m.SetSecondaryHandler(h)
			a, b := clipItem("a"), clipItem("b")
			a.SecondaryEvents = []SecondaryEvent{
				{Type: "graphics_off", Layer: 1, OffsetMs: 0, Anchor: "end"},
				{Type: "graphics_off", Layer: 2, OffsetMs: -100, Anchor: "end"},
			}
			b.Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
			require.NoError(t, r.m.LoadPlaylist(1, testPlaylist(a, b)))
			r.waitCue(t)
			period := ch.frameDuration

			r.cut(readPTS)
			due := dueAtStart(readPTS, fps)
			// Each picture the run puts out is one slot on from the cut
			// tick's: the run puts the due ones out at once, then one per
			// period of its wall clock.
			commit, first := -1, -1
			fired := map[string][]int{}
			seen := 0
			for slot := 0; slot < n+5; slot++ {
				if slot > due {
					wall.Add(int64(period))
				}
				r.waitEvents(t, fmt.Sprintf("picture slot %d", slot), func(evs []cueEvent) bool {
					return countKind(evs, "video") >= slot+1
				})
				st := ch.scheduler.State()
				if first < 0 && st == SchedTransitioning {
					first = slot
				}
				if commit < 0 && st == SchedPlaying && ch.scheduler.CurrentItem().ID == "b" {
					commit = slot
				}
				calls := h.getCalls()
				for _, c := range calls[seen:] {
					fired[c.Args] = append(fired[c.Args], slot)
				}
				seen = len(calls)
			}
			require.Greater(t, first, due, "the transition started while the run caught up")
			require.Equal(t, n-1, commit, "the transition commits on the item's last picture")
			require.Equal(t, []int{commit}, fired["layer=1"], "the end-anchored event fires once, on the commit")
			require.Len(t, fired["layer=2"], 1, "the event 100 ms before the end fires once")
			require.LessOrEqual(t, fired["layer=2"][0], commit)
		})
	}
}

// An item whose clip fails to open, or to read on, is left as one that
// ended is, but it has not reached its end: the events it had not reached
// do not fire as it is left. Counted as an end, every event of an item
// that never aired, start-anchored ones included, fired in one burst on
// the tick the next item took over.
func TestPlayoutFailedItemDoesNotFireItsEvents(t *testing.T) {
	cases := []struct {
		name       string
		failAt     int // picture the read fails at, -1 for a failed open
		transition *TransitionConfig
	}{
		{"open fails", -1, nil},
		{"open fails, into a transition", -1, &TransitionConfig{Type: "mix", DurationMs: 400}},
		{"read fails", 4, nil},
		{"read fails, into a transition", 4, &TransitionConfig{Type: "mix", DurationMs: 400}},
	}
	for _, tc := range cases {
		for _, fps := range channelRates {
			t.Run(fmt.Sprintf("%s/%.3ffps", tc.name, fps), func(t *testing.T) {
				n := int(math.Round(2 * fps))
				a, b := clipItem("a"), clipItem("b")
				a.DurationMs = 10000
				a.SecondaryEvents = []SecondaryEvent{
					{Type: "graphics_on", Layer: 1, Template: "t", OffsetMs: 5000, Anchor: "start"},
					{Type: "graphics_off", Layer: 1, OffsetMs: -1000, Anchor: "end"},
				}
				b.Transition = tc.transition
				assets := failingAssets{pcmAssets: pcmAssets{
					b.AssetURI: pcmClip(2*n, fps, 48000, 0, 1, math.MaxInt64),
				}, failAt: tc.failAt}
				if tc.failAt >= 0 {
					assets.pcmAssets[a.AssetURI] = pcmClip(n, fps, 48000, 1, 0, math.MaxInt64)
				}
				ch, tick, _ := pcmChannelAt(t, fps, assets, a, b)
				h := &mockHandler{}
				ch.SetSecondaryHandler(h)
				took := false
				for i := 0; i < n; i++ {
					tick(i)
					took = took || ch.scheduler.State() == SchedPlaying && ch.scheduler.CurrentItem().ID == "b"
				}
				require.True(t, took, "b never took over")
				require.Empty(t, h.getCalls(), "events of the failed item fired")
			})
		}
	}
}

// failingAssets opens the clips of pcmAssets, those of a clip whose read
// fails at picture failAt, when it is not negative, and fails to open a
// clip it does not have.
type failingAssets struct {
	pcmAssets
	failAt int
	all    bool // every clip's read fails, not only item a's
}

func (f failingAssets) Open(uri string) (FrameIterator, error) {
	it, err := f.pcmAssets.Open(uri)
	if err != nil || f.failAt < 0 || !f.all && uri != clipItem("a").AssetURI {
		return it, err
	}
	return &failingRead{pcmIterator: it.(pcmIterator), at: f.failAt}, nil
}

type failingRead struct {
	pcmIterator
	at, n int
}

func (f *failingRead) Next() (*DecodedFrame, error) {
	if f.n >= f.at {
		return nil, fmt.Errorf("decode error at picture %d", f.n)
	}
	f.n++
	return f.pcmIterator.Next()
}

// A transition completes on the tick transitionTicks after the one it
// started on, the count a transition at an item's end is started by, at
// every channel rate and every length of a whole number of milliseconds
// up to 10 s, both on the channel's tick clock, which steps by its period
// in whole nanoseconds, and on ticks placed on the exact grid. Where a
// tick fell short of the transition's length on the stepped clock, it
// blended one tick past the end of the clip it overlaps; a fixed
// allowance for that ended it a tick early where a tick falls a few
// microseconds short of the length, as at 119.88 and 239.76 fps.
func TestScheduler_TransitionCompletesOnItsCountedTick(t *testing.T) {
	for _, fps := range channelRates {
		period := time.Duration(float64(time.Second) / fps) // as the channel's
		clocks := map[string]func(i int) time.Duration{
			"stepped": func(i int) time.Duration { return time.Duration(i) * period },
			"grid":    func(i int) time.Duration { return time.Duration(math.Round(float64(i) * 1e9 / fps)) },
		}
		for name, at := range clocks {
			for _, from := range []int{0, 37} {
				for ms := int64(1); ms <= 10_000; ms++ {
					n := transitionTicks(ms, fps)
					elapsed := func(k int) time.Duration { return at(from+k) - at(from) }
					if n > 1 && transitionDone(elapsed(n-1), n-1, ms) {
						t.Fatalf("%.3f fps, %s clock from tick %d: %d ms completes on tick %d, not %d", fps, name, from, ms, n-1, n)
					}
					if !transitionDone(elapsed(n), n, ms) {
						t.Fatalf("%.3f fps, %s clock from tick %d: %d ms has not completed on tick %d", fps, name, from, ms, n)
					}
				}
			}
		}
	}

	// The scheduler counts the ticks itself.
	for _, fps := range channelRates {
		period := time.Duration(float64(time.Second) / fps)
		for ms := int64(1); ms <= 10_000; ms += 97 {
			for _, d := range []int64{ms, 659} {
				s := NewScheduler()
				pl := schedPlaylist(schedClipItem("a", 0), schedClipItem("b", 0))
				pl.Items[1].Transition = &TransitionConfig{Type: "mix", DurationMs: int(d)}
				require.NoError(t, s.LoadPlaylist(pl))
				s.Start()
				now := time.Now()
				s.Tick(now)
				require.NoError(t, s.Next())
				now = now.Add(period)
				require.NotNil(t, s.Tick(now).Transition)
				n := transitionTicks(d, fps)
				for k := 1; ; k++ {
					now = now.Add(period)
					if s.Tick(now).Transition == nil {
						require.Equal(t, n, k, "%.3f fps, %d ms: the tick it completes on", fps, d)
						break
					}
					require.Less(t, k, n, "%.3f fps, %d ms: still blending", fps, d)
				}
			}
		}
	}
}

// A failure belongs to the item it was signalled for: an item the operator
// moves to before the tick that would leave the failed one still ends at
// its own natural end, and the handover from it is one (FromEnd).
func TestScheduler_FailureDoesNotOutliveAMove(t *testing.T) {
	s := NewScheduler()
	require.NoError(t, s.LoadPlaylist(schedPlaylist(schedClipItem("a", 0), schedClipItem("b", 0), schedClipItem("c", 0), schedClipItem("d", 0))))
	s.Start()
	now := time.Now()
	require.Equal(t, "a", s.Tick(now).Item.ID)

	s.SignalItemFailed()
	require.NoError(t, s.JumpTo("c"))
	now = now.Add(20 * time.Millisecond)
	d := s.Tick(now)
	require.Equal(t, "c", d.Item.ID)
	require.False(t, d.FromEnd, "a move is not an end")

	s.SignalItemComplete()
	now = now.Add(20 * time.Millisecond)
	d = s.Tick(now)
	require.Equal(t, "d", d.Item.ID)
	require.True(t, d.FromEnd, "c ended at its natural end")

	// A cursor set by peer sync moves off a failed item as well.
	s.SignalItemFailed()
	s.SetCursor(1, ModeAutomated, 0)
	now = now.Add(20 * time.Millisecond)
	d = s.Tick(now)
	require.Equal(t, "b", d.Item.ID)
	require.False(t, d.FromEnd, "a cursor set is not an end")

	s.SignalItemComplete()
	now = now.Add(20 * time.Millisecond)
	d = s.Tick(now)
	require.Equal(t, "c", d.Item.ID)
	require.True(t, d.FromEnd, "b ended at its natural end")
}

// A clip that fails to read on under the transition at its end has not
// reached its end either: the transition commits as one from an item left
// part-way, and the events due by the item's end do not fire.
func TestPlayoutReadFailureUnderTheEndTransitionIsNoEnd(t *testing.T) {
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3ffps", fps), func(t *testing.T) {
			n := int(math.Round(2 * fps))
			a, b := clipItem("a"), clipItem("b")
			a.SecondaryEvents = []SecondaryEvent{{Type: "graphics_off", Layer: 1, OffsetMs: 0, Anchor: "end"}}
			b.Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
			ch, tick, _ := pcmChannelAt(t, fps, failingAssets{pcmAssets: pcmAssets{
				a.AssetURI: pcmClip(n, fps, 48000, 1, 0, math.MaxInt64),
				b.AssetURI: pcmClip(2*n, fps, 48000, 0, 1, math.MaxInt64),
			}, failAt: n - 2}, a, b)
			h := &mockHandler{}
			ch.SetSecondaryHandler(h)
			first, took := -1, false
			for i := 0; i < n+10; i++ {
				tick(i)
				if first < 0 && ch.scheduler.State() == SchedTransitioning {
					first = i
				}
				took = took || ch.scheduler.State() == SchedPlaying && ch.scheduler.CurrentItem().ID == "b"
			}
			require.True(t, took, "b never took over")
			require.GreaterOrEqual(t, first, 0, "the transition never ran")
			require.Less(t, first, n-2, "the read failed before the transition started")
			require.Empty(t, h.getCalls(), "the end event of an item that failed under its transition fired")
		})
	}
}

// A clip whose read fails, left playing as in manual mode, reads the error
// again on every tick; the as-run log has it once. Each clip that fails
// has its own.
func TestPlayoutReadFailureIsLoggedOnce(t *testing.T) {
	a, b := clipItem("a"), clipItem("b")
	ch, tick, _ := pcmChannel(t, failingAssets{pcmAssets: pcmAssets{
		a.AssetURI: pcmClip(50, 50, 48000, 1, 0, math.MaxInt64),
		b.AssetURI: pcmClip(50, 50, 48000, 0, 1, math.MaxInt64),
	}, failAt: 4}, a, b)
	var errs []AsRunEvent
	ch.SetAsRunLog(func(ev AsRunEvent) {
		if ev.Type == AsRunError {
			errs = append(errs, ev)
		}
	})
	ch.TakeManual()
	for i := 0; i < 20; i++ {
		tick(i)
	}
	require.Equal(t, "a", ch.scheduler.CurrentItem().ID, "manual mode left the failed item")
	require.Len(t, errs, 1, "read errors logged")
	require.Equal(t, "a", errs[0].ItemID)

	c := clipItem("c")
	ch, tick, _ = pcmChannel(t, failingAssets{pcmAssets: pcmAssets{
		a.AssetURI: pcmClip(50, 50, 48000, 1, 0, math.MaxInt64),
		b.AssetURI: pcmClip(50, 50, 48000, 0, 1, math.MaxInt64),
		c.AssetURI: pcmClip(50, 50, 48000, 0, 1, math.MaxInt64),
	}, failAt: 4, all: true}, a, b, c)
	errs = nil
	ch.SetAsRunLog(func(ev AsRunEvent) {
		if ev.Type == AsRunError {
			errs = append(errs, ev)
		}
	})
	for i := 0; i < 20; i++ {
		tick(i)
	}
	var ids []string
	for _, ev := range errs {
		ids = append(ids, ev.ItemID)
	}
	require.Equal(t, []string{"a", "b", "c"}, ids, "the items whose read errors are logged")

	// Into a transition, from which the incoming clip goes on in the slot
	// it was read in during the transition.
	b.Transition = &TransitionConfig{Type: "mix", DurationMs: 400}
	ch, tick, _ = pcmChannel(t, failingAssets{pcmAssets: pcmAssets{
		a.AssetURI: pcmClip(50, 50, 48000, 1, 0, math.MaxInt64),
		b.AssetURI: pcmClip(50, 50, 48000, 0, 1, math.MaxInt64),
	}, failAt: 30, all: true}, a, b)
	errs = nil
	ch.SetAsRunLog(func(ev AsRunEvent) {
		if ev.Type == AsRunError {
			errs = append(errs, ev)
		}
	})
	for i := 0; i < 90; i++ {
		tick(i)
	}
	ids = nil
	for _, ev := range errs {
		ids = append(ids, ev.ItemID)
	}
	require.Equal(t, []string{"a", "b"}, ids, "the items whose read errors are logged, through a transition")
}
