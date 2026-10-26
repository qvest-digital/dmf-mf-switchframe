// server/cmd/switchframe/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/comms"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/oidcauth"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/srt"
)

func init() {
	// Reduce GC frequency for real-time frame processing.
	// GOGC=400 means GC triggers at 5x live heap (vs default 2x).
	// Override with GOGC environment variable.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(400)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(2 << 30) // 2 GB
	}
}

// AppConfig holds all configuration parsed from flags and environment.
type AppConfig struct {
	Demo             bool
	FrameSync        bool
	LowLatencySync   bool
	FRCQuality       string
	Format           string
	ColorFormat      string // "standard", "standard-hevc", or "professional"
	DemoVideoDir     string
	LogLevel         string
	AdminAddr        string
	AdminToken       string   // Bearer token for admin endpoints (/metrics, /debug)
	AllowedOrigins   []string // CORS allowed origins (empty = wildcard *)
	APIToken         string
	APITokenSource   string // where APIToken came from: "flag", "env" or "generated"
	ReplayBufferSecs int
	ReplayTmpfsDir   string // Directory for tmpfs-backed replay buffers ("none" to disable)
	Addr             string
	HTTPFallback     bool
	HTTPAddr         string
	TLSCert          string
	TLSKey           string
	HTTPSSelfSigned  bool   // serve HTTPS on --addr's TCP port with the generated certificate
	WebTransportHost string // host the browser opens WebTransport on (empty = the page's host)
	StateDir         string // State directory (env: SWITCHFRAME_STATE_DIR, default: ~/.switchframe)

	// Relay encoding (SRT source → WebTransport).
	RelayBitrate    int    // Relay encode bitrate in bps (default 2000000)
	RelayResolution string // Relay encode resolution (e.g. "720p", "480p", "source")

	// Preview proxy encoding.
	PreviewProxy         bool   // Enable low-bitrate preview encoding for source relays
	PreviewResolution    string // Preview resolution (e.g. "480p", "360p")
	PreviewBitrate       int    // Preview bitrate in bps (default 500000)
	PreviewFrameInterval int    // Encode every Nth frame (1=all, 2=half rate). Default 1.

	// Program output encoder settings.
	OutputBitrate  int    // --output-bitrate (bps, 0 = auto from resolution)
	OutputGOPSecs  int    // --output-gop-secs (1, 2, or 4)
	OutputPreset   string // --output-preset (low-latency, balanced, quality)
	OutputTimecode bool   // --output-timecode (SMPTE 12M timecode insertion)

	// SCTE-35 signaling.
	SCTE35            bool   // Enable SCTE-35 insertion
	SCTE35PID         uint16 // MPEG-TS PID for SCTE-35 data
	SCTE35PreRollMs   int64  // Default pre-roll time in milliseconds
	SCTE35HeartbeatMs int64  // Heartbeat interval in milliseconds (0 = disabled)
	SCTE35Verify      bool   // Round-trip encode verification
	SCTE35WebhookURL  string // Webhook URL for event notifications
	SCTE104           bool   // Enable SCTE-104 on MXL data flows

	// Closed captions.
	Captions bool // Enable CEA-608/708 closed captioning

	// Clip storage.
	ClipStorageMax   int64         // --clip-storage-max (bytes, default 10GB)
	ClipEphemeralTTL time.Duration // --clip-ephemeral-ttl (default 24h)

	// Playout.
	PlayoutEnabled      bool // --playout (enable playout subsystem)
	PlayoutCacheMaxGB   int  // --playout-cache-max (GB, default 200)
	PlayoutAsRunRetDays int  // --playout-asrun-retention (days, default 30)

	// SRT input.
	SRTListen    string // SRT listener address (e.g., ":6464")
	SRTLatencyMs int    // Default SRT latency in milliseconds

	// SRT output port range (cloud-allocated).
	SRTOutputPortBase int    // --srt-output-ports base (0 = unconstrained)
	SRTOutputPortEnd  int    // --srt-output-ports end
	Domain            string // --domain (public hostname for connection URLs)

	// Invite tokens: token -> role for O(1) lookup (env: SWITCHFRAME_INVITE_TOKENS).
	InviteTokens map[string]string

	// OpenID Connect sign-in (--oidc-*); nil = disabled.
	OIDC *oidcauth.Config

	// MXL integration.
	MXLSources        []string // Flow UUIDs to subscribe as sources
	MXLOutput         string   // Flow name for program output (empty = disabled)
	MXLOutputVideoDef string   // Path to output video flow definition JSON
	MXLOutputAudioDef string   // Path to output audio flow definition JSON

	// MXLNMOS configures the NMOS node for named MXL inputs (--mxl-nmos-*).
	MXLNMOS     mxlNMOSConfig
	MXLDomain   string // MXL shared memory domain path
	MXLDiscover bool   // List available MXL flows and exit

	// AI inference (segmentation, depth, SAM, tracking — all via sidecar).
	AISidecarBin string // Path to switchframe-ai sidecar binary (empty = AI disabled)
	AIModelsDir  string // Path to directory with all AI model files (empty = auto-discover)

	// Neural key (CorridorKey).
	NeuralKeyModelPath string // Path to CorridorKey ONNX model (empty = disabled)

	// ASR (speech-to-text).
	ASRSidecarBin       string // path to switchframe-asr binary
	ASRSidecarModelDir  string // path to streaming model directory
	ASRWhisperModelsDir string // Whisper GGML models directory for ASR sidecar
	ASRDefaultBackend   string // default ASR backend (parakeet or whisper)
	ASRWhisperThreads   int    // whisper.cpp inference threads (0 = auto)

	// CEF (HTML5 graphics) sidecar.
	CEFBinary       string // path to switchframe-cef binary or .app bundle binary (empty = built-in default)
	CEFTemplatesDir string // path to HTML5 graphics templates directory (empty = built-in default)

	// Persistent cache directory for mmap'd LUTs and decoded assets.
	CacheDir string

	// ST 2110 integration.
	ST2110Port     string // DPDK port identifier (e.g. "0000:31:00.0")
	ST2110IP       string // local IP for TX
	ST2110Inputs   int    // number of ST 2110 inputs (default 4)
	ST2110PTP      string // PTP mode (default "auto")
	ST2110NMOSPort int    // NMOS API HTTP port (default 8090)

	// Outputs started with the process (--outputs-config): SRT and RTMP
	// destinations and the recording, which otherwise exist only once
	// created over the API.
	OutputsConfig string

	// Inter-engine peer health monitoring.
	PeerEngineURL string // --peer-engine-url (URL of the other engine for health polling)

	// PeerPublicURL is the peer engine's origin as the browser reaches it,
	// handed to the UI for its second engine. Empty: PeerEngineURL.
	PeerPublicURL string // --peer-public-url or SWITCHFRAME_PEER_PUBLIC_URL

	// PeerCertHash is a comma-separated list of hex SHA-256 leaf-certificate
	// fingerprints accepted when TLS-connecting to the peer engine. Required
	// when PeerEngineURL uses an RFC 1918 IP (standard LE certs only cover
	// FQDNs). Ignored when the URL host is a DNS name. A two-element list
	// enables cert rotation without dropping connectivity.
	PeerCertHash string // --peer-cert-hash or SWITCHFRAME_PEER_CERT_HASH

	// EngineLabel identifies this engine in an active-active pair ("a" or "b").
	// Empty means single-engine mode; all peer-role logic no-ops in that mode.
	EngineLabel string // --engine-label or SWITCHFRAME_ENGINE_LABEL

	// ControlPlaneSecret enables POST /api/peer/force-leader. The control
	// plane presents this value in the X-Control-Plane-Secret header to
	// authorize force-takeover. Empty disables the endpoint (returns 501).
	ControlPlaneSecret string // --control-plane-secret or SWITCHFRAME_CONTROL_PLANE_SECRET

	// InitialProgramSource is the source key an engine goes live on at
	// start when no peer is on air to follow. Empty: the first input that
	// delivers frames.
	InitialProgramSource string // --initial-program-source or SWITCHFRAME_INITIAL_PROGRAM_SOURCE

	// Reference clock: single clock source drives synchronous A/V processing.
	RefClock bool // --ref-clock (experimental)

	// Cache warming (AMI build).
	WarmCache bool // --warm-cache (generate caches and exit)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseConfig()
	if err != nil {
		return err
	}

	if err := validateEngineLabel(cfg.EngineLabel); err != nil {
		return err
	}

	if err := peer.ValidatePeerTLSConfig(cfg.PeerEngineURL, cfg.PeerCertHash); err != nil {
		return err
	}

	// Auto-enable SRT listener in demo mode so demo SRT pushers have
	// something to connect to without requiring --srt-listen explicitly.
	if cfg.Demo && cfg.SRTListen == "" {
		cfg.SRTListen = srt.DefaultPort
	}

	// Auto-enable playout and SCTE-35 in demo mode so the demo playlist
	// can demonstrate ad break insertion via secondary events.
	if cfg.Demo {
		cfg.PlayoutEnabled = true
		cfg.SCTE35 = true
	}

	// --warm-cache: pre-generate caches and exit (used during AMI build).
	if cfg.WarmCache {
		return warmCache(cfg)
	}

	app := &App{cfg: cfg}
	defer app.Close()

	if err := app.initInfra(); err != nil {
		return err
	}
	if err := app.initPrismServer(); err != nil {
		return err
	}
	if err := app.initCoreEngine(); err != nil {
		return err
	}
	if err := app.initCommandQueue(); err != nil {
		return err
	}
	if err := app.initOutput(); err != nil {
		return err
	}
	if err := app.initSubsystems(); err != nil {
		return err
	}
	if err := app.initBuiltinSourcePreview(); err != nil {
		return err
	}
	if err := app.initMXL(); err != nil {
		if err == errDiscoverExit {
			return nil // clean exit after --mxl-discover
		}
		return err
	}
	if err := app.initMXLNMOS(); err != nil {
		return err
	}
	if err := app.initST2110(context.Background()); err != nil {
		return err
	}
	if err := app.initSCTE35(); err != nil {
		return err
	}
	if err := app.initSCTE104(); err != nil {
		return err
	}
	app.initPicTiming()
	if err := app.initCaptions(); err != nil {
		return err
	}
	if err := app.initASR(); err != nil {
		return err
	}
	if err := app.initClips(); err != nil {
		return err
	}
	if cfg.PlayoutEnabled {
		if err := app.initPlayout(); err != nil {
			return err
		}
	}
	if err := app.initSRT(); err != nil {
		return err
	}
	if err := app.initStartupOutputs(); err != nil {
		return err
	}
	// Operator voice comms (always available, no config flag needed).
	app.commsMgr = comms.NewManager(func() {
		app.broadcastState(nil)
	})

	if err := app.initAPI(); err != nil {
		return err
	}
	app.startMXLNMOS()

	// ASR engine is initialized but not active — the user enables auto
	// captions via the UI (Captions panel → Auto button). This prevents
	// unwanted transcription on session start.

	return app.Run(context.Background())
}

