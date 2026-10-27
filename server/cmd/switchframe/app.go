package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	"github.com/zsiec/switchframe/server/asr"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/colorgrade"
	"github.com/zsiec/switchframe/server/comms"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/debug"
	"github.com/zsiec/switchframe/server/demo"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/fastctrl"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/graphics/textrender"
	"github.com/zsiec/switchframe/server/htmlgfx"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/oidcauth"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/perf"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/preview"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/scte104"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/sidecar"
	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// errDiscoverExit is returned by initMXL when --mxl-discover completes
// successfully. The caller should treat this as a clean exit rather than
// calling os.Exit directly (which would skip deferred cleanup).
var errDiscoverExit = errors.New("mxl discover completed")

// apiTokenFile is the file under the state directory that holds a generated
// API token, readable by the owner only.
const apiTokenFile = "api-token"

// writeFileAtomic replaces path with a new owner-only file holding data. The
// data goes into a temporary file created with mode 0600, which is renamed
// over path, so an existing file of a wider mode, or a symlink at path, is
// replaced rather than written through.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// announceAuth logs how API authentication is set up. The token itself never
// reaches the log or stdout, which container platforms collect into the same
// log store. A generated token is written to a file in the state directory
// instead, so that it can still be looked up.
func announceAuth(cfg AppConfig, log *slog.Logger) error {
	if cfg.Demo {
		log.Info("demo mode: API authentication disabled")
		return nil
	}
	if cfg.APITokenSource != "generated" {
		log.Info("API authentication enabled", "token_source", cfg.APITokenSource)
		return nil
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	path := filepath.Join(cfg.StateDir, apiTokenFile)
	if err := writeFileAtomic(path, []byte(cfg.APIToken+"\n")); err != nil {
		return fmt.Errorf("write generated API token: %w", err)
	}
	log.Info("API authentication enabled", "token_source", "generated", "token_file", path)
	return nil
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
	programPreviewRelay *distribution.Relay
	replayRelay         *distribution.Relay

	// Program preview encoder (low-bitrate H.264 for browser program monitor)
	programPreviewEnc *preview.Encoder

	// Preview encoders for built-in virtual sources (source:black,
	// source:colorbars). Each encoder is wired to a MoQ-published
	// per-source relay so browser multiview tiles render BARS/BLACK
	// thumbnails even before the operator routes them to PGM/PVW.
	builtinPreviewEncoders map[string]*preview.Encoder

	// Core engine
	sw            *switcher.Switcher
	mixer         *audio.Mixer
	cmdQueue      *cmdqueue.Queue
	cmdDispatcher *cmdqueue.Dispatcher
	cmdQueueWAL   *cmdqueue.WAL

	// Output
	outputMgr     *output.Manager
	confidenceMon *output.ConfidenceMonitor
	// Directory of the recording --outputs-config started, offered to clip
	// import the way a recording started over the API is.
	startupRecordingDir string

	// Subsystems
	debugCollector *debug.Collector
	presetStore    *preset.Store
	macroStore     *macro.Store
	operatorStore  *operator.Store
	sessionMgr     *operator.SessionManager
	compositor     *graphics.Compositor
	keyProcessor   *graphics.KeyProcessor
	keyBridge      *graphics.KeyProcessorBridge
	replayMgr      *replay.Manager
	stingerStore   *stinger.Store
	wipeMapStore   *wipemap.WipeMapStore
	dveCompositor  *dve.Compositor
	dvePresetStore *dve.PresetStore
	fastCtrl       *fastctrl.Dispatcher
	stmapRegistry  *stmap.Registry
	stmapStore     *stmap.Store

	// Color grading
	colorGradeCtrl   *colorGradeController
	sourceCorrectors *colorgrade.SourceCorrectorManager

	// Text rendering engines
	textRenderer   *textrender.Renderer
	tickerEngine   *graphics.TickerEngine
	textAnimEngine *graphics.TextAnimationEngine

	// Animated PNG sequence engine
	sequenceEngine *graphics.SequenceEngine

	// HTML5 graphics sidecar
	htmlGfxMgr *htmlgfx.Manager

	// Closed captions
	captionMgr *caption.Manager

	// Operator comms
	commsMgr *comms.Manager
	// commsStreamsMu guards commsStreams: operatorID → that operator's
	// active comms bidi stream. A reconnect supersedes (closes) the prior
	// stream, and only the current stream's teardown may Leave() the comms
	// session — a stale lingering stream's deferred Leave must not tear
	// down the reconnected operator's fresh session. See app_comms.go.
	commsStreamsMu sync.Mutex
	commsStreams   map[string]io.ReadWriteCloser

	// Clip players
	clipMgr    *clip.Manager
	clipStore  *clip.Store
	clipRelays [clip.MaxPlayers]*distribution.Relay

	// Playout channels
	playoutMgr         *playout.Manager
	playoutCache       *playout.Cache
	playoutPrefetcher  *playout.Prefetcher
	playoutSamples     []playout.SampleClip
	playoutPlaylistDir string
	playoutRelays      [playout.MaxChannels]*distribution.Relay

	// GPU pipeline (Metal on macOS, CUDA on Linux). Accessed only via
	// gpu()/setGPUState (app_gpu.go): the format-change rebuild replaces
	// the whole state from the SetPipelineFormat caller's goroutine while
	// decode callbacks and lifecycle hooks read it from their own
	// goroutines.
	gpuState        atomic.Pointer[gpuState]
	gpuRawVideoSink *atomic.Pointer[gpu.RawSinkFunc] // GPU raw sink for MXL output

	// AI sidecar (nil when not using sidecar mode)
	aiSidecarHost *sidecar.Host
	aiSidecarPool *sidecar.CUDAGPUPool

	// AI segmentation (CUDA + TensorRT or sidecar proxy, nil on other platforms)
	segEngine  gpu.Segmenter
	segAdapter *segmentationStateAdapter // non-nil when segEngine is active

	// ASR engine (Whisper speech-to-text, nil when unavailable)
	asrEngine      *asr.Engine
	captionPacer   *captionPacer
	cachedASRState atomic.Pointer[internal.ASRState]

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

	// Reference clock (when --ref-clock is active)
	refClock        *clock.ReferenceClock
	refClockEncoder audio.Encoder // AAC encoder for ref clock path (needs Close on shutdown)

	// MXL integration
	mxlInstance *mxl.Instance
	mxlSources  []*mxl.Source // configured --mxl-sources; teardown owned by closeSubsystems
	// namedMXLInputs holds the readers of --mxl-sources entries given a
	// name, which can be moved to other flows while they run.
	namedMXLInputs mxlInputs
	// mxlNMOS is the NMOS node for the named MXL inputs; nil when off.
	mxlNMOS   *mxlNMOS
	mxlOutput *mxl.Output

	// demoMXLSources holds only the synthetic sources created by startMXLDemo.
	// Tracked separately from mxlSources so stopMXLDemo tears down exactly what
	// it created and does not stop real configured sources (which closeSubsystems
	// owns). Kept distinct to avoid conflating demo and production ownership in
	// one slice, which would misbehave under a partial/restart teardown.
	demoMXLSources []*mxl.Source

	// ST 2110 integration
	st2110State *st2110State

	// A/V sync: gate relay audio until the first video keyframe has been
	// broadcast, preventing the browser from playing audio before video is
	// decodable. Set true by broadcastOwnedToProgram (video path) on the
	// first IDR; checked by the mixer output callback (audio path).
	programRelayVideoStarted atomic.Bool

	// Peer health monitoring
	peerPoller *peer.HealthPoller

	// Performance monitoring
	perfSampler *perf.Sampler

	// API + middleware
	api        *control.API
	authMW     func(http.Handler) http.Handler
	oidcAuth   *oidcauth.Authenticator // nil = OpenID Connect sign-in disabled
	operatorMW func(http.Handler) http.Handler

	// Background goroutine tracking for clean shutdown
	bgWG sync.WaitGroup
	// bgCtx is cancelled at the start of shutdown so background workers
	// launched before Run() (e.g. the ST map cache prewarm) can abort their
	// long-running loops instead of forcing bgWG.Wait() to block until they
	// finish. Cancelled in closeSubsystems before bgWG.Wait().
	bgCtx    context.Context
	bgCancel context.CancelFunc
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
	if err := announceAuth(a.cfg, slog.Default()); err != nil {
		return err
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

	return a.initOIDC()
}

// initPrismServer creates the Prism distribution server and registers the
// program and replay relays.
func (a *App) initPrismServer() error {
	a.initFastControl()

	config := distribution.ServerConfig{
		Addr:         a.cfg.Addr,
		Cert:         a.cert,
		ExternalCert: a.externalCert,
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
			mux.Handle("/auth/", control.SecurityHeadersMiddleware(a.authHandler()))

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
		OnViewerAdded: func(streamKey string) {
			if streamKey == "program" || streamKey == "program-preview" {
				if a.sw != nil {
					a.sw.RequestKeyframe()
				}
				if gs := a.gpu(); streamKey == "program-preview" && gs != nil && gs.previewNode != nil {
					gs.previewNode.ForceIDR()
				}
			}
		},
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
	if a.cfg.RefClock {
		return a.initCoreEngineRefClock()
	}
	return a.initCoreEngineLegacy()
}

// initCoreEngineLegacy is the existing audio/video wiring path: wall-clock PTS,
// mixer with internal ticker, PTS seeding from video.
func (a *App) initCoreEngineLegacy() error {
	// Create audio mixer -- sends mixed audio to the program relay
	// and the program preview relay (for browser low-bitrate monitoring).
	a.mixer = audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			// Hold relay audio until the first video keyframe has been
			// broadcast. This prevents the browser from playing audio
			// before video is decodable at stream start.
			if !a.programRelayVideoStarted.Load() {
				return
			}
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

	if err := a.initSwitcherCommon(); err != nil {
		return err
	}

	// Wire audio mixer to the switcher (wall-clock PTS path).
	a.sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		a.mixer.IngestFrame(sourceKey, frame)
	})
	a.sw.SetMixer(a.mixer)
	a.mixer.ExpectVideoSeed()
	a.sw.SetOnFirstVideoPTS(func(pts int64) {
		a.mixer.SeedPTSFromVideo(pts)
	})
	a.sw.EnableWallClockVideoPTS()

	return nil
}

// initCoreEngineRefClock wires the reference clock path: a single clock drives
// both video and audio processing synchronously, eliminating wall-clock PTS
// and the mixer's internal ticker.
func (a *App) initCoreEngineRefClock() error {
	slog.Info("reference clock mode enabled")

	// Create clock-driven mixer: no internal ticker, output comes from sync loop.
	a.mixer = audio.NewMixer(audio.MixerConfig{
		SampleRate:     48000,
		Channels:       2,
		ClockDriven:    true,
		DecoderFactory: audioDecoderFactory(),
		EncoderFactory: audioEncoderFactory(),
	})
	a.mixer.SetMetrics(a.appMetrics)

	if err := a.initSwitcherCommon(); err != nil {
		return err
	}

	// Wire audio ingest: sources still push to ring buffers as before.
	a.sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		a.mixer.IngestFrame(sourceKey, frame)
	})
	a.sw.SetMixer(a.mixer)

	// Create reference clock from pipeline format.
	pf := a.sw.PipelineFormat()
	a.refClock = clock.NewReferenceClock(pf.FPSNum, pf.FPSDen, 48000, 90000, true)

	// Create AAC encoder for the sync loop's audio path.
	aacEnc, err := audio.NewFDKEncoder(48000, 2)
	if err != nil {
		return fmt.Errorf("create AAC encoder for ref clock: %w", err)
	}
	a.refClockEncoder = aacEnc

	// Create AAC frame buffer wired to the encoder.
	aacBuf := clock.NewAACFrameBuffer(2, 48000, aacEnc.Encode)
	aacBuf.SetBasePTS(90000)

	// Wire clock recovery — detects source frame rate changes and adjusts clock.
	cr := clock.NewClockRecovery(pf.FPSNum, pf.FPSDen, 30)
	a.sw.SetClockRecovery(cr)

	// Wire to switcher.
	a.sw.SetReferenceClock(a.refClock)
	a.sw.SetAACBuffer(aacBuf)
	a.sw.SetSyncMixer(a.mixer)

	// Wire audio output callback so the sync loop can deliver AAC frames
	// to both the program relay (browser) and output manager (muxer/SRT).
	a.sw.SetOutputAudioCallback(func(frame *media.AudioFrame) {
		// Hold relay audio until the first video keyframe has been
		// broadcast. This prevents the browser from playing audio
		// before video is decodable at stream start.
		if !a.programRelayVideoStarted.Load() {
			return
		}
		// Adjust audio PTS for browser relay to reflect actual content age.
		// Audio content is delayed by the ring buffer FIFO relative to video
		// content (newest-wins). Subtracting the ring buffer depth makes audio
		// PTS older, so the browser renderer holds video frames until audio
		// catches up — aligning content ages. Same adjustment as the legacy path.
		relayPTS := frame.PTS - a.mixer.RingBufferLatency90k()
		relayFrame := *frame
		relayFrame.PTS = relayPTS
		a.programRelay.BroadcastAudio(&relayFrame)
		if a.programPreviewRelay != nil {
			a.programPreviewRelay.BroadcastAudio(&relayFrame)
		}
	})

	// DON'T call: EnableWallClockVideoPTS, ExpectVideoSeed, SetOnFirstVideoPTS
	// The reference clock provides PTS directly.
	// Transition + source decoder factories are set by initSwitcherCommon
	// (with 10-bit color-aware factory when the pipeline is 422/10-bit).

	return nil
}

