//go:build cgo && !noffmpeg

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

// countingHandler counts the log records whose message is msg.
type countingHandler struct {
	slog.Handler
	msg string
	n   *atomic.Int64
}

func (h countingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == h.msg {
		h.n.Add(1)
	}
	return h.Handler.Handle(ctx, r)
}

func (h countingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return countingHandler{h.Handler.WithAttrs(as), h.msg, h.n}
}

func (h countingHandler) WithGroup(g string) slog.Handler {
	return countingHandler{h.Handler.WithGroup(g), h.msg, h.n}
}

// A cued playout channel is a source from its cue, and the operator's
// start of it offers it again. The second offer must leave the source as
// the cue registered it: registered afresh, it lost what the operator had
// set on it, its state on the switcher and the mixer started over, and a
// second relay encoder replaced the first. The channel's stop withdraws
// the source once.
func TestInitPlayout_CuedThenStartedChannelIsRegisteredOnce(t *testing.T) {
	var encoders atomic.Int64
	prev := slog.Default()
	// A handler of its own: the default one writes through the log package,
	// which a replaced default sends back into slog.
	slog.SetDefault(slog.New(countingHandler{slog.NewTextHandler(io.Discard, nil), "playout: CPU relay encoder registered", &encoders}))
	t.Cleanup(func() { slog.SetDefault(prev) })

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
	t.Cleanup(func() { closePlayoutApp(a) })
	a.playoutMgr.SetOnStateChange(func() {})

	clipDir := filepath.Join(stateDir, "clips")
	require.NoError(t, os.MkdirAll(clipDir, 0o755))
	fixture, err := os.ReadFile("../../output/testdata/fixture.ts")
	require.NoError(t, err)
	asset := filepath.Join(clipDir, "fixture.ts")
	require.NoError(t, os.WriteFile(asset, fixture, 0o644))

	const key = "playout:1"
	label := func() string { return a.sw.State().Sources[key].Label }
	require.NoError(t, a.playoutMgr.LoadPlaylist(1, &playout.Playlist{ID: "pl", Name: "pl", Items: []playout.PlaylistItem{
		{ID: "c", Type: playout.ItemClip, AssetURI: asset, Title: "c", Loop: true},
	}}))
	require.True(t, a.sw.HasSource(key), "the cue offers the channel")
	require.NoError(t, a.sw.SetLabel(context.Background(), key, "Channel One"))
	require.NoError(t, a.mixer.SetTrim(key, 6))
	require.EqualValues(t, 1, encoders.Load(), "the cue starts one relay encoder")

	require.NoError(t, a.playoutMgr.Start(1))
	require.Equal(t, "Channel One", label(), "the start registered the source afresh")
	require.InDelta(t, 6, a.mixer.ChannelStates()[key].Trim, 1e-9, "the start reset the mixer channel")
	require.EqualValues(t, 1, encoders.Load(), "the start replaced the relay encoder")

	require.NoError(t, a.playoutMgr.Stop(1))
	require.False(t, a.sw.HasSource(key), "the stop withdraws the source")
}