// parseConfig parses command-line flags and resolves the API token from
// flag > environment variable > auto-generate.
func parseConfig() (AppConfig, error) {
	demoFlag := flag.Bool("demo", false, "Start with simulated camera sources")
	demoVideoDir := flag.String("demo-video", "", "Directory containing MPEG-TS clips for real video demo (requires --demo)")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	adminAddr := flag.String("admin-addr", "127.0.0.1:9090", "Admin/metrics server listen address")
	adminTokenFlag := flag.String("admin-token", "", "Bearer token for admin endpoints (/metrics, /debug); if empty, these endpoints are unprotected")
	allowedOriginsFlag := flag.String("allowed-origins", "", "Comma-separated allowed CORS origins (e.g., https://switchframe.dev); empty = allow all")
	apiTokenFlag := flag.String("api-token", "", "Bearer token for API authentication (env: SWITCHFRAME_API_TOKEN)")
	frameSyncFlag := flag.Bool("frame-sync", false, "Enable freerun frame synchronizer (aligns sources to common frame boundary)")
	lowLatencySyncFlag := flag.Bool("low-latency-sync", false, "Use source-driven frame sync (lower latency, less smooth output)")
	frcQualityFlag := flag.String("frc-quality", "none", "Frame rate conversion: none, nearest, blend, mcfi")
	formatFlag := flag.String("format", "1080p29.97", "Video standard (e.g. 1080p29.97, 1080p25, 720p59.94)")
	colorFormatFlag := flag.String("color-format", "standard", "Pipeline color format profile (standard, standard-hevc, professional)")
	replayBufferSecs := flag.Int("replay-buffer-secs", 300, "Per-source replay buffer duration in seconds (0 to disable, max 300)")
	replayTmpfsDir := flag.String("replay-tmpfs-dir", "/dev/shm/switchframe/replay", "Directory for tmpfs-backed replay buffers (use 'none' to disable)")
	addrFlag := flag.String("addr", ":8080", "QUIC/HTTP3 listen address (e.g., :8080, 0.0.0.0:443)")
	httpFallbackFlag := flag.Bool("http-fallback", false, "Start a plain HTTP/1.1 API server on TCP :8081 for curl/scripts")
	httpAddrFlag := flag.String("http-addr", ":8081", "HTTP/1.1 fallback listen address (requires --http-fallback)")
	tlsCertFlag := flag.String("tls-cert", "", "Path to TLS certificate PEM file (e.g. from mkcert)")
	tlsKeyFlag := flag.String("tls-key", "", "Path to TLS private key PEM file")
	webTransportHostFlag := flag.String("webtransport-host", "", "Host the browser opens WebTransport on, when it is not the host the UI is served from (e.g. behind an HTTP proxy that carries no UDP)")
	httpsSelfSignedFlag := flag.Bool("https-self-signed", false, "Without --tls-cert, serve the UI and API over HTTPS on the TCP side of --addr with the generated self-signed certificate")

	// Relay encoding flags.
	relayBitrateFlag := flag.Int("relay-bitrate", 2000000, "Relay encode bitrate in bps for SRT source WebTransport distribution")
	relayResolutionFlag := flag.String("relay-resolution", "720p", "Relay encode resolution (720p, 480p, 360p, or source for no scaling)")

	// Preview proxy encoding flags.
	previewProxyFlag := flag.Bool("preview-proxy", false, "Enable low-bitrate preview encoding for browser source previews")
	previewResolutionFlag := flag.String("preview-resolution", "480p", "Preview encode resolution (e.g. 480p, 360p, 720p)")
	previewBitrateFlag := flag.Int("preview-bitrate", 500000, "Preview encode bitrate in bps")
	previewFrameIntervalFlag := flag.Int("preview-frame-interval", 1, "Preview encode every Nth frame (1=all, 2=half rate)")

	// Program output encoder flags.
	outputBitrateFlag := flag.Int("output-bitrate", 0, "Program output encoder bitrate in bps (0 = auto from resolution)")
	outputGOPSecsFlag := flag.Int("output-gop-secs", 0, "Program output GOP/keyframe interval in seconds (1, 2, or 4; 0 = default 2)")
	outputPresetFlag := flag.String("output-preset", "", "Program output encoder preset (low-latency, balanced, quality; empty = balanced)")
	outputTimecodeFlag := flag.Bool("output-timecode", false, "Enable SMPTE 12M timecode insertion in program output")

	// SCTE-35 flags.
	scte35Flag := flag.Bool("scte35", false, "Enable SCTE-35 insertion")
	scte35PIDFlag := flag.Int("scte35-pid", 0x102, "SCTE-35 PID in MPEG-TS output")
	scte35PreRollFlag := flag.Int("scte35-preroll", 4000, "Default pre-roll in milliseconds for scheduled cues")
	scte35HeartbeatFlag := flag.Int("scte35-heartbeat", 5000, "Interval between splice_null heartbeats in milliseconds (0 to disable)")
	scte35VerifyFlag := flag.Bool("scte35-verify", true, "Verify SCTE-35 encoding by round-trip decode")
	scte35WebhookFlag := flag.String("scte35-webhook", "", "Webhook URL for SCTE-35 event notifications")

	// SCTE-104 flag (requires --scte35 and MXL integration).
	scte104Flag := flag.Bool("scte104", false, "Enable SCTE-104 on MXL data flows (requires --scte35)")

	// Caption flag.
	captionsFlag := flag.Bool("captions", false, "Enable CEA-608/708 closed captioning")

	// Clip storage flags.
	clipStorageMaxFlag := flag.Int64("clip-storage-max", 10<<30, "Maximum clip storage in bytes (default 10GB)")
	clipEphemeralTTLFlag := flag.Duration("clip-ephemeral-ttl", 24*time.Hour, "TTL for ephemeral clips (default 24h)")

	// Playout flags.
	playoutFlag := flag.Bool("playout", false, "Enable playout subsystem")
	playoutCacheMaxFlag := flag.Int("playout-cache-max", 200, "Playout cache max size in GB")
	playoutAsRunRetFlag := flag.Int("playout-asrun-retention", 30, "As-run log retention in days")

	// SRT input flags.
	srtListenFlag := flag.String("srt-listen", "", "SRT listener address for incoming push connections (e.g., :6464)")
	srtLatencyFlag := flag.Int("srt-latency", 120, "Default SRT latency in milliseconds")

	// Cloud integration flags.
	srtOutputPortsFlag := flag.String("srt-output-ports", "", "Allowed SRT output port range (e.g., 7464-7467)")
	domainFlag := flag.String("domain", "", "Public domain for connection URLs (e.g., switchframe.dev)")

	// MXL integration flags.
	mxlSourcesFlag := flag.String("mxl-sources", "", "Comma-separated MXL source specs: videoUUID[:audioUUID[:dataUUID]], keyed by the video flow, or name=[videoUUID[:audioUUID[:dataUUID]]], keyed by the name, which can be moved to other flows while running and may be booked without flows (env: SWITCHFRAME_MXL_SOURCES)")
	mxlOutput := flag.String("mxl-output", "", "MXL flow name for program output")
	mxlNMOSPortFlag := flag.Int("mxl-nmos-port", 0, "port of the NMOS node (IS-04, IS-05, BCP-007-03) through which a controller connects the named MXL inputs to flows; 0 = off (env: SWITCHFRAME_MXL_NMOS_PORT)")
	mxlNMOSSeedFlag := flag.String("mxl-nmos-seed", "", "seed of the NMOS resource ids; the same on both engines of a pair and across restarts (env: SWITCHFRAME_MXL_NMOS_SEED)")
	mxlNMOSLabelFlag := flag.String("mxl-nmos-label", "", "label of the NMOS node and device; empty = switchframe (env: SWITCHFRAME_MXL_NMOS_LABEL)")
	mxlNMOSHostFlag := flag.String("mxl-nmos-host", "", "host a registry and controllers reach this engine's NMOS node at; empty = hostname (env: SWITCHFRAME_MXL_NMOS_HOST)")
	mxlNMOSRegistryFlag := flag.String("mxl-nmos-registry-url", "", "origin of the IS-04 Registration API to register with; empty = do not register (env: SWITCHFRAME_MXL_NMOS_REGISTRY_URL)")
	mxlNMOSQueryFlag := flag.String("mxl-nmos-query-url", "", "origin of the IS-04 Query API connected senders are looked up in; empty = the registry's (env: SWITCHFRAME_MXL_NMOS_QUERY_URL)")
	mxlNMOSPeerVideoDefFlag := flag.String("mxl-nmos-peer-output-video-def", "", "path to the peer engine's MXL output video flow definition JSON, offered as a sender of the peer's program (env: SWITCHFRAME_MXL_NMOS_PEER_OUTPUT_VIDEO_DEF)")
	mxlNMOSPeerAudioDefFlag := flag.String("mxl-nmos-peer-output-audio-def", "", "path to the peer engine's MXL output audio flow definition JSON, offered as a sender of the peer's program (env: SWITCHFRAME_MXL_NMOS_PEER_OUTPUT_AUDIO_DEF)")
	mxlOutputVideoDef := flag.String("mxl-output-video-def", "", "Path to MXL output video flow definition JSON")
	mxlOutputAudioDef := flag.String("mxl-output-audio-def", "", "Path to MXL output audio flow definition JSON")
	mxlDomain := flag.String("mxl-domain", "/dev/shm/mxl", "MXL shared memory domain path")
	mxlDiscover := flag.Bool("mxl-discover", false, "List available MXL flows and exit")

	// AI segmentation flags.
	aiSidecarFlag := flag.String("ai-sidecar", "", "Path to switchframe-ai sidecar binary (all AI inference)")
	aiModelsDirFlag := flag.String("ai-models-dir", "", "Directory with AI model files (ONNX + .plan) forwarded to sidecar")

	// Neural key flags.
	neuralKeyModelFlag := flag.String("neural-key-model", "", "Path to CorridorKey ONNX model for neural green screen keying (requires CUDA + TensorRT)")

	// ASR (speech-to-text) flags.
	asrSidecarFlag := flag.String("asr-sidecar", "", "Path to switchframe-asr sidecar binary")
	asrSidecarModelDirFlag := flag.String("asr-sidecar-model-dir", "", "Path to streaming ASR model directory")
	asrWhisperModelsDirFlag := flag.String("asr-whisper-models-dir", "", "Whisper GGML models directory for ASR sidecar")
	asrDefaultBackendFlag := flag.String("asr-default-backend", "parakeet", "Default ASR backend (parakeet or whisper)")
	asrWhisperThreadsFlag := flag.Int("asr-whisper-threads", 0, "Whisper inference threads (0 = auto: min(NumCPU, 8))")

	// CEF (HTML5 graphics) sidecar flags.
	cefBinaryFlag := flag.String("cef-binary", "", "Path to switchframe-cef sidecar binary (relative to the switchframe binary or absolute). Empty = use built-in Linux default.")
	cefTemplatesDirFlag := flag.String("cef-templates-dir", "", "Path to HTML5 graphics templates directory. Empty = use built-in Linux default.")

	// Persistent cache directory.
	cacheDirFlag := flag.String("cache-dir", defaultCacheDir(), "persistent cache directory for mmap'd LUTs and decoded assets")

	// ST 2110 integration flags.
	st2110PortFlag := flag.String("st2110-port", "", "DPDK port identifier for ST 2110 (e.g. 0000:31:00.0)")
	st2110IPFlag := flag.String("st2110-ip", "", "Local IP address for ST 2110 TX sessions")
	st2110InputsFlag := flag.Int("st2110-inputs", 4, "Number of ST 2110 input receivers")
	st2110PTPFlag := flag.String("st2110-ptp", "auto", "PTP mode for ST 2110 (auto, l2, l4, none)")
	st2110NMOSPortFlag := flag.Int("st2110-nmos-port", 8090, "NMOS API HTTP port for IS-04/IS-05")

	// Outputs started with the process.
	outputsConfigFlag := flag.String("outputs-config", "", "Path to a JSON file of SRT/RTMP destinations and a recording to start with the process")

	// Peer engine health monitoring.
	peerEngineURLFlag := flag.String("peer-engine-url", "", "URL of the peer engine for inter-engine health monitoring (e.g., https://engine-b:8080)")
	peerPublicURLFlag := flag.String("peer-public-url", "", "Peer engine origin as the browser reaches it, when it differs from --peer-engine-url (e.g. https://engine-b.example.com)")
	peerCertHashFlag := flag.String("peer-cert-hash", "", "SHA-256 hex of the peer engine's leaf cert DER (comma-separated pair during rotation); required when --peer-engine-url uses a private IP")
	engineLabelFlag := flag.String("engine-label", "", "engine label for active-active pairs (\"a\" or \"b\"); empty = single-engine")
	controlPlaneSecretFlag := flag.String("control-plane-secret", "", "shared secret authorizing POST /api/peer/force-leader; empty disables the endpoint")
	initialProgramSourceFlag := flag.String("initial-program-source", "", "source key put on air at start when no peer engine is on air to follow, e.g. mxl:cam1; empty = the first input delivering frames (env: SWITCHFRAME_INITIAL_PROGRAM_SOURCE)")

	// OpenID Connect sign-in.
	oidcIssuerFlag := flag.String("oidc-issuer", "", "OpenID Connect issuer URL; enables sign-in with that provider (env: SWITCHFRAME_OIDC_ISSUER)")
	oidcClientIDFlag := flag.String("oidc-client-id", "", "OpenID Connect client ID (env: SWITCHFRAME_OIDC_CLIENT_ID)")
	oidcClientSecretFileFlag := flag.String("oidc-client-secret-file", "", "File holding the OpenID Connect client secret; omit for a public client (env: SWITCHFRAME_OIDC_CLIENT_SECRET_FILE)")
	oidcRedirectURLFlag := flag.String("oidc-redirect-url", "", "Callback URL registered with the provider; empty = <scheme>://<request host>/auth/callback (env: SWITCHFRAME_OIDC_REDIRECT_URL)")
	oidcScopesFlag := flag.String("oidc-scopes", "", "Comma-separated scopes to request; empty = openid,profile,email (env: SWITCHFRAME_OIDC_SCOPES)")
	oidcRoleClaimFlag := flag.String("oidc-role-claim", "", "ID token claim holding the user's roles or groups, dot-separated for a nested claim; empty = groups (env: SWITCHFRAME_OIDC_ROLE_CLAIM)")
	oidcRoleMapFlag := flag.String("oidc-role-map", "", "Comma-separated value=role pairs mapping claim values to operator roles; empty = values named like a role (env: SWITCHFRAME_OIDC_ROLE_MAP)")
	oidcDefaultRoleFlag := flag.String("oidc-default-role", "", "Role for a signed-in user with no mapped claim value; empty = refuse them (env: SWITCHFRAME_OIDC_DEFAULT_ROLE)")

	// Reference clock (experimental).
	refClockFlag := flag.Bool("ref-clock", false, "Use reference clock for synchronous A/V processing (experimental)")

	// Cache warming (for AMI build).
	warmCacheFlag := flag.Bool("warm-cache", false, "Pre-generate caches (ST map LUTs) for all resolutions and exit")

	flag.Parse()

	// Parse SRT output port range if provided.
	var srtOutputBase, srtOutputEnd int
	if *srtOutputPortsFlag != "" {
		parts := strings.SplitN(*srtOutputPortsFlag, "-", 2)
		if len(parts) != 2 {
			return AppConfig{}, fmt.Errorf("--srt-output-ports must be base-end (e.g., 7464-7467)")
		}
		base, err := strconv.Atoi(parts[0])
		if err != nil {
			return AppConfig{}, fmt.Errorf("--srt-output-ports base: %w", err)
		}
		end, err := strconv.Atoi(parts[1])
		if err != nil {
			return AppConfig{}, fmt.Errorf("--srt-output-ports end: %w", err)
		}
		if base > end || base <= 0 {
			return AppConfig{}, fmt.Errorf("--srt-output-ports: base (%d) must be > 0 and <= end (%d)", base, end)
		}
		srtOutputBase = base
		srtOutputEnd = end
	}

	// Validate SCTE-35 PID when enabled.
	if *scte35Flag {
		if err := validateSCTE35PID(*scte35PIDFlag); err != nil {
			return AppConfig{}, err
		}
	}

	// Validate output encoder options.
	encOpts := codec.EncoderOptions{
		Bitrate:  *outputBitrateFlag,
		GOPSecs:  *outputGOPSecsFlag,
		Preset:   codec.OutputPreset(*outputPresetFlag),
		Timecode: *outputTimecodeFlag,
	}
	if err := encOpts.Validate(); err != nil {
		return AppConfig{}, err
	}

	// MXL sources: CLI flag takes precedence over environment variable.
	var mxlSources []string
	if *mxlSourcesFlag != "" {
		mxlSources = splitAndTrim(*mxlSourcesFlag)
	} else if envSources := os.Getenv("SWITCHFRAME_MXL_SOURCES"); envSources != "" {
		mxlSources = splitAndTrim(envSources)
	}

	// Resolve API token: flag > env > auto-generate.
	apiToken, apiTokenSource := *apiTokenFlag, "flag"
	if apiToken == "" {
		apiToken, apiTokenSource = os.Getenv("SWITCHFRAME_API_TOKEN"), "env"
	}
	if apiToken == "" {
		var genErr error
		apiToken, genErr = control.GenerateToken()
		if genErr != nil {
			return AppConfig{}, fmt.Errorf("generate API token: %w", genErr)
		}
		apiTokenSource = "generated"
	}

	// Resolve state directory: env > default (~/.switchframe).
	stateDir := os.Getenv("SWITCHFRAME_STATE_DIR")
	if stateDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return AppConfig{}, fmt.Errorf("determine home directory: %w", err)
		}
		stateDir = filepath.Join(homeDir, ".switchframe")
	}

	// Parse invite tokens: "director:tok1,audio:tok2,..."
	// Stored as token -> role (reversed) for O(1) lookup during registration.
	var inviteTokens map[string]string
	if envTokens := os.Getenv("SWITCHFRAME_INVITE_TOKENS"); envTokens != "" {
		inviteTokens = make(map[string]string)
		for _, pair := range strings.Split(envTokens, ",") {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				role, token := parts[0], parts[1]
				inviteTokens[token] = role
			}
		}
	}

	oidcCfg, err := oidcSettings{
		Issuer:           resolveEnvOrFlag(*oidcIssuerFlag, "SWITCHFRAME_OIDC_ISSUER"),
		ClientID:         resolveEnvOrFlag(*oidcClientIDFlag, "SWITCHFRAME_OIDC_CLIENT_ID"),
		ClientSecretFile: resolveEnvOrFlag(*oidcClientSecretFileFlag, "SWITCHFRAME_OIDC_CLIENT_SECRET_FILE"),
		RedirectURL:      resolveEnvOrFlag(*oidcRedirectURLFlag, "SWITCHFRAME_OIDC_REDIRECT_URL"),
		Scopes:           resolveEnvOrFlag(*oidcScopesFlag, "SWITCHFRAME_OIDC_SCOPES"),
		RoleClaim:        resolveEnvOrFlag(*oidcRoleClaimFlag, "SWITCHFRAME_OIDC_ROLE_CLAIM"),
		RoleMap:          resolveEnvOrFlag(*oidcRoleMapFlag, "SWITCHFRAME_OIDC_ROLE_MAP"),
		DefaultRole:      resolveEnvOrFlag(*oidcDefaultRoleFlag, "SWITCHFRAME_OIDC_DEFAULT_ROLE"),
	}.config()
	if err != nil {
		return AppConfig{}, err
	}

	// Parse allowed origins for CORS.
	var allowedOrigins []string
	if *allowedOriginsFlag != "" {
		allowedOrigins = splitAndTrim(*allowedOriginsFlag)
	}

	return AppConfig{
		Demo:                 *demoFlag,
		FrameSync:            *frameSyncFlag,
		LowLatencySync:       *lowLatencySyncFlag,
		FRCQuality:           *frcQualityFlag,
		Format:               *formatFlag,
		ColorFormat:          *colorFormatFlag,
		DemoVideoDir:         *demoVideoDir,
		LogLevel:             *logLevel,
		AdminAddr:            *adminAddr,
		AdminToken:           resolveEnvOrFlag(*adminTokenFlag, "SWITCHFRAME_ADMIN_TOKEN"),
		AllowedOrigins:       allowedOrigins,
		APIToken:             apiToken,
		APITokenSource:       apiTokenSource,
		ReplayBufferSecs:     *replayBufferSecs,
		ReplayTmpfsDir:       *replayTmpfsDir,
		Addr:                 *addrFlag,
		HTTPFallback:         *httpFallbackFlag,
		HTTPAddr:             *httpAddrFlag,
		TLSCert:              *tlsCertFlag,
		TLSKey:               *tlsKeyFlag,
		HTTPSSelfSigned:      *httpsSelfSignedFlag,
		WebTransportHost:     *webTransportHostFlag,
		RelayBitrate:         *relayBitrateFlag,
		RelayResolution:      *relayResolutionFlag,
		PreviewProxy:         *previewProxyFlag,
		PreviewResolution:    *previewResolutionFlag,
		PreviewBitrate:       *previewBitrateFlag,
		PreviewFrameInterval: *previewFrameIntervalFlag,
		OutputBitrate:        *outputBitrateFlag,
		OutputGOPSecs:        *outputGOPSecsFlag,
		OutputPreset:         *outputPresetFlag,
		OutputTimecode:       *outputTimecodeFlag,
		SCTE35:               *scte35Flag,
		SCTE35PID:            uint16(*scte35PIDFlag),
		SCTE35PreRollMs:      int64(*scte35PreRollFlag),
		SCTE35HeartbeatMs:    int64(*scte35HeartbeatFlag),
		SCTE35Verify:         *scte35VerifyFlag,
		SCTE35WebhookURL:     *scte35WebhookFlag,
		SCTE104:              *scte104Flag,
		Captions:             *captionsFlag,
		ClipStorageMax:       *clipStorageMaxFlag,
		ClipEphemeralTTL:     *clipEphemeralTTLFlag,
		PlayoutEnabled:       *playoutFlag,
		PlayoutCacheMaxGB:    *playoutCacheMaxFlag,
		PlayoutAsRunRetDays:  *playoutAsRunRetFlag,
		SRTListen:            *srtListenFlag,
		SRTLatencyMs:         *srtLatencyFlag,
		SRTOutputPortBase:    srtOutputBase,
		SRTOutputPortEnd:     srtOutputEnd,
		Domain:               *domainFlag,
		InviteTokens:         inviteTokens,
		OIDC:                 oidcCfg,
		MXLSources:           mxlSources,
		MXLOutput:            *mxlOutput,
		MXLOutputVideoDef:    *mxlOutputVideoDef,
		MXLOutputAudioDef:    *mxlOutputAudioDef,
		MXLDomain:            *mxlDomain,
		MXLDiscover:          *mxlDiscover,
		StateDir:             stateDir,
		AISidecarBin:         *aiSidecarFlag,
		AIModelsDir:          *aiModelsDirFlag,
		NeuralKeyModelPath:   *neuralKeyModelFlag,
		ASRSidecarBin:        *asrSidecarFlag,
		ASRSidecarModelDir:   *asrSidecarModelDirFlag,
		ASRWhisperModelsDir:  *asrWhisperModelsDirFlag,
		ASRDefaultBackend:    *asrDefaultBackendFlag,
		ASRWhisperThreads:    *asrWhisperThreadsFlag,
		CEFBinary:            *cefBinaryFlag,
		CEFTemplatesDir:      *cefTemplatesDirFlag,
		CacheDir:             *cacheDirFlag,
		ST2110Port:           *st2110PortFlag,
		ST2110IP:             *st2110IPFlag,
		ST2110Inputs:         *st2110InputsFlag,
		ST2110PTP:            *st2110PTPFlag,
		ST2110NMOSPort:       *st2110NMOSPortFlag,
		OutputsConfig:        *outputsConfigFlag,
		PeerEngineURL:        resolveEnvOrFlag(*peerEngineURLFlag, "SWITCHFRAME_PEER_ENGINE_URL"),
		MXLNMOS: mxlNMOSConfig{
			Port:        resolveIntEnvOrFlag(*mxlNMOSPortFlag, "SWITCHFRAME_MXL_NMOS_PORT"),
			Seed:        resolveEnvOrFlag(*mxlNMOSSeedFlag, "SWITCHFRAME_MXL_NMOS_SEED"),
			Label:       resolveEnvOrFlag(*mxlNMOSLabelFlag, "SWITCHFRAME_MXL_NMOS_LABEL"),
			Host:        resolveEnvOrFlag(*mxlNMOSHostFlag, "SWITCHFRAME_MXL_NMOS_HOST"),
			RegistryURL: resolveEnvOrFlag(*mxlNMOSRegistryFlag, "SWITCHFRAME_MXL_NMOS_REGISTRY_URL"),
			QueryURL:    resolveEnvOrFlag(*mxlNMOSQueryFlag, "SWITCHFRAME_MXL_NMOS_QUERY_URL"),

			PeerOutputVideoDef: resolveEnvOrFlag(*mxlNMOSPeerVideoDefFlag, "SWITCHFRAME_MXL_NMOS_PEER_OUTPUT_VIDEO_DEF"),
			PeerOutputAudioDef: resolveEnvOrFlag(*mxlNMOSPeerAudioDefFlag, "SWITCHFRAME_MXL_NMOS_PEER_OUTPUT_AUDIO_DEF"),
		},
		PeerCertHash:         resolveEnvOrFlag(*peerCertHashFlag, "SWITCHFRAME_PEER_CERT_HASH"),
		PeerPublicURL:        resolveEnvOrFlag(*peerPublicURLFlag, "SWITCHFRAME_PEER_PUBLIC_URL"),
		EngineLabel:          resolveEnvOrFlag(*engineLabelFlag, "SWITCHFRAME_ENGINE_LABEL"),
		ControlPlaneSecret:   resolveEnvOrFlag(*controlPlaneSecretFlag, "SWITCHFRAME_CONTROL_PLANE_SECRET"),
		InitialProgramSource: resolveEnvOrFlag(*initialProgramSourceFlag, "SWITCHFRAME_INITIAL_PROGRAM_SOURCE"),
		RefClock:             *refClockFlag,
		WarmCache:            *warmCacheFlag,
	}, nil
}

