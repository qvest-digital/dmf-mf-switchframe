package playout

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestChannel_NextNow takes the next item over queued Next presses: the
// status names it before the tick, the tick airs it, and the presses do
// not move the channel on afterwards.
func TestChannel_NextNow(t *testing.T) {
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3f", fps), func(t *testing.T) {
			ch := NewChannel(1, ChannelConfig{
				FPS:                  fps,
				AssetProvider:        fakeProvider(3),
				AllowedLocalPrefixes: []string{"/"},
			})
			pl := testPlaylist(liveItem("a", "cam-1"), liveItem("b", "cam-2"),
				liveItem("c", "cam-3"), liveItem("d", "cam-4"))
			require.NoError(t, ch.LoadPlaylist(pl))
			require.ErrorIs(t, ch.NextNow(), ErrNotOnAir)
			var armed []string
			ch.SetLiveSourcePrepare(func(src string) { armed = append(armed, src) })
			ch.mu.Lock()
			ch.state = StateOnAir
			ch.scheduler.Start()
			ch.mu.Unlock()
			wall := time.Unix(0, 0)
			now := func() time.Time { return wall }
			k := tickClock{period: ch.frameDuration, next: wall.Add(ch.frameDuration)}
			tick := func() {
				wall = wall.Add(ch.frameDuration)
				ch.runDue(&k, now)
			}
			tick()

			require.NoError(t, ch.Next())
			require.NoError(t, ch.Next())
			armed = nil
			require.NoError(t, ch.NextNow())
			require.Equal(t, "b", ch.Status().CurrentItemID, "the cursor moves before the tick")
			require.Contains(t, armed, "cam-2", "the item taken is armed before the tick")
			for range 3 {
				tick()
				require.Equal(t, "b", ch.Status().CurrentItemID)
			}
		})
	}
}

// An item NextNow takes goes out through the same transition and audio
// path as an advance: the blend carries the incoming item's audio and
// never goes back to the outgoing item's, and a NextNow during a
// transition cuts to its item, dropping the item that was coming in with
// its open clip and its crossfade, whose audio goes out no more.
func TestChannel_NextNowAudio(t *testing.T) {
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3f", fps), func(t *testing.T) {
			provider := &multiAssetProvider{assets: map[string][]DecodedFrame{
				"local:///clips/a.ts": fakeFramesWithAudioTag(300, 0xAA),
				"local:///clips/b.ts": fakeFramesWithAudioTag(300, 0xBB),
				"local:///clips/c.ts": fakeFramesWithAudioTag(300, 0xCC),
			}}
			ch := NewChannel(1, ChannelConfig{
				AllowedLocalPrefixes: []string{"/"},
				AssetProvider:        provider,
				Width:                64,
				Height:               48,
				FPS:                  fps,
			})
			ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
			var mu sync.Mutex
			var tags []byte
			ch.SetAudioOutput(func(data []byte, _ int64, _, _ int) {
				mu.Lock()
				tags = append(tags, data[0])
				mu.Unlock()
			})
			pl := testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"))
			pl.DefaultTransition = TransitionConfig{Type: "dissolve", DurationMs: 400}
			require.NoError(t, ch.LoadPlaylist(pl))
			ch.mu.Lock()
			ch.state = StateOnAir
			ch.scheduler.Start()
			ch.mu.Unlock()
			now := time.Unix(0, 0)
			ticks := func(d time.Duration) (out []byte, transitioned bool) {
				for end := now.Add(d); now.Before(end); now = now.Add(ch.frameDuration) {
					mu.Lock()
					before := len(tags)
					mu.Unlock()
					ch.tick(now)
					transitioned = transitioned || ch.scheduler.State() == SchedTransitioning
					mu.Lock()
					out = append(out, tags[before:]...)
					mu.Unlock()
				}
				return out, transitioned
			}
			ticks(200 * time.Millisecond)

			// NextNow mid-clip: a dissolves into b.
			require.NoError(t, ch.NextNow())
			out, transitioned := ticks(600 * time.Millisecond)
			require.True(t, transitioned, "NextNow cut instead of running the transition")
			seenB := false
			for i, tag := range out {
				switch {
				case tag == 0xBB:
					seenB = true
				case tag != 0xAA || seenB:
					t.Fatalf("audio %d after NextNow is % X: % X", i, tag, out)
				}
			}
			require.True(t, seenB, "b's audio never went out")
			require.Equal(t, "b", ch.Status().CurrentItemID)

			// From a, Next starts a dissolving into b, and NextNow during
			// that transition takes c.
			require.NoError(t, ch.JumpTo("a"))
			ticks(200 * time.Millisecond)
			require.NoError(t, ch.Next())
			_, transitioned = ticks(100 * time.Millisecond)
			require.True(t, transitioned)
			require.NoError(t, ch.NextNow())
			out, _ = ticks(200 * time.Millisecond)
			require.Equal(t, "c", ch.Status().CurrentItemID)
			require.Equal(t, SchedPlaying, ch.scheduler.State())
			require.NotEmpty(t, out)
			for i, tag := range out {
				if tag != 0xCC {
					t.Fatalf("audio %d after NextNow during a transition is % X, want only c's: % X", i, tag, out)
				}
			}
			ch.mu.Lock()
			in, xfade := ch.inClipItemID, ch.xfade
			ch.mu.Unlock()
			require.Empty(t, in, "the clip that was coming in is still open")
			require.Nil(t, xfade, "the crossfade of the transition left is still running")
		})
	}
}

