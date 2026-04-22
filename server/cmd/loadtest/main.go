// loadtest drives N synthetic V210 sources into an in-process switcher +
// mixer and emits one NDJSON sample per tick plus a final summary.
//
// Phase 1 measures only what the in-process wiring actually exercises —
// per-source ingest (RawFrameCount, Health) and runtime counters
// (goroutines, GC pauses). Pipeline and mixer-cycle metrics require
// BuildPipeline + an encoder factory (Phase 2). See the README and
// docs/reference/loadtest.md for usage.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/perf"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// audioTickCadenceNs is the outputTicker cadence for a 48 kHz mixer with
// 1024-sample frames: 1024 / 48000 ≈ 21.333 ms.
const audioTickCadenceNs int64 = 1024 * 1_000_000_000 / 48000

// noopAudioEncoder is a zero-cost audio.Encoder so the mixer tick loop
// runs recordAndOutput without pulling in the FDK cgo dependency. The
// encoded output is immediately discarded; we only care that the mixer
// updates its MaxInterFrameGapNs / FramesOutput counters.
type noopAudioEncoder struct{}

func (noopAudioEncoder) Encode(pcm []float32) ([]byte, error) { return []byte{}, nil }
func (noopAudioEncoder) Close() error                          { return nil }

func noopAudioEncoderFactory(sampleRate, channels int) (audio.Encoder, error) {
	return noopAudioEncoder{}, nil
}

func mockVideoEncoderFactory(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
	return transition.NewMockEncoder(), nil
}

const maxSources = 256

type config struct {
	sources      int
	width        int
	height       int
	fps          float64
	duration     time.Duration
	warmup       time.Duration
	output       string
	profile      string
	apiToken     string
	outputPath   string
	tickInterval time.Duration
}

type sourceTick struct {
	Health        string  `json:"health"`
	RawFrameCount int64   `json:"raw_frame_count"`
	IngestFPS     float64 `json:"ingest_fps"`
}

type tickEvent struct {
	Phase    string                `json:"phase"`
	T        float64               `json:"t"`
	Sources  map[string]sourceTick `json:"sources"`
	Pipeline pipelineMetrics       `json:"pipeline"`
	Mixer    mixerMetrics          `json:"mixer"`
	Runtime  runtimeMetrics        `json:"runtime"`
}

type finalEvent struct {
	Phase      string          `json:"phase"`
	DurationS  float64         `json:"duration_s"`
	Samples    int             `json:"samples"`
	Sources    sourceSummary   `json:"sources"`
	Pipeline   pipelineSummary `json:"pipeline"`
	Mixer      mixerSummary    `json:"mixer"`
	Runtime    runtimeSummary  `json:"runtime"`
	SourceKeys []string        `json:"source_keys"`
	Config     configDump      `json:"config"`
}

type pipelineMetrics struct {
	LastNs             int64 `json:"last_ns"`
	E2ELastNs          int64 `json:"e2e_last_ns"`
	DeadlineViolations int64 `json:"deadline_violations"`
	QueueLen           int   `json:"queue_len"`
	ProcDropped        int64 `json:"proc_dropped"`
}

type mixerMetrics struct {
	MaxInterFrameGapNs int64   `json:"max_inter_frame_gap_ns"`
	SlippageMs         float64 `json:"slippage_ms"`
	FramesOutput       int64   `json:"frames_output"`
	MixCycleLastNs     int64   `json:"mix_cycle_last_ns"`
}

type runtimeMetrics struct {
	GCPauseLastNs uint64 `json:"gc_pause_last_ns"`
	Goroutines    int    `json:"goroutines"`
}

type sourceSummary struct {
	MinIngestFPS  float64 `json:"min_ingest_fps"`
	MeanIngestFPS float64 `json:"mean_ingest_fps"`
	TotalFrames   int64   `json:"total_raw_frames"`
}

type pipelineSummary struct {
	P50Ns                   int64 `json:"p50_ns"`
	P95Ns                   int64 `json:"p95_ns"`
	P99Ns                   int64 `json:"p99_ns"`
	MaxNs                   int64 `json:"max_ns"`
	TotalDeadlineViolations int64 `json:"total_deadline_violations"`
	TotalProcDropped        int64 `json:"total_proc_dropped"`
	MaxQueueLen             int   `json:"max_queue_len"`
}

type mixerSummary struct {
	MaxSlippageMs     float64 `json:"max_slippage_ms"`
	MeanSlippageMs    float64 `json:"mean_slippage_ms"`
	TotalFramesOutput int64   `json:"total_frames_output"`
}

