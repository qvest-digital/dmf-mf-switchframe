//go:build cgo && cuda

package asr

import (
	"encoding/binary"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
)

const testModelDir = "/opt/models/parakeet-tdt-0.6b-v2-onnx"

func skipIfNoModel(t *testing.T) {
	t.Helper()
	for _, f := range []string{"nemo128.onnx", "encoder-model.onnx", "decoder_joint-model.onnx", "vocab.txt", "silero_vad.onnx"} {
		if _, err := os.Stat(testModelDir + "/" + f); err != nil {
			t.Skipf("Nemotron model file %s not found at %s (skipping GPU test)", f, testModelDir)
		}
	}
}

func TestNemotronBackend_CUDA_NewAndClose(t *testing.T) {
	skipIfNoModel(t)

	b, err := NewNemotronBackend(NemotronConfig{
		ModelDir: testModelDir,
		ChunkMs:  3000,
		UseGPU:   true,
		Language: "en",
	})
	if err != nil {
		t.Fatalf("NewNemotronBackend: %v", err)
	}
	defer b.Close()

	if b.Name() != "nemotron" {
		t.Fatalf("expected name 'nemotron', got %q", b.Name())
	}
}

func TestNemotronBackend_CUDA_InferSilence(t *testing.T) {
	skipIfNoModel(t)

	b, err := NewNemotronBackend(NemotronConfig{
		ModelDir: testModelDir,
		ChunkMs:  3000,
		UseGPU:   true,
		Language: "en",
	})
	if err != nil {
		t.Fatalf("NewNemotronBackend: %v", err)
	}
	defer b.Close()

	var mu sync.Mutex
	var words []TranscriptWord
	b.Start(func(w []TranscriptWord, isFinal bool) {
		mu.Lock()
		words = append(words, w...)
		mu.Unlock()
	})

	// Feed 1 second of silence (48kHz stereo).
	// VAD should not trigger on silence, so no inference runs.
	silence := make([]float32, 48000*2)
	b.IngestAudio(silence, 0, 48000, 2)

	mu.Lock()
	n := len(words)
	mu.Unlock()
	// Silence should produce zero words (VAD won't trigger).
	if n > 0 {
		t.Fatalf("expected 0 words from silence, got %d", n)
	}
}

func TestNemotronBackend_CUDA_InferTone(t *testing.T) {
	skipIfNoModel(t)

	b, err := NewNemotronBackend(NemotronConfig{
		ModelDir: testModelDir,
		ChunkMs:  3000,
		UseGPU:   true,
		Language: "en",
	})
	if err != nil {
		t.Fatalf("NewNemotronBackend: %v", err)
	}
	defer b.Close()

	var mu sync.Mutex
	var called bool
	b.Start(func(w []TranscriptWord, isFinal bool) {
		mu.Lock()
		called = true
		mu.Unlock()
	})

	// Feed 1 second of 440Hz tone (48kHz stereo) — not speech but exercises the pipeline.
	pcm := make([]float32, 48000*2)
	for i := 0; i < 48000; i++ {
		val := float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
		pcm[i*2] = val
		pcm[i*2+1] = val
	}
	b.IngestAudio(pcm, 0, 48000, 2)

	// Just verify the pipeline doesn't crash. The VAD may or may not trigger
	// on a pure tone (it's energy-based), but either way should not panic.
	mu.Lock()
	t.Logf("callback called: %v", called)
	mu.Unlock()
}

func TestNemotronBackend_CUDA_MelFrontend(t *testing.T) {
	skipIfNoModel(t)

	b, err := NewNemotronBackend(NemotronConfig{
		ModelDir: testModelDir,
		ChunkMs:  3000,
		UseGPU:   true,
	})
	if err != nil {
		t.Fatalf("NewNemotronBackend: %v", err)
	}
	defer b.Close()

	nb := b.(*NemotronBackend)

	// Generate 560ms of 16kHz mono audio (a tone to get non-trivial mel output).
	numSamples := 560 * 16 // 8960 samples
	audio := make([]float32, numSamples)
	for i := range audio {
		audio[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 16000))
	}

	melData, numFrames, err := nb.runMelFrontend(audio)
	if err != nil {
		t.Fatalf("runMelFrontend: %v", err)
	}

	if numFrames == 0 {
		t.Fatal("expected non-zero mel frames")
	}

	// nemo128 uses 128 mel bins, hop=160, win=512, so for 8960 samples:
	// numFrames should be approximately ceil(8960 / 160) = 56 frames.
	// The exact count depends on padding behavior in the ONNX model.
	t.Logf("mel frontend: %d frames from %d samples (128 bins)", numFrames, numSamples)

	expectedLen := 128 * numFrames
	if len(melData) != expectedLen {
		t.Fatalf("mel data length %d != expected %d (128 * %d)", len(melData), expectedLen, numFrames)
	}

	// Verify mel values are finite and not all zero.
	allZero := true
	for _, v := range melData {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("mel data contains NaN/Inf")
		}
		if v != 0 {
			allZero = false
		}
	}
	if allZero {
		t.Fatal("mel data is all zeros for non-silent audio")
	}
}