// The PCM of an item NextNow takes is crossfaded with the item on air's
// through the transition, as on an advance; a NextNow during a transition
// cuts the PCM with the pictures, so only the item taken is heard, at full
// level, from a fresh start of the stream.
func TestChannel_NextNowPCM(t *testing.T) {
	for _, fps := range channelRates {
		t.Run(fmt.Sprintf("%.3f", fps), func(t *testing.T) {
			a, b, c, d := clipItem("a"), clipItem("b"), clipItem("c"), clipItem("d")
			n := int(4 * fps)
			assets := pcmAssets{
				a.AssetURI: pcmClip(n, fps, 48000, 1, 0, math.MaxInt64),
				b.AssetURI: pcmClip(n, fps, 48000, 0, 1, math.MaxInt64),
				c.AssetURI: pcmClip(n, fps, 48000, 0.25, 0.25, math.MaxInt64),
				d.AssetURI: pcmClip(n, fps, 48000, 0.5, 0.5, math.MaxInt64),
			}
			ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: assets, Width: 16, Height: 16, FPS: fps})
			ch.compositor = NewCompositor(16, 16, func(_, _, _ []byte, _, _ int, _ float32, _ string) {})
			ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
			var chunks []pcmChunk
			at := 0
			ch.SetPCMOutput(func(pcm []float32, pts int64, channels int, start bool) {
				chunks = append(chunks, pcmChunk{at, append([]float32(nil), pcm...), pts, channels, start})
			})
			pl := testPlaylist(a, b, c, d)
			pl.DefaultTransition = TransitionConfig{Type: "mix", DurationMs: 400}
			require.NoError(t, ch.LoadPlaylist(pl))
			ch.mu.Lock()
			ch.state = StateOnAir
			ch.scheduler.Start()
			ch.mu.Unlock()
			t0 := time.Unix(0, 0)
			tick := func(i int) {
				at = i
				ch.tick(t0.Add(time.Duration(i) * ch.frameDuration))
			}
			periods := func(d time.Duration) int { return int(d / ch.frameDuration) }

			i := 0
			for ; i < periods(200*time.Millisecond); i++ {
				tick(i)
			}
			require.NoError(t, ch.NextNow())
			from := i
			for end := i + periods(200*time.Millisecond); i < end; i++ {
				tick(i)
			}
			mixed := false
			for _, ck := range chunks {
				if ck.tick < from {
					continue
				}
				for s := 0; s+1 < len(ck.pcm); s += ck.channels {
					mixed = mixed || (ck.pcm[s] > 0 && ck.pcm[s+1] > 0)
				}
			}
			require.True(t, mixed, "NextNow's transition did not crossfade a's and b's PCM")

			// Let b go on air, start b into c, and take d during it.
			for end := i + periods(400*time.Millisecond); i < end; i++ {
				tick(i)
			}
			require.Equal(t, "b", ch.Status().CurrentItemID)
			require.NoError(t, ch.Next())
			for end := i + periods(100*time.Millisecond); i < end; i++ {
				tick(i)
			}
			require.Equal(t, SchedTransitioning, ch.scheduler.State())
			require.NoError(t, ch.NextNow())
			cut := i
			for end := i + periods(200*time.Millisecond); i < end; i++ {
				tick(i)
			}
			require.Equal(t, "d", ch.Status().CurrentItemID)
			first := true
			for _, ck := range chunks {
				if ck.tick < cut {
					continue
				}
				if first {
					require.True(t, ck.start, "the PCM stream did not start afresh on the cut")
					first = false
				}
				for s, v := range ck.pcm {
					if v != 0.5 {
						t.Fatalf("PCM on tick %d after the cut on %d: sample %d is %v, want only d's at full level", ck.tick, cut, s, v)
					}
				}
			}
			require.False(t, first, "no PCM went out after the cut")
		})
	}
}

