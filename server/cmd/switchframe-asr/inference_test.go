package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/zsiec/switchframe/server/asr"
)

// testVocab returns a simple SentencePiece-style vocab for testing.
// Index 0 = blank, 1 = "▁hello", 2 = "▁world", 3 = "▁break", 4 = "ing", 5 = "▁news"
func testVocab() []string {
	return []string{
		"<blank>",  // 0
		"▁hello",   // 1
		"▁world",   // 2
		"▁break",   // 3
		"ing",      // 4
		"▁news",    // 5
		"▁the",     // 6
		"▁a",       // 7
	}
}

func TestTokenAssemblerBasic(t *testing.T) {
	vocab := testVocab()
	ta := newTokenAssembler(vocab, 0, 80) // blank=0, 80ms per frame

	// Feed "▁hello" token, then "▁world" token.
	// When ▁world arrives, ▁hello should be emitted.
	tokens := []decodedToken{
		{id: 1, prob: 0.9, framePos: 0},  // ▁hello
		{id: 2, prob: 0.85, framePos: 1}, // ▁world
	}

	words := ta.feed(tokens)

	// "hello" should be emitted when "world" starts.
	if len(words) != 1 {
		t.Fatalf("expected 1 word, got %d", len(words))
	}
	if words[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", words[0].Text)
	}

	// Flush to get "world".
	remaining := ta.flush()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 word from flush, got %d", len(remaining))
	}
	if remaining[0].Text != "world" {
		t.Errorf("expected 'world', got %q", remaining[0].Text)
	}
}

func TestTokenAssemblerSubwords(t *testing.T) {
	vocab := testVocab()
	ta := newTokenAssembler(vocab, 0, 80)

	// Feed "▁break" then "ing" then "▁news".
	// "ing" doesn't start with ▁, so it's part of the same word as "▁break".
	// When "▁news" arrives, "breaking" should be emitted.
	tokens := []decodedToken{
		{id: 3, prob: 0.95, framePos: 0}, // ▁break
		{id: 4, prob: 0.90, framePos: 1}, // ing
		{id: 5, prob: 0.88, framePos: 2}, // ▁news
	}

	words := ta.feed(tokens)

	if len(words) != 1 {
		t.Fatalf("expected 1 word, got %d", len(words))
	}
	if words[0].Text != "breaking" {
		t.Errorf("expected 'breaking', got %q", words[0].Text)
	}

	// Flush to get "news".
	remaining := ta.flush()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 word from flush, got %d", len(remaining))
	}
	if remaining[0].Text != "news" {
		t.Errorf("expected 'news', got %q", remaining[0].Text)
	}
}

func TestTokenAssemblerBlankSkip(t *testing.T) {
	vocab := testVocab()
	ta := newTokenAssembler(vocab, 0, 80)

	// Feed blank, ▁hello, blank, blank, ▁world.
	// Blanks should be ignored.
	tokens := []decodedToken{
		{id: 0, prob: 0.99, framePos: 0},  // blank
		{id: 1, prob: 0.9, framePos: 1},   // ▁hello
		{id: 0, prob: 0.99, framePos: 2},  // blank
		{id: 0, prob: 0.99, framePos: 3},  // blank
		{id: 2, prob: 0.85, framePos: 4},  // ▁world
	}

	words := ta.feed(tokens)

	if len(words) != 1 {
		t.Fatalf("expected 1 word, got %d", len(words))
	}
	if words[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", words[0].Text)
	}

	remaining := ta.flush()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 word from flush, got %d", len(remaining))
	}
	if remaining[0].Text != "world" {
		t.Errorf("expected 'world', got %q", remaining[0].Text)
	}
}

func TestTokenAssemblerGapDetection(t *testing.T) {
	vocab := testVocab()
	ta := newTokenAssembler(vocab, 0, 80) // 80ms per frame

	// Feed ▁hello at frame 0, then ▁world at frame 20.
	// Gap should be (20 - 0) * 80 = 1600ms.
	tokens1 := []decodedToken{
		{id: 1, prob: 0.9, framePos: 0}, // ▁hello
	}
	ta.feed(tokens1) // no word emitted yet (nothing to trigger flush)

	tokens2 := []decodedToken{
		{id: 2, prob: 0.85, framePos: 20}, // ▁world (triggers "hello" emission)
	}
	words := ta.feed(tokens2)

	if len(words) != 1 {
		t.Fatalf("expected 1 word, got %d", len(words))
	}

	// First word "hello" should have GapBefore = 0 (no previous word).
	if words[0].GapBefore != 0 {
		t.Errorf("first word gap = %d, want 0", words[0].GapBefore)
	}

	// Now feed ▁news at frame 40 to emit "world" and check its gap.
	tokens3 := []decodedToken{
		{id: 5, prob: 0.88, framePos: 40}, // ▁news
	}
	words2 := ta.feed(tokens3)

	if len(words2) != 1 {
		t.Fatalf("expected 1 word, got %d", len(words2))
	}

	// "world" starts at frame 20, "hello" ended at frame 0.
	// Gap = (20 - 0) * 80 = 1600ms.
	if words2[0].GapBefore != 1600 {
		t.Errorf("gap = %d, want 1600", words2[0].GapBefore)
	}
}