type runtimeSummary struct {
	GCPauseP99Ns  uint64 `json:"gc_pause_p99_ns"`
	MaxGoroutines int    `json:"max_goroutines"`
}

type configDump struct {
	Sources        int     `json:"sources"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	FPS            float64 `json:"fps"`
	DurationS      float64 `json:"duration_s"`
	WarmupS        float64 `json:"warmup_s"`
	TickIntervalMs int64   `json:"tick_interval_ms"`
	APITokenOK     bool    `json:"api_token_set"`
	Platform       string  `json:"platform"`
	GoVersion      string  `json:"go_version"`
	RaceEnabled    bool    `json:"race_enabled"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

func run(argv []string, stdout, stderr *os.File) error {
	cfg, err := parseFlags(argv, stderr)
	if err != nil {
		return err
	}
	return runWithConfig(cfg, stdout, stderr)
}

func parseFlags(argv []string, stderr *os.File) (config, error) {
	fs := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg config
	var resolution string
	fs.IntVar(&cfg.sources, "sources", 4, "number of synthetic sources to drive (1..256)")
	fs.StringVar(&resolution, "resolution", "1920x1080", "video resolution WxH")
	fs.Float64Var(&cfg.fps, "fps", 60, "video frames per second")
	fs.DurationVar(&cfg.duration, "duration", 60*time.Second, "total run time including warmup")
	fs.DurationVar(&cfg.warmup, "warmup", 10*time.Second, "warmup window excluded from final summary")
	fs.DurationVar(&cfg.tickInterval, "tick-interval", time.Second, "metric emission interval")
	fs.StringVar(&cfg.output, "output", "json", "output format: json (NDJSON) — csv reserved")
	fs.StringVar(&cfg.profile, "profile", "none", "profile: cpu, mem, both, none")
	fs.StringVar(&cfg.apiToken, "api-token", os.Getenv("SWITCHFRAME_API_TOKEN"), "API token for future remote-driver mode (env: SWITCHFRAME_API_TOKEN)")
	fs.StringVar(&cfg.outputPath, "output-file", "", "optional file to write NDJSON to (stdout if empty)")

	if err := fs.Parse(argv); err != nil {
		return cfg, err
	}

	w, h, err := parseResolution(resolution)
	if err != nil {
		return cfg, err
	}
	cfg.width, cfg.height = w, h

	if cfg.sources < 1 {
		return cfg, errors.New("--sources must be >= 1")
	}
	if cfg.sources > maxSources {
		return cfg, fmt.Errorf("--sources must be <= %d", maxSources)
	}
	if cfg.fps <= 0 {
		return cfg, errors.New("--fps must be > 0")
	}
	if cfg.tickInterval < 10*time.Millisecond {
		return cfg, errors.New("--tick-interval must be >= 10ms")
	}
	if cfg.duration <= cfg.warmup {
		return cfg, fmt.Errorf("--duration (%s) must be greater than --warmup (%s)", cfg.duration, cfg.warmup)
	}
	switch cfg.output {
	case "json":
	case "csv":
		return cfg, fmt.Errorf("--output %q is reserved but not yet wired; use json", cfg.output)
	default:
		return cfg, fmt.Errorf("--output must be one of: json, csv (got %q)", cfg.output)
	}
	switch cfg.profile {
	case "none", "cpu", "mem", "both":
	default:
		return cfg, fmt.Errorf("--profile must be one of: none, cpu, mem, both (got %q)", cfg.profile)
	}
	if (cfg.profile == "mem" || cfg.profile == "both") && cfg.sources > 16 {
		fmt.Fprintf(stderr, "loadtest: warning: --profile %s with --sources %d adds significant heap pressure\n", cfg.profile, cfg.sources)
	}
	return cfg, nil
}

func parseResolution(s string) (int, int, error) {
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("--resolution must be WxH (got %q)", s)
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("resolution width %q: %w", parts[0], err)
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("resolution height %q: %w", parts[1], err)
	}
	if w <= 0 || h <= 0 || w%6 != 0 || h%2 != 0 {
		return 0, 0, fmt.Errorf("resolution %dx%d invalid: width must be positive multiple of 6, height must be positive and even", w, h)
	}
	return w, h, nil
}

