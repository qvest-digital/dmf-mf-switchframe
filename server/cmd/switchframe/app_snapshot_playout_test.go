package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/playout"
)

// A peer snapshot carries a loop release, so the synced engine ends the loop
// at the same pass the peer does, and drops it when the peer has none.
func TestApplyPlayoutChannels_CarriesLoopExit(t *testing.T) {
	mgr := playout.NewManager(playout.ManagerConfig{
		AssetProvider:        playout.NewFakeAssetProvider(make([]playout.DecodedFrame, 300)),
		AllowedLocalPrefixes: []string{"/"},
	})
	pl := &playout.Playlist{ID: "pl", Name: "pl", Items: []playout.PlaylistItem{
		{ID: "a", Type: playout.ItemClip, AssetURI: "local:///a.ts", Title: "a", Loop: true},
		{ID: "b", Type: playout.ItemLive, Source: "cam", Title: "b"},
	}}
	require.NoError(t, mgr.LoadPlaylist(1, pl))
	require.NoError(t, mgr.Start(1))
	defer func() { _ = mgr.Stop(1) }()
	a := &App{playoutMgr: mgr}

	status := func() playout.ChannelStatus { return mgr.ChannelStates()[0] }
	snap := internal.PlayoutChannelSnapshot{
		ChannelIndex: 0, ChannelState: "on_air", SchedulerState: "playing",
		CurrentItemIndex: 0, ElapsedMs: 100, PlaylistHash: mgr.PlaylistHash(1),
	}

	snap.LoopExit = true
	require.NoError(t, a.ApplyPlayoutChannels([]internal.PlayoutChannelSnapshot{snap}))
	require.True(t, status().LoopExit, "the peer's release is taken over")

	snap.LoopExit = false
	require.NoError(t, a.ApplyPlayoutChannels([]internal.PlayoutChannelSnapshot{snap}))
	require.False(t, status().LoopExit, "a release the peer does not have is dropped")
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, "a", status().CurrentItemID)
}

// Between Previous and the next tick the cursor names x while a, released
// from its loop, is still on air. A synced engine seeks to the cursor's
// item and then ends its loop, so the snapshot carries a release only while
// the item on air is the one at the cursor: a's release would land on x,
// refused when x does not loop and a wrong release when it does.
func TestPlayoutChannelSnapshot_LoopExitOnlyForTheItemAtTheCursor(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		loopExit                bool
		playingIndex, itemIndex int
		want                    bool
	}{
		{"item on air left by the cursor", true, 1, 0, false},
		{"item on air at the cursor", true, 1, 1, true},
		{"no release", false, 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := playout.ChannelStatus{
				State: "on_air", SchedulerState: "playing", Mode: "automated",
				CurrentItemID: "x", ItemIndex: tc.itemIndex,
				Looping: true, LoopExit: tc.loopExit, PlayingIndex: tc.playingIndex,
			}
			snap := playoutChannelSnapshot(2, st, 1500, "h")
			require.Equal(t, tc.want, snap.LoopExit)
			require.Equal(t, internal.PlayoutChannelSnapshot{
				ChannelIndex: 2, ChannelState: "on_air", CurrentItemIndex: tc.itemIndex,
				CurrentItemID: "x", ElapsedMs: 1500, SchedulerState: "playing",
				SchedulerMode: "automated", PlaylistHash: "h", LoopExit: tc.want,
			}, snap)
		})
	}
}