func TestTokenAssemblerConfidence(t *testing.T) {
	vocab := testVocab()
	ta := newTokenAssembler(vocab, 0, 80)

	// Feed "▁break" (0.8) then "ing" (0.6), then flush.
	// Geometric mean = exp((ln(0.8) + ln(0.6)) / 2)
	tokens := []decodedToken{
		{id: 3, prob: 0.8, framePos: 0}, // ▁break
		{id: 4, prob: 0.6, framePos: 1}, // ing
	}
	ta.feed(tokens) // no word emitted yet (no next word boundary)

	words := ta.flush()
	if len(words) != 1 {
		t.Fatalf("expected 1 word, got %d", len(words))
	}

	// Geometric mean of 0.8 and 0.6:
	expected := float32(math.Exp((math.Log(0.8) + math.Log(0.6)) / 2.0))
	diff := math.Abs(float64(words[0].Confidence - expected))
	if diff > 0.001 {
		t.Errorf("confidence = %f, want %f (diff=%f)", words[0].Confidence, expected, diff)
	}
}

func TestTokenAssemblerFlushEmpty(t *testing.T) {
	vocab := testVocab()
	ta := newTokenAssembler(vocab, 0, 80)

	words := ta.flush()
	if words != nil {
		t.Errorf("expected nil from empty flush, got %v", words)
	}
}

func TestTokenAssemblerReset(t *testing.T) {
	vocab := testVocab()
	ta := newTokenAssembler(vocab, 0, 80)

	// Feed some tokens.
	tokens := []decodedToken{
		{id: 1, prob: 0.9, framePos: 5}, // ▁hello
	}
	ta.feed(tokens)

	// Reset should clear everything.
	ta.reset()

	// Flush should return nil.
	words := ta.flush()
	if words != nil {
		t.Errorf("expected nil after reset+flush, got %v", words)
	}

	// After reset, lastFrame should be -1 (no gap on next word).
	tokens2 := []decodedToken{
		{id: 2, prob: 0.85, framePos: 10}, // ▁world
	}
	ta.feed(tokens2)
	words2 := ta.flush()
	if len(words2) != 1 {
		t.Fatalf("expected 1 word after reset, got %d", len(words2))
	}
	if words2[0].GapBefore != 0 {
		t.Errorf("gap after reset = %d, want 0", words2[0].GapBefore)
	}
}

