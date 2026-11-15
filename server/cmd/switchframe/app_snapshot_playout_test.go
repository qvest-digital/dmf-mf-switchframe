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
// item and then ends its loop, so the snapshot must not carry a's release
// there: it would land on x, refused when x does not loop and a wrong
// release when it does.
func TestPlayoutChannelSnapshots_LoopExitOnlyForTheItemAtTheCursor(t *testing.T) {
	for _, xLoops := range []bool{false, true} {
		mgr := playout.NewManager(playout.ManagerConfig{
			AssetProvider:        playout.NewFakeAssetProvider(make([]playout.DecodedFrame, 300)),
			AllowedLocalPrefixes: []string{"/"},
		})
		// One frame a second leaves a second between ticks to act in.
		mgr.SetPipelineFormat(64, 48, 1)
		pl := &playout.Playlist{ID: "pl", Name: "pl", Items: []playout.PlaylistItem{
			{ID: "x", Type: playout.ItemClip, AssetURI: "local:///x.ts", Title: "x", Loop: xLoops},
			{ID: "a", Type: playout.ItemClip, AssetURI: "local:///a.ts", Title: "a", Loop: true},
		}}
		require.NoError(t, mgr.LoadPlaylist(1, pl))
		require.NoError(t, mgr.JumpTo(1, "a"))
		require.NoError(t, mgr.Start(1))
		a := &App{playoutMgr: mgr}
		status := func() playout.ChannelStatus { return mgr.ChannelStates()[0] }

		inWindow := false
		for attempt := 0; attempt < 3 && !inWindow; attempt++ {
			// Wait for a tick to have put a on air, then act right after it.
			require.Eventually(t, func() bool { return mgr.ChannelElapsedMs(1) > 0 }, 3*time.Second, time.Millisecond)
			require.NoError(t, mgr.EndLoop(1))
			require.NoError(t, mgr.Previous(1))
			snaps := a.playoutChannelSnapshots()
			st := status()
			// Still before the next tick: a on air with its release, the
			// cursor on x. After a tick x would be on air.
			inWindow = st.LoopExit && st.PlayingIndex == 1 && st.ItemIndex == 0
			if !inWindow {
				// A tick ran in between; start over from a.
				require.NoError(t, mgr.JumpTo(1, "a"))
				time.Sleep(1100 * time.Millisecond)
				continue
			}
			require.Equal(t, 0, snaps[0].ChannelIndex)
			require.Equal(t, 0, snaps[0].CurrentItemIndex)
			require.False(t, snaps[0].LoopExit, "x loops %v: a's release went out with x", xLoops)
		}
		require.True(t, inWindow, "no attempt acted between two ticks")
		_ = mgr.Stop(1)
	}
}
