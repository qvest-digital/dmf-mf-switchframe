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
	"path/filepath"
	"sync"
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
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// streamCallbackRouter safely forwards OnStreamRegistered/OnStreamUnregistered
// callbacks to the Switcher and AudioMixer, which are initialized after the
// distribution server that invokes these callbacks. Callers must call Init()
// before any stream registrations that need forwarding.
type streamCallbackRouter struct {
	mu       sync.Mutex
	sw       *switcher.Switcher
	mixer    *audio.AudioMixer
	replayMgr *replay.Manager
}

// Init sets the Switcher and AudioMixer targets. After Init returns, all
// subsequent OnRegistered/OnUnregistered calls will be forwarded.
func (r *streamCallbackRouter) Init(sw *switcher.Switcher, mixer *audio.AudioMixer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sw = sw
	r.mixer = mixer
}

// SetReplayManager adds replay manager to the router.
func (r *streamCallbackRouter) SetReplayManager(rm *replay.Manager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replayMgr = rm
}

// OnRegistered handles a new stream. It skips the "program" and "replay"
// streams (internal relays) and guards against calls that arrive before Init().
func (r *streamCallbackRouter) OnRegistered(key string, relay *distribution.Relay) {
	if key == "program" || key == "replay" {
		return
	}
	r.mu.Lock()
	sw, mixer, rm := r.sw, r.mixer, r.replayMgr
	r.mu.Unlock()

	if sw == nil || mixer == nil {
		slog.Warn("stream registered before switcher/mixer initialized, ignoring", "key", key)
		return
	}
	slog.Info("stream registered, adding source", "key", key)
	sw.RegisterSource(key, relay)
	mixer.AddChannel(key)
	_ = mixer.SetAFV(key, true) // cameras default to audio-follows-video

	// Register replay viewer on the source relay.
	if rm != nil {
		if err := rm.AddSource(key); err != nil {
			slog.Warn("replay: could not add source", "key", key, "err", err)
		} else if v := rm.Viewer(key); v != nil {
			relay.AddViewer(v)
		}
	}
}

