package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/zsiec/prism/certs"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/comms"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/debug"
	"github.com/zsiec/switchframe/server/demo"
	"github.com/zsiec/switchframe/server/fastctrl"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/graphics/textrender"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/layout"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/perf"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/preview"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/scte104"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// errDiscoverExit is returned by initMXL when --mxl-discover completes
// successfully. The caller should treat this as a clean exit rather than
// calling os.Exit directly (which would skip deferred cleanup).
var errDiscoverExit = errors.New("mxl discover completed")

// tokenPrefix returns a safe display prefix for an API token.
func tokenPrefix(token string) string {
	if len(token) == 0 {
		return "***"
	}
	n := len(token)
	if n > 8 {
		n = 8
	}
	return token[:n] + "..."
}

// App holds all subsystems for the switchframe server. Init methods are called
// in order before Run() starts the Prism distribution server.
type App struct {
	cfg AppConfig

	// Infrastructure
	cert         *certs.CertInfo
	externalCert bool
	appMetrics   *metrics.Metrics
	controlPub   *control.ChannelPublisher

	// Prism server + relays
	server              *distribution.Server
	programRelay        *distribution.Relay
	rawProgramRelay     *distribution.Relay
	programPreviewRelay *distribution.Relay
	replayRelay         *distribution.Relay

	// Program preview encoder (low-bitrate H.264 for browser program monitor)
	programPreviewEnc *preview.Encoder

	// Core engine
	sw    *switcher.Switcher
	mixer *audio.Mixer

	// Output
	outputMgr     *output.Manager
	confidenceMon *output.ConfidenceMonitor

	// Subsystems
	debugCollector   *debug.Collector
	presetStore      *preset.Store
	macroStore       *macro.Store
	operatorStore    *operator.Store
	sessionMgr       *operator.SessionManager
	compositor       *graphics.Compositor
	keyProcessor     *graphics.KeyProcessor
	keyBridge        *graphics.KeyProcessorBridge
	replayMgr        *replay.Manager
	stingerStore     *stinger.Store
	layoutCompositor *layout.Compositor
	layoutStore      *layout.Store
	fastCtrl         *fastctrl.Dispatcher
	stmapRegistry    *stmap.Registry
	stmapStore       *stmap.Store

	// Text rendering engines
	textRenderer   *textrender.Renderer
	tickerEngine   *graphics.TickerEngine
	textAnimEngine *graphics.TextAnimationEngine

	// Closed captions
	captionMgr *caption.Manager

	// Operator comms
	commsMgr *comms.Manager

	// Clip players
	clipMgr    *clip.Manager
	clipStore  *clip.Store
	clipRelays [clip.MaxPlayers]*distribution.Relay

	// SCTE-35 signaling
	scte35Injector *scte35.Injector
	scte35Rules    *scte35.RulesStore

	// SRT input
	srtListener  *srt.Listener
	srtCaller    *srt.Caller
	srtStore     *srt.Store
	srtStats     *srt.StatsManager
	srtSources   map[string]*srtSourceState
	srtSourcesMu sync.Mutex
	srtCtx       context.Context

	// Source A/V PTS tracking: broadcast encoders often set audio PTS ahead
	// of video PTS by several hundred ms. Our system discards source PTS
	// and assigns wall-clock PTS, destroying this relationship. We track
	// the source PTS gap and add it to the lip-sync offset to compensate.
	sourceLastVideoPTS atomic.Int64 // latest source video PTS (linearized)
	sourceLastAudioPTS atomic.Int64 // latest source audio PTS (linearized)
	sourceAVGapInited  atomic.Bool  // true after both video and audio PTS seen

	// MXL integration
	mxlInstance *mxl.Instance
	mxlSources  []*mxl.Source
	mxlOutput   *mxl.Output

	// Performance monitoring
	perfSampler *perf.Sampler

	// API + middleware
	api        *control.API
	authMW     func(http.Handler) http.Handler
	operatorMW func(http.Handler) http.Handler

	// Background goroutine tracking for clean shutdown
	bgWG sync.WaitGroup
}

// statePath joins path components under the state directory.
func (a *App) statePath(elem ...string) string {
	return filepath.Join(append([]string{a.cfg.StateDir}, elem...)...)
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
	a.appMetrics = metrics.NewMetrics(metrics.GetRegistry())

	slog.Info("switchframe starting", "log_level", a.cfg.LogLevel)
	if a.cfg.Demo {
		slog.Info("demo mode: API authentication disabled")
	} else {
		slog.Info("API authentication enabled", "token_prefix", tokenPrefix(a.cfg.APIToken))
		// Print full token to stdout (not stderr) so operators can capture it
		// without it leaking into log files routed from stderr.
		_, _ = fmt.Fprintf(os.Stdout, "\n  API Token: %s\n\n", a.cfg.APIToken)
	}

	logSystemTuning(slog.Default())

	// Load external TLS certificate (e.g. from mkcert) or generate self-signed.
	if a.cfg.TLSCert != "" && a.cfg.TLSKey != "" {
		tlsCert, err := tls.LoadX509KeyPair(a.cfg.TLSCert, a.cfg.TLSKey)
		if err != nil {
			return fmt.Errorf("load TLS certificate: %w", err)
		}
		parsed, err := x509.ParseCertificate(tlsCert.Certificate[0])
		if err != nil {
			return fmt.Errorf("parse TLS certificate: %w", err)
		}
		fingerprint := sha256.Sum256(tlsCert.Certificate[0])
		a.cert = &certs.CertInfo{
			TLSCert:     tlsCert,
			Fingerprint: fingerprint,
			NotAfter:    parsed.NotAfter,
		}
		a.externalCert = true
		slog.Info("using external TLS certificate",
			"path", a.cfg.TLSCert,
			"expires", parsed.NotAfter.Format(time.RFC3339))
	} else {
		cert, err := certs.Generate(14 * 24 * time.Hour)
		if err != nil {
			return fmt.Errorf("generate certificate: %w", err)
		}
		a.cert = cert
		slog.Info("certificate generated",
			"fingerprint", cert.FingerprintBase64(),
			"expires", cert.NotAfter.Format(time.RFC3339))
	}

	// Create channel-based state publisher for MoQ control track.
	a.controlPub = control.NewChannelPublisher(64)

	// In demo mode, skip auth entirely for ease of use.
	// Otherwise, accept both the session API token and registered operator tokens.
	if a.cfg.Demo {
		a.authMW = control.NoopAuthMiddleware()
	} else {
		operatorCheck := control.OperatorTokenChecker(func(token string) bool {
			_, err := a.operatorStore.GetByToken(token)
			return err == nil
		})
		a.authMW = control.AuthMiddleware(a.cfg.APIToken, operatorCheck)
	}

	return nil
}

