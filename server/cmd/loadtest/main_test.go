package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadtest_SmokeRunLowSources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke run in -short")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "ndjson.log")

	cfg := config{
		sources:      2,
		width:        360,
		height:       240,
		fps:          30,
		duration:     2 * time.Second,
		warmup:       500 * time.Millisecond,
		tickInterval: 100 * time.Millisecond,
		output:       "json",
		profile:      "none",
		outputPath:   outPath,
	}

	stderrFile, err := os.CreateTemp(dir, "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer stderrFile.Close()

	if err := runWithConfig(cfg, stderrFile, stderrFile); err != nil {
		_, _ = stderrFile.Seek(0, 0)
		body, _ := io.ReadAll(stderrFile)
		t.Fatalf("runWithConfig: %v\nstderr:\n%s", err, body)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected >=3 NDJSON lines, got %d: %q", len(lines), string(data))
	}

	var sawWarmup, sawMeasure, sawFinal bool
	var finalLine string
	var lastTick tickEvent
	for _, line := range lines {
		var hdr struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(line), &hdr); err != nil {
			t.Fatalf("parse ndjson: %v\nline: %s", err, line)
		}
		switch hdr.Phase {
		case "warmup":
			sawWarmup = true
		case "measure":
			sawMeasure = true
			var ev tickEvent
			if err := json.Unmarshal([]byte(line), &ev); err == nil {
				lastTick = ev
			}
		case "final":
			if sawFinal {
				t.Fatalf("multiple final events")
			}
			sawFinal = true
			finalLine = line
		}
	}

	if !sawWarmup {
		t.Error("no warmup-phase lines emitted")
	}
	if !sawMeasure {
		t.Error("no measure-phase lines emitted")
	}
	if !sawFinal {
		t.Fatal("no final event emitted")
	}

	if len(lastTick.Sources) < cfg.sources {
		t.Fatalf("last measure tick has %d sources, want >= %d", len(lastTick.Sources), cfg.sources)
	}
	for _, key := range []string{"mxl:loadtest0", "mxl:loadtest1"} {
		src, ok := lastTick.Sources[key]
		if !ok {
			t.Errorf("missing source %q in tick", key)
			continue
		}
		if src.RawFrameCount == 0 {
			t.Errorf("source %q RawFrameCount = 0 — ingest path not driving frames", key)
		}
	}

	var final finalEvent
	if err := json.Unmarshal([]byte(finalLine), &final); err != nil {
		t.Fatalf("parse final: %v", err)
	}
	if final.Samples < 1 {
		t.Errorf("final.samples = %d, want >= 1", final.Samples)
	}
	if len(final.SourceKeys) != cfg.sources {
		t.Errorf("final.source_keys = %v, want len %d", final.SourceKeys, cfg.sources)
	}
	if final.Config.Sources != cfg.sources {
		t.Errorf("final.config.sources = %d, want %d", final.Config.Sources, cfg.sources)
	}
	if final.DurationS < 1.5 || final.DurationS > 5.0 {
		t.Errorf("final.duration_s = %.2f, want ~2.0", final.DurationS)
	}
	if final.Sources.TotalFrames == 0 {
		t.Error("final.sources.total_raw_frames = 0 — no ingest at all")
	}
	if final.Pipeline.P99Ns == 0 {
		t.Error("final.pipeline.p99_ns = 0 — pipeline not exercised (encoder factory missing?)")
	}
	if final.Mixer.TotalFramesOutput == 0 {
		t.Error("final.mixer.total_frames_output = 0 — mixer tick not emitting (encoder factory missing?)")
	}
}

func TestParseFlags_ResolutionValidation(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"1920x1080", false},
		{"360x240", false},
		{"1921x1080", true},   // width not multiple of 6
		{"1920x1081", true},   // odd height
		{"bad", true},
		{"1920", true},
		{"0x0", true},
		{"19a20x1080", true},  // trailing garbage in width
		{"1920xabc", true},    // non-numeric height
		{"-6x8", true},        // negative values
	}
	for _, tc := range cases {
		_, _, err := parseResolution(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseResolution(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
	}
}

func TestParseFlags_ProfileAndOutput(t *testing.T) {
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	cases := []struct {
		name    string
		argv    []string
		wantErr bool
	}{
		{"defaults", []string{"--duration", "2s", "--warmup", "1s"}, false},
		{"bad_profile", []string{"--profile", "xxx"}, true},
		{"csv_reserved", []string{"--output", "csv"}, true},
		{"bad_output", []string{"--output", "zebra"}, true},
		{"duration_le_warmup", []string{"--duration", "5s", "--warmup", "10s"}, true},
		{"sources_too_high", []string{"--sources", "10000"}, true},
		{"sources_zero", []string{"--sources", "0"}, true},
		{"tick_too_small", []string{"--tick-interval", "1ms"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFlags(tc.argv, stderr)
			if (err != nil) != tc.wantErr {
				t.Errorf("parseFlags(%v) err=%v, wantErr=%v", tc.argv, err, tc.wantErr)
			}
		})
	}
}