func TestLoadModelConfig(t *testing.T) {
	dir := t.TempDir()

	configJSON := `{
		"model": "parakeet-tdt-0.6b",
		"chunk_ms": 160,
		"sample_rate": 16000,
		"mel_bins": 80,
		"encoder_dim": 1024,
		"vocab_size": 1025,
		"num_duration_bins": 5,
		"blank_token_id": 1024,
		"subsampling_factor": 8
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadModelConfig(dir)
	if err != nil {
		t.Fatalf("loadModelConfig: %v", err)
	}

	if cfg.Model != "parakeet-tdt-0.6b" {
		t.Errorf("model = %q, want %q", cfg.Model, "parakeet-tdt-0.6b")
	}
	if cfg.ChunkMs != 160 {
		t.Errorf("chunk_ms = %d, want 160", cfg.ChunkMs)
	}
	if cfg.SampleRate != 16000 {
		t.Errorf("sample_rate = %d, want 16000", cfg.SampleRate)
	}
	if cfg.MelBins != 80 {
		t.Errorf("mel_bins = %d, want 80", cfg.MelBins)
	}
	if cfg.EncoderDim != 1024 {
		t.Errorf("encoder_dim = %d, want 1024", cfg.EncoderDim)
	}
	if cfg.VocabSize != 1025 {
		t.Errorf("vocab_size = %d, want 1025", cfg.VocabSize)
	}
	if cfg.NumDurationBins != 5 {
		t.Errorf("num_duration_bins = %d, want 5", cfg.NumDurationBins)
	}
	if cfg.BlankTokenID != 1024 {
		t.Errorf("blank_token_id = %d, want 1024", cfg.BlankTokenID)
	}
	if cfg.SubsamplingFactor != 8 {
		t.Errorf("subsampling_factor = %d, want 8", cfg.SubsamplingFactor)
	}
}

func TestLoadModelConfigMissing(t *testing.T) {
	dir := t.TempDir()

	_, err := loadModelConfig(dir)
	if err == nil {
		t.Error("expected error for missing config.json")
	}
}

func TestLoadModelConfigInvalid(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := loadModelConfig(dir)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadVocab(t *testing.T) {
	dir := t.TempDir()
	vocabPath := filepath.Join(dir, "vocab.txt")

	content := "<blank>\n▁hello\n▁world\n▁break\ning\n▁news\n"
	if err := os.WriteFile(vocabPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	vocab, err := loadVocab(vocabPath)
	if err != nil {
		t.Fatalf("loadVocab: %v", err)
	}

	if len(vocab) != 6 {
		t.Fatalf("expected 6 vocab entries, got %d", len(vocab))
	}
	if vocab[0] != "<blank>" {
		t.Errorf("vocab[0] = %q, want %q", vocab[0], "<blank>")
	}
	if vocab[1] != "▁hello" {
		t.Errorf("vocab[1] = %q, want %q", vocab[1], "▁hello")
	}
	if vocab[4] != "ing" {
		t.Errorf("vocab[4] = %q, want %q", vocab[4], "ing")
	}
}

func TestTokenAssemblerInvalidTokenID(t *testing.T) {
	vocab := testVocab()
	ta := newTokenAssembler(vocab, 0, 80)

	// Feed tokens with out-of-range IDs — they should be silently skipped.
	tokens := []decodedToken{
		{id: -1, prob: 0.9, framePos: 0},  // negative
		{id: 1, prob: 0.9, framePos: 1},   // ▁hello (valid)
		{id: 99, prob: 0.9, framePos: 2},  // out of range
	}
	ta.feed(tokens)

	words := ta.flush()
	if len(words) != 1 {
		t.Fatalf("expected 1 word, got %d", len(words))
	}
	if words[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", words[0].Text)
	}
}

// --- Mock backends for batch inference testing ---

// mockBackend implements inferenceBackend for testing batch inference.
type mockBackend struct {
	melResult     []float32
	melFrames     int
	melErr        error
	encoderResult []float32
	encodedFrames int
	encoderErr    error
	decoderLogits []float32
	decoderState1 []float32
	decoderState2 []float32
	decoderErr    error
	decoderCalls  int
	closed        bool
}

func (m *mockBackend) runMel(audio []float32) ([]float32, int, error) {
	return m.melResult, m.melFrames, m.melErr
}

func (m *mockBackend) runEncoder(melFeatures []float32, melFrames int) ([]float32, int, error) {
	return m.encoderResult, m.encodedFrames, m.encoderErr
}

func (m *mockBackend) runDecoder(encoderFrame []float32, targets []int32, states1, states2 []float32) ([]float32, []float32, []float32, error) {
	m.decoderCalls++
	return m.decoderLogits, m.decoderState1, m.decoderState2, m.decoderErr
}

func (m *mockBackend) gpuStream() unsafe.Pointer { return nil }

func (m *mockBackend) close() {
	m.closed = true
}

// makeTestEngine creates an inferenceEngine with a mock backend for testing.
// vocab: 0=<blank>, 1=▁hello, 2=▁world, 3=▁break, 4=ing, 5=▁news
// vocabSize=8, blankTokenID=0, numDurationBins=4, encoderDim=4
func makeTestEngine(mb inferenceBackend) *inferenceEngine {
	vocab := testVocab() // 8 entries
	cfg := &modelConfig{
		VocabSize:         len(vocab),
		BlankTokenID:      0,
		NumDurationBins:   4,
		EncoderDim:        4,
		SubsamplingFactor: 8,
	}
	assembler := newTokenAssembler(vocab, 0, 80)
	return &inferenceEngine{
		config:    cfg,
		vocab:     vocab,
		assembler: assembler,
		backend:   mb,
	}
}

// makeLogits builds a logits slice where tokenID gets a high score,
// blank (0) gets a low score, and duration bin durBin is highest.
// logits has vocabSize + numDurationBins entries.
func makeLogits(vocabSize, tokenID, numDurBins, durBin int) []float32 {
	total := vocabSize + numDurBins
	logits := make([]float32, total)
	// Default all vocab entries to -10.
	for i := 0; i < vocabSize; i++ {
		logits[i] = -10.0
	}
	// Set the winning token high.
	if tokenID >= 0 && tokenID < vocabSize {
		logits[tokenID] = 5.0
	}
	// Duration bins: set the winner.
	for d := 0; d < numDurBins; d++ {
		logits[vocabSize+d] = -5.0
	}
	if durBin >= 0 && durBin < numDurBins {
		logits[vocabSize+durBin] = 2.0
	}
	return logits
}

// sequenceMockBackend returns different decoder logits on each call.
type sequenceMockBackend struct {
	melResult     []float32
	melFrames     int
	encoderResult []float32
	encodedFrames int
	decoderLogits [][]float32
	decoderCall   int
}

func (s *sequenceMockBackend) runMel(audio []float32) ([]float32, int, error) {
	return s.melResult, s.melFrames, nil
}

func (s *sequenceMockBackend) runEncoder(melFeatures []float32, melFrames int) ([]float32, int, error) {
	return s.encoderResult, s.encodedFrames, nil
}

func (s *sequenceMockBackend) runDecoder(encoderFrame []float32, targets []int32, states1, states2 []float32) ([]float32, []float32, []float32, error) {
	idx := s.decoderCall
	s.decoderCall++
	if idx < len(s.decoderLogits) {
		return s.decoderLogits[idx], nil, nil, nil
	}
	// Return blank if we run out.
	return makeLogits(8, 0, 4, 1), nil, nil, nil
}

func (s *sequenceMockBackend) gpuStream() unsafe.Pointer { return nil }

func (s *sequenceMockBackend) close() {}

func TestTranscribeNilBackend(t *testing.T) {
	engine := &inferenceEngine{}
	words := engine.Transcribe(make([]float32, 16000))
	if words != nil {
		t.Errorf("expected nil from nil backend, got %v", words)
	}
}

func TestTranscribeMinSegment(t *testing.T) {
	engine := makeTestEngine(&mockBackend{})
	// Below 500ms (8000 samples at 16kHz).
	result := engine.Transcribe(make([]float32, 4000))
	if result != nil {
		t.Fatal("expected nil for short segment")
	}
}

func TestTranscribeMelError(t *testing.T) {
	mb := &mockBackend{
		melErr: fmt.Errorf("mel failed"),
	}
	engine := makeTestEngine(mb)
	words := engine.Transcribe(make([]float32, 16000))
	if words != nil {
		t.Errorf("expected nil on mel error, got %v", words)
	}
}

func TestTranscribeEncoderError(t *testing.T) {
	mb := &mockBackend{
		melResult:  []float32{1.0},
		melFrames:  1,
		encoderErr: fmt.Errorf("encoder failed"),
	}
	engine := makeTestEngine(mb)
	words := engine.Transcribe(make([]float32, 16000))
	if words != nil {
		t.Errorf("expected nil on encoder error, got %v", words)
	}
}

func TestTranscribeZeroEncodedFrames(t *testing.T) {
	mb := &mockBackend{
		melResult:     []float32{1.0},
		melFrames:     1,
		encoderResult: []float32{},
		encodedFrames: 0,
	}
	engine := makeTestEngine(mb)
	words := engine.Transcribe(make([]float32, 16000))
	if words != nil {
		t.Errorf("expected nil on zero encoded frames, got %v", words)
	}
}

func TestTranscribeDecoderError(t *testing.T) {
	mb := &mockBackend{
		melResult:     []float32{1.0},
		melFrames:     1,
		encoderResult: []float32{0.1, 0.2, 0.3, 0.4},
		encodedFrames: 1,
		decoderErr:    fmt.Errorf("decoder failed"),
	}
	engine := makeTestEngine(mb)
	words := engine.Transcribe(make([]float32, 16000))
	// Decoder error should not produce words.
	if words != nil {
		t.Errorf("expected nil on decoder error, got %v", words)
	}
}

func TestTranscribeSingleWordEmission(t *testing.T) {
	// Simulate two encoder frames producing tokens "▁hello" then "▁world".
	vocabSize := 8
	numDurBins := 4

	encoded := make([]float32, 8) // 2 frames * encoderDim=4
	logitsHello := makeLogits(vocabSize, 1, numDurBins, 1) // token 1=▁hello, dur=1
	logitsWorld := makeLogits(vocabSize, 2, numDurBins, 1) // token 2=▁world, dur=1

	seqBackend := &sequenceMockBackend{
		melResult:     []float32{1.0},
		melFrames:     1,
		encoderResult: encoded,
		encodedFrames: 2,
		decoderLogits: [][]float32{logitsHello, logitsWorld},
	}

	engine := makeTestEngine(seqBackend)
	words := engine.Transcribe(make([]float32, 16000))

	// Both "hello" and "world" should be emitted (greedyDecode uses temp assembler + flush).
	if len(words) != 2 {
		t.Fatalf("expected 2 words, got %d: %v", len(words), words)
	}
	if words[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", words[0].Text)
	}
	if words[1].Text != "world" {
		t.Errorf("expected 'world', got %q", words[1].Text)
	}
	if words[0].Confidence <= 0 || words[0].Confidence > 1 {
		t.Errorf("confidence out of range: %f", words[0].Confidence)
	}
}

func TestTranscribeBlankTokenSkipped(t *testing.T) {
	vocabSize := 8
	numDurBins := 4

	encoded := make([]float32, 8)
	logitsBlank := makeLogits(vocabSize, 0, numDurBins, 1) // token 0=<blank>

	seqBackend := &sequenceMockBackend{
		melResult:     []float32{1.0},
		melFrames:     1,
		encoderResult: encoded,
		encodedFrames: 2,
		decoderLogits: [][]float32{logitsBlank, logitsBlank},
	}

	engine := makeTestEngine(seqBackend)
	words := engine.Transcribe(make([]float32, 16000))
	if words != nil {
		t.Errorf("expected nil for blank-only tokens, got %v", words)
	}
}

func TestTranscribeDurationSkip(t *testing.T) {
	vocabSize := 8
	numDurBins := 4

	encoded := make([]float32, 4*4) // 4 frames * encoderDim=4
	logitsHello := makeLogits(vocabSize, 1, numDurBins, 3) // dur=3 -> skip 3
	logitsWorld := makeLogits(vocabSize, 2, numDurBins, 1) // dur=1

	seqBackend := &sequenceMockBackend{
		melResult:     []float32{1.0},
		melFrames:     1,
		encoderResult: encoded,
		encodedFrames: 4,
		decoderLogits: [][]float32{logitsHello, logitsWorld},
	}

	engine := makeTestEngine(seqBackend)
	words := engine.Transcribe(make([]float32, 16000))

	// Only 2 decoder calls should have been made (frame 0, then frame 3).
	if seqBackend.decoderCall != 2 {
		t.Errorf("expected 2 decoder calls, got %d", seqBackend.decoderCall)
	}

	// Both words emitted (batch mode flushes assembler).
	if len(words) != 2 {
		t.Fatalf("expected 2 words, got %d: %v", len(words), words)
	}
	if words[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", words[0].Text)
	}
	if words[1].Text != "world" {
		t.Errorf("expected 'world', got %q", words[1].Text)
	}
}

func TestTranscribeSubwordConcatenation(t *testing.T) {
	vocabSize := 8
	numDurBins := 4

	encoded := make([]float32, 3*4) // 3 frames
	seqBackend := &sequenceMockBackend{
		melResult:     []float32{1.0},
		melFrames:     1,
		encoderResult: encoded,
		encodedFrames: 3,
		decoderLogits: [][]float32{
			makeLogits(vocabSize, 3, numDurBins, 1), // ▁break
			makeLogits(vocabSize, 4, numDurBins, 1), // ing
			makeLogits(vocabSize, 5, numDurBins, 1), // ▁news
		},
	}

	engine := makeTestEngine(seqBackend)
	words := engine.Transcribe(make([]float32, 16000))

	if len(words) != 2 {
		t.Fatalf("expected 2 words, got %d: %v", len(words), words)
	}
	if words[0].Text != "breaking" {
		t.Errorf("expected 'breaking', got %q", words[0].Text)
	}
	if words[1].Text != "news" {
		t.Errorf("expected 'news', got %q", words[1].Text)
	}
}

func TestResetState(t *testing.T) {
	engine := makeTestEngine(&mockBackend{})
	// Just verify resetState doesn't panic.
	engine.resetState()
}

// Verify the unused import is actually used.
var _ asr.TranscriptWord
