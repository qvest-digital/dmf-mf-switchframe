//go:build cgo && !noffmpeg

package main

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/switcher"
)

// closePlayoutApp closes what initPlayout started on a, and the switcher and
// mixer the test gave it, as App.Close does.
func closePlayoutApp(a *App) {
	a.playoutMgr.Close()
	a.playoutPrefetcher.Stop()
	a.playoutCache.Close()
	a.sw.Close()
	_ = a.mixer.Close()
}

// An engine whose playout was closed leaves no goroutine of it running.
// The playout watchdog ran on after every close, and its stall callback
// held the playout manager and through it the switcher and mixer of each
// engine: fifty in one test process ran out of memory.
func TestInitPlayout_ClosedEngineLeavesNoGoroutines(t *testing.T) {
	run := func() {
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
		require.NoError(t, a.initPlayout())
		closePlayoutApp(a)
	}
	settled := func(want int) int {
		n := runtime.NumGoroutine()
		for deadline := time.Now().Add(5 * time.Second); n > want && time.Now().Before(deadline); n = runtime.NumGoroutine() {
			time.Sleep(10 * time.Millisecond)
		}
		return n
	}
	// The first engine starts what the process keeps once started; the
	// count it leaves stands once it stops falling.
	run()
	base := runtime.NumGoroutine()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n >= base {
			break
		}
		base = n
	}
	for range 3 {
		run()
	}
	require.LessOrEqual(t, settled(base), base, "goroutines left by three closed engines")
}
