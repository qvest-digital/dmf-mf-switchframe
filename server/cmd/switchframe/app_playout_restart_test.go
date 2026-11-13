//go:build cgo && !noffmpeg

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

// A playout channel's PTS start over on every start, from the program's
// last broadcast PTS, whose distance to the clock is the program
// encoder's delay at that moment. The anchor that places the channel's
// video and audio on the engine timeline outlived the run that placed
// it: a channel started again within 2 s of where that anchor puts it was
// placed as the earlier run was, and its audio landed that far off the
// mixer's read point, behind it (silent) or over a ring's length ahead
// (gaps). Each start places the channel afresh.
func TestInitPlayout_RestartedChannelIsPlacedAfresh(t *testing.T) {
	stateDir := t.TempDir()
	cert, err := certs.Generate(time.Hour)
	require.NoError(t, err)
	srv, err := distribution.NewServer(distribution.ServerConfig{Addr: ":0", Cert: cert})
	require.NoError(t, err)
	a := &App{
		cfg: AppConfig{
			StateDir:          stateDir,
			PlayoutCacheMaxGB: 1,
			PreviewResolution: "360p",
			PreviewBitrate:    300000,
		},
		sw:     switcher.New(distribution.NewRelay()),
		server: srv,
		mixer: audio.NewMixer(audio.MixerConfig{
			SampleRate:     48000,
			Channels:       2,
			DecoderFactory: audioDecoderFactory(),
		}),
	}
	require.NoError(t, a.initPlayout())
	t.Cleanup(func() {
		a.playoutMgr.Close()
		a.sw.Close()
		_ = a.mixer.Close()
	})
	a.playoutMgr.SetOnStateChange(func() {})

	clipDir := filepath.Join(stateDir, "clips")
	require.NoError(t, os.MkdirAll(clipDir, 0o755))
	fixture, err := os.ReadFile("../../output/testdata/fixture.ts")
	require.NoError(t, err)
	asset := filepath.Join(clipDir, "fixture.ts")
	require.NoError(t, os.WriteFile(asset, fixture, 0o644))

	const key = "playout:1"
	// An earlier run placed the channel a second ago: its PTS 0 then was
	// the clock then.
	playoutAnchor(key).PTS(0)
	time.Sleep(time.Second)

	// The test engine broadcasts no program, so this run's PTS start at 0
	// again, now.
	require.NoError(t, a.playoutMgr.LoadPlaylist(1, &playout.Playlist{ID: "pl", Name: "pl", Items: []playout.PlaylistItem{
		{ID: "c", Type: playout.ItemClip, AssetURI: asset, Title: "c", Loop: true},
	}}))
	require.NoError(t, a.playoutMgr.Start(1))
	require.NoError(t, a.playoutMgr.Stop(1))

	lag := clock.NowPTS() - playoutAnchor(key).PTS(0)
	require.Less(t, lag, int64(300*90), "the restarted channel's PTS 0 is placed %d ms before now: where the earlier run put it", lag/90)
}