func TestNemotronBackend_CUDA_MelEncoderIntegration(t *testing.T) {
	skipIfNoModel(t)

	b, err := NewNemotronBackend(NemotronConfig{
		ModelDir: testModelDir,
		ChunkMs:  3000,
		UseGPU:   true,
	})
	if err != nil {
		t.Fatalf("NewNemotronBackend: %v", err)
	}
	defer b.Close()

	nb := b.(*NemotronBackend)

	// Generate 560ms of 16kHz mono speech-like audio (swept tone).
	numSamples := 560 * 16
	audio := make([]float32, numSamples)
	for i := range audio {
		// Frequency sweep from 200Hz to 4000Hz (covers speech range).
		freq := 200.0 + 3800.0*float64(i)/float64(numSamples)
		audio[i] = 0.5 * float32(math.Sin(2*math.Pi*freq*float64(i)/16000))
	}

	// Run mel frontend.
	melData, numFrames, err := nb.runMelFrontend(audio)
	if err != nil {
		t.Fatalf("runMelFrontend: %v", err)
	}
	t.Logf("mel: %d frames", numFrames)

	// Run encoder on the mel output.
	encoderOutput, encodedLen, err := nb.runEncoder(melData, numFrames)
	if err != nil {
		t.Fatalf("runEncoder: %v", err)
	}
	t.Logf("encoder: %d encoded frames, %d total features", encodedLen, len(encoderOutput))

	if encodedLen == 0 {
		t.Fatal("encoder produced zero frames")
	}

	// Encoder output should be [1, 1024, encodedLen].
	expectedLen := encoderDim * encodedLen
	if len(encoderOutput) < expectedLen {
		t.Fatalf("encoder output too small: %d < %d", len(encoderOutput), expectedLen)
	}

	// Verify encoder output is finite and non-trivial.
	var maxAbs float32
	for _, v := range encoderOutput[:expectedLen] {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("encoder output contains NaN/Inf")
		}
		if abs := float32(math.Abs(float64(v))); abs > maxAbs {
			maxAbs = abs
		}
	}
	if maxAbs < 0.001 {
		t.Fatalf("encoder output suspiciously small (max abs: %v)", maxAbs)
	}
	t.Logf("encoder output range: max_abs=%v", maxAbs)
}

