// server/cmd/switchframe/main.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
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
	"github.com/zsiec/switchframe/server/debug"
	"github.com/zsiec/switchframe/server/demo"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	demoFlag := flag.Bool("demo", false, "Start with simulated camera sources")
	demoVideoDir := flag.String("demo-video", "", "Directory containing MPEG-TS clips for real video demo (requires --demo)")
	flag.Parse()

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
			if h := uiHandler(); h != nil {
				// Mount embedded UI as catch-all (after API routes)
				mux.Handle("/", h)
			}
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

	// Configure transition engine with OpenH264 codec factories
	sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewOpenH264Decoder()
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewOpenH264Encoder(w, h, bitrate, fps)
		},
	})

	// Create output manager for recording and SRT output.
	outputMgr := output.NewOutputManager(programRelay)
	outputMgr.SetSRTWiring(output.SRTConnect, output.SRTAcceptLoop)
	defer outputMgr.Close()

	// Create debug collector for pipeline instrumentation.
	debugCollector := debug.NewCollector()
	debugCollector.Register("switcher", sw)
	debugCollector.Register("mixer", mixer)
	debugCollector.Register("output", outputMgr)

	// Create REST API now that switcher, mixer, and output manager exist.
	api = control.NewAPI(sw, control.WithMixer(mixer), control.WithOutputManager(outputMgr), control.WithDebugCollector(debugCollector))

	// enrichState patches a ControlRoomState snapshot with output status.
	enrichState := func(state internal.ControlRoomState) internal.ControlRoomState {
		if recStatus := outputMgr.RecordingStatus(); recStatus.Active {
			state.Recording = &recStatus
		}
		if srtStatus := outputMgr.SRTOutputStatus(); srtStatus.Active {
			state.SRTOutput = &srtStatus
		}
		return state
	}

	// Wire state publisher: enrich switcher state with output status before broadcast.
	// Note: AFV program changes and crossfade are wired automatically via
	// SetMixer (Switcher calls OnProgramChange/OnCut during Cut).
	sw.OnStateChange(func(state internal.ControlRoomState) {
		controlPub.Publish(enrichState(state))
	})

	// Output state changes (recording start/stop, SRT connect/disconnect)
	// also trigger a full state broadcast.
	outputMgr.OnStateChange(func() {
		controlPub.Publish(enrichState(sw.State()))
	})

	sw.StartHealthMonitor(1 * time.Second)

	demoStats := demo.NewDemoStats()
	if *demoFlag {
		const nCams = 4
		slog.Info("demo mode: starting simulated camera sources", "count", nCams, "videoDir", *demoVideoDir)
		// Register demo streams with Prism so MoQ clients can subscribe.
		// OnStreamRegistered fires synchronously, wiring sw.RegisterSource + mixer.AddChannel.
		relays := make([]*distribution.Relay, nCams)
		for i := range nCams {
			key := fmt.Sprintf("cam%d", i+1)
			relays[i] = server.RegisterStream(key)
			// Set fallback video info for synthetic mode. Real video mode
			// overrides this in StartSources after demuxing SPS data.
			if *demoVideoDir == "" {
				relays[i].SetVideoInfo(distribution.VideoInfo{
					Codec:  "avc1.42C01E",
					Width:  320,
					Height: 240,
				})
			}
		}
		stopDemo := demo.StartSources(ctx, sw, relays, demoStats, *demoVideoDir)
		defer stopDemo()

		// Copy video info from first source to program relay so the MoQ
		// catalog advertises codec/resolution and avcC decoder config.
		// Without this, browsers can't configure their decoder until a
		// keyframe with SPS/PPS arrives in frame extensions.
		if len(relays) > 0 {
			programRelay.SetVideoInfo(relays[0].VideoInfo())
		}
	}
	debugCollector.Register("demo", demoStats)

	// Start a plain HTTP server on TCP for the REST API. Prism's distribution
	// server only listens on QUIC/UDP, so the Vite dev proxy (and curl) can't
	// reach it. This TCP listener mirrors the same API routes.
	apiMux := http.NewServeMux()
	api.RegisterOnMux(apiMux)
	apiMux.HandleFunc("GET /api/cert-hash", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(map[string]string{
			"hash": cert.FingerprintBase64(),
			"addr": addr,
		})
	})
	httpSrv := &http.Server{
		Handler: apiMux,
	}
	httpLn, err := net.Listen("tcp", ":8081")
	if err != nil {
		return fmt.Errorf("listen TCP :8081: %w", err)
	}
	go func() {
		slog.Info("HTTP API server listening", "addr", ":8081")
		if err := httpSrv.Serve(httpLn); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP API server error", "err", err)
		}
	}()
	go func() {
		<-ctx.Done()
		httpSrv.Close()
	}()

	slog.Info("starting Prism distribution server", "addr", addr)
	return server.Start(ctx)
}