// initSwitcherCommon initializes the switcher with settings shared by both
// the legacy and reference clock paths.
func (a *App) initSwitcherCommon() error {
	// Create switcher with Prism's relay so frames reach MoQ viewers.
	a.sw = switcher.New(a.programRelay)
	a.sw.SetMetrics(a.appMetrics)

	// Persist + seed lastCommandSeq so /api/peer/health survives a restart
	// without the peer interpreting seq=0 as divergence. Wire
	// BEFORE initCommandQueue so the WAL-replay seq tracker (installed in
	// app_cmdqueue) can push any post-persist seqs through the now-active
	// persist pipeline.
	if a.cfg.StateDir != "" {
		seqPath := a.statePath("switcher_seq.json")
		seq, err := loadSwitcherSeq(seqPath)
		if err != nil {
			slog.Error("switcher seq file unreadable; starting at seq 0 (peer will see restart as divergence until commands catch up)",
				"path", seqPath, "error", err)
		} else if seq > 0 {
			a.sw.SetInitialCommandSeq(seq)
			slog.Info("restored switcher lastCommandSeq from disk", "seq", seq, "path", seqPath)
		}
		a.sw.SetSeqPersistFn(func(s uint64) error {
			return saveSwitcherSeq(seqPath, s)
		})
	}

	// Gate relay audio until the first video keyframe is broadcast.
	// This prevents the browser from playing audio before video is
	// decodable, eliminating the ~400ms audio-leads-video gap at start.
	a.sw.SetOnKeyframeBroadcast(func() {
		a.programRelayVideoStarted.Store(true)
	})

	// Record which codec was selected at startup for debug snapshot.
	encName, decName := codec.ProbeEncoders()
	a.sw.SetCodecInfo(encName, decName, codec.HWDeviceCtx() != nil)
	a.sw.SetAvailableEncoders(codec.ListAvailableEncoders())

	// Apply pipeline format from config.
	colorCfg, err := switcher.ParseColorProfile(a.cfg.ColorFormat)
	if err != nil {
		return err
	}
	if err := colorCfg.Validate(); err != nil {
		return fmt.Errorf("invalid color profile: %w", err)
	}

	if f, ok := switcher.FormatPresets[a.cfg.Format]; ok {
		f.Color = colorCfg

		slog.Info("pipeline color format",
			"profile", a.cfg.ColorFormat,
			"colorFormat", colorCfg.ColorFormat.String(),
			"codec", colorCfg.Codec.String())

		if err := a.sw.SetPipelineFormat(f); err != nil {
			return fmt.Errorf("set pipeline format: %w", err)
		}
	} else {
		return fmt.Errorf("unknown format preset: %q (use one of: 1080p29.97, 1080p25, etc.)", a.cfg.Format)
	}

	// Enable frame sync if requested.
	if a.cfg.FrameSync {
		a.sw.SetFrameSync(true, 0) // 0 = default 30fps

		// In reference clock mode, disable the FrameSync timer entirely.
		// The sync loop pops frames directly from the ring buffers. Without
		// this, two goroutines (tickLoop + sync loop) race over the same
		// ring buffers and process frames through different paths.
		if a.cfg.RefClock {
			a.sw.SetFrameSyncBufferOnly(true)
		}

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

	// Configure transition engine with auto-detected video codec.
	// When the pipeline is professional (10-bit 422), use native 10-bit decoders
	// to preserve quality by avoiding the lossy 10-bit→8-bit→10-bit round-trip.
	srcDecFactory := decoderFactoryForColorFormat(colorCfg.ColorFormat)
	a.sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: srcDecFactory,
	})

	// Always-decode: every source gets a dedicated decoder goroutine
	// producing raw YUV. With 10-bit decoders, sources that provide 10-bit
	// content output YUV422P10LE directly, avoiding lossy downconversion.
	a.sw.SetSourceDecoderFactory(srcDecFactory)

	return nil
}

