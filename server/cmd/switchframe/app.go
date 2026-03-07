package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/debug"
	"github.com/zsiec/switchframe/server/demo"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/switcher"
)

// App holds all subsystems for the switchframe server. Init methods are called
// in order before Run() starts the Prism distribution server.
type App struct {
	cfg AppConfig

	// Infrastructure
	cert       *certs.CertInfo
	appMetrics *metrics.Metrics
	controlPub *control.ChannelPublisher

	// Prism server + relays
	server       *distribution.Server
	programRelay *distribution.Relay
	replayRelay  *distribution.Relay

	// Core engine
	sw    *switcher.Switcher
	mixer *audio.AudioMixer

	// Output
	outputMgr     *output.OutputManager
	confidenceMon *output.ConfidenceMonitor

	// Subsystems
	debugCollector *debug.Collector
	presetStore    *preset.PresetStore
	macroStore     *macro.Store
	operatorStore  *operator.Store
	sessionMgr     *operator.SessionManager
	compositor     *graphics.Compositor
	keyProcessor   *graphics.KeyProcessor
	keyBridge      *graphics.KeyProcessorBridge
	replayMgr      *replay.Manager

	// API + middleware
	api        *control.API
	authMW     func(http.Handler) http.Handler
	operatorMW func(http.Handler) http.Handler
}

// initInfra sets up certificates, logging, codec probing, metrics, and the
// control publisher.
func (a *App) initInfra() error {
	// Configure structured logging before any slog calls.
	var lvl slog.LevelVar
	if err := lvl.UnmarshalText([]byte(a.cfg.LogLevel)); err != nil {
		return fmt.Errorf("invalid --log-level %q: %w", a.cfg.LogLevel, err)
	}
	opts := &slog.HandlerOptions{Level: &lvl}
	var handler slog.Handler
	if os.Getenv("APP_ENV") == "production" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))

	// Probe available video codecs at startup (cached for process lifetime).
	encName, decName := codec.ProbeEncoders()
	slog.Info("video codec selected", "encoder", encName, "decoder", decName)

	// Register subsystem metrics on the shared Prometheus registry.
	a.appMetrics = metrics.NewMetrics(metrics.Registry)

	slog.Info("switchframe starting", "log_level", a.cfg.LogLevel)
	if a.cfg.Demo {
		slog.Info("demo mode: API authentication disabled")
	} else {
		slog.Info("API authentication enabled", "token_prefix", a.cfg.APIToken[:8]+"...")
		// Print full token to stdout (not stderr) so operators can capture it
		// without it leaking into log files routed from stderr.
		_, _ = fmt.Fprintf(os.Stdout, "\n  API Token: %s\n\n", a.cfg.APIToken)
	}

	// Generate self-signed TLS certificate for WebTransport (<=14 days validity).
	cert, err := certs.Generate(14 * 24 * time.Hour)
	if err != nil {
		return fmt.Errorf("generate certificate: %w", err)
	}
	a.cert = cert
	slog.Info("certificate generated",
		"fingerprint", cert.FingerprintBase64(),
		"expires", cert.NotAfter.Format(time.RFC3339))

	// Create channel-based state publisher for MoQ control track.
	a.controlPub = control.NewChannelPublisher(64)

	// In demo mode, skip auth entirely for ease of use.
	a.authMW = control.AuthMiddleware(a.cfg.APIToken)
	if a.cfg.Demo {
		a.authMW = control.NoopAuthMiddleware()
	}

	return nil
}

// initPrismServer creates the Prism distribution server and registers the
// program and replay relays.
func (a *App) initPrismServer() error {
	config := distribution.ServerConfig{
		Addr: a.cfg.Addr,
		Cert: a.cert,
		ExtraRoutes: func(mux *http.ServeMux) {
			// Register API routes on a sub-mux so we can wrap with auth + operator middleware.
			apiSubMux := http.NewServeMux()
			a.api.RegisterOnMux(apiSubMux)
			// Chain: auth -> operator (role/lock check) -> handler
			authedAPI := a.authMW(a.operatorMW(apiSubMux))
			mux.Handle("/api/", authedAPI)
			if h := uiHandler(); h != nil {
				// Mount embedded UI as catch-all (after API routes)
				mux.Handle("/", h)
			}
		},
		OnStreamRegistered:   a.onStreamRegistered,
		OnStreamUnregistered: a.onStreamUnregistered,
		ControlCh:            a.controlPub.Ch(),
	}

	server, err := distribution.NewServer(config)
	if err != nil {
		return fmt.Errorf("create distribution server: %w", err)
	}
	a.server = server

	// Get Prism's relay for "program" -- MoQ viewers subscribe to this.
	a.programRelay = server.RegisterStream("program")

	return nil
}

