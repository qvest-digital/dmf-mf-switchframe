package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

// A playout channel cued and not started is a source the switcher cuts
// and transitions to, and the first program tick that shows it starts it.
// A channel not cut to stays cued, and a stopped one is no source.
func TestCutToACuedPlayoutChannelStartsIt(t *testing.T) {
	frames := make([]playout.DecodedFrame, 500)
	for i := range frames {
		frames[i] = playout.DecodedFrame{YUV: make([]byte, 64*48*3/2), Width: 64, Height: 48, PTS: int64(i) * 1800, IsKeyframe: i == 0}
	}
	mgr := playout.NewManager(playout.ManagerConfig{
		AssetProvider:        playout.NewFakeAssetProvider(frames),
		AllowedLocalPrefixes: []string{"/"},
		PrerollItems:         1,
	})
	defer mgr.Close()
	mgr.SetPipelineFormat(64, 48, 50)

	// The program ticks run until the channel starts: they need a relay.
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	defer sw.Close()
	mgr.OnChannelLifecycle(
		func(_ int, key string) {
			if !sw.HasSource(key) {
				sw.RegisterTimedReplaySource(key)
			}
		},
		func(_ int, key string) { sw.UnregisterSource(key) },
	)
	mgr.OnCuedStart(placePlayoutRun)
	sw.SetOnProgramTick(startCuedOnProgramTick(nil, mgr))

	pl := &playout.Playlist{ID: "pl", Name: "pl", Items: []playout.PlaylistItem{
		{ID: "a", Type: playout.ItemClip, AssetURI: "local:///clips/a.ts", Title: "a"},
	}}
	require.NoError(t, mgr.LoadPlaylist(1, pl))
	require.NoError(t, mgr.LoadPlaylist(2, pl))
	require.True(t, sw.HasSource("playout:1"), "a cued playout channel is a source")
	require.True(t, sw.HasSource("playout:2"))

	require.NoError(t, sw.Cut(context.Background(), "playout:1"))
	state := func(id int) string { return mgr.ChannelStates()[id-1].State }
	require.Eventually(t, func() bool { return state(1) == "on_air" }, 5*time.Second, time.Millisecond, "the cut started the channel")
	assert.Equal(t, "cued", state(2), "the channel not cut to stays cued")

	require.NoError(t, mgr.Stop(1))
	assert.False(t, sw.HasSource("playout:1"), "a stopped channel is no source")
	require.NoError(t, mgr.Stop(2))
	assert.False(t, sw.HasSource("playout:2"), "a cued channel stopped is no source")
}

// A run a program tick started from its cue is stamped on the engine
// timeline from that tick's read point, which lies a read delay behind
// the clock: its anchor keeps it there. Placed afresh at its first
// picture, it went to the next frame start at or after the clock instead,
// so its first picture aired that read delay late.
func TestCuedPlayoutRunKeepsTheReadPointItStartedOn(t *testing.T) {
	var tai atomic.Int64
	scriptTAI(t, &tai)
	t.Cleanup(func() { playoutRate.Store(nil) })
	for _, r := range programRates {
		t.Run(fmt.Sprintf("%d_%d", r.Num, r.Den), func(t *testing.T) {
			rate := func() clock.Rate { return r }
			playoutRate.Store(&rate)
			const key = "playout:test"
			t.Cleanup(func() { playoutAnchors.Delete(key) })
			base := clock.TimestampToIndex(r, clock.TAINow()) + 10
			tai.Store(clock.IndexToTimestamp(r, base) + 1)
			read := clock.IndexPTS(r, base-clock.ReadDelay(r))

			playoutAnchors.Delete(key)
			placePlayoutRun(key, read)
			for k := uint64(0); k < 3; k++ {
				pts := clock.IndexPTS(r, base-clock.ReadDelay(r)+k)
				require.Equal(t, pts, playoutAnchor(key).PTS(pts), "picture %d", k)
			}
		})
	}
}

// An engine without playout passes the program tick of a playout key on
// to nothing, and the clip players' keys still reach them.
func TestProgramTickWithoutPlayoutChannels(t *testing.T) {
	require.NotPanics(t, func() { startCuedOnProgramTick(nil, nil)("playout:1", 0) })
}
