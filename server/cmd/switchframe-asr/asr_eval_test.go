//go:build whisper

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/asr"
	"github.com/zsiec/switchframe/server/stinger"
)

// TestASREvalFixtures runs the streaming whisper pipeline against a
// directory of synthetic WAV fixtures and reports per-fixture and
// aggregate accuracy + latency metrics. It is gated on:
//
//   - the `whisper` build tag (so it never runs in default `go test`)
//   - the ASR_EVAL_FIXTURES_DIR env var (skipped if unset)
//
// Run:
//
//	ASR_EVAL_FIXTURES_DIR=test/asr-fixtures \
//	  go test -tags whisper -v -timeout 5m -run TestASREvalFixtures \
//	    ./cmd/switchframe-asr
//
// Optional env: ASR_EVAL_MODELS_DIR (default /opt/whisper.cpp/models),
// ASR_EVAL_MODEL (default = backend's auto-pick), ASR_EVAL_THREADS (0=auto).
func TestASREvalFixtures(t *testing.T) {
	fixturesDir := os.Getenv("ASR_EVAL_FIXTURES_DIR")
	if fixturesDir == "" {
		t.Skip("ASR_EVAL_FIXTURES_DIR not set; eval harness skipped")
	}
	modelsDir := envOr("ASR_EVAL_MODELS_DIR", "/opt/whisper.cpp/models")
	modelName := os.Getenv("ASR_EVAL_MODEL")
	threads := atoiOr(os.Getenv("ASR_EVAL_THREADS"), 0)

	fixtures, err := discoverEvalFixtures(fixturesDir)
	if err != nil {
		t.Fatalf("discover fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatalf("no fixtures found in %s; run scripts/asr-eval/gen-fixtures.sh first", fixturesDir)
	}

	threadCount := resolveWhisperThreads(threads, runtime.NumCPU())
	backend, err := newWhisperASRBackend(modelsDir, true, threadCount)
	if err != nil {
		t.Fatalf("load whisper backend: %v", err)
	}
	defer backend.Close()
	if modelName != "" {
		if err := backend.SetModel(modelName); err != nil {
			t.Fatalf("load model %q: %v", modelName, err)
		}
	}

	t.Logf("Backend: %s, model: %s, threads: %d", backend.Name(), backend.ModelName(), threadCount)
	t.Logf("Fixtures: %d in %s", len(fixtures), fixturesDir)
	t.Logf("")

	var results []evalResult
	for _, f := range fixtures {
		r, err := runEvalFixture(backend, f)
		if err != nil {
			t.Errorf("  %s: ERROR: %v", f.name, err)
			continue
		}
		results = append(results, r)
		t.Logf("%s", formatResult(r))
		if r.WER > 0.10 {
			t.Logf("    REF: %s", r.Reference)
			t.Logf("    HYP: %s", r.Hypothesis)
		}
	}

	t.Logf("")
	t.Logf("---- aggregate ----")
	for _, line := range formatSummary(results) {
		t.Logf("%s", line)
	}
}

const evalSampleRate = 16000

type evalFixture struct {
	name    string
	wavPath string
	refPath string
}

type evalResult struct {
	Fixture        string
	AudioDurMs     int
	InferDurMs     int
	WER            float64
	RefWords       int
	HypWords       int
	PartialCommits int
	NumInferences  int
	FirstWordMs    int
	AvgCommitLagMs float64
	Reference      string
	Hypothesis     string
}

func discoverEvalFixtures(dir string) ([]evalFixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read fixtures dir: %w", err)
	}
	wavs := map[string]string{}
	refs := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".wav"):
			base := strings.TrimSuffix(name, ".wav")
			wavs[base] = filepath.Join(dir, name)
		case strings.HasSuffix(name, ".ref.txt"):
			base := strings.TrimSuffix(name, ".ref.txt")
			refs[base] = filepath.Join(dir, name)
		}
	}
	var out []evalFixture
	for base, wavPath := range wavs {
		refPath, ok := refs[base]
		if !ok {
			continue
		}
		out = append(out, evalFixture{name: base, wavPath: wavPath, refPath: refPath})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func runEvalFixture(b *whisperASRBackend, f evalFixture) (evalResult, error) {
	wavBytes, err := os.ReadFile(f.wavPath)
	if err != nil {
		return evalResult{}, err
	}
	samples, sr, channels, err := stinger.ParseWAV(wavBytes)
	if err != nil {
		return evalResult{}, fmt.Errorf("parse wav: %w", err)
	}
	if sr != evalSampleRate {
		return evalResult{}, fmt.Errorf("expected %d Hz, got %d Hz", evalSampleRate, sr)
	}
	if channels != 1 {
		samples = downmixEval(samples, channels)
	}

	refBytes, err := os.ReadFile(f.refPath)
	if err != nil {
		return evalResult{}, err
	}
	ref := strings.TrimSpace(string(refBytes))

	// Reset prompt context so prior fixtures don't leak across.
	b.promptWords = b.promptWords[:0]

	const (
		stepSamples         = evalSampleRate * 1  // 1s step, mirrors main.go
		maxUtteranceSamples = evalSampleRate * 25 // 25s force flush
	)

	state := newStreamingState()
	var committed []asr.TranscriptWord
	var commitMs []int
	var inferTotal time.Duration
	nInf := 0
	partialCount := 0
	firstWordMs := -1

	recordCommits := func(words []asr.TranscriptWord, atMs int, partial bool) {
		if len(words) == 0 {
			return
		}
		for _, w := range words {
			if firstWordMs < 0 {
				firstWordMs = atMs
			}
			committed = append(committed, w)
			commitMs = append(commitMs, atMs)
			if partial {
				partialCount++
			}
		}
	}

	rollPromptOnUtteranceEnd := func() {
		texts := make([]string, 0)
		for _, w := range state.committedSoFar() {
			if w.Text != "" {
				texts = append(texts, w.Text)
			}
		}
		b.AppendPromptContext(texts)
	}

	// Streaming mode is selected via env. The eval supports both so we
	// can compare:
	//   ASR_EVAL_MODE=stream  (default) -- per-step LocalAgreement-2
	//   ASR_EVAL_MODE=batch            -- single Transcribe at utterance end
	mode := envOr("ASR_EVAL_MODE", "stream")

	utteranceStart := 0
	for utteranceStart < len(samples) {
		utteranceEnd := utteranceStart + maxUtteranceSamples
		if utteranceEnd > len(samples) {
			utteranceEnd = len(samples)
		}

		if mode == "stream" {
			// Per-step LA-2 commits.
			stepEnd := utteranceStart + stepSamples
			for stepEnd < utteranceEnd {
				t0 := time.Now()
				hyp := b.Transcribe(samples[utteranceStart:stepEnd])
				inferTotal += time.Since(t0)
				nInf++
				atMs := stepEnd * 1000 / evalSampleRate
				recordCommits(state.acceptHypothesis(hyp), atMs, true)
				stepEnd += stepSamples
			}
		}

		// Final pass for the utterance.
		t0 := time.Now()
		hyp := b.Transcribe(samples[utteranceStart:utteranceEnd])
		inferTotal += time.Since(t0)
		nInf++
		atMs := utteranceEnd * 1000 / evalSampleRate
		recordCommits(state.flushFinal(hyp), atMs, false)
		rollPromptOnUtteranceEnd()

		state.reset()
		utteranceStart = utteranceEnd
	}
	finalMs := len(samples) * 1000 / evalSampleRate

	parts := make([]string, len(committed))
	for i, w := range committed {
		parts[i] = w.Text
	}
	hypText := strings.Join(parts, " ")

	avgLag := computeAvgLag(commitMs, finalMs, len(normalizeWords(ref)))

	return evalResult{
		Fixture:        f.name,
		AudioDurMs:     finalMs,
		InferDurMs:     int(inferTotal.Milliseconds()),
		WER:            wer(ref, hypText),
		RefWords:       len(normalizeWords(ref)),
		HypWords:       len(committed),
		PartialCommits: partialCount,
		NumInferences:  nInf,
		FirstWordMs:    firstWordMs,
		AvgCommitLagMs: avgLag,
		Reference:      ref,
		Hypothesis:     hypText,
	}, nil
}

func computeAvgLag(commitMs []int, audioDurMs, refWords int) float64 {
	if len(commitMs) == 0 || audioDurMs == 0 || refWords == 0 {
		return 0
	}
	n := len(commitMs)
	if refWords < n {
		n = refWords
	}
	total := 0
	for i := 0; i < n; i++ {
		expected := (i + 1) * audioDurMs / refWords
		total += commitMs[i] - expected
	}
	return float64(total) / float64(n)
}

func downmixEval(samples []float32, channels int) []float32 {
	if channels < 2 {
		return samples
	}
	frames := len(samples) / channels
	out := make([]float32, frames)
	scale := 1.0 / float32(channels)
	for i := 0; i < frames; i++ {
		var sum float32
		for c := 0; c < channels; c++ {
			sum += samples[i*channels+c]
		}
		out[i] = sum * scale
	}
	return out
}

func formatResult(r evalResult) string {
	rtFactor := 0.0
	if r.AudioDurMs > 0 {
		rtFactor = float64(r.InferDurMs) / float64(r.AudioDurMs)
	}
	partialPct := 0.0
	if r.HypWords > 0 {
		partialPct = float64(r.PartialCommits) * 100 / float64(r.HypWords)
	}
	return fmt.Sprintf("  %-14s  WER=%5.1f%%  audio=%5dms  infer=%5dms (%.2fx RT)  inferences=%d  words=%d/%d  partial=%4.0f%%  first=%dms  lag=%+5.0fms",
		r.Fixture,
		r.WER*100,
		r.AudioDurMs,
		r.InferDurMs,
		rtFactor,
		r.NumInferences,
		r.HypWords,
		r.RefWords,
		partialPct,
		r.FirstWordMs,
		r.AvgCommitLagMs,
	)
}

func formatSummary(rs []evalResult) []string {
	if len(rs) == 0 {
		return nil
	}
	var totalRefWords, totalErrors int
	var totalAudio, totalInfer int
	var lagSum float64
	var lagCount int
	for _, r := range rs {
		totalRefWords += r.RefWords
		totalErrors += int(r.WER * float64(r.RefWords))
		totalAudio += r.AudioDurMs
		totalInfer += r.InferDurMs
		if r.RefWords > 0 {
			lagSum += r.AvgCommitLagMs
			lagCount++
		}
	}
	corpusWER := 0.0
	if totalRefWords > 0 {
		corpusWER = float64(totalErrors) / float64(totalRefWords)
	}
	rtFactor := 0.0
	if totalAudio > 0 {
		rtFactor = float64(totalInfer) / float64(totalAudio)
	}
	avgLag := 0.0
	if lagCount > 0 {
		avgLag = lagSum / float64(lagCount)
	}
	return []string{
		fmt.Sprintf("  fixtures: %d   total ref words: %d   corpus WER: %.1f%%", len(rs), totalRefWords, corpusWER*100),
		fmt.Sprintf("  audio: %dms   infer: %dms   throughput: %.2fx realtime", totalAudio, totalInfer, rtFactor),
		fmt.Sprintf("  mean per-fixture commit lag: %+.0fms (negative = ahead of speaker)", avgLag),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}