// initOutput creates the output manager, wires SRT, and attaches the
// confidence monitor.
func (a *App) initOutput() error {
	a.outputMgr = output.NewManager()
	// SetEngineLabel MUST run before any AddDestination / RestoreDestinations
	// call: the engine-label guard on the manager short-circuits on empty
	// label, and destinations added before the label is set are stamped
	// with an empty Engine field.
	a.outputMgr.SetEngineLabel(a.cfg.EngineLabel)
	a.outputMgr.SetSRTWiring(output.SRTConnect, output.SRTAcceptLoop)
	a.outputMgr.SetMetrics(a.appMetrics)
	if pf := a.sw.PipelineFormat(); pf.Width > 0 && pf.Height > 0 {
		a.outputMgr.SetResolution(pf.Width, pf.Height)
	}

	if a.cfg.RefClock {
		a.outputMgr.LatchLipSync(0)
	} else {
		// Legacy path: dynamic lip-sync for SRT/recording output (muxer path only).
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
	}

	// CBR pacing is NOT enabled for SRT output. SRT has its own congestion
	// control and jitter buffer that handle VBR natively. CBR null-padding
	// adds buffering latency (10ms tick) and at typical video bitrates the
	// pacer operates in 92% burst mode, defeating the purpose of CBR.
	// The 512-slot SRT listener buffer absorbs muxer bursts instead.

	// Attach confidence monitor for 1fps program output thumbnail.
	// Use codec-aware decoder factory: when the pipeline is HEVC (10-bit professional),
	// the program output is HEVC-encoded and needs an HEVC-capable decoder.
	a.confidenceMon = output.NewConfidenceMonitor(decoderFactoryForColorFormat(a.sw.PipelineFormat().Color.ColorFormat))
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

	if a.cfg.RefClock {
		// Reference clock path: audio comes from the sync loop's AAC frame
		// buffer, delivered via the switcher's output audio callback.
		a.sw.SetOutputAudioCallbackDirect(a.outputMgr.DirectWriteAudio)
	} else {
		// Legacy path: mixer output goes straight to the muxer.
		a.mixer.SetOutputAudioCallback(a.outputMgr.DirectWriteAudio)
	}

	return nil
}

func buildHTMLGfxConfig(cfg AppConfig, width, height, fps int) htmlgfx.Config {
	out := htmlgfx.Config{
		BinaryPath:   "switchframe-cef",
		TemplatesDir: "/opt/switchframe-cef/demo-templates",
		PoolSize:     4,
		Width:        width,
		Height:       height,
		FPS:          fps,
	}
	if cfg.CEFBinary != "" {
		out.BinaryPath = cfg.CEFBinary
	}
	if cfg.CEFTemplatesDir != "" {
		out.TemplatesDir = cfg.CEFTemplatesDir
	}
	return out
}