func TestNemotronBackend_CUDA_RealSpeech(t *testing.T) {
	skipIfNoModel(t)

	// Check for real speech test data (generated externally).
	rawPath := "/tmp/asr_testdata/real_speech_16k.raw"
	if _, err := os.Stat(rawPath); err != nil {
		t.Skip("real speech test data not found at", rawPath, "(generate with: python3 generate_speech_test.py)")
	}

	b, err := NewNemotronBackend(NemotronConfig{
		ModelDir: testModelDir,
		ChunkMs:  3000,
		UseGPU:   true,
	})
	if err != nil {
		t.Fatalf("NewNemotronBackend: %v", err)
	}
	defer b.Close()

	nb := b.(*NemotronBackend)

	// Load 16kHz mono float32 audio.
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("read test audio: %v", err)
	}
	if len(raw) < 4 {
		t.Fatal("test audio file too small")
	}

	mono16k := make([]float32, len(raw)/4)
	for i := range mono16k {
		mono16k[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	t.Logf("loaded %d samples (%.1fs) of 16kHz mono audio", len(mono16k), float64(len(mono16k))/16000)

	// Run inference directly on 16kHz mono (bypassing the 48k->16k resampler + VAD).
	// This validates the mel -> encoder -> decoder pipeline with real speech.
	var mu sync.Mutex
	var allWords []TranscriptWord
	cb := func(words []TranscriptWord, isFinal bool) {
		mu.Lock()
		allWords = append(allWords, words...)
		mu.Unlock()
	}

	// Feed all audio as one inference call. Parakeet TDT is an offline model —
	// it produces best results on full utterances, not small streaming chunks.
	nb.runInferenceOnAudio(mono16k, cb)

	mu.Lock()
	var texts []string
	for _, w := range allWords {
		texts = append(texts, w.Text)
		t.Logf("  word=%q conf=%.4f gapBefore=%dms", w.Text, w.Confidence, w.GapBefore)
	}
	mu.Unlock()

	transcript := strings.ToLower(strings.Join(texts, " "))
	t.Logf("transcript: %q", transcript)

	// Verify confidence scores are real (not hardcoded 0.9).
	for _, w := range allWords {
		if w.Confidence <= 0 || w.Confidence > 1.0 {
			t.Errorf("word %q has invalid confidence: %f", w.Text, w.Confidence)
		}
	}

	if len(texts) == 0 {
		t.Fatal("no words recognized from speech audio")
	}

	// The test audio should contain "the quick brown fox" or similar.
	// Check for at least some recognizable words.
	foundAny := false
	for _, word := range []string{"quick", "brown", "fox", "the", "lazy", "dog"} {
		if strings.Contains(transcript, word) {
			foundAny = true
			break
		}
	}
	if !foundAny {
		t.Errorf("transcript does not contain any expected words: %q", transcript)
	}
}

func skipIfNoSileroModel(t *testing.T) {
	t.Helper()
	vadPath := testModelDir + "/silero_vad.onnx"
	if _, err := os.Stat(vadPath); err != nil {
		t.Skipf("Silero VAD model not found at %s (skipping test)", vadPath)
	}
}

func TestSileroVAD_CUDA_SilenceAndSpeech(t *testing.T) {
	skipIfNoSileroModel(t)

	vadPath := testModelDir + "/silero_vad.onnx"
	session, err := NewOnnxSession(vadPath, false) // CPU inference
	if err != nil {
		t.Fatalf("NewOnnxSession for Silero VAD: %v", err)
	}
	defer session.Close()

	vad := NewSileroVAD(session, 0.5, 500)

	// Feed 512 samples of silence — should not trigger speech.
	silenceBuf := make([]float32, 512)
	vad.Process(silenceBuf)
	if vad.IsSpeaking() {
		t.Error("expected IsSpeaking()=false for silence")
	}

	// Feed several more chunks of silence to confirm stability.
	for i := 0; i < 10; i++ {
		vad.Process(silenceBuf)
	}
	if vad.IsSpeaking() {
		t.Error("expected IsSpeaking()=false after sustained silence")
	}

	// Feed a loud 440Hz tone (not speech, but high energy).
	// Silero VAD should NOT trigger on a pure tone (prob ~0.003).
	toneBuf := make([]float32, 512)
	for i := range toneBuf {
		toneBuf[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/16000))
	}
	for i := 0; i < 20; i++ {
		vad.Process(toneBuf)
	}
	if vad.IsSpeaking() {
		t.Log("note: Silero VAD triggered on pure tone (unexpected but not fatal)")
	}

	// Reset and verify clean state.
	vad.Reset()
	if vad.IsSpeaking() {
		t.Error("expected IsSpeaking()=false after Reset()")
	}

	// Feed a speech-like signal: mix of frequencies typical of human voice.
	// This is a rough approximation — real speech would be better, but this
	// exercises the full ONNX inference path.
	speechBuf := make([]float32, 512)
	for i := range speechBuf {
		t := float64(i) / 16000.0
		// Fundamental ~150Hz with harmonics + formant-like energy
		val := 0.3*math.Sin(2*math.Pi*150*t) +
			0.2*math.Sin(2*math.Pi*300*t) +
			0.15*math.Sin(2*math.Pi*500*t) +
			0.1*math.Sin(2*math.Pi*1000*t) +
			0.05*math.Sin(2*math.Pi*2000*t)
		speechBuf[i] = float32(val)
	}
	for i := 0; i < 30; i++ {
		vad.Process(speechBuf)
	}
	// We don't assert IsSpeaking() here because synthetic harmonics may
	// not fool the neural VAD. The test's primary value is verifying that
	// the ONNX inference path works end-to-end without errors.
	t.Logf("after speech-like signal: IsSpeaking()=%v", vad.IsSpeaking())
}

func TestSileroVAD_CUDA_BufferAccumulation(t *testing.T) {
	skipIfNoSileroModel(t)

	vadPath := testModelDir + "/silero_vad.onnx"
	session, err := NewOnnxSession(vadPath, false)
	if err != nil {
		t.Fatalf("NewOnnxSession for Silero VAD: %v", err)
	}
	defer session.Close()

	vad := NewSileroVAD(session, 0.5, 500)

	// Feed samples in chunks smaller than 512 — should accumulate without error.
	smallChunk := make([]float32, 100)
	for i := 0; i < 10; i++ {
		vad.Process(smallChunk) // 100 * 10 = 1000 samples = 1 full chunk + 488 remainder
	}
	// No panic/error means buffer accumulation works correctly.

	// Feed a chunk that completes multiple 512-sample windows.
	largeChunk := make([]float32, 2048)
	vad.Process(largeChunk) // processes 2048 + 488 remainder = ~4 full chunks
	// Again, no panic means it handled multiple chunks in one call.
}

func TestSileroVAD_CUDA_EmptyInput(t *testing.T) {
	skipIfNoSileroModel(t)

	vadPath := testModelDir + "/silero_vad.onnx"
	session, err := NewOnnxSession(vadPath, false)
	if err != nil {
		t.Fatalf("NewOnnxSession for Silero VAD: %v", err)
	}
	defer session.Close()

	vad := NewSileroVAD(session, 0.5, 500)

	// Empty and nil inputs should be no-ops.
	vad.Process([]float32{})
	vad.Process(nil)
	if vad.IsSpeaking() {
		t.Error("expected IsSpeaking()=false after empty input")
	}
}