// initPrismServer creates the Prism distribution server and registers the
// program and replay relays.
func (a *App) initPrismServer() error {
	a.initFastControl()

	config := distribution.ServerConfig{
		Addr: a.cfg.Addr,
		Cert: a.cert,
		ExtraRoutes: func(mux *http.ServeMux) {
			// Register API routes on a sub-mux so we can wrap with middleware.
			apiSubMux := http.NewServeMux()
			a.api.RegisterOnMux(apiSubMux)

			// Chain (outermost first): CORS -> logger -> metrics -> auth -> operator -> maxbytes -> handler
			var apiHandler http.Handler = apiSubMux
			apiHandler = control.MaxBytesMiddleware(apiHandler)
			apiHandler = a.operatorMW(apiHandler)
			apiHandler = a.authMW(apiHandler)
			apiHandler = control.MetricsMiddleware(apiHandler)
			apiHandler = control.LoggerMiddleware(slog.Default())(apiHandler)
			apiHandler = control.CORSMiddleware(a.cfg.AllowedOrigins)(apiHandler)
			apiHandler = control.SecurityHeadersMiddleware(apiHandler)

			// Cert-hash is already registered by Prism (distribution/server.go)
			// on this mux — no need to register it here.

			mux.Handle("/api/", apiHandler)

			if h := uiHandler(); h != nil {
				mux.Handle("/", control.SecurityHeadersMiddleware(h))
			}
		},
		OnStreamRegistered:   a.onStreamRegistered,
		OnStreamUnregistered: a.onStreamUnregistered,
		ControlCh:            a.controlPub.Ch(),
		// NOTE: Datagrams bypass operator auth/lock middleware (acceptable for
		// trusted-LAN single-operator use). Revisit if multi-operator security
		// requires per-datagram authentication.
		OnDatagram: func(streamKey string, data []byte) {
			if err := a.fastCtrl.Dispatch(data); err != nil {
				slog.Debug("fast-control datagram error", "error", err, "stream", streamKey)
			}
		},
		OnBidirectionalStream: a.handleCommsBidiStream,
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
	// Create audio mixer -- sends mixed audio to the program relay
	// and the program preview relay (for browser low-bitrate monitoring).
	a.mixer = audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			// Adjust audio PTS for browser relay to reflect actual content age.
			// Audio content is delayed by the ring buffer FIFO (~30ms) relative
			// to video content (newest-wins, zero delay). Subtracting the ring
			// buffer depth makes audio PTS older, so the browser renderer holds
			// video frames until audio catches up — aligning content ages.
			// The muxer path (SRT/recording) has its own lip-sync offset.
			relayPTS := frame.PTS - a.mixer.RingBufferLatency90k()
			relayFrame := *frame // shallow copy
			relayFrame.PTS = relayPTS
			a.programRelay.BroadcastAudio(&relayFrame)
			if a.programPreviewRelay != nil {
				a.programPreviewRelay.BroadcastAudio(&relayFrame)
			}
		},
		DecoderFactory: audioDecoderFactory(),
		EncoderFactory: audioEncoderFactory(),
	})
	a.mixer.SetMetrics(a.appMetrics)

	// Create switcher with Prism's relay so frames reach MoQ viewers.
	a.sw = switcher.New(a.programRelay)
	a.sw.SetMetrics(a.appMetrics)

	// Record which codec was selected at startup for debug snapshot.
	encName, decName := codec.ProbeEncoders()
	a.sw.SetCodecInfo(encName, decName, codec.HWDeviceCtx() != nil)
	a.sw.SetAvailableEncoders(codec.ListAvailableEncoders())

	// Apply pipeline format from config.
	if f, ok := switcher.FormatPresets[a.cfg.Format]; ok {
		if err := a.sw.SetPipelineFormat(f); err != nil {
			return fmt.Errorf("set pipeline format: %w", err)
		}
	} else {
		return fmt.Errorf("unknown format preset: %q (use one of: 1080p29.97, 1080p25, etc.)", a.cfg.Format)
	}

	// Enable frame sync if requested.
	if a.cfg.FrameSync {
		a.sw.SetFrameSync(true, 0) // 0 = default 30fps
		// Default: clock-driven output (steady timing like a hardware frame sync).
		// --low-latency-sync disables this for minimum latency at the cost of
		// output jitter from source delivery patterns.
		if !a.cfg.LowLatencySync {
			a.sw.SetClockDrivenSync(true)
		}
		// Enable frame rate conversion if requested.
		q := switcher.ParseFRCQuality(a.cfg.FRCQuality)
		if q != switcher.FRCNone {
			a.sw.SetFRCQuality(q)
		}
	}

	if !a.cfg.FrameSync && a.cfg.FRCQuality != "" && a.cfg.FRCQuality != "none" {
		slog.Warn("--frc-quality has no effect without --frame-sync")
	}

	// Wire audio mixer to the switcher.
	a.sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		a.mixer.IngestFrame(sourceKey, frame)
	})
	a.sw.SetMixer(a.mixer)
	a.mixer.ExpectVideoSeed()
	a.sw.SetOnFirstVideoPTS(func(pts int64) {
		a.mixer.SeedPTSFromVideo(pts)
	})
	a.sw.EnableWallClockVideoPTS()

	// Configure transition engine with auto-detected video codec.
	a.sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: decoderFactory(),
	})

	// Always-decode: every H.264 source gets a dedicated decoder goroutine
	// producing raw YUV420, eliminating keyframe wait on cuts/transitions.
	a.sw.SetSourceDecoderFactory(decoderFactory())

	return nil
}