// initSubsystems creates the debug collector, stores, graphics compositor,
// upstream keyer, and replay manager.
func (a *App) initSubsystems() error {
	// Shutdown context for background workers started here (before Run()).
	// Cancelled at the start of closeSubsystems so long-running prewarm loops
	// abort promptly instead of blocking bgWG.Wait() until completion.
	a.bgCtx, a.bgCancel = context.WithCancel(context.Background())

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
	if a.oidcAuth != nil {
		a.operatorStore.SetTokenResolver(a.resolveOIDCOperator)
	}

	stingerDir := a.statePath("stingers")
	stingerCacheDir := ""
	if a.cfg.CacheDir != "" {
		stingerCacheDir = filepath.Join(a.cfg.CacheDir, "stingers")
	}
	a.stingerStore, err = stinger.NewStore(stingerDir, 0, stingerCacheDir)
	if err != nil {
		return fmt.Errorf("create stinger store: %w", err)
	}
	slog.Info("stinger store initialized", "path", stingerDir, "cacheDir", stingerCacheDir)

	// Initialize wipe pattern store with builtin patterns.
	wipeDir := a.statePath("wipe_patterns")
	a.wipeMapStore, err = wipemap.NewWipeMapStore(wipeDir)
	if err != nil {
		return fmt.Errorf("create wipe map store: %w", err)
	}
	a.wipeMapStore.RegisterBuiltins()
	slog.Info("wipe map store initialized", "path", wipeDir, "patterns", len(a.wipeMapStore.List()))

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

	// HTML5 graphics sidecar manager.
	a.htmlGfxMgr = htmlgfx.NewManager(a.compositor, buildHTMLGfxConfig(a.cfg, pf.Width, pf.Height, pf.FPSNum/pf.FPSDen))
	if err := a.htmlGfxMgr.Start(); err != nil {
		slog.Warn("HTML5 graphics sidecar failed to start", "error", err)
		// Non-fatal: server runs without HTML5 graphics.
	}

	// DVE compositor (PIP / multi-layout).
	format := a.sw.PipelineFormat()
	a.dveCompositor = dve.NewCompositor(format.Width, format.Height)
	a.sw.SetDVECompositor(a.dveCompositor)

	// DVE preset store (user-created presets).
	dvePresetDir := a.statePath("dve_presets")
	if dvePS, err := dve.NewPresetStore(dvePresetDir); err != nil {
		slog.Warn("dve preset store init failed", "error", err)
	} else {
		a.dvePresetStore = dvePS
	}

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
			// Use mmap-cached LUT build when cache dir is configured.
			if a.cfg.CacheDir != "" {
				cacheDir := filepath.Join(a.cfg.CacheDir, "stmap",
					name+"_"+stmap.CacheKey(meta.Generator, meta.Params, meta.Width, meta.Height, meta.FrameCount))
				anim.BuildProcessorsCached(cacheDir)
			} else {
				anim.BuildProcessors()
			}
			_ = a.stmapRegistry.StoreAnimated(anim)
		}
		if n := len(staticNames) + len(animNames); n > 0 {
			slog.Info("stmap store loaded", "path", stmapStoreDir, "static", len(staticNames), "animated", len(animNames))
		}
	}

	// Wire ST map registry to switcher for per-source correction.
	a.sw.SetSTMapRegistry(a.stmapRegistry)

	// Wire wipe map store to switcher for gradient-map wipe transitions.
	if a.wipeMapStore != nil {
		a.sw.SetWipeMapStore(a.wipeMapStore)
	}

	// Background prewarm of the built-in animated ST map caches, gated on
	// --cache-dir and --stmap-prewarm.
	a.startSTMapPrewarm()

	// Color grading store and controller.
	uploadLooksDir := a.statePath("luts")
	colorGradeStore, err := colorgrade.NewStore(uploadLooksDir)
	if err != nil {
		slog.Warn("color grade store init failed", "error", err)
	} else {
		a.colorGradeCtrl = newColorGradeController(colorGradeStore)
		a.debugCollector.Register("colorgrade", a.colorGradeCtrl)
		slog.Info("color grade store loaded",
			"upload", uploadLooksDir,
			"looks", len(colorGradeStore.List()),
		)
	}

	// Per-source color corrector manager (GPU per-source color grading).
	a.sourceCorrectors = colorgrade.NewSourceCorrectorManager()

	// Upstream key processor (chroma/luma keying).
	a.keyProcessor = graphics.NewKeyProcessor()
	a.keyBridge = graphics.NewKeyProcessorBridge(a.keyProcessor)
	a.keyBridge.SetScaleFunc(transition.ScaleYUV420)
	a.sw.SetKeyBridge(a.keyBridge)

	// Key processor changes trigger pipeline rebuild so Active() filtering
	// reflects whether any upstream keys are configured. Broadcast state so
	// the UI sees the updated key-fill tally immediately without waiting for
	// an unrelated mutation to trigger a state push.
	a.keyProcessor.OnChange(func() {
		a.sw.RebuildPipeline()
		a.broadcastState(nil)
	})

	// Register program preview relay early so wireGPUPipeline can use it
	// for the GPU preview encode node (which broadcasts directly to this relay).
	a.programPreviewRelay = a.server.RegisterStream("program-preview")

	// Expose program-preview relay viewer stats for diagnostics.
	a.debugCollector.Register("program_preview_relay", &previewRelayDebugProvider{app: a})

	// GPU pipeline auto-detection and wiring.
	// Must happen after stmap registry is set and before BuildPipeline().
	gs := initGPU(a.sw)
	a.setGPUState(gs)
	wireGPUPipeline(gs, a.sw, a)

	// Rebuild GPU pipeline on format change (e.g., 1080p → 720p).
	// Full teardown + recreate: pool, encoder, pipeline, source manager
	// all need to match the new dimensions.
	if gs != nil {
		a.sw.SetOnFormatChange(a.rebuildGPUPipelineForFormat)
	}

	// Pipeline encoder for the video processing chain.
	// Use the codec and color format from the pipeline config to select the
	// appropriate encoder: AVC 8-bit, HEVC 8-bit, or HEVC 10-bit 4:2:2.
	pipelineColor := a.sw.PipelineFormat().Color
	pipelineCodec := pipelineColor.Codec
	var encOpts *codec.EncoderOptions
	if a.cfg.OutputBitrate != 0 || a.cfg.OutputGOPSecs != 0 || a.cfg.OutputPreset != "" || a.cfg.OutputTimecode {
		encOpts = &codec.EncoderOptions{
			Bitrate:  a.cfg.OutputBitrate,
			GOPSecs:  a.cfg.OutputGOPSecs,
			Preset:   codec.OutputPreset(a.cfg.OutputPreset),
			Timecode: a.cfg.OutputTimecode,
		}
	}
	a.sw.SetEncoderOpts(encOpts)
	a.sw.SetPipelineCodecs(encoderFactoryForCodecWithOpts(pipelineCodec, encOpts, pipelineColor.ColorFormat), pipelineCodec)
	a.sw.SetPipelineVideoInfoCallback(a.videoInfoCallback("pipeline"))
	if err := a.sw.BuildPipeline(); err != nil {
		return fmt.Errorf("build pipeline: %w", err)
	}

	// Replay manager.
	a.replayRelay = a.server.RegisterStream("replay")
	if a.cfg.ReplayBufferSecs > 0 {
		a.replayMgr = replay.NewManager(
			a.replayRelay,
			replay.Config{
				BufferDurationSecs: a.cfg.ReplayBufferSecs,
				TmpfsDir:           a.cfg.ReplayTmpfsDir,
			},
			decoderFactory(),
			encoderFactory(),
		)
		a.debugCollector.Register("replay", a.replayMgr)

		// Wire replay playback lifecycle: register/unregister replay as a
		// raw YUV switcher source + mixer channel.
		var replayPreviewGroupID atomic.Uint32
		a.replayMgr.OnPlaybackLifecycle(
			func() {
				a.sw.RegisterReplaySource("replay")
				a.mixer.AddChannel("replay")
				_ = a.mixer.SetAFV("replay", true)
				// If program is already "replay" (user cut before OnReady),
				// re-trigger AFV activation so the new channel becomes active.
				a.mixer.OnProgramChange(a.sw.ProgramSource())

				// Register replay source with GPU preview encoder.
				if gs := a.gpu(); a.cfg.PreviewProxy && gs != nil && gs.sourceManager != nil {
					pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
					curPF := a.sw.PipelineFormat()
					var replayVideoInfoSent bool
					gs.sourceManager.RegisterSource("replay", curPF.Width, curPF.Height, &gpu.PreviewConfig{
						Width:   pw,
						Height:  ph,
						Bitrate: a.cfg.PreviewBitrate,
						FPSNum:  curPF.FPSNum,
						FPSDen:  curPF.FPSDen,
						OnPreview: func(data []byte, isIDR bool, pts int64) {
							avc1 := codec.AnnexBToAVC1(data)
							if isIDR {
								replayPreviewGroupID.Add(1)
							}
							frame := &media.VideoFrame{
								PTS: pts, DTS: pts, IsKeyframe: isIDR,
								WireData: avc1, Codec: "h264",
								GroupID: replayPreviewGroupID.Load(),
							}
							if isIDR {
								for _, nalu := range codec.ExtractNALUs(avc1) {
									if len(nalu) == 0 {
										continue
									}
									switch nalu[0] & 0x1F {
									case 7:
										frame.SPS = nalu
									case 8:
										frame.PPS = nalu
									}
								}
								if !replayVideoInfoSent && frame.SPS != nil && frame.PPS != nil {
									avcC := a.buildAVCConfig(frame.SPS, frame.PPS)
									if avcC != nil {
										a.replayRelay.SetVideoInfo(a.buildVideoInfo(frame.SPS, avcC, pw, ph))
										replayVideoInfoSent = true
									}
								}
							}
							a.replayRelay.BroadcastVideo(frame)
						},
					})
					slog.Info("replay: GPU preview encoder registered", "key", "replay")
				}
			},
			func() {
				// Release GPU preview encoder and cached frames.
				if gs := a.gpu(); gs != nil && gs.sourceManager != nil {
					gs.sourceManager.RemoveSource("replay")
				}
				replayPreviewGroupID.Store(0)
				a.sw.UnregisterSource("replay")
				a.mixer.RemoveChannel("replay")
			},
		)

		// Wire raw video output: player sends decoded YUV directly to
		// the switcher pipeline (keying → compositor → encode → program).
		a.replayMgr.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64) {
			pf := &switcher.ProcessingFrame{
				Data:       yuv,
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

		tmpfsStatus := a.cfg.ReplayTmpfsDir
		if tmpfsStatus == "none" || tmpfsStatus == "" {
			tmpfsStatus = "disabled"
		}
		slog.Info("replay manager initialized", "bufferSecs", a.cfg.ReplayBufferSecs, "tmpfsDir", tmpfsStatus)

		// Enable GPU replay with NVOFA motion blur if available.
		a.wireGPUReplay()
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
	for _, raw := range a.cfg.MXLSources {
		spec, err := parseMXLSourceSpec(raw)
		if err != nil {
			return fmt.Errorf("mxl: %w", err)
		}
		videoFlowID, audioFlowID, dataFlowID := spec.Video, spec.Audio, spec.Data

		flowName := spec.Key()

		// Register with switcher and mixer.
		a.sw.RegisterMXLSourceLabeled(flowName, spec.Label())
		a.mixer.AddChannel(flowName)
		_ = a.mixer.SetAFV(flowName, true)

		// Register relay for browser delivery + replay.
		relay := a.server.RegisterStream(flowName)

		var videoFlow mxl.DiscreteReader
		var audioFlow mxl.ContinuousReader
		var dataFlow mxl.DiscreteReader
		if spec.Name != "" {
			// A named input reads through retargetable readers, which open
			// their flows themselves and can be moved to others while the
			// source runs. Video and audio readers exist even without a
			// flow, so a later connection has a reader loop to take over.
			in := &mxlInput{}
			pf := a.sw.PipelineFormat()
			videoFlow = mxl.NewRetargetableReader(inst.OpenReader, expectMXLVideo(pf.FPSNum, pf.FPSDen), nil)
			audioFlow = mxl.NewRetargetableAudioReader(inst.OpenAudioReader, expectMXLFormat(mxl.DataFormatAudio), nil)
			in.Video = videoFlow.(mxl.Retargetable)
			in.Audio = audioFlow.(mxl.Retargetable)
			if dataFlowID != "" {
				dataFlow = mxl.NewRetargetableReader(inst.OpenReader, expectMXLFormat(mxl.DataFormatData), nil)
				in.Data = dataFlow.(mxl.Retargetable)
			}
			a.namedMXLInputs.add(spec.Name, in)
			if err := a.retargetMXLInput(spec.Name, &videoFlowID, &audioFlowID, &dataFlowID); err != nil {
				return fmt.Errorf("mxl: %w", err)
			}
		} else {
			// Open MXL video flow.
			vf, err := inst.OpenReader(videoFlowID)
			if err != nil {
				slog.Warn("mxl: could not open video flow", "flowID", videoFlowID, "error", err)
			} else {
				videoFlow = mxl.NewReopeningReader(videoFlowID, vf, inst.OpenReader, nil)
			}

			// Open MXL audio flow if specified.
			if audioFlowID != "" {
				af, err := inst.OpenAudioReader(audioFlowID)
				if err != nil {
					slog.Warn("mxl: could not open audio flow", "flowID", audioFlowID, "error", err)
				} else {
					audioFlow = mxl.NewReopeningAudioReader(audioFlowID, af, inst.OpenAudioReader, nil)
				}
			}

			// Open MXL data flow if specified (for SCTE-104 ancillary data).
			if dataFlowID != "" {
				df, err := inst.OpenReader(dataFlowID)
				if err != nil {
					slog.Warn("mxl: could not open data flow", "flowID", dataFlowID, "error", err)
				} else {
					dataFlow = mxl.NewReopeningReader(dataFlowID, df, inst.OpenReader, nil)
				}
			}
		}

		// Capture relay for OnVideoInfo closure.
		pf := a.sw.PipelineFormat()
		sourceRelay := relay
		// Enable lossless 10-bit V210 conversion when the pipeline is professional (10-bit 4:2:2).
		lossless10bit := pf.Color.ColorFormat == switcher.ColorYUV422_10bit
		srcCfg := mxl.SourceConfig{
			FlowName:            flowName,
			VideoFlowID:         videoFlowID,
			Width:               pf.Width,
			Height:              pf.Height,
			FPSNum:              pf.FPSNum,
			FPSDen:              pf.FPSDen,
			Lossless10bit:       lossless10bit,
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
				frameFmt := switcher.ColorYUV420_8bit
				if lossless10bit {
					frameFmt = switcher.ColorYUV422_10bit
				}
				pf := &switcher.ProcessingFrame{
					Data:   yuv,
					Format: frameFmt,
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

		if gs := a.gpu(); a.cfg.PreviewProxy && gs != nil && gs.sourceManager != nil {
			// GPU preview encoding — register source with GPU source manager.
			// IngestYUV (triggered by OnRawVideo → IngestRawVideo → handleRawVideoFrame)
			// handles upload + ST map + cache + preview encode.
			pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
			var gpuGroupID atomic.Uint32
			var gpuVideoInfoSent bool
			mxlRelay := relay // capture for closure
			gs.sourceManager.RegisterSource(flowName, pf.Width, pf.Height, &gpu.PreviewConfig{
				Width:   pw,
				Height:  ph,
				Bitrate: a.cfg.PreviewBitrate,
				FPSNum:  srcCfg.FPSNum,
				FPSDen:  srcCfg.FPSDen,
				OnPreview: func(data []byte, isIDR bool, pts int64) {
					avc1 := codec.AnnexBToAVC1(data)
					if isIDR {
						gpuGroupID.Add(1)
					}
					frame := &media.VideoFrame{
						PTS: pts, DTS: pts, IsKeyframe: isIDR,
						WireData: avc1, Codec: "h264", GroupID: gpuGroupID.Load(),
					}
					if isIDR {
						for _, nalu := range codec.ExtractNALUs(avc1) {
							if len(nalu) == 0 {
								continue
							}
							switch nalu[0] & 0x1F {
							case 7:
								frame.SPS = nalu
							case 8:
								frame.PPS = nalu
							}
						}
						if !gpuVideoInfoSent && frame.SPS != nil && frame.PPS != nil {
							avcC := moq.BuildAVCDecoderConfig(frame.SPS, frame.PPS)
							if avcC != nil {
								mxlRelay.SetVideoInfo(distribution.VideoInfo{
									Codec: codec.ParseSPSCodecString(frame.SPS),
									Width: pw, Height: ph, DecoderConfig: avcC,
								})
								slog.Info("MXL source: GPU preview VideoInfo set", "key", flowName, "w", pw, "h", ph)
								gpuVideoInfoSent = true
							}
						}
					}
					mxlRelay.BroadcastVideo(frame)
				},
			})
			// Set a no-op preview encoder so the MXL source's encodeAndBroadcastVideo
			// delegates relay to the preview encoder (which does nothing — GPU handles it)
			// and only uses the CPU full-quality path for replay.
			srcCfg.PreviewEncoder = gpuNoOpPreviewEncoder{}
			slog.Info("mxl: GPU preview encoder registered", "flow", flowName)
		} else if a.cfg.PreviewProxy {
			// CPU preview encoder fallback.
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
		// the MXL writer's WriteVideo/WriteVideo10bit.
		// Route based on frame format: 10-bit uses lossless YUV422P10→V210 path,
		// 8-bit uses the standard YUV420→V210 path.
		a.sw.SetRawVideoSink(switcher.RawVideoSink(func(pf *switcher.ProcessingFrame) {
			if pf.Format == switcher.ColorYUV422_10bit {
				out.Writer().WriteVideo10bit(pf.Data, pf.Width, pf.Height, pf.PTS)
			} else {
				out.Writer().WriteVideo(pf.Data, pf.Width, pf.Height, pf.PTS)
			}
		}))
		a.updateGPURawVideoSink()
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

	// Construct the peer health poller (not yet started) so the API can
	// expose this engine's label/epoch on /api/peer/health from startup.
	// Start() runs later, once the HTTP listener is up.
	if a.cfg.PeerEngineURL != "" {
		pollerOpts := []peer.Option{
			peer.WithLocalIdentity(a.cfg.EngineLabel),
		}
		if a.cfg.PeerCertHash != "" {
			pinned, err := peer.NewPinnedHTTPClient(a.cfg.PeerCertHash, 3*time.Second)
			if err != nil {
				return fmt.Errorf("peer cert-hash pinning: %w", err)
			}
			pollerOpts = append(pollerOpts, peer.WithHTTPClient(pinned))
			slog.Info("peer TLS pinning active", "peerURL", a.cfg.PeerEngineURL)
		}
		if a.cfg.StateDir != "" {
			epochPath := a.statePath("peer_epoch.json")
			e, err := loadPeerEpoch(epochPath)
			switch {
			case err != nil:
				// A malformed file is louder than missing-file: the whole
				// point of persistence is to preserve force-leader across
				// restart, and silently falling back to 0 would undo it.
				// Don't abort startup — a degraded engine is better than
				// no engine — but surface the fact loudly so ops can fix.
				slog.Error("peer epoch file unreadable; starting at epoch 0 (force-leader state will be lost until re-bumped)",
					"path", epochPath, "error", err)
			case e > 0:
				pollerOpts = append(pollerOpts, peer.WithInitialEpoch(e))
				slog.Info("restored peer epoch from disk", "epoch", e, "path", epochPath)
			}
			pollerOpts = append(pollerOpts, peer.WithEpochPersist(func(e uint64) error {
				return savePeerEpoch(epochPath, e)
			}))
		}
		a.peerPoller = peer.NewHealthPoller(a.cfg.PeerEngineURL, pollerOpts...)
	}

	apiOpts := []control.APIOption{
		control.WithMixer(a.mixer),
		control.WithPeerPoller(a.peerPoller),
		control.WithControlPlaneSecret(a.cfg.ControlPlaneSecret),
		control.WithOutputManager(a.outputMgr),
		control.WithDebugCollector(a.debugCollector),
		control.WithPresetStore(a.presetStore),
		control.WithCompositor(a.compositor),
		control.WithMacroStore(a.macroStore),
		control.WithKeyer(a.keyProcessor),
		control.WithCommandQueue(a.cmdQueue),
		control.WithSeqTracker(a.sw.TrackCommandSeq),
		control.WithKeyBridge(a.keyBridge),
		control.WithOperatorStore(a.operatorStore),
		control.WithSessionManager(a.sessionMgr),
		control.WithStingerStore(a.stingerStore),
		control.WithWipeMapStore(a.wipeMapStore),
		control.WithDVECompositor(a.dveCompositor),
		control.WithDVEPresetStore(a.dvePresetStore),
		control.WithPerfSampler(a.perfSampler),
		control.WithSTMapRegistry(a.stmapRegistry),
		control.WithSTMapStore(a.stmapStore),
		control.WithLastPongSentFn(a.fastCtrl.LastPongSentUs),
		control.WithAppMetrics(a.appMetrics),
		control.WithThumbnailCache(a.sw.ThumbnailCache()),
	}
	if a.cfg.CacheDir != "" {
		stmapCacheBase := filepath.Join(a.cfg.CacheDir, "stmap")
		apiOpts = append(apiOpts, control.WithSTMapCacheDir(stmapCacheBase))
	}
	if a.colorGradeCtrl != nil {
		apiOpts = append(apiOpts, control.WithColorGradeManager(a.colorGradeCtrl))
	}
	if a.sourceCorrectors != nil {
		apiOpts = append(apiOpts, control.WithSourceCorrectorManager(a.sourceCorrectors))
	}
	if a.replayMgr != nil {
		apiOpts = append(apiOpts, control.WithReplayManager(a.replayMgr))
	}
	if a.startupRecordingDir != "" {
		apiOpts = append(apiOpts, control.WithRecordingDir(a.startupRecordingDir))
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
	if a.playoutMgr != nil {
		apiOpts = append(apiOpts, control.WithPlayoutManager(a.playoutMgr))
	}
	if a.playoutCache != nil {
		apiOpts = append(apiOpts, control.WithPlayoutCache(a.playoutCache))
	}
	if a.playoutPrefetcher != nil {
		apiOpts = append(apiOpts, control.WithPlayoutPrefetcher(a.playoutPrefetcher))
	}
	if len(a.playoutSamples) > 0 {
		apiOpts = append(apiOpts, control.WithPlayoutSamples(a.playoutSamples))
	}
	if a.playoutPlaylistDir != "" {
		apiOpts = append(apiOpts, control.WithPlayoutSaveDir(a.playoutPlaylistDir))
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
	if a.segEngine != nil && a.segAdapter != nil {
		apiOpts = append(apiOpts, control.WithAISegmentManager(&aiSegmentManagerAdapter{
			engine:  a.segEngine,
			adapter: a.segAdapter,
		}))
	}
	if a.asrEngine != nil {
		apiOpts = append(apiOpts, control.WithASRManager(&asrManagerAdapter{
			engine:        a.asrEngine,
			captionMgr:    a.captionMgr,
			pacer:         a.captionPacer,
			onCacheUpdate: a.updateASRStateCache,
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

	// Create animated sequence engine for graphics layers.
	if a.compositor != nil {
		a.sequenceEngine = graphics.NewSequenceEngine(a.compositor)
		a.compositor.SetSequenceEngine(a.sequenceEngine)
		apiOpts = append(apiOpts, control.WithSequenceEngine(a.sequenceEngine))
		slog.Info("sequence engine initialized")
	}

	// Pass invite tokens if cloud provided them. The session API token is
	// always wired through so handleOperatorRegister can recognize the
	// session owner even when no invite tokens are configured; demo-mode
	// flag likewise relaxes the register gate only when Demo is on.
	if len(a.cfg.InviteTokens) > 0 {
		apiOpts = append(apiOpts, control.WithInviteTokens(a.cfg.InviteTokens))
	}
	apiOpts = append(apiOpts,
		control.WithSessionAPIToken(a.cfg.APIToken),
		control.WithDemoMode(a.cfg.Demo),
	)
	if !a.cfg.Demo && len(a.cfg.InviteTokens) == 0 && a.operatorStore != nil {
		slog.Warn("operator registration locked to session API token holder (no invite tokens configured); set SWITCHFRAME_INVITE_TOKENS to enable role-based invite links")
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

	// Late-bind snapshot provider — requires API to exist first (App delegates
	// to subsystems wired above), but the provider is a lightweight adapter.
	a.api.SetSnapshotProvider(&snapshotProviderAdapter{app: a})

	// Late-bind HTML5 graphics manager to the API.
	if a.htmlGfxMgr != nil {
		a.api.SetHTMLGfxManager(a.htmlGfxMgr)
	}

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

	if a.cfg.Demo {
		// Synthetic cam1-cam4 SRT cameras are retired — each was a full
		// encode+relay pipeline that saturated the program loop on busy
		// hosts and made the built-in source:colorbars sprite visibly
		// stutter when selected on program. MXL demo flows (below) and
		// SRT clip pushers (below) remain — they exercise the real MXL
		// and SRT ingest code paths and are valuable for end-to-end
		// validation. For a minimal "no external feeds configured" runtime,
		// operators can cut to source:black or source:colorbars.

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

		// Load demo stingers and playout playlist asynchronously. Stinger
		// PNG-sequence + audio synthesis takes ~5-8s per clip and demo
		// playlist generation runs ffmpeg on synthetic sources — together
		// they easily blow past the container health-check budget if run
		// inline. None of this is required for the engine to be healthy:
		// the UI just gains stingers and a playlist a few seconds late.
		a.bgWG.Add(1)
		go func() {
			defer a.bgWG.Done()

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
					if ctx.Err() != nil {
						return
					}
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

			if a.playoutMgr != nil {
				demoClipDir := filepath.Join(a.cfg.StateDir, "playout-cache", "demo")
				if err := os.MkdirAll(demoClipDir, 0o755); err != nil {
					slog.Warn("demo: failed to create dir for playout clips", "error", err)
					return
				}
				playlist, err := demo.GenerateDemoPlaylist(demoClipDir, a.playoutSamples)
				if err != nil {
					slog.Warn("demo: failed to generate playout playlist", "error", err)
					return
				}
				if playlist == nil {
					return
				}
				if err := a.playoutMgr.LoadPlaylist(1, playlist); err != nil {
					slog.Warn("demo: failed to load playout playlist", "error", err)
					return
				}
				slog.Info("demo: playout playlist loaded", "items", len(playlist.Items), "channel", 1)
				if a.playoutPrefetcher != nil {
					a.playoutPrefetcher.UpdatePlaylist(playlist, 0)
				}
				demo.SetupDemoPods(a.playoutMgr, 1)
			}
		}()
	}

	// Program preview encoder — low-bitrate H.264 for browser program monitor.
	// Browsers subscribe to "program-preview" instead of the full-quality
	// 10 Mbps "program" relay, reducing bandwidth by ~70%.
	//
	// When the GPU pipeline is active with a preview encode node, the GPU
	// handles scale + encode entirely in VRAM (no CPU download). The relay
	// is registered early so wireGPUPipeline can reference it.
	//
	// When the GPU pipeline is not active (or doesn't have a preview node),
	// fall back to the CPU path: tap raw YUV from the pipeline and re-encode
	// at 720p/3 Mbps via preview.Encoder.
	if a.programPreviewRelay == nil {
		a.programPreviewRelay = a.server.RegisterStream("program-preview")
	}
	if gs := a.gpu(); gs != nil && gs.previewNode != nil {
		// GPU preview encode is active — wire ForceIDR on source cuts.
		var lastProgramSource string
		gpuPreviewNode := gs.previewNode
		a.sw.OnStateChange(func(state internal.ControlRoomState) {
			if state.ProgramSource != lastProgramSource {
				lastProgramSource = state.ProgramSource
				gpuPreviewNode.ForceIDR()
			}
		})
		slog.Info("program preview uses GPU encode, skipping CPU preview encoder")
	} else {
		pf := a.sw.PipelineFormat()
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

			// Downconvert 10-bit frames to 8-bit for the CPU preview encoder.
			// The preview encoder always expects YUV420P 8-bit input.
			var previewDownconvertBuf []byte
			a.sw.SetRawPreviewSink(switcher.RawVideoSink(func(frame *switcher.ProcessingFrame) {
				if frame.Format == switcher.ColorYUV422_10bit {
					dstSize := switcher.ColorYUV420_8bit.FrameSize(frame.Width, frame.Height)
					if len(previewDownconvertBuf) < dstSize {
						previewDownconvertBuf = make([]byte, dstSize)
					}
					if err := switcher.Downconvert422_10to420(frame.Data, previewDownconvertBuf[:dstSize], frame.Width, frame.Height); err != nil {
						return
					}
					pe.Send(previewDownconvertBuf[:dstSize], frame.Width, frame.Height, frame.PTS)
				} else {
					pe.Send(frame.Data, frame.Width, frame.Height, frame.PTS)
				}
			}))

			slog.Info("program preview encoder enabled (CPU fallback)",
				"width", previewW, "height", previewH,
				"bitrate", 3_000_000)
		}
	}

	// Start peer engine health poller (constructed earlier so API could
	// advertise our label/epoch from startup).
	if a.peerPoller != nil {
		a.peerPoller.Start()
		slog.Info("peer health poller started",
			"peerURL", a.cfg.PeerEngineURL,
			"engineLabel", a.cfg.EngineLabel)
	}

	// On air without an operator cut: on a peer's program, else on a
	// source of this engine's own.
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		peer.RunStartup(a.bgCtx, a.peerPoller, &startupEngine{app: a}, peer.StartupConfig{
			Token:      a.cfg.APIToken,
			Tick:       time.Second,
			LeaderWait: leaderWait,
		})
	}()

	// Start admin server (Prometheus metrics, health, readiness, pprof, cert-hash).
	stopAdmin, _ := StartAdminServer(ctx, a.cfg.AdminAddr, a.advertisedAddr(), a.cert.FingerprintBase64(), a.externalCert, a.cfg.AdminToken)
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

	// Start reference clock sync loop if configured. Must happen after all
	// output wiring is complete (video/audio callbacks are set).
	if a.cfg.RefClock && a.refClock != nil {
		a.sw.StartSyncLoop()
		a.refClock.Start()
		slog.Info("reference clock started")
	}

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
	// Cancel the background-worker context first so long-running loops
	// started before Run() (the ST map cache prewarm) abort instead of
	// blocking bgWG.Wait() until they finish.
	if a.bgCancel != nil {
		a.bgCancel()
	}

	// Wait for background goroutines (metering ticker, prewarm) to exit.
	a.bgWG.Wait()

	// Tear down built-in source preview encoders. Must happen before the
	// switcher is closed so the tap is detached before the producer tries
	// to invoke a closed encoder. The switcher's own Close() will stop
	// the producer goroutine afterward.
	a.closeBuiltinSourcePreview()

	a.closeMXLNMOS()

	// ST 2110 cleanup (before core engine).
	a.closeST2110()

	// MXL cleanup first (before core engine).
	if a.mxlOutput != nil {
		a.mxlOutput.Stop()
	}
	for _, src := range a.mxlSources {
		src.Stop()
	}
	// Demo sources are normally stopped by stopMXLDemo (deferred in Run), but
	// stop them here too so teardown is complete regardless of defer ordering.
	// src.Stop() is sync.Once-guarded, so a double-stop is a safe no-op.
	for _, src := range a.demoMXLSources {
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

	if a.peerPoller != nil {
		a.peerPoller.Stop()
	}
	if a.perfSampler != nil {
		a.perfSampler.Stop()
	}

	if a.cmdQueueWAL != nil {
		_ = a.cmdQueueWAL.Close()
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
	if a.playoutMgr != nil {
		a.playoutMgr.Close()
	}
	if a.playoutPrefetcher != nil {
		a.playoutPrefetcher.Stop()
	}
	if a.playoutCache != nil {
		a.playoutCache.Close()
	}
	if a.htmlGfxMgr != nil {
		_ = a.htmlGfxMgr.Close()
	}
	if a.tickerEngine != nil {
		a.tickerEngine.Close()
	}
	if a.textAnimEngine != nil {
		a.textAnimEngine.Close()
	}
	if a.sequenceEngine != nil {
		a.sequenceEngine.Close()
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
	// ASR engine cleanup (before mixer — it reads from mixer's raw audio sink).
	if a.captionPacer != nil {
		a.captionPacer.Stop()
	}
	if a.asrEngine != nil {
		a.asrEngine.Close()
	}
	// AI segmentation engine cleanup before GPU (holds GPU resources).
	if a.segEngine != nil {
		a.segEngine.Close()
	}
	// AI sidecar cleanup: close host (kills sidecar process), then pool (unmaps shm).
	if a.aiSidecarHost != nil {
		_ = a.aiSidecarHost.Close()
	}
	if a.aiSidecarPool != nil {
		_ = a.aiSidecarPool.Close()
	}
	// GPU cleanup after switcher (pipeline frames must be released first).
	closeGPU(a.gpu())
	if a.refClockEncoder != nil {
		_ = a.refClockEncoder.Close()
	}
	if a.mixer != nil {
		_ = a.mixer.Close()
	}
}

// handleCertHash returns the WebTransport certificate fingerprint and QUIC address.
// No authentication required — browsers need this to establish WebTransport connections.
// advertisedAddr is the WebTransport address /api/cert-hash hands the
// browser: --addr, with the host replaced by --webtransport-host when set.
func (a *App) advertisedAddr() string {
	return advertiseAddr(a.cfg.Addr, a.cfg.WebTransportHost)
}

// advertiseAddr replaces the host of listen address addr with host, keeping
// its port. An empty host leaves addr as it is.
func advertiseAddr(addr, host string) string {
	if host == "" {
		return addr
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return net.JoinHostPort(host, port)
}

func (a *App) handleCertHash(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"hash":    a.cert.FingerprintBase64(),
		"addr":    a.advertisedAddr(),
		"trusted": a.externalCert,
	})
}

// startSTMapPrewarm starts prewarmSTMapCache in the background when a cache
// directory is set and --stmap-prewarm is on.
func (a *App) startSTMapPrewarm() {
	if a.cfg.CacheDir == "" || !a.cfg.STMapPrewarm {
		return
	}
	a.bgWG.Add(1)
	go a.prewarmSTMapCache(a.bgCtx)
}

// prewarmSTMapCache pre-generates mmap-cached LUT files for all built-in
// animated ST map effects at the current pipeline resolution. This runs in
// the background so the first use of any effect is instant (mmap load)
// rather than a multi-second generation pause.
func (a *App) prewarmSTMapCache(ctx context.Context) {
	defer a.bgWG.Done()

	pf := a.sw.PipelineFormat()
	w, h := pf.Width, pf.Height
	if w == 0 || h == 0 {
		return
	}

	const defaultFrameCount = 90

	for _, info := range stmap.AnimatedGeneratorInfoList() {
		// Abort promptly on shutdown instead of blocking bgWG.Wait() through
		// the full ~3-minute / ~28 GB generation of every remaining effect.
		if ctx.Err() != nil {
			slog.Info("stmap cache prewarm cancelled", "completed_through", info.Name)
			return
		}

		// Compute default params for this generator.
		params := make(map[string]float64, len(info.Params))
		for name, p := range info.Params {
			params[name] = p.Default
		}

		cacheDir := filepath.Join(a.cfg.CacheDir, "stmap",
			info.Name+"_"+stmap.CacheKey(info.Name, params, w, h, defaultFrameCount))

		// Skip if the cache directory already has all frame files.
		if stmap.DirComplete(cacheDir, defaultFrameCount) {
			slog.Debug("stmap cache already warm", "effect", info.Name, "dir", cacheDir)
			continue
		}

		// Generate the animated map and build cached processors.
		anim, err := stmap.GenerateAnimated(info.Name, params, w, h, defaultFrameCount)
		if err != nil {
			slog.Warn("stmap prewarm generate failed", "effect", info.Name, "error", err)
			continue
		}
		anim.BuildProcessorsCached(cacheDir)
		// Close immediately — we're just populating the cache files, not keeping
		// the LUTs mapped in memory.
		_ = anim.Close()
		slog.Info("stmap cache warmed", "effect", info.Name, "dir", cacheDir)
	}
	slog.Info("stmap cache prewarm complete", "width", w, "height", h)
}
