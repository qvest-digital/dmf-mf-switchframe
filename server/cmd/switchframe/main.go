// server/cmd/switchframe/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/zsiec/switchframe/server/control"
)

func init() {
	// Reduce GC frequency for real-time frame processing.
	// GOGC=400 means GC triggers at 5x live heap (vs default 2x).
	// Override with GOGC environment variable.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(400)
	}
}

// AppConfig holds all configuration parsed from flags and environment.
type AppConfig struct {
	Demo             bool
	FrameSync        bool
	DemoVideoDir     string
	LogLevel         string
	AdminAddr        string
	APIToken         string
	ReplayBufferSecs int
	Addr             string

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
	replayBufferSecs := flag.Int("replay-buffer-secs", 60, "Per-source replay buffer duration in seconds (0 to disable, max 300)")

	// MXL integration flags.
	mxlSourcesFlag := flag.String("mxl-sources", "", "Comma-separated MXL source specs as videoUUID or videoUUID:audioUUID (env: SWITCHFRAME_MXL_SOURCES)")
	mxlOutput := flag.String("mxl-output", "", "MXL flow name for program output")
	mxlOutputVideoDef := flag.String("mxl-output-video-def", "", "Path to MXL output video flow definition JSON")
	mxlOutputAudioDef := flag.String("mxl-output-audio-def", "", "Path to MXL output audio flow definition JSON")
	mxlDomain := flag.String("mxl-domain", "/dev/shm/mxl", "MXL shared memory domain path")
	mxlDiscover := flag.Bool("mxl-discover", false, "List available MXL flows and exit")

	flag.Parse()

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
		Demo:              *demoFlag,
		FrameSync:         *frameSyncFlag,
		DemoVideoDir:      *demoVideoDir,
		LogLevel:          *logLevel,
		AdminAddr:         *adminAddr,
		APIToken:          apiToken,
		ReplayBufferSecs:  *replayBufferSecs,
		Addr:              ":8080",
		MXLSources:        mxlSources,
		MXLOutput:         *mxlOutput,
		MXLOutputVideoDef: *mxlOutputVideoDef,
		MXLOutputAudioDef: *mxlOutputAudioDef,
		MXLDomain:         *mxlDomain,
		MXLDiscover:       *mxlDiscover,
	}, nil
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