// initOutput creates the output manager, wires SRT, and attaches the
// confidence monitor.
func (a *App) initOutput() error {
	a.outputMgr = output.NewManager()
	a.outputMgr.SetSRTWiring(output.SRTConnect, output.SRTAcceptLoop)
	a.outputMgr.SetMetrics(a.appMetrics)

	// Dynamic lip-sync for SRT/recording output (muxer path only).
	// The browser path handles its own compensation in audio-decoder.ts.
	// Combines ring buffer depth + source A/V PTS gap.
	a.outputMgr.SetLipSyncSource(func() int64 {
		ringBuf := a.mixer.RingBufferLatency90k()

		// Source A/V gap: video PTS - audio PTS. If positive, video PTS is
		// ahead of audio PTS at the source, meaning audio content is from
		// an earlier source moment. We need to delay video by this amount.
		var sourceGap int64
		if a.sourceAVGapInited.Load() {
			vPTS := a.sourceLastVideoPTS.Load()
			aPTS := a.sourceLastAudioPTS.Load()
			gap := vPTS - aPTS
			if gap > 0 && gap < 270000 { // sanity: 0 to 3 seconds
				sourceGap = gap
			}
		}

		return ringBuf + sourceGap
	})

	// CBR pacing is NOT enabled for SRT output. SRT has its own congestion
	// control and jitter buffer that handle VBR natively. CBR null-padding
	// adds buffering latency (10ms tick) and at typical video bitrates the
	// pacer operates in 92% burst mode, defeating the purpose of CBR.
	// The 512-slot SRT listener buffer absorbs muxer bursts instead.

	// Attach confidence monitor for 1fps program output thumbnail.
	a.confidenceMon = output.NewConfidenceMonitor(decoderFactory())
	a.outputMgr.SetConfidenceMonitor(a.confidenceMon)

	// When the output muxer starts, request an IDR keyframe from the
	// encoder so the TSMuxer can initialize immediately.
	a.outputMgr.OnMuxerStart(func() {
		if a.sw != nil {
			a.sw.RequestKeyframe()
		}
	})

	// Wire direct output path: video frames go straight from the pipeline
	// encode goroutine to the MPEG-TS muxer, bypassing the relay → viewer
	// → channel path (3 goroutine hops + 3 channel buffers eliminated).
	a.sw.SetOutputVideoCallback(a.outputMgr.DirectWriteVideo)

	// Wire direct audio path: mixer output goes straight to the muxer.
	a.mixer.SetOutputAudioCallback(a.outputMgr.DirectWriteAudio)

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

	// Ensure state directory exists.
	if err := os.MkdirAll(a.cfg.StateDir, 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	// Stores: presets, macros, operators.
	var err error

	presetPath := a.statePath("presets.json")
	a.presetStore, err = preset.NewStore(presetPath)
	if err != nil {
		return fmt.Errorf("create preset store: %w", err)
	}
	slog.Info("preset store initialized", "path", presetPath)

	macroPath := a.statePath("macros.json")
	a.macroStore, err = macro.NewStore(macroPath)
	if err != nil {
		return fmt.Errorf("create macro store: %w", err)
	}
	slog.Info("macro store initialized", "path", macroPath)

	operatorPath := a.statePath("operators.json")
	a.operatorStore, err = operator.NewStore(operatorPath)
	if err != nil {
		return fmt.Errorf("create operator store: %w", err)
	}
	slog.Info("operator store initialized", "path", operatorPath)

	stingerDir := a.statePath("stingers")
	a.stingerStore, err = stinger.NewStore(stingerDir, 0)
	if err != nil {
		return fmt.Errorf("create stinger store: %w", err)
	}
	slog.Info("stinger store initialized", "path", stingerDir)

	// Session manager for operator session tracking and subsystem locks.
	a.sessionMgr = operator.NewSessionManager()

	// Operator middleware for role/lock enforcement.
	a.operatorMW = operator.NewMiddleware(a.operatorStore, a.sessionMgr)

	// Graphics compositor (DSK).
	a.compositor = graphics.NewCompositor()
	a.compositor.SetResolutionProvider(func() (int, int) {
		// Use pipeline format as the authoritative resolution. This is always set
		// (unlike programRelay.VideoInfo which is zero until the first frame is
		// broadcast), ensuring graphics are rendered at the correct resolution
		// from startup.
		if f := a.sw.PipelineFormat(); f.Width > 0 && f.Height > 0 {
			return f.Width, f.Height
		}
		// Fallback to relay info (should never be needed).
		vi := a.programRelay.VideoInfo()
		return vi.Width, vi.Height
	})
	pf := a.sw.PipelineFormat()
	a.compositor.SetPipelineFPS(pf.FPSNum, pf.FPSDen)
	a.sw.SetCompositor(a.compositor)

	// Layout compositor (PIP / multi-layout).
	format := a.sw.PipelineFormat()
	a.layoutCompositor = layout.NewCompositor(format.Width, format.Height)
	a.sw.SetLayoutCompositor(a.layoutCompositor)

	layoutPresetPath := a.statePath("layout_presets.json")
	a.layoutStore = layout.NewStore(layoutPresetPath)
	slog.Info("layout store initialized", "path", layoutPresetPath)

	// ST map registry and file store.
	a.stmapRegistry = stmap.NewRegistry()
	stmapStoreDir := a.statePath("stmaps")
	stmapSt, err := stmap.NewStore(stmapStoreDir)
	if err != nil {
		slog.Warn("stmap store init failed", "error", err)
	} else {
		a.stmapStore = stmapSt
		// Load persisted static maps.
		staticNames, _ := a.stmapStore.ListStatic()
		for _, name := range staticNames {
			m, loadErr := a.stmapStore.LoadStatic(name)
			if loadErr != nil {
				slog.Warn("stmap load failed", "name", name, "error", loadErr)
				continue
			}
			_ = a.stmapRegistry.Store(m)
		}
		// Load persisted animated map metadata and regenerate.
		animNames, _ := a.stmapStore.ListAnimated()
		for _, name := range animNames {
			meta, loadErr := a.stmapStore.LoadAnimatedMeta(name)
			if loadErr != nil {
				slog.Warn("stmap animated load failed", "name", name, "error", loadErr)
				continue
			}
			anim, genErr := stmap.GenerateAnimated(meta.Generator, meta.Params, meta.Width, meta.Height, meta.FrameCount)
			if genErr != nil {
				slog.Warn("stmap animated regenerate failed", "name", name, "error", genErr)
				continue
			}
			anim.Name = name
			_ = a.stmapRegistry.StoreAnimated(anim)
		}
		if n := len(staticNames) + len(animNames); n > 0 {
			slog.Info("stmap store loaded", "path", stmapStoreDir, "static", len(staticNames), "animated", len(animNames))
		}
	}

	// Wire ST map registry to switcher for per-source correction.
	a.sw.SetSTMapRegistry(a.stmapRegistry)

	// Upstream key processor (chroma/luma keying).
	a.keyProcessor = graphics.NewKeyProcessor()
	a.keyBridge = graphics.NewKeyProcessorBridge(a.keyProcessor)
	a.keyBridge.SetScaleFunc(transition.ScaleYUV420)
	a.sw.SetKeyBridge(a.keyBridge)

	// Key processor changes trigger pipeline rebuild so Active() filtering
	// reflects whether any upstream keys are configured.
	a.keyProcessor.OnChange(func() {
		a.sw.RebuildPipeline()
	})

	// Pipeline encoder for the video processing chain.
	a.sw.SetPipelineCodecs(encoderFactory())
	a.sw.SetPipelineVideoInfoCallback(a.videoInfoCallback("pipeline"))
	if err := a.sw.BuildPipeline(); err != nil {
		return fmt.Errorf("build pipeline: %w", err)
	}

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
		// raw YUV switcher source + mixer channel.
		a.replayMgr.OnPlaybackLifecycle(
			func() {
				a.sw.RegisterReplaySource("replay")
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

		// Wire raw video output: player sends decoded YUV directly to
		// the switcher pipeline (keying → compositor → encode → program).
		a.replayMgr.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64) {
			pf := &switcher.ProcessingFrame{
				YUV:        yuv,
				Width:      w,
				Height:     h,
				PTS:        pts,
				DTS:        pts,
				IsKeyframe: true,
			}
			a.sw.IngestReplayVideo("replay", pf)
		})

		// Wire audio directly to mixer (skip relay encode/decode hop).
		a.replayMgr.SetAudioOutput(func(frame *media.AudioFrame) {
			a.mixer.IngestFrame("replay", frame)
		})

		// Wire WSOLA audio codec factories for slow-motion time-stretching.
		a.replayMgr.SetAudioCodecFactories(
			audioDecoderFactory(),
			audioEncoderFactory(),
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

// initMXL handles MXL discovery, source registration, and output wiring.
// Called between initSubsystems and initAPI.
func (a *App) initMXL() error {
	// Handle --mxl-discover: list flows and exit.
	if a.cfg.MXLDiscover {
		flows, err := mxl.Discover(a.cfg.MXLDomain)
		if err != nil {
			return fmt.Errorf("mxl discover: %w", err)
		}
		fmt.Println("Available MXL flows:")
		for _, f := range flows {
			active := ""
			if f.Active {
				active = " [active]"
			}
			switch f.Format {
			case mxl.DataFormatVideo:
				fmt.Printf("  %s (%s, %dx%d, %s)%s\n",
					f.ID, f.MediaType, f.Width, f.Height, f.Name, active)
			case mxl.DataFormatAudio:
				fmt.Printf("  %s (%s, %dHz %dch, %s)%s\n",
					f.ID, f.MediaType, f.SampleRate, f.Channels, f.Name, active)
			default:
				fmt.Printf("  %s (%s, %s)%s\n",
					f.ID, f.MediaType, f.Name, active)
			}
		}
		return errDiscoverExit
	}

	// No MXL sources or output configured — skip.
	if len(a.cfg.MXLSources) == 0 && a.cfg.MXLOutput == "" {
		return nil
	}

	// Create MXL instance.
	inst, err := mxl.NewInstance(a.cfg.MXLDomain)
	if err != nil {
		return fmt.Errorf("mxl: %w", err)
	}
	a.mxlInstance = inst

	// Register MXL sources.
	// Each spec is "videoUUID", "videoUUID:audioUUID", or "videoUUID:audioUUID:dataUUID".
	for _, spec := range a.cfg.MXLSources {
		parts := strings.SplitN(spec, ":", 3)
		videoFlowID := parts[0]
		var audioFlowID, dataFlowID string
		if len(parts) > 1 {
			audioFlowID = parts[1]
		}
		if len(parts) > 2 {
			dataFlowID = parts[2]
		}

		flowName := "mxl:" + videoFlowID

		// Register with switcher and mixer.
		a.sw.RegisterMXLSource(flowName)
		a.mixer.AddChannel(flowName)
		_ = a.mixer.SetAFV(flowName, true)

		// Register relay for browser delivery + replay.
		relay := a.server.RegisterStream(flowName)

		// Open MXL video flow.
		videoFlow, err := inst.OpenReader(videoFlowID)
		if err != nil {
			slog.Warn("mxl: could not open video flow", "flowID", videoFlowID, "error", err)
		}

		// Open MXL audio flow if specified.
		var audioFlow mxl.ContinuousReader
		if audioFlowID != "" {
			af, err := inst.OpenAudioReader(audioFlowID)
			if err != nil {
				slog.Warn("mxl: could not open audio flow", "flowID", audioFlowID, "error", err)
			} else {
				audioFlow = af
			}
		}

		// Open MXL data flow if specified (for SCTE-104 ancillary data).
		var dataFlow mxl.DiscreteReader
		if dataFlowID != "" {
			df, err := inst.OpenReader(dataFlowID)
			if err != nil {
				slog.Warn("mxl: could not open data flow", "flowID", dataFlowID, "error", err)
			} else {
				dataFlow = df
			}
		}

		// Capture relay for OnVideoInfo closure.
		pf := a.sw.PipelineFormat()
		sourceRelay := relay
		srcCfg := mxl.SourceConfig{
			FlowName:            flowName,
			VideoFlowID:         videoFlowID,
			Width:               pf.Width,
			Height:              pf.Height,
			FPSNum:              pf.FPSNum,
			FPSDen:              pf.FPSDen,
			EncoderFactory:      encoderFactory(),
			AudioEncoderFactory: audioEncoderFactoryForMXL(),
			Relay:               relay,
			OnVideoInfo: func(sps, pps []byte, w, h int) {
				avcC := moq.BuildAVCDecoderConfig(sps, pps)
				if avcC != nil {
					sourceRelay.SetVideoInfo(distribution.VideoInfo{
						Codec:         codec.ParseSPSCodecString(sps),
						Width:         w,
						Height:        h,
						DecoderConfig: avcC,
					})
					slog.Info("MXL source: relay VideoInfo set", "key", flowName, "w", w, "h", h)
				}
			},
			OnRawVideo: func(key string, yuv []byte, w, h int, pts int64) {
				pf := &switcher.ProcessingFrame{
					YUV:    yuv,
					Width:  w,
					Height: h,
					PTS:    pts,
					DTS:    pts,
					Codec:  "h264",
				}
				a.sw.IngestRawVideo(key, pf)
			},
			OnRawAudio: func(key string, pcm []float32, pts int64, channels int) {
				a.mixer.IngestPCM(key, pcm, pts, channels)
			},
		}

		// Wire SCTE-104 input: data grains → parse → inject into SCTE-35 injector.
		// The injector is created later in initSCTE35, so we capture the pointer
		// and check at runtime. This avoids an init ordering dependency.
		if a.cfg.SCTE104 && dataFlow != nil {
			srcCfg.OnDataGrain = func(key string, data []byte, pts int64) {
				if a.scte35Injector == nil {
					return
				}
				payload, err := scte104.ParseST291(data)
				if err != nil {
					slog.Debug("scte104: invalid ST 291 packet", "source", key, "error", err)
					return
				}
				msg, err := scte104.Decode(payload)
				if err != nil {
					slog.Warn("scte104: failed to decode SCTE-104", "source", key, "error", err)
					return
				}
				cue, err := scte104.ToCueMessage(msg)
				if err != nil {
					slog.Warn("scte104: failed to translate to CueMessage", "source", key, "error", err)
					return
				}
				cue.Source = "scte104"
				preRollMs := scte104.PreRollMs(msg)
				if preRollMs > 0 {
					if _, err := a.scte35Injector.ScheduleCue(cue, preRollMs); err != nil {
						slog.Warn("scte104: failed to schedule cue", "source", key, "preRollMs", preRollMs, "error", err)
					}
				} else {
					if _, err := a.scte35Injector.InjectCue(cue); err != nil {
						slog.Warn("scte104: failed to inject cue", "source", key, "error", err)
					}
				}
			}
		}

		if a.cfg.PreviewProxy {
			pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
			pe, err := preview.NewEncoder(preview.Config{
				SourceKey:     flowName,
				Width:         pw,
				Height:        ph,
				Bitrate:       a.cfg.PreviewBitrate,
				FPSNum:        srcCfg.FPSNum,
				FPSDen:        srcCfg.FPSDen,
				Relay:         relay,
				FrameInterval: a.cfg.PreviewFrameInterval,
			})
			if err != nil {
				slog.Error("mxl: preview encoder failed", "flow", flowName, "error", err)
			} else {
				srcCfg.PreviewEncoder = pe
			}
		}

		// Register replay viewer on source relay, or pass directly to source
		// when preview proxy is active (so replay gets full-quality frames).
		if a.replayMgr != nil {
			if err := a.replayMgr.AddSource(flowName); err != nil {
				slog.Warn("mxl: could not add replay source", "key", flowName, "err", err)
			} else if v := a.replayMgr.Viewer(flowName); v != nil {
				if srcCfg.PreviewEncoder != nil {
					// Dual-encode mode: replay viewer gets full-quality frames
					// directly from the source's encoder, not from the relay.
					srcCfg.ReplayViewer = v
				} else {
					relay.AddViewer(v)
				}
			}
		}

		src := mxl.NewSource(srcCfg)

		src.Start(context.Background(), videoFlow, audioFlow, dataFlow)
		a.mxlSources = append(a.mxlSources, src)

		slog.Info("MXL source registered",
			"videoFlowID", videoFlowID,
			"audioFlowID", audioFlowID,
			"dataFlowID", dataFlowID,
			"key", flowName)
	}

	// Configure MXL output.
	if a.cfg.MXLOutput != "" {
		outFmt := a.sw.PipelineFormat()
		out := mxl.NewOutput(mxl.OutputConfig{
			FlowName:   a.cfg.MXLOutput,
			Width:      outFmt.Width,
			Height:     outFmt.Height,
			VideoRate:  mxl.Rational{Numerator: int64(outFmt.FPSNum), Denominator: int64(outFmt.FPSDen)},
			SampleRate: 48000,
			Channels:   2,
		})

		// Load flow definitions from files (or fall back to empty).
		videoFlowDef := "{}"
		if a.cfg.MXLOutputVideoDef != "" {
			data, err := os.ReadFile(a.cfg.MXLOutputVideoDef)
			if err != nil {
				return fmt.Errorf("mxl: read output video flow def: %w", err)
			}
			videoFlowDef = string(data)
		}
		audioFlowDef := "{}"
		if a.cfg.MXLOutputAudioDef != "" {
			data, err := os.ReadFile(a.cfg.MXLOutputAudioDef)
			if err != nil {
				return fmt.Errorf("mxl: read output audio flow def: %w", err)
			}
			audioFlowDef = string(data)
		}

		// Open MXL flows for writing.
		videoWriter, err := inst.OpenWriter(videoFlowDef)
		if err != nil {
			slog.Warn("mxl: could not open output video flow", "error", err)
		}
		audioWriter, err := inst.OpenAudioWriter(audioFlowDef)
		if err != nil {
			slog.Warn("mxl: could not open output audio flow", "error", err)
		}

		// Adapt between switcher's RawVideoSink (*ProcessingFrame) and
		// the MXL writer's WriteVideo (flat YUV params).
		a.sw.SetRawVideoSink(switcher.RawVideoSink(func(pf *switcher.ProcessingFrame) {
			out.Writer().WriteVideo(pf.YUV, pf.Width, pf.Height, pf.PTS)
		}))
		a.mixer.SetRawAudioSink(audio.RawAudioSink(out.Writer().WriteAudio))

		out.StartLifecycle(context.Background(), videoWriter, audioWriter)
		a.mxlOutput = out

		slog.Info("MXL output started", "flow", a.cfg.MXLOutput)
	}

	return nil
}

// initSCTE35 creates the SCTE-35 injector and rules store if enabled.
func (a *App) initSCTE35() error {
	if !a.cfg.SCTE35 {
		return nil
	}

	rulesPath := a.statePath("scte35_rules.json")
	var err error
	a.scte35Rules, err = scte35.LoadRulesStore(rulesPath)
	if err != nil {
		return fmt.Errorf("create SCTE-35 rules store: %w", err)
	}

	config := scte35.InjectorConfig{
		HeartbeatInterval: time.Duration(a.cfg.SCTE35HeartbeatMs) * time.Millisecond,
		DefaultPreRollMs:  a.cfg.SCTE35PreRollMs,
		SCTE35PID:         a.cfg.SCTE35PID,
		VerifyEncoding:    a.cfg.SCTE35Verify,
		WebhookURL:        a.cfg.SCTE35WebhookURL,
		OnSpliceOut:       a.sw.RequestKeyframe,
	}

	muxerSink := func(data []byte) {
		_ = a.outputMgr.InjectSCTE35(data)
	}
	ptsFn := a.sw.LastBroadcastVideoPTS

	a.scte35Injector = scte35.NewInjector(config, muxerSink, ptsFn)
	a.scte35Injector.SetRuleEngine(a.scte35Rules.Engine())
	a.outputMgr.SetSCTE35Injector(a.scte35Injector, a.cfg.SCTE35PID)

	slog.Info("SCTE-35 injector initialized",
		"pid", fmt.Sprintf("0x%X", a.cfg.SCTE35PID),
		"preRollMs", a.cfg.SCTE35PreRollMs,
		"heartbeatMs", a.cfg.SCTE35HeartbeatMs,
		"verify", a.cfg.SCTE35Verify)
	return nil
}

// initSCTE104 wires SCTE-104 output on the injector if enabled.
// Input path is wired in initMXL (OnDataGrain callback on MXL sources).
// Output: injector scte104Sink → FromCueMessage → Encode → WrapST291 → MXL WriteDataGrain
func (a *App) initSCTE104() error {
	if !a.cfg.SCTE104 {
		return nil
	}
	if a.scte35Injector == nil {
		return errors.New("--scte104 requires --scte35")
	}

	// Output path: wire scte104Sink on the injector to write SCTE-104
	// data grains to MXL output.
	if a.mxlOutput != nil {
		a.scte35Injector.SetSCTE104Sink(func(cue *scte35.CueMessage) {
			msg, err := scte104.FromCueMessage(cue)
			if err != nil {
				slog.Warn("scte104: failed to convert CueMessage to SCTE-104", "error", err)
				return
			}
			data, err := scte104.Encode(msg)
			if err != nil {
				slog.Warn("scte104: failed to encode SCTE-104 message", "error", err)
				return
			}
			packet, wrapErr := scte104.WrapST291(data)
			if wrapErr != nil {
				slog.Warn("scte104: failed to wrap ST 291 packet", "error", wrapErr)
				return
			}
			if err := a.mxlOutput.Writer().WriteDataGrain(packet); err != nil {
				slog.Warn("scte104: failed to write data grain", "error", err)
			}
		})
	}

	slog.Info("SCTE-104 initialized",
		"mxlOutput", a.mxlOutput != nil,
		"mxlSources", len(a.mxlSources))
	return nil
}

// initAPI creates the REST API and wires state callbacks.
func (a *App) initAPI() error {
	// Create and start the performance sampler.
	a.perfSampler = perf.NewSampler(
		&perf.SwitcherAdapter{SW: a.sw},
		&perf.MixerAdapter{Mixer: a.mixer},
		&perf.OutputAdapter{Manager: a.outputMgr},
	)
	a.perfSampler.Start()

	// Wire preview encoder stats for the perf sampler (1Hz tick).
	a.perfSampler.SetPreviewStats(func() map[string]perf.PreviewEncoderStats {
		result := make(map[string]perf.PreviewEncoderStats)

		// Collect SRT source preview encoders.
		a.srtSourcesMu.Lock()
		for key, ss := range a.srtSources {
			if ss.previewEnc != nil {
				snap := ss.previewEnc.GetStats()
				result[key] = perf.PreviewEncoderStats{
					FramesIn:      snap.FramesIn,
					FramesOut:     snap.FramesOut,
					FramesDropped: snap.FramesDropped,
					LastEncodeMs:  snap.LastEncodeMs,
					AvgEncodeMs:   snap.AvgEncodeMs,
				}
			}
		}
		a.srtSourcesMu.Unlock()

		// Collect MXL source preview encoders.
		for _, src := range a.mxlSources {
			if pe, ok := src.PreviewEncoderRaw().(*preview.Encoder); ok && pe != nil {
				snap := pe.GetStats()
				result[src.FlowName()] = perf.PreviewEncoderStats{
					FramesIn:      snap.FramesIn,
					FramesOut:     snap.FramesOut,
					FramesDropped: snap.FramesDropped,
					LastEncodeMs:  snap.LastEncodeMs,
					AvgEncodeMs:   snap.AvgEncodeMs,
				}
			}
		}

		return result
	})

	apiOpts := []control.APIOption{
		control.WithMixer(a.mixer),
		control.WithOutputManager(a.outputMgr),
		control.WithDebugCollector(a.debugCollector),
		control.WithPresetStore(a.presetStore),
		control.WithCompositor(a.compositor),
		control.WithMacroStore(a.macroStore),
		control.WithKeyer(a.keyProcessor),
		control.WithKeyBridge(a.keyBridge),
		control.WithOperatorStore(a.operatorStore),
		control.WithSessionManager(a.sessionMgr),
		control.WithStingerStore(a.stingerStore),
		control.WithLayoutCompositor(a.layoutCompositor),
		control.WithLayoutStore(a.layoutStore),
		control.WithPerfSampler(a.perfSampler),
		control.WithSTMapRegistry(a.stmapRegistry),
	}
	if a.replayMgr != nil {
		apiOpts = append(apiOpts, control.WithReplayManager(a.replayMgr))
	}
	if a.scte35Injector != nil {
		apiOpts = append(apiOpts, control.WithSCTE35(a.scte35Injector, a.scte35Rules))
	}
	if a.captionMgr != nil {
		apiOpts = append(apiOpts, control.WithCaptionManager(a.captionMgr))
	}
	if a.clipMgr != nil {
		apiOpts = append(apiOpts, control.WithClipManager(a.clipMgr))
	}
	if a.clipStore != nil {
		apiOpts = append(apiOpts, control.WithClipStore(a.clipStore))
	}
	if a.commsMgr != nil {
		apiOpts = append(apiOpts, control.WithCommsManager(a.commsMgr))
	}
	if a.srtCaller != nil && a.srtStore != nil && a.srtStats != nil {
		apiOpts = append(apiOpts, control.WithSRTManager(&srtManagerAdapter{
			app:    a,
			caller: a.srtCaller,
			stats:  a.srtStats,
			store:  a.srtStore,
		}))
	}

	// Create text rendering engines for ticker and text animation.
	if a.compositor != nil {
		tr, err := textrender.NewRenderer()
		if err != nil {
			slog.Warn("text renderer init failed, ticker/text-anim disabled", "error", err)
		} else {
			a.textRenderer = tr
			a.tickerEngine = graphics.NewTickerEngine(a.compositor, tr)
			a.textAnimEngine = graphics.NewTextAnimationEngine(a.compositor, tr)
			apiOpts = append(apiOpts,
				control.WithTickerEngine(a.tickerEngine),
				control.WithTextAnimEngine(a.textAnimEngine),
			)
			slog.Info("text rendering engines initialized (ticker + text-animation)")
		}
	}

	// Pass invite tokens if cloud provided them.
	if len(a.cfg.InviteTokens) > 0 {
		apiOpts = append(apiOpts, control.WithInviteTokens(a.cfg.InviteTokens))
		apiOpts = append(apiOpts, control.WithSessionAPIToken(a.cfg.APIToken))
	}

	// Constrain SRT listener output ports if cloud provided a range.
	if a.cfg.SRTOutputPortBase > 0 {
		var ports []int
		for p := a.cfg.SRTOutputPortBase; p <= a.cfg.SRTOutputPortEnd; p++ {
			ports = append(ports, p)
		}
		apiOpts = append(apiOpts, control.WithAllowedOutputPorts(ports))
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

	// Start SRT listener and restore persisted caller pulls.
	a.startSRT(ctx)

	demoStats := demo.NewStats()
	if a.cfg.Demo {
		const nCams = 4
		slog.Info("demo mode: starting simulated camera sources", "count", nCams, "videoDir", a.cfg.DemoVideoDir)

		// Demo sources always use the standard Prism relay → sourceViewer path.
		// Preview proxy encoding only applies to SRT/MXL sources where
		// the relay encode path is independent of the switcher pipeline.
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
		stopDemo := demo.StartSources(ctx, a.sw, relays, demoStats, a.cfg.DemoVideoDir, a.sw.PipelineFormat().FrameDuration())
		defer stopDemo()

		// Copy video info from first source to program relay.
		if len(relays) > 0 {
			a.programRelay.SetVideoInfo(relays[0].VideoInfo())
		}

		// Add raw MXL demo sources (exercises IngestRawVideo/IngestPCM path).
		stopMXLDemo := a.startMXLDemo(ctx)
		defer stopMXLDemo()

		// Start SRT demo sources (push test clips to local SRT listener).
		if a.cfg.SRTListen != "" && a.cfg.DemoVideoDir != "" {
			clips := []string{
				filepath.Join(a.cfg.DemoVideoDir, "tears_of_steel.ts"),
				filepath.Join(a.cfg.DemoVideoDir, "sintel.ts"),
			}
			// Filter to clips that actually exist on disk.
			var validClips []string
			for _, c := range clips {
				if _, err := os.Stat(c); err == nil {
					validClips = append(validClips, c)
				}
			}
			if len(validClips) > 0 {
				// Give the SRT listener a moment to start accepting connections.
				time.Sleep(200 * time.Millisecond)
				// Use explicit IPv4 loopback — ":6464" resolves to IPv6 on macOS
				// which may not have a route.
				dialAddr := a.cfg.SRTListen
				if strings.HasPrefix(dialAddr, ":") {
					dialAddr = "127.0.0.1" + dialAddr
				}
				go demo.StartSRTSources(ctx, dialAddr, validClips, slog.Default())
				slog.Info("demo: SRT push sources started", "clips", len(validClips), "addr", a.cfg.SRTListen)
			}
		}

		// Load demo stingers if none exist yet.
		if len(a.stingerStore.List()) == 0 {
			vi := a.programRelay.VideoInfo()
			w, h := vi.Width, vi.Height
			if w == 0 || h == 0 {
				w, h = 320, 240
			}
			numFrames := 30 // 1 second at 30fps

			demoStingers := []struct {
				name string
				gen  func(int, int, int) ([]byte, error)
			}{
				{"whoosh", demo.GenerateWhooshStingerZip},
				{"slam", demo.GenerateSlamStingerZip},
				{"musical", demo.GenerateMusicalStingerZip},
			}
			for _, ds := range demoStingers {
				zipData, err := ds.gen(w, h, numFrames)
				if err != nil {
					slog.Warn("failed to generate demo stinger", "name", ds.name, "error", err)
					continue
				}
				if err := a.stingerStore.Upload(ds.name, zipData); err != nil {
					slog.Warn("failed to load demo stinger", "name", ds.name, "error", err)
				} else {
					slog.Info("demo stinger loaded", "name", ds.name, "resolution", fmt.Sprintf("%dx%d", w, h))
				}
			}
		}
	}
	a.debugCollector.Register("demo", demoStats)

	// Raw YUV program monitor — sends uncompressed YUV420 to local browsers.
	if a.cfg.RawProgramMonitor {
		a.rawProgramRelay = a.server.RegisterStream("program-raw")

		// Determine output resolution.
		pf := a.sw.PipelineFormat()
		rawW, rawH := pf.Width, pf.Height
		switch a.cfg.RawMonitorScale {
		case "720p":
			rawW, rawH = 1280, 720
		case "480p":
			rawW, rawH = 854, 480
		case "360p":
			rawW, rawH = 640, 360
		case "":
			// native resolution
		default:
			slog.Warn("unknown --raw-monitor-scale, using native", "scale", a.cfg.RawMonitorScale)
		}

		a.rawProgramRelay.SetVideoInfo(distribution.VideoInfo{
			Codec:  "raw/yuv420",
			Width:  rawW,
			Height: rawH,
		})

		needsScale := rawW != pf.Width || rawH != pf.Height

		// Pre-allocate reusable buffers for the raw monitor hot path.
		// Scale buffer: reused every frame (consumed immediately by copy into pack buffer).
		var scaleBuf []byte
		if needsScale {
			scaleBuf = make([]byte, rawW*rawH*3/2)
		}
		// Triple-buffered pack buffers: BroadcastVideoNoCache doesn't retain
		// WireData, but viewer SendVideo may still be serializing the previous
		// frame to QUIC while we fill the next buffer. 3 buffers ensure the
		// one being written is never the one being read by the transport.
		packSize := 8 + rawW*rawH*3/2
		packBufs := [3][]byte{
			make([]byte, packSize),
			make([]byte, packSize),
			make([]byte, packSize),
		}
		packIdx := 0
		a.sw.SetRawMonitorSink(switcher.RawVideoSink(func(frame *switcher.ProcessingFrame) {
			yuv := frame.YUV
			w, h := frame.Width, frame.Height

			// Scale if needed (into pre-allocated buffer).
			if needsScale {
				transition.ScaleYUV420(yuv, w, h, scaleBuf, rawW, rawH)
				yuv = scaleBuf
				w, h = rawW, rawH
			}

			// Pack into next triple-buffer slot.
			packed := packBufs[packIdx]
			packIdx = (packIdx + 1) % 3
			packed[0] = byte(w >> 24)
			packed[1] = byte(w >> 16)
			packed[2] = byte(w >> 8)
			packed[3] = byte(w)
			packed[4] = byte(h >> 24)
			packed[5] = byte(h >> 16)
			packed[6] = byte(h >> 8)
			packed[7] = byte(h)
			copy(packed[8:], yuv)

			// Allocate frame per-call so async viewers don't see stale PTS/DTS.
			// The struct is small (5 fields); triple-buffered WireData handles byte data.
			monitorFrame := &media.VideoFrame{
				PTS:        frame.PTS,
				DTS:        frame.DTS,
				IsKeyframe: true,
				Codec:      "raw/yuv420",
				WireData:   packed,
			}
			a.rawProgramRelay.BroadcastVideoNoCache(monitorFrame)
		}))

		slog.Info("raw program monitor enabled",
			"width", rawW, "height", rawH,
			"scale", a.cfg.RawMonitorScale)

		// Also register "replay-raw" for raw YUV replay monitoring.
		if a.replayMgr != nil {
			rawReplayRelay := a.server.RegisterStream("replay-raw")
			rawReplayRelay.SetVideoInfo(distribution.VideoInfo{
				Codec:  "raw/yuv420",
				Width:  rawW,
				Height: rawH,
			})

			// Triple-buffered pack buffers for replay-raw (same pattern as program-raw).
			replayPackBufs := [3][]byte{
				make([]byte, packSize),
				make([]byte, packSize),
				make([]byte, packSize),
			}
			replayPackIdx := 0

			a.replayMgr.SetRawMonitorOutput(func(yuv []byte, w, h int, pts int64) {
				packed := replayPackBufs[replayPackIdx]
				replayPackIdx = (replayPackIdx + 1) % 3
				packed[0] = byte(w >> 24)
				packed[1] = byte(w >> 16)
				packed[2] = byte(w >> 8)
				packed[3] = byte(w)
				packed[4] = byte(h >> 24)
				packed[5] = byte(h >> 16)
				packed[6] = byte(h >> 8)
				packed[7] = byte(h)
				copy(packed[8:], yuv)

				replayFrame := &media.VideoFrame{
					PTS:        pts,
					DTS:        pts,
					IsKeyframe: true,
					Codec:      "raw/yuv420",
					WireData:   packed,
				}
				rawReplayRelay.BroadcastVideoNoCache(replayFrame)
			})

			slog.Info("raw replay monitor enabled", "width", rawW, "height", rawH)
		}
	}

	// Program preview encoder — low-bitrate H.264 for browser program monitor.
	// Taps the raw YUV pipeline output (same pattern as MXL and raw monitor sinks)
	// and re-encodes at 3 Mbps. Browsers subscribe to "program-preview" instead
	// of the full-quality 10 Mbps "program" relay, reducing bandwidth by ~70%.
	{
		pf := a.sw.PipelineFormat()
		a.programPreviewRelay = a.server.RegisterStream("program-preview")
		// Scale to 720p for faster encode (~2ms vs ~5ms at 1080p) and lower
		// bandwidth (~1.5 Mbps). The program monitor canvas is not full-screen,
		// so 720p is visually indistinguishable. Faster encode also tightens
		// delivery timing, reducing the video-ahead-of-audio offset that causes
		// the renderer's look-ahead tolerance to intermittently skip frames.
		previewW, previewH := 1280, 720
		pe, err := preview.NewEncoder(preview.Config{
			SourceKey: "program-preview",
			Width:     previewW,
			Height:    previewH,
			Bitrate:   3_000_000,
			FPSNum:    pf.FPSNum,
			FPSDen:    pf.FPSDen,
			Relay:     a.programPreviewRelay,
			Preset:    "veryfast",
		})
		if err != nil {
			slog.Error("program preview encoder failed", "error", err)
		} else {
			a.programPreviewEnc = pe
			a.debugCollector.Register("preview:program", pe)

			// Force IDR on source cuts to prevent P-frame artifacts from the
			// old scene smearing into the new one. The main encoder uses
			// forceNextIDR; the preview encoder needs the same treatment.
			var lastProgramSource string
			a.sw.OnStateChange(func(state internal.ControlRoomState) {
				if state.ProgramSource != lastProgramSource {
					lastProgramSource = state.ProgramSource
					pe.ForceKeyframe()
				}
			})

			a.sw.SetRawPreviewSink(switcher.RawVideoSink(func(frame *switcher.ProcessingFrame) {
				// PTS is already in wall-clock domain — rewritten in
				// videoProcessingLoop before pipeline.Run(). All sinks
				// (preview, MXL, raw monitor) see the same PTS as the
				// program relay and audio mixer.
				pe.Send(frame.YUV, frame.Width, frame.Height, frame.PTS)
			}))

			slog.Info("program preview encoder enabled",
				"width", previewW, "height", previewH,
				"bitrate", 3_000_000)
		}
	}

	// Start admin server (Prometheus metrics, health, readiness, pprof, cert-hash).
	stopAdmin, _ := StartAdminServer(ctx, a.cfg.AdminAddr, a.cfg.Addr, a.cert.FingerprintBase64(), a.externalCert, a.cfg.AdminToken)
	defer stopAdmin()

	// Optionally start HTTP/1.1 fallback for curl/scripts.
	if a.cfg.HTTPFallback {
		stopHTTP, err := a.startHTTPAPIServer(ctx)
		if err != nil {
			return err
		}
		defer stopHTTP()
	} else {
		slog.Info("HTTP/1.1 fallback disabled (use --http-fallback to enable)")
	}

	// Periodic audio metering state broadcast (~10Hz). Server-side peaks
	// are updated on every audio frame, but state broadcasts are normally
	// event-driven (only on user actions). This ticker ensures VU meters
	// in the browser update smoothly for sources that lack client-side
	// audio decoders (e.g. MXL/raw PCM sources).
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.broadcastState(nil)
			}
		}
	}()

	// All components initialized -- mark ready for readiness probe.
	readyFlag.Store(true)

	slog.Info("starting Prism distribution server", "addr", a.cfg.Addr)
	return a.server.Start(ctx)
}

// logSystemTuning checks OS-level resource limits and logs warnings when
// they are below recommended values for real-time video processing.
func logSystemTuning(log *slog.Logger) {
	var rlimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlimit); err == nil {
		if rlimit.Cur < 65536 {
			log.Warn("low file descriptor limit", "current", rlimit.Cur, "recommended", 65536)
		}
	}
}

