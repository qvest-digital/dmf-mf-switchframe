package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/switcher"
)

// A clip loaded into a player and not playing is a source the switcher
// cuts and transitions to, and the first program tick that shows it
// starts it, as loaded: looping or not.
func TestCutToALoadedClipPlayerStartsIt(t *testing.T) {
	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.ts"), []byte("x"), 0o644))
	c := &clip.Clip{Name: "c", Filename: "c.ts", Source: clip.SourceUpload, Codec: "h264", Width: 64, Height: 36, FPSNum: 50, FPSDen: 1, DurationMs: 1000, ByteSize: 1}
	require.NoError(t, store.Add(c))
	mgr := clip.NewManager(store, clip.ManagerConfig{
		DemuxFunc: func(string) ([]clip.BufferedFrame, []clip.BufferedAudioFrame, error) {
			return make([]clip.BufferedFrame, 500), nil, nil
		},
		OutputRate: func() (int, int) { return 50, 1 },
		Timed:      true,
	})
	defer mgr.Close()

	// The program ticks run until the clip starts: they need a relay.
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	defer sw.Close()
	mgr.OnPlayerLifecycle(
		func(_ int, key string) { sw.RegisterTimedReplaySource(key) },
		func(_ int, key string) { sw.UnregisterSource(key) },
	)
	sw.SetOnProgramTick(startClipOnProgramTick(mgr))

	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.SetLoop(1, true))
	require.NoError(t, mgr.Load(2, c.ID))
	require.True(t, sw.HasSource("clip:1"), "a loaded clip player is a source")
	require.True(t, sw.HasSource("clip:2"))

	require.NoError(t, sw.Cut(context.Background(), "clip:1"))
	require.Eventually(t, func() bool { return mgr.PlayerStates()[0].State == clip.StatePlaying }, 2*time.Second, time.Millisecond, "the cut started the clip")
	st := mgr.PlayerStates()
	assert.True(t, st[0].Loop)
	assert.Equal(t, clip.StateLoaded, st[1].State, "the clip not cut to stays cued")

	require.NoError(t, mgr.Stop(1))
	assert.True(t, sw.HasSource("clip:1"), "a stopped clip stays a source")
	assert.Equal(t, "clip:1", sw.ProgramSource())

	require.NoError(t, mgr.Eject(2))
	assert.False(t, sw.HasSource("clip:2"), "an ejected clip player is no source")
}