// Two moves before a tick, NextNow twice or NextNow then Next, run the
// transition from the clip on air: the item NextNow first took never
// aired, so its clip is not opened as the outgoing side. Previous then
// Next leaves the clip on air playing on. NextNow from a hands the
// preroll c, the clip after the one it took, before the tick.
func TestChannel_NextNowMovesOutFromTheClipOnAir(t *testing.T) {
	for _, tc := range []struct {
		name, from, presses, out, in string
	}{
		{"NextNow twice", "a", "NN", "a", "c"},
		{"NextNow then Next", "a", "Nn", "a", "c"},
		{"Previous then Next", "b", "pn", "b", ""},
	} {
		for _, fps := range channelRates {
			t.Run(fmt.Sprintf("%s/%.3f", tc.name, fps), func(t *testing.T) {
				provider := &multiAssetProvider{assets: map[string][]DecodedFrame{}}
				for _, id := range []string{"a", "b", "c", "d"} {
					provider.assets["local:///clips/"+id+".ts"] = fakeFramesWithAudioTag(300, id[0])
				}
				ch := NewChannel(1, ChannelConfig{
					AllowedLocalPrefixes: []string{"/"},
					AssetProvider:        provider,
					PrerollItems:         1,
					Width:                64,
					Height:               48,
					FPS:                  fps,
				})
				ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
				pl := testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"), clipItem("d"))
				pl.DefaultTransition = TransitionConfig{Type: "dissolve", DurationMs: 400}
				require.NoError(t, ch.LoadPlaylist(pl))
				ch.mu.Lock()
				ch.state = StateOnAir
				ch.scheduler.Start()
				ch.mu.Unlock()
				now := time.Unix(0, 0)
				tick := func() {
					ch.tick(now)
					now = now.Add(ch.frameDuration)
				}
				require.NoError(t, ch.JumpTo(tc.from))
				for end := now.Add(300 * time.Millisecond); now.Before(end); {
					tick()
				}
				ch.mu.Lock()
				frame := ch.clipFrame
				ch.mu.Unlock()
				for i, p := range tc.presses {
					switch p {
					case 'N':
						require.NoError(t, ch.NextNow())
					case 'n':
						require.NoError(t, ch.Next())
					case 'p':
						require.NoError(t, ch.Previous())
					}
					if i == 0 && p == 'N' {
						ch.preroll.mu.Lock()
						_, wanted := ch.preroll.entries["c"]
						ch.preroll.mu.Unlock()
						require.True(t, wanted, "the preroll does not have the clip after the one taken")
					}
				}
				tick()
				ch.mu.Lock()
				primary, incoming, at := ch.clipItemID, ch.inClipItemID, ch.clipFrame
				ch.mu.Unlock()
				require.Equal(t, tc.out, primary, "the clip on the outgoing side")
				require.Greater(t, frame, 1)
				require.GreaterOrEqual(t, at, frame, "the clip on air was reopened")
				require.Equal(t, tc.in, incoming)
				if tc.in == "" {
					require.Equal(t, SchedPlaying, ch.scheduler.State())
				} else {
					require.Equal(t, SchedTransitioning, ch.scheduler.State())
				}
			})
		}
	}
}

