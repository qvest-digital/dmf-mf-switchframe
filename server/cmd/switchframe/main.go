// server/cmd/switchframe/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/control"
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
	Demo       bool
	FrameSync  bool
	FRCQuality string
	Format           string
	DemoVideoDir     string
	LogLevel         string
	AdminAddr        string
	APIToken         string
	ReplayBufferSecs int
	Addr             string
	HTTPFallback     bool
	TLSCert          string
	TLSKey           string

	// Raw program monitor.
	RawProgramMonitor bool   // Enable raw YUV420 program monitor track
	RawMonitorScale   string // Resolution for raw monitor (e.g. 720p, 480p)

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

	// SRT input.
	SRTListen    string // SRT listener address (e.g., ":6464")
	SRTLatencyMs int    // Default SRT latency in milliseconds

	// MXL integration.
	MXLSources        []string // Flow UUIDs to subscribe as sources
	MXLOutput         string   // Flow name for program output (empty = disabled)
	MXLOutputVideoDef string   // Path to output video flow definition JSON
	MXLOutputAudioDef string   // Path to output audio flow definition JSON
	MXLDomain         string   // MXL shared memory domain path
	MXLDiscover       bool     // List available MXL flows and exit
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
	if err := app.initOutput(); err != nil {
		return err
	}
	if err := app.initSubsystems(); err != nil {
		return err
	}
	if err := app.initMXL(); err != nil {
		if err == errDiscoverExit {
			return nil // clean exit after --mxl-discover
		}
		return err
	}
	if err := app.initSCTE35(); err != nil {
		return err
	}
	if err := app.initSCTE104(); err != nil {
		return err
	}
	if err := app.initCaptions(); err != nil {
		return err
	}
	if err := app.initClips(); err != nil {
		return err
	}
	if err := app.initSRT(); err != nil {
		return err
	}
	if err := app.initAPI(); err != nil {
		return err
	}

	return app.Run(context.Background())
}

// parseConfig parses command-line flags and resolves the API token from
// flag > environment variable > auto-generate.
func parseConfig() (AppConfig, error) {
	demoFlag := flag.Bool("demo", false, "Start with simulated camera sources")
	demoVideoDir := flag.String("demo-video", "", "Directory containing MPEG-TS clips for real video demo (requires --demo)")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	adminAddr := flag.String("admin-addr", ":9090", "Admin/metrics server listen address")
	apiTokenFlag := flag.String("api-token", "", "Bearer token for API authentication (env: SWITCHFRAME_API_TOKEN)")
	frameSyncFlag := flag.Bool("frame-sync", false, "Enable freerun frame synchronizer (aligns sources to common frame boundary)")
	frcQualityFlag := flag.String("frc-quality", "none", "Frame rate conversion: none, nearest, blend, mcfi")
	formatFlag := flag.String("format", "1080p29.97", "Video standard (e.g. 1080p29.97, 1080p25, 720p59.94)")
	replayBufferSecs := flag.Int("replay-buffer-secs", 60, "Per-source replay buffer duration in seconds (0 to disable, max 300)")
	httpFallbackFlag := flag.Bool("http-fallback", false, "Start a plain HTTP/1.1 API server on TCP :8081 for curl/scripts")
	tlsCertFlag := flag.String("tls-cert", "", "Path to TLS certificate PEM file (e.g. from mkcert)")
	tlsKeyFlag := flag.String("tls-key", "", "Path to TLS private key PEM file")

	// Raw program monitor flags.
	rawProgramMonitorFlag := flag.Bool("raw-program-monitor", false, "Enable raw YUV420 program monitor track for low-latency local display")
	rawMonitorScaleFlag := flag.String("raw-monitor-scale", "", "Resolution for raw program monitor (e.g. 720p, 480p; default: pipeline resolution)")

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

	// SRT input flags.
	srtListenFlag := flag.String("srt-listen", "", "SRT listener address for incoming push connections (e.g., :6464)")
	srtLatencyFlag := flag.Int("srt-latency", 120, "Default SRT latency in milliseconds")

	// MXL integration flags.
	mxlSourcesFlag := flag.String("mxl-sources", "", "Comma-separated MXL source specs as videoUUID or videoUUID:audioUUID or videoUUID:audioUUID:dataUUID (env: SWITCHFRAME_MXL_SOURCES)")
	mxlOutput := flag.String("mxl-output", "", "MXL flow name for program output")
	mxlOutputVideoDef := flag.String("mxl-output-video-def", "", "Path to MXL output video flow definition JSON")
	mxlOutputAudioDef := flag.String("mxl-output-audio-def", "", "Path to MXL output audio flow definition JSON")
	mxlDomain := flag.String("mxl-domain", "/dev/shm/mxl", "MXL shared memory domain path")
	mxlDiscover := flag.Bool("mxl-discover", false, "List available MXL flows and exit")

	flag.Parse()

	// Validate SCTE-35 PID when enabled.
	if *scte35Flag {
		if err := validateSCTE35PID(*scte35PIDFlag); err != nil {
			return AppConfig{}, err
		}
	}

	// MXL sources: CLI flag takes precedence over environment variable.
	var mxlSources []string
	if *mxlSourcesFlag != "" {
		mxlSources = splitAndTrim(*mxlSourcesFlag)
	} else if envSources := os.Getenv("SWITCHFRAME_MXL_SOURCES"); envSources != "" {
		mxlSources = splitAndTrim(envSources)
	}

	// Resolve API token: flag > env > auto-generate.
	apiToken := *apiTokenFlag
	if apiToken == "" {
		apiToken = os.Getenv("SWITCHFRAME_API_TOKEN")
	}
	if apiToken == "" {
		var genErr error
		apiToken, genErr = control.GenerateToken()
		if genErr != nil {
			return AppConfig{}, fmt.Errorf("generate API token: %w", genErr)
		}
	}

	return AppConfig{
		Demo:       *demoFlag,
		FrameSync:  *frameSyncFlag,
		FRCQuality: *frcQualityFlag,
		Format:            *formatFlag,
		DemoVideoDir:      *demoVideoDir,
		LogLevel:          *logLevel,
		AdminAddr:         *adminAddr,
		APIToken:          apiToken,
		ReplayBufferSecs:  *replayBufferSecs,
		Addr:              ":8080",
		HTTPFallback:      *httpFallbackFlag,
		TLSCert:           *tlsCertFlag,
		TLSKey:            *tlsKeyFlag,
		RawProgramMonitor: *rawProgramMonitorFlag,
		RawMonitorScale:   *rawMonitorScaleFlag,
		SCTE35:            *scte35Flag,
		SCTE35PID:         uint16(*scte35PIDFlag),
		SCTE35PreRollMs:   int64(*scte35PreRollFlag),
		SCTE35HeartbeatMs: int64(*scte35HeartbeatFlag),
		SCTE35Verify:      *scte35VerifyFlag,
		SCTE35WebhookURL:  *scte35WebhookFlag,
		SCTE104:           *scte104Flag,
		Captions:          *captionsFlag,
		ClipStorageMax:    *clipStorageMaxFlag,
		ClipEphemeralTTL:  *clipEphemeralTTLFlag,
		SRTListen:         *srtListenFlag,
		SRTLatencyMs:      *srtLatencyFlag,
		MXLSources:        mxlSources,
		MXLOutput:         *mxlOutput,
		MXLOutputVideoDef: *mxlOutputVideoDef,
		MXLOutputAudioDef: *mxlOutputAudioDef,
		MXLDomain:         *mxlDomain,
		MXLDiscover:       *mxlDiscover,
	}, nil
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
