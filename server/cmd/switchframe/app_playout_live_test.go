//go:build cgo && !noffmpeg

package main

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

// On an engine without a GPU, a live playlist item aired the channel's idle
// picture instead of the input it names: the channel's source tap read the
// GPU source manager only.
func TestInitPlayout_LiveItemAirsInputWithoutGPU(t *testing.T) {
	cert, err := certs.Generate(time.Hour)
	require.NoError(t, err)
	srv, err := distribution.NewServer(distribution.ServerConfig{Addr: ":0", Cert: cert})
	require.NoError(t, err)
	a := &App{
		cfg: AppConfig{
			StateDir:          t.TempDir(),
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
	require.Nil(t, a.gpu())
	require.NoError(t, a.initPlayout())
	defer a.playoutMgr.Close()
	a.playoutMgr.SetOnStateChange(func() {})

	const w, h = 64, 36
	picture := bytes.Repeat([]byte{0x51}, switcher.ColorYUV420_8bit.FrameSize(w, h))
	var mu sync.Mutex
	aired := false
	a.playoutMgr.SetRawVideoOutput(func(key string, yuv []byte, fw, fh int, pts int64, isKeyframe bool) {
		if key == "playout:1" && fw == w && fh == h && bytes.Equal(yuv, picture) {
			mu.Lock()
			aired = true
			mu.Unlock()
		}
	})

	a.sw.RegisterMXLSourceLabeled("mxl:mxl-1", "sender-a")
	a.sw.RegisterMXLSourceLabeled("mxl:mxl-2", "sender-b")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for pts := int64(0); ; pts += 1800 {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			// The next item's input first, so that any picture of the
			// input on air that came after both were armed implies one of it.
			a.sw.IngestRawVideo("mxl:mxl-2", &switcher.ProcessingFrame{
				Data: append([]byte(nil), picture...), Format: switcher.ColorYUV420_8bit,
				Width: w, Height: h, PTS: pts, DTS: pts, Codec: "h264",
			})
			a.sw.IngestRawVideo("mxl:mxl-1", &switcher.ProcessingFrame{
				Data: append([]byte(nil), picture...), Format: switcher.ColorYUV420_8bit,
				Width: w, Height: h, PTS: pts, DTS: pts, Codec: "h264",
			})
		}
	}()

	require.NoError(t, a.playoutMgr.LoadPlaylist(1, &playout.Playlist{ID: "pl", Name: "pl", Items: []playout.PlaylistItem{
		{ID: "l", Type: playout.ItemLive, Source: "mxl:mxl-1", Title: "cam"},
		{ID: "n", Type: playout.ItemLive, Source: "mxl:mxl-2", Title: "next cam"},
	}}))
	require.NoError(t, a.playoutMgr.Start(1))
	defer func() { _ = a.playoutMgr.Stop(1) }()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return aired
	}, 10*time.Second, 20*time.Millisecond, "the channel must air the input's picture, not its idle picture")

	// Airing the next item's input on its first tick needs its picture
	// before anything reads it: the first read of an unarmed source is nil.
	mu.Lock()
	aired = false
	mu.Unlock()
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return aired
	}, 10*time.Second, 20*time.Millisecond)
	_, armed := a.sw.ReadLiveTap("mxl:mxl-2", nil)
	require.True(t, armed, "the item after the one on air must be armed")
}