func runWithConfig(cfg config, stdout, stderr *os.File) error {
	if cfg.profile == "cpu" || cfg.profile == "both" {
		f, err := os.Create("/tmp/loadtest-cpu.prof")
		if err != nil {
			return fmt.Errorf("cpu profile: %w", err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			return fmt.Errorf("cpu profile start: %w", err)
		}
		defer pprof.StopCPUProfile()
	}

	var enc *json.Encoder
	var outFile *os.File
	if cfg.outputPath != "" {
		f, err := os.Create(cfg.outputPath)
		if err != nil {
			return fmt.Errorf("output file: %w", err)
		}
		outFile = f
		enc = json.NewEncoder(f)
	} else {
		enc = json.NewEncoder(stdout)
	}
	defer func() {
		if outFile != nil {
			_ = outFile.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	engine, err := buildEngine(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.close()

	final, err := runLoop(ctx, cfg, engine, enc)
	if err != nil {
		return err
	}

	if cfg.profile == "mem" || cfg.profile == "both" {
		f, err := os.Create("/tmp/loadtest-mem.prof")
		if err != nil {
			return fmt.Errorf("mem profile: %w", err)
		}
		defer f.Close()
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			return fmt.Errorf("mem profile write: %w", err)
		}
	}

	if err := enc.Encode(final); err != nil {
		return fmt.Errorf("encode final: %w", err)
	}
	return nil
}

type engineHandle struct {
	sw        *switcher.Switcher
	mixer     *audio.Mixer
	swAdapter *perf.SwitcherAdapter
	mxAdapter *perf.MixerAdapter
	sources   []*mxl.Source
	keys      []string
}

func (e *engineHandle) close() {
	// Ordering is load-bearing: sources first so no further IngestRawVideo
	// can race switcher.Close (which closes the videoProcCh). Mixer after
	// sources so pending IngestPCM drains.
	for _, src := range e.sources {
		src.Stop()
	}
	if e.sw != nil {
		e.sw.Close()
	}
	if e.mixer != nil {
		_ = e.mixer.Close()
	}
}

func buildEngine(ctx context.Context, cfg config) (*engineHandle, error) {
	relay := distribution.NewRelay()
	sw := switcher.New(relay)

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate:     48000,
		Channels:       2,
		EncoderFactory: noopAudioEncoderFactory,
	})
	sw.SetMixer(mixer)
	sw.SetFrameSync(true, 0)

	sw.SetPipelineCodecs(mockVideoEncoderFactory)
	if err := sw.BuildPipeline(); err != nil {
		sw.Close()
		_ = mixer.Close()
		return nil, fmt.Errorf("build pipeline: %w", err)
	}

	pf := sw.PipelineFormat()
	h := &engineHandle{sw: sw, mixer: mixer}

	for i := 0; i < cfg.sources; i++ {
		key := fmt.Sprintf("mxl:loadtest%d", i)
		sw.RegisterMXLSource(key)
		mixer.AddChannel(key)

		videoReader := mxl.NewDemoVideoReader(cfg.width, cfg.height, cfg.fps, i)
		audioReader := mxl.NewDemoAudioReader(48000, 2)

		src := mxl.NewSource(mxl.SourceConfig{
			FlowName:   key,
			Width:      cfg.width,
			Height:     cfg.height,
			FPSNum:     pf.FPSNum,
			FPSDen:     pf.FPSDen,
			SampleRate: 48000,
			Channels:   2,
			OnRawVideo: func(sourceKey string, yuv []byte, w, hh int, pts int64) {
				frame := &switcher.ProcessingFrame{
					Data:   yuv,
					Width:  w,
					Height: hh,
					PTS:    pts,
					DTS:    pts,
					Codec:  "h264",
				}
				sw.IngestRawVideo(sourceKey, frame)
			},
			OnRawAudio: func(sourceKey string, pcm []float32, pts int64, channels int) {
				mixer.IngestPCM(sourceKey, pcm, pts, channels)
			},
		})
		src.Start(ctx, videoReader, audioReader)
		h.sources = append(h.sources, src)
		h.keys = append(h.keys, key)
	}

	if len(h.keys) > 0 {
		if err := sw.Cut(ctx, h.keys[0]); err != nil {
			h.close()
			return nil, fmt.Errorf("cut to first source: %w", err)
		}
	}

	h.swAdapter = &perf.SwitcherAdapter{SW: sw}
	h.mxAdapter = &perf.MixerAdapter{Mixer: mixer}
	return h, nil
}

type ingestTracker struct {
	lastCount int64
	lastTime  time.Time
}

func runLoop(ctx context.Context, cfg config, engine *engineHandle, enc *json.Encoder) (*finalEvent, error) {
	deadline := time.Now().Add(cfg.duration)
	warmupEnd := time.Now().Add(cfg.warmup)

	var measureSamples []tickEvent
	var maxGoroutines int
	var gcPauses []uint64
	var perSourceLast = make(map[string]int64)

	trackers := make(map[string]*ingestTracker, len(engine.keys))

	ticker := time.NewTicker(cfg.tickInterval)
	defer ticker.Stop()

	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			return summarize(cfg, engine, measureSamples, summarizeAgg{
				maxGoroutines: maxGoroutines, gcPauses: gcPauses,
				perSourceLast: perSourceLast, start: start,
			}), nil
		case now := <-ticker.C:
			if now.After(deadline) {
				return summarize(cfg, engine, measureSamples, summarizeAgg{
					maxGoroutines: maxGoroutines, gcPauses: gcPauses,
					perSourceLast: perSourceLast, start: start,
				}), nil
			}

			phase := "measure"
			if now.Before(warmupEnd) {
				phase = "warmup"
			}

			swSample := engine.swAdapter.PerfSample()
			mxSample := engine.mxAdapter.PerfSample()

			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			latestPauseIdx := (mem.NumGC + 255) % 256
			gcPauseLast := mem.PauseNs[latestPauseIdx]
			numG := runtime.NumGoroutine()

			sources := make(map[string]sourceTick, len(engine.keys))
			for _, key := range engine.keys {
				src := swSample.Sources[key]
				tr, ok := trackers[key]
				if !ok {
					tr = &ingestTracker{}
					trackers[key] = tr
				}
				var fps float64
				if !tr.lastTime.IsZero() {
					dt := now.Sub(tr.lastTime).Seconds()
					if dt > 0 {
						fps = float64(src.RawFrameCount-tr.lastCount) / dt
					}
				}
				tr.lastCount = src.RawFrameCount
				tr.lastTime = now
				sources[key] = sourceTick{
					Health:        src.Health,
					RawFrameCount: src.RawFrameCount,
					IngestFPS:     fps,
				}
				perSourceLast[key] = src.RawFrameCount
			}

			// Only compute slippage once the mixer has actually emitted frames.
			// Before the first recordAndOutput the gap field is 0 and subtracting
			// the expected cadence yields a phantom 21.33 ms.
			var slippageMs float64
			if mxSample.FramesOutput > 1 && mxSample.MaxInterFrameGapNs > 0 {
				slippageMs = math.Abs(float64(mxSample.MaxInterFrameGapNs-audioTickCadenceNs)) / 1e6
			}

			ev := tickEvent{
				Phase:   phase,
				T:       now.Sub(start).Seconds(),
				Sources: sources,
				Pipeline: pipelineMetrics{
					LastNs:             swSample.PipelineLastNs,
					E2ELastNs:          swSample.E2ELastNs,
					DeadlineViolations: swSample.DeadlineViolations,
					QueueLen:           swSample.QueueLen,
					ProcDropped:        swSample.ProcDropped,
				},
				Mixer: mixerMetrics{
					MaxInterFrameGapNs: mxSample.MaxInterFrameGapNs,
					SlippageMs:         slippageMs,
					FramesOutput:       mxSample.FramesOutput,
					MixCycleLastNs:     mxSample.MixCycleLastNs,
				},
				Runtime: runtimeMetrics{
					GCPauseLastNs: gcPauseLast,
					Goroutines:    numG,
				},
			}
			if err := enc.Encode(ev); err != nil {
				return nil, fmt.Errorf("encode tick: %w", err)
			}

			if phase == "measure" {
				measureSamples = append(measureSamples, ev)
				if numG > maxGoroutines {
					maxGoroutines = numG
				}
				if gcPauseLast > 0 {
					gcPauses = append(gcPauses, gcPauseLast)
				}
			}
		}
	}
}

