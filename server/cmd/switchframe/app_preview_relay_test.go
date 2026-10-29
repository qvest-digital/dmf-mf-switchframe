//go:build cgo && !noffmpeg

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/preview"
)

// relayVideoSize runs a preview encoder of cfg over 1080p pictures and
// returns the size its relay announces and the size the stream's SPS
// declares.
func relayVideoSize(t *testing.T, cfg preview.Config) (distribution.VideoInfo, int, int) {
	t.Helper()
	relay := &recordingRelay{}
	cfg.Relay = relay
	enc, err := preview.NewEncoder(cfg)
	require.NoError(t, err)
	defer enc.Stop()

	pic := grey420(1920, 1080)
	deadline := time.Now().Add(10 * time.Second)
	for n := 0; ; n++ {
		enc.Send(pic, 1920, 1080, int64(n)*1800)
		if got, info := relay.counts(); got > 0 && len(info) > 0 {
			break
		}
		require.True(t, time.Now().Before(deadline), "no encoded pictures on the relay")
		time.Sleep(20 * time.Millisecond)
	}
	_, info := relay.counts()
	relay.mu.Lock()
	first := relay.video[0]
	relay.mu.Unlock()
	require.True(t, first.IsKeyframe)
	require.NotNil(t, first.SPS)
	w, h := clip.ParseSPSDimensions(first.SPS)
	return info[0], w, h
}

// Under the preview proxy an MXL or SRT source's browser relay is the
// preview size, as the GPU preview proxy encodes it. Encoded at the
// source's 1080p at the preview bitrate, every tile of it cost the
// browser a 1080p decode.
func TestSourcePreviewConfig_EncodesAtPreviewSize(t *testing.T) {
	a := &App{cfg: AppConfig{PreviewProxy: true, PreviewResolution: "480p", PreviewBitrate: 500_000}}
	info, w, h := relayVideoSize(t, a.sourcePreviewConfig("mxl:cam1", nil, 50, 1))
	require.Equal(t, 852, info.Width)
	require.Equal(t, 480, info.Height)
	require.Equal(t, 852, w)
	require.Equal(t, 480, h)
}

// A built-in source's relay exists with or without the preview proxy.
// Under the proxy it is scaled down like every other source's; without
// it, it keeps the source's own size.
func TestBuiltinPreviewConfig_ScalesUnderThePreviewProxy(t *testing.T) {
	a := &App{cfg: AppConfig{PreviewProxy: true, PreviewResolution: "360p"}}
	cfg := a.builtinPreviewConfig("source:colorbars", nil, 50, 1)
	require.Equal(t, 500_000, cfg.Bitrate)
	info, w, h := relayVideoSize(t, cfg)
	require.Equal(t, 640, info.Width)
	require.Equal(t, 360, info.Height)
	require.Equal(t, 640, w)
	require.Equal(t, 360, h)

	a.cfg.PreviewProxy = false
	info, _, _ = relayVideoSize(t, a.builtinPreviewConfig("source:colorbars", nil, 50, 1))
	require.Equal(t, 1920, info.Width)
	require.Equal(t, 1080, info.Height)
}