// validateEngineLabel returns an error if label is not one of "", "a", "b".
// Accepted at startup; any other value (including uppercase or whitespace)
// is rejected so misconfiguration surfaces immediately rather than causing
// silent split-brain under partition.
func validateEngineLabel(label string) error {
	switch label {
	case "", "a", "b":
		return nil
	default:
		return fmt.Errorf("--engine-label must be \"\", \"a\", or \"b\"; got %q", label)
	}
}

// resolveEnvOrFlag returns the flag value if non-empty, else the env var value.
// resolveIntEnvOrFlag is resolveEnvOrFlag for an integer flag whose zero
// value means unset. An unparsable environment value counts as unset.
func resolveIntEnvOrFlag(flagVal int, envKey string) int {
	if flagVal != 0 {
		return flagVal
	}
	v, err := strconv.Atoi(os.Getenv(envKey))
	if err != nil {
		return 0
	}
	return v
}

func resolveEnvOrFlag(flagVal, envKey string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(envKey)
}

// validateSCTE35PID checks that the given PID is in the valid MPEG-TS range
// for user-defined PIDs [0x0020, 0x1FFE].
func validateSCTE35PID(pid int) error {
	if pid < 0x20 || pid > 0x1FFE {
		return fmt.Errorf("--scte35-pid %d out of valid MPEG-TS PID range [0x0020, 0x1FFE]", pid)
	}
	return nil
}

// splitAndTrim splits a comma-separated string and trims whitespace.
func splitAndTrim(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// defaultCacheDir returns a platform-appropriate persistent cache directory.
// Linux (production): /data/switchframe/cache
// macOS (dev):        ~/.switchframe/cache
func defaultCacheDir() string {
	if runtime.GOOS == "linux" {
		return "/data/switchframe/cache"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "switchframe", "cache")
	}
	return filepath.Join(home, ".switchframe", "cache")
}
