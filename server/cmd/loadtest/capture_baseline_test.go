package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCaptureBaseline is an opt-in helper that writes a fresh baseline
// under the same test-runtime conditions the pinned regression test
// runs under (race detector state matters — see the baseline schema's
// race_enabled field). Invoke manually:
//
//	cd server
//	LOADTEST_CAPTURE_BASELINE=1 go test -race -count=1 -run TestCaptureBaseline ./cmd/loadtest -v
//
// The final event is round-tripped through finalEvent so any schema
// rename in main.go that drops a field will fail this test loudly,
// not silently produce a stale baseline.
func TestCaptureBaseline(t *testing.T) {
	if os.Getenv("LOADTEST_CAPTURE_BASELINE") != "1" {
		t.Skip("set LOADTEST_CAPTURE_BASELINE=1 to capture")
	}
	dir := t.TempDir()
	outPath := filepath.Join(dir, "run.ndjson")
	cfg := config{
		sources: 32, width: 1920, height: 1080, fps: 60,
		duration: 15 * time.Second, warmup: 5 * time.Second, tickInterval: 1 * time.Second,
		output: "json", profile: "none", outputPath: outPath,
	}
	stderrFile, err := os.CreateTemp(dir, "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer stderrFile.Close()
	if err := runWithConfig(cfg, stderrFile, stderrFile); err != nil {
		_, _ = stderrFile.Seek(0, 0)
		body, _ := io.ReadAll(stderrFile)
		t.Fatalf("run: %v\n%s", err, body)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	final, err := lastFinalEvent(data)
	if err != nil {
		t.Fatalf("find final: %v", err)
	}
	pretty, err := json.MarshalIndent(final, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("testdata/loadtest_baseline.json", pretty, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("baseline written: testdata/loadtest_baseline.json (p99_ns=%d, platform=%s, race=%v)",
		final.Pipeline.P99Ns, final.Config.Platform, final.Config.RaceEnabled)
}

// lastFinalEvent scans NDJSON and returns the last phase:"final" object,
// decoded into a finalEvent so the caller catches schema drift.
func lastFinalEvent(data []byte) (finalEvent, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var last finalEvent
	var found bool
	for sc.Scan() {
		line := sc.Bytes()
		var hdr struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal(line, &hdr); err != nil {
			continue
		}
		if hdr.Phase != "final" {
			continue
		}
		if err := json.Unmarshal(line, &last); err != nil {
			return finalEvent{}, err
		}
		found = true
	}
	if err := sc.Err(); err != nil {
		return finalEvent{}, err
	}
	if !found {
		return finalEvent{}, io.ErrUnexpectedEOF
	}
	return last, nil
}
