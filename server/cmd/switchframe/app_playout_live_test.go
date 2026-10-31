//go:build cgo && !noffmpeg

package main

import (
	"bytes"
	"context"
	"strings"
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
			DecoderFactory: audioDecoderFactory(),
		}),
	}
	require.Nil(t, a.gpu())
	require.NoError(t, a.initPlayout())
	defer a.playoutMgr.Close()
	a.playoutMgr.SetOnStateChange(func() {})

	const w, h = 64, 36
	picture := bytes.Repeat([]byte{0x51}, switcher.ColorYUV420_8bit.FrameSize(w, h))
	nextPicture := bytes.Repeat([]byte{0x62}, switcher.ColorYUV420_8bit.FrameSize(w, h))
	// What the channel airs, in order: 'a' for the first item's input, 'b'
	// for the second's, '-' for anything else, such as the idle picture.
	var mu sync.Mutex
	var aired []byte
	a.playoutMgr.SetRawVideoOutput(func(key string, yuv []byte, fw, fh int, pts int64, isKeyframe bool) {
		if key != "playout:1" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case fw == w && fh == h && bytes.Equal(yuv, picture):
			aired = append(aired, 'a')
		case fw == w && fh == h && bytes.Equal(yuv, nextPicture):
			aired = append(aired, 'b')
		default:
			aired = append(aired, '-')
		}
	})
	airedSoFar := func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(aired)
	}

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
			a.sw.IngestRawVideo("mxl:mxl-1", &switcher.ProcessingFrame{
				Data: append([]byte(nil), picture...), Format: switcher.ColorYUV420_8bit,
				Width: w, Height: h, PTS: pts, DTS: pts, Codec: "h264",
			})
			a.sw.IngestRawVideo("mxl:mxl-2", &switcher.ProcessingFrame{
				Data: append([]byte(nil), nextPicture...), Format: switcher.ColorYUV420_8bit,
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
		return strings.Contains(airedSoFar(), "aaa")
	}, 10*time.Second, 20*time.Millisecond, "the channel must air the input's picture, not its idle picture")

	// The second item's source is armed while the first airs, so its first
	// tick airs its input: the first read of an unarmed source is nil.
	require.NoError(t, a.playoutMgr.Next(1))
	require.Eventually(t, func() bool {
		return strings.Contains(airedSoFar(), "b")
	}, 10*time.Second, 20*time.Millisecond, "the channel must air the next item's input")
	got := airedSoFar()
	fromFirst := got[strings.Index(got, "a"):]
	require.Regexp(t, `^a+b+$`, fromFirst, "nothing else airs between the two inputs")
}