// A seek by peer sync, or a cursor restored by recovery, puts another item
// on air without a tick, so a Next before the next tick advances from the
// item the cursor was set to, not from the one the last tick played:
// after a seek to c the dissolve runs c->d on c's clip, and a's clip is
// not reopened as the outgoing side.
func TestChannel_SeekThenNextGoesOutFromTheSeekTarget(t *testing.T) {
	for _, seek := range []string{"SeekToItem", "SetCursor"} {
		for _, fps := range channelRates {
			t.Run(fmt.Sprintf("%s/%.3f", seek, fps), func(t *testing.T) {
				provider := &multiAssetProvider{assets: map[string][]DecodedFrame{}}
				for _, id := range []string{"a", "b", "c", "d"} {
					provider.assets["local:///clips/"+id+".ts"] = fakeFramesWithAudioTag(300, id[0])
				}
				ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider, Width: 64, Height: 48, FPS: fps})
				ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
				pl := testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"), clipItem("d"))
				pl.DefaultTransition = TransitionConfig{Type: "dissolve", DurationMs: 400}
				require.NoError(t, ch.LoadPlaylist(pl))
				ch.mu.Lock()
				ch.state = StateOnAir
				ch.scheduler.Start()
				ch.mu.Unlock()
				now := time.Unix(0, 0)
				for end := now.Add(200 * time.Millisecond); now.Before(end); now = now.Add(ch.frameDuration) {
					ch.tick(now)
				}
				if seek == "SeekToItem" {
					require.NoError(t, ch.SeekToItem(2, 100))
				} else {
					ch.SetCursor(2)
				}
				require.NoError(t, ch.Next())
				ch.tick(now)
				ch.scheduler.mu.Lock()
				out, in := ch.scheduler.transOutgoing, ch.scheduler.transIncoming
				ch.scheduler.mu.Unlock()
				require.NotNil(t, out, "no transition started")
				require.Equal(t, "c", out.ID)
				require.Equal(t, "d", in.ID)
				if seek == "SeekToItem" {
					ch.mu.Lock()
					primary, incoming := ch.clipItemID, ch.inClipItemID
					ch.mu.Unlock()
					require.Equal(t, "c", primary, "the seek's clip is not the outgoing side")
					require.Equal(t, "d", incoming)
				}
			})
		}
	}
}

// While Previous has moved the cursor off a looping clip on air before a
// tick, the status reports that clip's loop, and its release.
func TestChannel_StatusLoopingFollowsTheItemPlaying(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: fakeProvider(30)})
	a := clipItem("a")
	a.Loop = true
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("x"), a, clipItem("b"))))
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()
	now := time.Unix(0, 0)
	ch.tick(now)
	require.NoError(t, ch.JumpTo("a"))
	ch.tick(now.Add(ch.frameDuration))
	require.True(t, ch.Status().Looping)

	require.NoError(t, ch.Previous())
	st := ch.Status()
	require.Equal(t, "x", st.CurrentItemID)
	require.True(t, st.Looping, "a still loops on air")
	require.False(t, st.LoopExit)
	require.NoError(t, ch.EndLoop())
	require.True(t, ch.Status().LoopExit)
}

// A transition that passes items over, from two Next or two NextNow
// presses, has the preroll prepare the clips after the one coming in, so
// the clip after it airs its first frame on the tick that cuts to it, and
// none for the item passed over, which never airs.
func TestChannel_PrerollDuringATransitionThatSkips(t *testing.T) {
	for _, presses := range []string{"nn", "NN"} {
		for _, fps := range channelRates {
			t.Run(fmt.Sprintf("%s/%.3f", presses, fps), func(t *testing.T) {
				provider := &multiAssetProvider{assets: map[string][]DecodedFrame{}}
				for _, id := range []string{"a", "b", "c", "d"} {
					provider.assets["local:///clips/"+id+".ts"] = fakeFramesWithAudioTag(300, id[0])
				}
				ch := NewChannel(1, ChannelConfig{
					AllowedLocalPrefixes: []string{"/"},
					AssetProvider:        provider,
					PrerollItems:         1,
					Width:                64,
					Height:               48,
					FPS:                  fps,
				})
				ch.SetRawVideoOutput(func([]byte, int, int, int64, bool) {})
				pl := testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"), clipItem("d"))
				pl.DefaultTransition = TransitionConfig{Type: "dissolve", DurationMs: 400}
				require.NoError(t, ch.LoadPlaylist(pl))
				ch.mu.Lock()
				ch.state = StateOnAir
				ch.scheduler.Start()
				ch.mu.Unlock()
				now := time.Unix(0, 0)
				ch.tick(now)
				for _, p := range presses {
					if p == 'n' {
						require.NoError(t, ch.Next())
					} else {
						require.NoError(t, ch.NextNow())
					}
				}
				now = now.Add(ch.frameDuration)
				ch.tick(now)
				require.Equal(t, SchedTransitioning, ch.scheduler.State())
				ch.preroll.mu.Lock()
				_, d := ch.preroll.entries["d"]
				_, b := ch.preroll.entries["b"]
				ch.preroll.mu.Unlock()
				require.True(t, d, "d, after the item coming in, is not prepared")
				require.False(t, b, "b, passed over, is prepared")
			})
		}
	}
}