// initCoreEngine creates the audio mixer and switcher, wires audio handling,
// and configures the transition engine.
func (a *App) initCoreEngine() error {
	// Create audio mixer -- sends mixed audio to the program relay.
	a.mixer = audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			a.programRelay.BroadcastAudio(frame)
		},
		DecoderFactory: audioDecoderFactory(),
		EncoderFactory: audioEncoderFactory(),
	})
	a.mixer.SetMetrics(a.appMetrics)

	// Create switcher with Prism's relay so frames reach MoQ viewers.
	a.sw = switcher.New(a.programRelay)
	a.sw.SetMetrics(a.appMetrics)

	// Enable frame sync if requested.
	if a.cfg.FrameSync {
		a.sw.SetFrameSync(true, 0) // 0 = default 30fps
	}

	// Wire audio mixer to the switcher.
	a.sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		a.mixer.IngestFrame(sourceKey, frame)
	})
	a.sw.SetMixer(a.mixer)

	// Configure transition engine with auto-detected video codec.
	a.sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: decoderFactory(),
	})

	return nil
}

// initOutput creates the output manager, wires SRT, and attaches the
// confidence monitor.
func (a *App) initOutput() error {
	a.outputMgr = output.NewOutputManager(a.programRelay)
	a.outputMgr.SetSRTWiring(output.SRTConnect, output.SRTAcceptLoop)
	a.outputMgr.SetMetrics(a.appMetrics)

	// Attach confidence monitor for 1fps program output thumbnail.
	a.confidenceMon = output.NewConfidenceMonitor(decoderFactory())
	a.outputMgr.SetConfidenceMonitor(a.confidenceMon)

	return nil
}

// initSubsystems creates the debug collector, stores, graphics compositor,
// upstream keyer, and replay manager.
func (a *App) initSubsystems() error {
	// Debug collector for pipeline instrumentation.
	a.debugCollector = debug.NewCollector()
	a.debugCollector.Register("switcher", a.sw)
	a.debugCollector.Register("mixer", a.mixer)
	a.debugCollector.Register("output", a.outputMgr)

	// Stores: presets, macros, operators.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}

	presetPath := filepath.Join(homeDir, ".switchframe", "presets.json")
	a.presetStore, err = preset.NewPresetStore(presetPath)
	if err != nil {
		return fmt.Errorf("create preset store: %w", err)
	}
	slog.Info("preset store initialized", "path", presetPath)

	macroPath := filepath.Join(homeDir, ".switchframe", "macros.json")
	a.macroStore, err = macro.NewStore(macroPath)
	if err != nil {
		return fmt.Errorf("create macro store: %w", err)
	}
	slog.Info("macro store initialized", "path", macroPath)

	operatorPath := filepath.Join(homeDir, ".switchframe", "operators.json")
	a.operatorStore, err = operator.NewStore(operatorPath)
	if err != nil {
		return fmt.Errorf("create operator store: %w", err)
	}
	slog.Info("operator store initialized", "path", operatorPath)

	// Session manager for operator session tracking and subsystem locks.
	a.sessionMgr = operator.NewSessionManager()

	// Operator middleware for role/lock enforcement.
	a.operatorMW = operator.NewMiddleware(a.operatorStore, a.sessionMgr)

	// Graphics compositor (DSK).
	a.compositor = graphics.NewCompositor()
	a.compositor.SetResolutionProvider(func() (int, int) {
		vi := a.programRelay.VideoInfo()
		return vi.Width, vi.Height
	})
	a.sw.SetCompositor(a.compositor)

	// Upstream key processor (chroma/luma keying).
	a.keyProcessor = graphics.NewKeyProcessor()
	a.keyBridge = graphics.NewKeyProcessorBridge(a.keyProcessor)
	a.keyBridge.SetDecoderFactory(decoderFactory())
	a.sw.SetKeyBridge(a.keyBridge)
	a.sw.SetKeyFillIngestor(a.keyBridge.IngestFillFrame)

	// Pipeline codec pool: single decode/encode cycle for the video processing chain.
	a.sw.SetPipelineCodecs(decoderFactory(), encoderFactory())
	a.sw.SetPipelineVideoInfoCallback(a.videoInfoCallback("pipeline"))

	// Replay manager.
	a.replayRelay = a.server.RegisterStream("replay")
	if a.cfg.ReplayBufferSecs > 0 {
		a.replayMgr = replay.NewManager(
			a.replayRelay,
			replay.Config{BufferDurationSecs: a.cfg.ReplayBufferSecs},
			decoderFactory(),
			encoderFactory(),
		)
		a.debugCollector.Register("replay", a.replayMgr)

		// Wire replay playback lifecycle: register/unregister replay as a
		// virtual switcher source + mixer channel.
		a.replayMgr.OnPlaybackLifecycle(
			func() {
				a.sw.RegisterVirtualSource("replay", a.replayRelay)
				a.mixer.AddChannel("replay")
				_ = a.mixer.SetAFV("replay", true)
				// If program is already "replay" (user cut before OnReady),
				// re-trigger AFV activation so the new channel becomes active.
				a.mixer.OnProgramChange(a.sw.ProgramSource())
			},
			func() {
				a.sw.UnregisterSource("replay")
				a.mixer.RemoveChannel("replay")
			},
		)

		// Wire replay VideoInfo so MoQ subscribers can discover tracks.
		a.replayMgr.OnVideoInfoChange(func(sps, pps []byte, w, h int) {
			avcC := a.buildAVCConfig(sps, pps)
			if avcC != nil {
				a.replayRelay.SetVideoInfo(a.buildVideoInfo(sps, avcC, w, h))
				slog.Info("replay: updated replay relay VideoInfo", "w", w, "h", h)
			}
		})

		// Anchor replay PTS to program timeline to prevent backward jumps.
		a.replayMgr.SetPTSProvider(a.sw.LastBroadcastVideoPTS)

		slog.Info("replay manager initialized", "bufferSecs", a.cfg.ReplayBufferSecs)
	}

	return nil
}