// OnUnregistered handles a removed stream. It skips the "program" and "replay"
// streams and guards against calls that arrive before Init().
func (r *streamCallbackRouter) OnUnregistered(key string) {
	if key == "program" || key == "replay" {
		return
	}
	r.mu.Lock()
	sw, mixer, rm := r.sw, r.mixer, r.replayMgr
	r.mu.Unlock()

	if sw == nil || mixer == nil {
		slog.Warn("stream unregistered before switcher/mixer initialized, ignoring", "key", key)
		return
	}
	slog.Info("stream unregistered, removing source", "key", key)
	sw.UnregisterSource(key)
	mixer.RemoveChannel(key)

	// Remove replay viewer from the source relay.
	if rm != nil {
		rm.RemoveSource(key)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	demoFlag := flag.Bool("demo", false, "Start with simulated camera sources")
	demoVideoDir := flag.String("demo-video", "", "Directory containing MPEG-TS clips for real video demo (requires --demo)")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	adminAddr := flag.String("admin-addr", ":9090", "Admin/metrics server listen address")
	apiTokenFlag := flag.String("api-token", "", "Bearer token for API authentication (env: SWITCHFRAME_API_TOKEN)")
	frameSyncFlag := flag.Bool("frame-sync", false, "Enable freerun frame synchronizer (aligns sources to common frame boundary)")
	replayBufferSecs := flag.Int("replay-buffer-secs", 60, "Per-source replay buffer duration in seconds (0 to disable, max 300)")
	flag.Parse()

	// Resolve API token: flag > env > auto-generate.
	apiToken := *apiTokenFlag
	if apiToken == "" {
		apiToken = os.Getenv("SWITCHFRAME_API_TOKEN")
	}
	if apiToken == "" {
		var err error
		apiToken, err = control.GenerateToken()
		if err != nil {
			return fmt.Errorf("generate API token: %w", err)
		}
	}

	// Configure structured logging before any slog calls.
	var lvl slog.LevelVar
	if err := lvl.UnmarshalText([]byte(*logLevel)); err != nil {
		return fmt.Errorf("invalid --log-level %q: %w", *logLevel, err)
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

	// Register subsystem metrics on the shared Prometheus registry so they
	// appear at /metrics on the admin server. The returned Metrics struct
	// is passed to subsystems (switcher, mixer, output manager) below.
	appMetrics := metrics.NewMetrics(metrics.Registry)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("switchframe starting", "log_level", *logLevel)
	if *demoFlag {
		slog.Info("demo mode: API authentication disabled")
	} else {
		slog.Info("API authentication enabled", "token_prefix", apiToken[:8]+"...")
		// Print full token to stdout (not stderr) so operators can capture it
		// without it leaking into log files routed from stderr.
		_, _ = fmt.Fprintf(os.Stdout, "\n  API Token: %s\n\n", apiToken)
	}

	// Generate self-signed TLS certificate for WebTransport (≤14 days validity).
	cert, err := certs.Generate(14 * 24 * time.Hour)
	if err != nil {
		return fmt.Errorf("generate certificate: %w", err)
	}
	slog.Info("certificate generated",
		"fingerprint", cert.FingerprintBase64(),
		"expires", cert.NotAfter.Format(time.RFC3339))

	// Stream callback router: safely forwards OnStreamRegistered /
	// OnStreamUnregistered to the Switcher and AudioMixer. The router
	// guards against nil dereferences if callbacks fire before Init().
	var cbRouter streamCallbackRouter

	// Create channel-based state publisher for MoQ control track.
	controlPub := control.NewChannelPublisher(64)

	// Create REST API (captures sw pointer; called during server.Start()
	// mux setup, after sw is initialized below).
	var api *control.API
	// Operator middleware is created after operator store/session manager
	// but the closure captures this variable.
	var operatorMW func(http.Handler) http.Handler

	addr := ":8080"

	// In demo mode, skip auth entirely for ease of use.
	authMW := control.AuthMiddleware(apiToken)
	if *demoFlag {
		authMW = control.NoopAuthMiddleware()
	}

	config := distribution.ServerConfig{
		Addr: addr,
		Cert: cert,
		ExtraRoutes: func(mux *http.ServeMux) {
			// Register API routes on a sub-mux so we can wrap with auth + operator middleware.
			apiSubMux := http.NewServeMux()
			api.RegisterOnMux(apiSubMux)
			// Chain: auth → operator (role/lock check) → handler
			authedAPI := authMW(operatorMW(apiSubMux))
			mux.Handle("/api/", authedAPI)
			if h := uiHandler(); h != nil {
				// Mount embedded UI as catch-all (after API routes)
				mux.Handle("/", h)
			}
		},
		OnStreamRegistered:   cbRouter.OnRegistered,
		OnStreamUnregistered: cbRouter.OnUnregistered,
		ControlCh:            controlPub.Ch(),
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
	mixer := audio.NewMixer(audio.MixerConfig{
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
	mixer.SetMetrics(appMetrics)
	defer func() { _ = mixer.Close() }()

	// Create switcher with Prism's relay so frames reach MoQ viewers.
	sw := switcher.New(programRelay)
	sw.SetMetrics(appMetrics)
	defer sw.Close()

	// Enable frame sync if requested (aligns all sources to common frame boundary).
	if *frameSyncFlag {
		sw.SetFrameSync(true, 0) // 0 = default 30fps
	}

	// Now that both switcher and mixer are initialized, wire them into
	// the stream callback router so future OnStreamRegistered /
	// OnStreamUnregistered calls are forwarded safely.
	cbRouter.Init(sw, mixer)

	// Wire audio mixer to the switcher: all source audio flows through the
	// mixer instead of being forwarded directly from the program source.
	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mixer.IngestFrame(sourceKey, frame)
	})
	sw.SetMixer(mixer)

	// Configure transition engine with auto-detected video codec
	sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return codec.NewVideoDecoder()
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return codec.NewVideoEncoder(w, h, bitrate, fps)
		},
	})

	// Create output manager for recording and SRT output.
	outputMgr := output.NewOutputManager(programRelay)
	outputMgr.SetSRTWiring(output.SRTConnect, output.SRTAcceptLoop)
	outputMgr.SetMetrics(appMetrics)
	defer func() { _ = outputMgr.Close() }()

	// Attach confidence monitor for 1fps program output thumbnail.
	// Lifecycle is owned by outputMgr — its Close() closes the monitor.
	confidenceMon := output.NewConfidenceMonitor(func() (transition.VideoDecoder, error) {
		return codec.NewVideoDecoder()
	})
	outputMgr.SetConfidenceMonitor(confidenceMon)

	// Create debug collector for pipeline instrumentation.
	debugCollector := debug.NewCollector()
	debugCollector.Register("switcher", sw)
	debugCollector.Register("mixer", mixer)
	debugCollector.Register("output", outputMgr)

	// Create preset store for saving/loading production presets.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	presetPath := filepath.Join(homeDir, ".switchframe", "presets.json")
	presetStore, err := preset.NewPresetStore(presetPath)
	if err != nil {
		return fmt.Errorf("create preset store: %w", err)
	}
	slog.Info("preset store initialized", "path", presetPath)

	// Create macro store for automating sequences of switcher operations.
	macroPath := filepath.Join(homeDir, ".switchframe", "macros.json")
	macroStore, err := macro.NewStore(macroPath)
	if err != nil {
		return fmt.Errorf("create macro store: %w", err)
	}
	slog.Info("macro store initialized", "path", macroPath)

	// Create operator store for multi-operator management.
	operatorPath := filepath.Join(homeDir, ".switchframe", "operators.json")
	operatorStore, err := operator.NewStore(operatorPath)
	if err != nil {
		return fmt.Errorf("create operator store: %w", err)
	}
	slog.Info("operator store initialized", "path", operatorPath)

	// Create session manager for operator session tracking and subsystem locks.
	sessionMgr := operator.NewSessionManager()
	defer sessionMgr.Close()

	// Create operator middleware for role/lock enforcement.
	operatorMW = operator.NewMiddleware(operatorStore, sessionMgr)

	// Create graphics compositor for the downstream keyer (DSK).
	// When active, it decodes program frames, composites RGBA overlay, and re-encodes.
	compositor := graphics.NewCompositor()
	compositor.SetCodecFactories(
		func() (transition.VideoDecoder, error) {
			return codec.NewVideoDecoder()
		},
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return codec.NewVideoEncoder(w, h, bitrate, fps)
		},
	)
	compositor.SetResolutionProvider(func() (int, int) {
		vi := programRelay.VideoInfo()
		return vi.Width, vi.Height
	})
	compositor.OnVideoInfoChange(func(sps, pps []byte, width, height int) {
		avcC := moq.BuildAVCDecoderConfig(sps, pps)
		if avcC != nil {
			programRelay.SetVideoInfo(distribution.VideoInfo{
				Codec:         codec.ParseSPSCodecString(sps),
				Width:         width,
				Height:        height,
				DecoderConfig: avcC,
			})
			slog.Info("graphics: updated program relay VideoInfo", "w", width, "h", height)
		}
	})
	sw.SetVideoProcessor(compositor.ProcessFrame)
	defer compositor.Close()

	// Create upstream key processor for chroma/luma keying on source frames.
	// When active, keyed sources are decoded and composited onto the program
	// frame before the DSK compositor runs.
	keyProcessor := graphics.NewKeyProcessor()
	keyBridge := graphics.NewKeyProcessorBridge(keyProcessor)
	keyBridge.SetCodecFactories(
		func() (transition.VideoDecoder, error) {
			return codec.NewVideoDecoder()
		},
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return codec.NewVideoEncoder(w, h, bitrate, fps)
		},
	)
	keyBridge.OnVideoInfoChange(func(sps, pps []byte, width, height int) {
		avcC := moq.BuildAVCDecoderConfig(sps, pps)
		if avcC != nil {
			programRelay.SetVideoInfo(distribution.VideoInfo{
				Codec:         codec.ParseSPSCodecString(sps),
				Width:         width,
				Height:        height,
				DecoderConfig: avcC,
			})
			slog.Info("keyer: updated program relay VideoInfo", "w", width, "h", height)
		}
	})
	sw.SetKeyProcessor(keyProcessor)
	sw.SetKeyFillIngestor(keyBridge.IngestFillFrame)
	sw.SetKeyBridgeProcessor(keyBridge.ProcessFrame)
	defer keyBridge.Close()

	// Create replay manager for instant replay / slow-motion.
	var replayMgr *replay.Manager
	replayRelay := server.RegisterStream("replay")
	if *replayBufferSecs > 0 {
		replayMgr = replay.NewManager(replayRelay, replay.Config{
			BufferDurationSecs: *replayBufferSecs,
		}, func() (transition.VideoDecoder, error) {
			return codec.NewVideoDecoder()
		}, func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return codec.NewVideoEncoder(w, h, bitrate, fps)
		})
		defer replayMgr.Close()
		cbRouter.SetReplayManager(replayMgr)
		debugCollector.Register("replay", replayMgr)
		slog.Info("replay manager initialized", "bufferSecs", *replayBufferSecs)
	}

	// Create REST API now that switcher, mixer, and output manager exist.
	apiOpts := []control.APIOption{
		control.WithMixer(mixer),
		control.WithOutputManager(outputMgr),
		control.WithDebugCollector(debugCollector),
		control.WithPresetStore(presetStore),
		control.WithCompositor(compositor),
		control.WithMacroStore(macroStore),
		control.WithKeyer(keyProcessor),
		control.WithOperatorStore(operatorStore),
		control.WithSessionManager(sessionMgr),
	}
	if replayMgr != nil {
		apiOpts = append(apiOpts, control.WithReplayManager(replayMgr))
	}
	api = control.NewAPI(sw, apiOpts...)

	// enrichState patches a ControlRoomState snapshot with output + graphics status.
	// gfxOverride, if non-nil, is used instead of calling compositor.Status()
	// (which would deadlock when called from the compositor's own callback).
	enrichState := func(state internal.ControlRoomState, gfxOverride *graphics.State) internal.ControlRoomState {
		if p := api.LastOperator(); p != nil {
			state.LastChangedBy = *p
		}
		if recStatus := outputMgr.RecordingStatus(); recStatus.Active {
			state.Recording = &recStatus
		}
		if srtStatus := outputMgr.SRTOutputStatus(); srtStatus.Active {
			state.SRTOutput = &srtStatus
		}
		var gfxStatus graphics.State
		if gfxOverride != nil {
			gfxStatus = *gfxOverride
		} else {
			gfxStatus = compositor.Status()
		}
		if gfxStatus.Active {
			state.Graphics = &internal.GraphicsState{
				Active:       gfxStatus.Active,
				Template:     gfxStatus.Template,
				FadePosition: gfxStatus.FadePosition,
			}
		}
		// Enrich with operator and lock state.
		if operatorStore.Count() > 0 {
			operators := operatorStore.List()
			sessions := sessionMgr.ActiveSessions()
			connectedSet := make(map[string]bool, len(sessions))
			for _, s := range sessions {
				connectedSet[s.OperatorID] = true
			}
			opInfos := make([]internal.OperatorInfo, len(operators))
			for i, op := range operators {
				opInfos[i] = internal.OperatorInfo{
					ID:        op.ID,
					Name:      op.Name,
					Role:      string(op.Role),
					Connected: connectedSet[op.ID],
				}
			}
			state.Operators = opInfos

			locks := sessionMgr.ActiveLocks()
			if len(locks) > 0 {
				lockMap := make(map[string]internal.LockInfo, len(locks))
				for sub, info := range locks {
					lockMap[string(sub)] = internal.LockInfo{
						HolderID:   info.HolderID,
						HolderName: info.HolderName,
						AcquiredAt: info.AcquiredAt.UnixMilli(),
					}
				}
				state.Locks = lockMap
			}
		}

		if replayMgr != nil {
			rs := replayMgr.Status()
			if rs.State != "idle" || rs.MarkIn != nil || len(rs.Buffers) > 0 {
				rState := &internal.ReplayState{
					State:      string(rs.State),
					Source:     rs.Source,
					Speed:      rs.Speed,
					Loop:       rs.Loop,
					Position:   rs.Position,
					MarkIn:     rs.MarkInUnixMs(),
					MarkOut:    rs.MarkOutUnixMs(),
					MarkSource: rs.MarkSource,
				}
				for _, b := range rs.Buffers {
					rState.Buffers = append(rState.Buffers, internal.ReplayBufferInfo{
						Source:       b.Source,
						FrameCount:   b.FrameCount,
						GOPCount:     b.GOPCount,
						DurationSecs: b.DurationSecs,
						BytesUsed:    b.BytesUsed,
					})
				}
				state.Replay = rState
			}
		}
		return state
	}

	// Allow REST API handlers to return enriched state (with output, graphics,
	// operator, and replay information) instead of the raw switcher state.
	api.SetEnrichFunc(func(s internal.ControlRoomState) internal.ControlRoomState {
		return enrichState(s, nil)
	})

	// Wire state publisher: enrich switcher state with output status before broadcast.
	// Note: AFV program changes and crossfade are wired automatically via
	// SetMixer (Switcher calls OnProgramChange/OnCut during Cut).
	sw.OnStateChange(func(state internal.ControlRoomState) {
		controlPub.Publish(enrichState(state, nil))
	})

	// Output state changes (recording start/stop, SRT connect/disconnect)
	// also trigger a full state broadcast.
	outputMgr.OnStateChange(func() {
		var empty string
		api.SetLastOperator(&empty)
		controlPub.Publish(enrichState(sw.State(), nil))
	})

	// Graphics overlay state changes: the callback receives a state snapshot
	// directly (avoids deadlock — can't call compositor.Status() from inside
	// the compositor's lock).
	compositor.OnStateChange(func(gfxState graphics.State) {
		var empty string
		api.SetLastOperator(&empty)
		controlPub.Publish(enrichState(sw.State(), &gfxState))
	})

	// Replay state changes trigger a full state broadcast.
	if replayMgr != nil {
		replayMgr.OnStateChange(func() {
			var empty string
			api.SetLastOperator(&empty)
			controlPub.Publish(enrichState(sw.State(), nil))
		})
	}

	// Operator session/lock changes trigger a full state broadcast.
	sessionMgr.OnStateChange(func() {
		var empty string
		api.SetLastOperator(&empty)
		controlPub.Publish(enrichState(sw.State(), nil))
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

	// Start admin server (Prometheus metrics, health, readiness, pprof).
	stopAdmin := StartAdminServer(ctx, *adminAddr)
	defer stopAdmin()

	// All components initialized — mark ready for readiness probe.
	readyFlag.Store(true)

	// Start a plain HTTP server on TCP for the REST API. Prism's distribution
	// server only listens on QUIC/UDP, so the Vite dev proxy (and curl) can't
	// reach it. This TCP listener mirrors the same API routes.
	apiMux := http.NewServeMux()
	api.RegisterOnMux(apiMux)
	apiMux.HandleFunc("GET /api/cert-hash", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"hash": cert.FingerprintBase64(),
			"addr": addr,
		})
	})
	var apiHandler http.Handler = apiMux
	apiHandler = operatorMW(apiHandler)
	apiHandler = control.MetricsMiddleware(apiHandler)
	apiHandler = control.LoggerMiddleware(slog.Default())(apiHandler)
	apiHandler = authMW(apiHandler)
	httpSrv := &http.Server{
		Handler: apiHandler,
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
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP API server shutdown error", "err", err)
		}
	}()

	slog.Info("starting Prism distribution server", "addr", addr)
	return server.Start(ctx)
}
