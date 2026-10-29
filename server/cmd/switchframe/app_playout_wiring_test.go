//go:build cgo && !noffmpeg

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

type countingViewer struct {
	mu           sync.Mutex
	video, audio int
}

func (v *countingViewer) ID() string { return "test" }
func (v *countingViewer) SendVideo(*media.VideoFrame) {
	v.mu.Lock()
	v.video++
	v.mu.Unlock()
}
func (v *countingViewer) SendAudio(*media.AudioFrame) {
	v.mu.Lock()
	v.audio++
	v.mu.Unlock()
}
func (v *countingViewer) SendCaptions(*ccx.CaptionFrame)  {}
func (v *countingViewer) Stats() distribution.ViewerStats { return distribution.ViewerStats{} }
func (v *countingViewer) counts() (int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.video, v.audio
}

// On an engine without a GPU preview encoder, a playout channel's relay
// carries the channel's pictures and its audio once the channel starts:
// the browser's tile and preview monitor show it and the tile meter moves.
func TestInitPlayout_RelayCarriesChannelVideoAndAudioWithoutGPU(t *testing.T) {
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
			ClockDriven:    true,
			DecoderFactory: audioDecoderFactory(),
			EncoderFactory: audioEncoderFactory(),
		}),
	}
	require.NoError(t, a.initPlayout())
	defer a.playoutMgr.Close()
	// State broadcasts need the whole engine; this test is about the relay.
	a.playoutMgr.SetOnStateChange(func() {})

	viewer := &countingViewer{}
	a.playoutRelays[0].AddViewer(viewer)

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

	deadline := time.Now().Add(15 * time.Second)
	for {
		v, au := viewer.counts()
		if v >= 5 && au >= 5 {
			return
		}
		if time.Now().After(deadline) {
			require.FailNow(t, fmt.Sprintf("relay carried %d video and %d audio frames", v, au))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