// initAPI creates the REST API and wires state callbacks.
func (a *App) initAPI() error {
	apiOpts := []control.APIOption{
		control.WithMixer(a.mixer),
		control.WithOutputManager(a.outputMgr),
		control.WithDebugCollector(a.debugCollector),
		control.WithPresetStore(a.presetStore),
		control.WithCompositor(a.compositor),
		control.WithMacroStore(a.macroStore),
		control.WithKeyer(a.keyProcessor),
		control.WithOperatorStore(a.operatorStore),
		control.WithSessionManager(a.sessionMgr),
	}
	if a.replayMgr != nil {
		apiOpts = append(apiOpts, control.WithReplayManager(a.replayMgr))
	}
	a.api = control.NewAPI(a.sw, apiOpts...)

	// Wire all state callbacks (enrichState, broadcastState, etc.).
	a.wireStateCallbacks()

	return nil
}

// buildAVCConfig constructs an AVC decoder configuration from SPS/PPS.
func (a *App) buildAVCConfig(sps, pps []byte) []byte {
	return moq.BuildAVCDecoderConfig(sps, pps)
}

// buildVideoInfo constructs a VideoInfo from SPS data, avcC bytes, and
// resolution.
func (a *App) buildVideoInfo(sps, avcC []byte, width, height int) distribution.VideoInfo {
	return distribution.VideoInfo{
		Codec:         codec.ParseSPSCodecString(sps),
		Width:         width,
		Height:        height,
		DecoderConfig: avcC,
	}
}

// Run starts the health monitor, demo mode, admin server, HTTP API server,
// and the Prism distribution server. It blocks until the context is cancelled.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	a.sw.StartHealthMonitor(1 * time.Second)

	demoStats := demo.NewDemoStats()
	if a.cfg.Demo {
		const nCams = 4
		slog.Info("demo mode: starting simulated camera sources", "count", nCams, "videoDir", a.cfg.DemoVideoDir)
		relays := make([]*distribution.Relay, nCams)
		for i := range nCams {
			key := fmt.Sprintf("cam%d", i+1)
			relays[i] = a.server.RegisterStream(key)
			if a.cfg.DemoVideoDir == "" {
				relays[i].SetVideoInfo(distribution.VideoInfo{
					Codec:  "avc1.42C01E",
					Width:  320,
					Height: 240,
				})
			}
		}
		stopDemo := demo.StartSources(ctx, a.sw, relays, demoStats, a.cfg.DemoVideoDir)
		defer stopDemo()

		// Copy video info from first source to program relay.
		if len(relays) > 0 {
			a.programRelay.SetVideoInfo(relays[0].VideoInfo())
		}
	}
	a.debugCollector.Register("demo", demoStats)

	// Start admin server (Prometheus metrics, health, readiness, pprof).
	stopAdmin := StartAdminServer(ctx, a.cfg.AdminAddr)
	defer stopAdmin()

	// Start HTTP API server on TCP :8081.
	stopHTTP, err := a.startHTTPAPIServer(ctx)
	if err != nil {
		return err
	}
	defer stopHTTP()

	// All components initialized -- mark ready for readiness probe.
	readyFlag.Store(true)

	slog.Info("starting Prism distribution server", "addr", a.cfg.Addr)
	return a.server.Start(ctx)
}

// Close cleans up all subsystems in reverse initialization order.
func (a *App) Close() {
	if a.keyBridge != nil {
		a.keyBridge.Close()
	}
	if a.compositor != nil {
		a.compositor.Close()
	}
	if a.replayMgr != nil {
		a.replayMgr.Close()
	}
	if a.sessionMgr != nil {
		a.sessionMgr.Close()
	}
	if a.outputMgr != nil {
		_ = a.outputMgr.Close()
	}
	if a.sw != nil {
		a.sw.Close()
	}
	if a.mixer != nil {
		_ = a.mixer.Close()
	}
}