type summarizeAgg struct {
	maxGoroutines int
	gcPauses      []uint64
	perSourceLast map[string]int64
	start         time.Time
}

func summarize(cfg config, engine *engineHandle, samples []tickEvent, agg summarizeAgg) *finalEvent {
	var gcP99 uint64
	if len(agg.gcPauses) > 0 {
		sorted := append([]uint64(nil), agg.gcPauses...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		idx := int(float64(len(sorted)-1) * 0.99)
		gcP99 = sorted[idx]
	}

	var totalFrames int64
	minFPS, meanFPS, haveFPS := aggregateIngestFPS(samples)
	for _, c := range agg.perSourceLast {
		totalFrames += c
	}
	if !haveFPS {
		minFPS, meanFPS = 0, 0
	}

	pipeStats := aggregatePipeline(samples)
	mixStats := aggregateMixer(samples)

	return &finalEvent{
		Phase:     "final",
		DurationS: time.Since(agg.start).Seconds(),
		Samples:   len(samples),
		Sources: sourceSummary{
			MinIngestFPS:  minFPS,
			MeanIngestFPS: meanFPS,
			TotalFrames:   totalFrames,
		},
		Pipeline: pipeStats,
		Mixer:    mixStats,
		Runtime: runtimeSummary{
			GCPauseP99Ns:  gcP99,
			MaxGoroutines: agg.maxGoroutines,
		},
		SourceKeys: engine.keys,
		Config: configDump{
			Sources:        cfg.sources,
			Width:          cfg.width,
			Height:         cfg.height,
			FPS:            cfg.fps,
			DurationS:      cfg.duration.Seconds(),
			WarmupS:        cfg.warmup.Seconds(),
			TickIntervalMs: cfg.tickInterval.Milliseconds(),
			APITokenOK:     cfg.apiToken != "",
			Platform:       runtime.GOOS + "/" + runtime.GOARCH,
			GoVersion:      runtime.Version(),
			RaceEnabled:    raceEnabled,
		},
	}
}

func aggregateIngestFPS(samples []tickEvent) (minFPS, meanFPS float64, have bool) {
	var sum float64
	var count int
	minFPS = 0
	first := true
	for _, s := range samples {
		for _, src := range s.Sources {
			if src.IngestFPS <= 0 {
				continue
			}
			sum += src.IngestFPS
			count++
			if first || src.IngestFPS < minFPS {
				minFPS = src.IngestFPS
				first = false
			}
		}
	}
	if count == 0 {
		return 0, 0, false
	}
	return minFPS, sum / float64(count), true
}

func aggregatePipeline(samples []tickEvent) pipelineSummary {
	// DeadlineViolations and ProcDropped are monotonic cumulative counters
	// on the Switcher; the last measure-phase sample holds the running
	// count. The difference between the first and last measure-phase
	// values is the count accrued during the measure window (warmup
	// accrual is excluded).
	lats := make([]int64, 0, len(samples))
	var maxQueue int
	var firstDL, firstDrop int64
	var lastDL, lastDrop int64
	haveFirst := false
	for _, s := range samples {
		if s.Pipeline.LastNs > 0 {
			lats = append(lats, s.Pipeline.LastNs)
		}
		if s.Pipeline.QueueLen > maxQueue {
			maxQueue = s.Pipeline.QueueLen
		}
		if !haveFirst {
			firstDL = s.Pipeline.DeadlineViolations
			firstDrop = s.Pipeline.ProcDropped
			haveFirst = true
		}
		lastDL = s.Pipeline.DeadlineViolations
		lastDrop = s.Pipeline.ProcDropped
	}
	p50, p95, p99, max := percentiles(lats)
	return pipelineSummary{
		P50Ns:                   p50,
		P95Ns:                   p95,
		P99Ns:                   p99,
		MaxNs:                   max,
		TotalDeadlineViolations: lastDL - firstDL,
		TotalProcDropped:        lastDrop - firstDrop,
		MaxQueueLen:             maxQueue,
	}
}

func aggregateMixer(samples []tickEvent) mixerSummary {
	var maxSlip, sumSlip float64
	var count int
	var framesOut int64
	for _, s := range samples {
		if s.Mixer.SlippageMs > 0 {
			sumSlip += s.Mixer.SlippageMs
			count++
			if s.Mixer.SlippageMs > maxSlip {
				maxSlip = s.Mixer.SlippageMs
			}
		}
		if s.Mixer.FramesOutput > framesOut {
			framesOut = s.Mixer.FramesOutput
		}
	}
	var meanSlip float64
	if count > 0 {
		meanSlip = sumSlip / float64(count)
	}
	return mixerSummary{
		MaxSlippageMs:     maxSlip,
		MeanSlippageMs:    meanSlip,
		TotalFramesOutput: framesOut,
	}
}

func percentiles(vs []int64) (p50, p95, p99, max int64) {
	if len(vs) == 0 {
		return 0, 0, 0, 0
	}
	sorted := append([]int64(nil), vs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := func(q float64) int64 {
		i := int(float64(len(sorted)-1) * q)
		return sorted[i]
	}
	return idx(0.5), idx(0.95), idx(0.99), sorted[len(sorted)-1]
}
