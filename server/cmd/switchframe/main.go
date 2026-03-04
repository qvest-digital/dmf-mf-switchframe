// server/cmd/switchframe/main.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/switcher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("switchframe starting")

	// Generate self-signed TLS certificate for WebTransport (≤14 days validity).
	cert, err := certs.Generate(14 * 24 * time.Hour)
	if err != nil {
		return fmt.Errorf("generate certificate: %w", err)
	}
	slog.Info("certificate generated",
		"fingerprint", cert.FingerprintBase64(),
		"expires", cert.NotAfter.Format(time.RFC3339))

	// Deferred pointers: closures below capture sw/mixer before they're
	// assigned. Safe because OnStreamRegistered is only called when external
	// SRT sources connect (after server.Start()), by which time both are set.
	var sw *switcher.Switcher
	var mixer *audio.AudioMixer

	// Create channel-based state publisher for MoQ control track.
	controlPub := control.NewChannelPublisher(16)

	// Create REST API (captures sw pointer; called during server.Start()
	// mux setup, after sw is initialized below).
	var api *control.API

	addr := ":8080"

	config := distribution.ServerConfig{
		Addr: addr,
		Cert: cert,
		ExtraRoutes: func(mux *http.ServeMux) {
			api.RegisterOnMux(mux)
		},
		OnStreamRegistered: func(key string, relay *distribution.Relay) {
			// RegisterStream("program") triggers this callback immediately,
			// before sw is initialized. Guard against that.
			if key == "program" {
				return
			}
			slog.Info("stream registered, adding source", "key", key)
			sw.RegisterSource(key, relay)
			mixer.AddChannel(key)
		},
		OnStreamUnregistered: func(key string) {
			if key == "program" {
				return
			}
			slog.Info("stream unregistered, removing source", "key", key)
			sw.UnregisterSource(key)
			mixer.RemoveChannel(key)
		},
		ControlCh: controlPub.Ch(),
	}

	server, err := distribution.NewServer(config)
	if err != nil {
		return fmt.Errorf("create distribution server: %w", err)
	}

	// Get Prism's relay for "program" — MoQ viewers subscribe to this.
	programRelay := server.RegisterStream("program")

	// Create audio mixer — sends mixed audio to the program relay.
	// DecoderFactory/EncoderFactory enable multi-channel mixing (decode AAC → PCM,
	// mix, encode PCM → AAC). Without them, only passthrough mode works.
	mixer = audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			programRelay.BroadcastAudio(frame)
		},
		DecoderFactory: func(sampleRate, channels int) (audio.AudioDecoder, error) {
			return audio.NewFDKDecoder(sampleRate, channels)
		},
		EncoderFactory: func(sampleRate, channels int) (audio.AudioEncoder, error) {
			return audio.NewFDKEncoder(sampleRate, channels)
		},
	})
	defer mixer.Close()

	// Create switcher with Prism's relay so frames reach MoQ viewers.
	sw = switcher.New(programRelay)
	defer sw.Close()

	// Wire audio mixer to the switcher: all source audio flows through the
	// mixer instead of being forwarded directly from the program source.
	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mixer.IngestFrame(sourceKey, frame)
	})
	sw.SetMixer(mixer)

	// Create REST API now that switcher and mixer exist.
	api = control.NewAPI(sw, control.WithMixer(mixer))

	// Wire AFV: when program source changes, update mixer channel states.
	// This must be registered BEFORE the controlPub callback so AFV state
	// is correct before the state snapshot is broadcast.
	sw.OnStateChange(func(state internal.ControlRoomState) {
		if state.ProgramSource != "" {
			mixer.OnProgramChange(state.ProgramSource)
		}
	})

	// Wire state publisher and health monitor.
	sw.OnStateChange(controlPub.Publish)
	sw.StartHealthMonitor(1 * time.Second)

	slog.Info("starting Prism distribution server", "addr", addr)
	return server.Start(ctx)
}
