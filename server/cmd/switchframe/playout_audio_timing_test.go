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

// A playout channel hands each video frame's AAC frames over with it, on
// the channel's frame ticker, under PTS that run on without a gap. Placed on
// the timeline by those PTS, the channel's audio stays on the program read
// point; each AAC frame placed at its arrival lands up to a frame period
// off it and the mixer keeps splicing the channel back.
func TestPlayoutAudioStaysOnTheReadPoint(t *testing.T) {
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
	defer a.playoutMgr.Close()
	a.playoutMgr.SetOnStateChange(func() {})

	clipDir := filepath.Join(stateDir, "clips")
	require.NoError(t, os.MkdirAll(clipDir, 0o755))
	fixture, err := os.ReadFile("../../output/testdata/fixture.ts")
	require.NoError(t, err)
	asset := filepath.Join(clipDir, "fixture.ts")
	require.NoError(t, os.WriteFile(asset, fixture, 0o644))
	require.NoError(t, a.playoutMgr.LoadPlaylist(1, &playout.Playlist{ID: "pl", Name: "pl", Items: []playout.PlaylistItem{
		{ID: "c", Type: playout.ItemClip, AssetURI: asset, Title: "c", Loop: true},
	}}))
	require.NoError(t, a.playoutMgr.Start(1))
	defer func() { _ = a.playoutMgr.Stop(1) }()
	a.mixer.SetActive("playout:1", true)

	// Ticks read contiguous periods, as the program does, with slack for
	// a loaded host, from the channel's first audio on. The first ticks
	// place the channel's ring on the read point. The fixture's audio runs
	// 0.7% longer than its video, which the channel stamps from one start,
	// so the window stays short of that drift reaching a period.
	const n = 960
	const warmup, ticks = 10, 60
	rate := clock.Rate{Num: 48000, Den: 1}
	deadline := time.Now().Add(15 * time.Second)
	read := clock.TimestampToIndex(rate, clock.TAINow()) - 5*n
	start := time.Now()
	heard, placed := -1, int64(0)
	for i := 0; heard < 0 || i < heard+warmup+ticks; i++ {
		require.False(t, time.Now().After(deadline), "playout channel delivered no audio")
		if heard >= 0 && i == heard+warmup {
			placed = a.mixer.ChannelRealigns()["playout:1"]
		}
		time.Sleep(time.Until(start.Add(time.Duration(i) * 20 * time.Millisecond)))
		if len(a.mixer.ProduceSamples(read, n, 0)) > 0 && heard < 0 {
			heard = i
		}
		read += n
	}

	// A host scheduling stall that makes the channel's ticker skip a frame
	// can still cost one.
	require.LessOrEqual(t, a.mixer.ChannelRealigns()["playout:1"]-placed, int64(1),
		"playout audio with continuous PTS must not be spliced back onto the read point")
}