// Close cleans up all subsystems with a 10-second drain timeout.
// If subsystem cleanup hangs (e.g. SRT connections or output buffers),
// shutdown proceeds after the timeout to avoid indefinite blocking
// (which would cause a SIGKILL in Kubernetes).
func (a *App) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.closeWithContext(ctx)
}

// closeWithContext runs subsystem cleanup in a goroutine and waits for
// either completion or context cancellation/timeout.
func (a *App) closeWithContext(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		a.closeSubsystems()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("clean shutdown complete")
	case <-ctx.Done():
		slog.Warn("shutdown timed out, forcing exit")
	}
}

// closeSubsystems cleans up all subsystems in reverse initialization order.
func (a *App) closeSubsystems() {
	// Wait for background goroutines (metering ticker) to exit.
	a.bgWG.Wait()

	// MXL cleanup first (before core engine).
	if a.mxlOutput != nil {
		a.mxlOutput.Stop()
	}
	for _, src := range a.mxlSources {
		src.Stop()
	}
	if a.mxlInstance != nil {
		_ = a.mxlInstance.Close()
	}

	// SRT cleanup: stop all active sources first, then the listener.
	a.stopSRTSources()
	if a.srtListener != nil {
		_ = a.srtListener.Close()
	}

	if a.perfSampler != nil {
		a.perfSampler.Stop()
	}

	if a.scte35Injector != nil {
		a.scte35Injector.Close()
	}

	if a.commsMgr != nil {
		a.commsMgr.Close()
	}
	if a.clipMgr != nil {
		a.clipMgr.Close()
	}
	if a.tickerEngine != nil {
		a.tickerEngine.Close()
	}
	if a.textAnimEngine != nil {
		a.textAnimEngine.Close()
	}
	if a.textRenderer != nil {
		_ = a.textRenderer.Close()
	}
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
	if a.programPreviewEnc != nil {
		a.programPreviewEnc.Stop()
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

// handleCertHash returns the WebTransport certificate fingerprint and QUIC address.
// No authentication required — browsers need this to establish WebTransport connections.
func (a *App) handleCertHash(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"hash":    a.cert.FingerprintBase64(),
		"addr":    a.cfg.Addr,
		"trusted": a.externalCert,
	})
}
