//go:build cgo && cuda

package asr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// blankTokenID is the blank token in the TDT vocabulary.
	blankTokenID = 1024

	// numDurationBins is the number of duration prediction bins in the TDT joint output.
	// The last 5 entries of the 1030-dim output predict how many encoder frames to skip.
	numDurationBins = 5

	// vocabSize is the vocabulary size (tokens 0-1024).
	vocabSize = 1025

	// jointOutputSize is vocabSize + numDurationBins.
	jointOutputSize = vocabSize + numDurationBins

	// encoderDim is the encoder output feature dimension.
	encoderDim = 1024

	// lstmHiddenSize is the LSTM hidden/cell state size per direction.
	lstmHiddenSize = 640

	// lstmNumLayers is the number of LSTM layers * directions.
	lstmNumLayers = 2

	// subsamplingFactor is the encoder temporal subsampling factor.
	subsamplingFactor = 8

	// wordBoundary is the SentencePiece word boundary marker.
	wordBoundary = "\u2581" // ▁ (U+2581)

	// maxTokenHistory caps lastTokens to prevent unbounded growth.
	// The TDT decoder only needs recent context; 128 tokens is generous.
	maxTokenHistory = 128

	// msPerEncoderFrame is the time duration of one encoder frame in milliseconds.
	// subsamplingFactor(8) * hopSize(160) / sampleRate(16000) * 1000 = 80ms.
	msPerEncoderFrame = 80
)

// decodedToken holds a decoded token with its softmax probability and
// the encoder frame position at which it was emitted. Used to compute
// per-word confidence and inter-word gap timing in tokensToWords.
type decodedToken struct {
	id       int32
	prob     float32 // softmax probability of this token
	framePos int     // encoder frame index when this token was emitted
}

// NemotronBackend implements Backend using NVIDIA Parakeet TDT 0.6B via ONNX Runtime.
// Parakeet TDT is an offline model that works best on full utterances. Short chunks
// produce garbage ("the the the the"). This backend uses VAD-gated inference:
// audio accumulates in a ring buffer, and inference runs on the full speech segment
// when speech ends or at periodic intervals during long speech.
type NemotronBackend struct {
	mu      sync.Mutex
	logOnce sync.Once

	melSession *OnnxSession // nemo128.onnx mel spectrogram frontend
	encoder    *OnnxSession // encoder ONNX session
	decoder    *OnnxSession // decoder-joint ONNX session
	vadSession *OnnxSession // silero_vad.onnx session (CPU) — kept for future use
	resampler  *Resampler   // 48kHz stereo -> 16kHz mono
	ringBuf    *AudioRingBuf
	sileroVAD  *SileroVAD   // neural VAD — too conservative for broadcast, kept for future
	vad        *VAD          // energy-based VAD — reliable on broadcast audio
	vocab      []string // token vocabulary from vocab.txt

	cfg NemotronConfig

	// Decoder state (reset at start of each speech segment for offline model).
	lstmState1 []float32 // [2, 1, 640] LSTM hidden
	lstmState2 []float32 // [2, 1, 640] LSTM cell
	lastTokens []int32   // previous decoder tokens

	// Inference state (mirrors WhisperBackend's VAD-gated pattern).
	speechStart    time.Time
	lastInfer      time.Time
	inferCount     int
	wasSpeaking    bool
	wasSpeakingPrev bool // tracks previous SileroVAD.IsSpeaking() for edge detection
	lastWordCount  int
	emittedWords   []string // texts of words emitted so far (for text-based dedup)

	onTranscript func([]TranscriptWord, bool)
	inferring    sync.Mutex // serializes inference (one at a time)
	started      bool
	closed       bool
	doneCh       chan struct{}
}

// NewNemotronBackend creates a new NemotronBackend with ONNX Runtime CUDA inference.
// The cfg.ModelDir must contain encoder-model.onnx, decoder_joint-model.onnx, and vocab.txt.
func NewNemotronBackend(cfg NemotronConfig) (Backend, error) {
	modelDir := cfg.ModelDir

	// Auto-discover model directory if not explicitly set.
	if modelDir == "" && cfg.ModelsDir != "" {
		candidate := filepath.Join(cfg.ModelsDir, "parakeet-tdt-0.6b-v2-onnx")
		if _, err := os.Stat(candidate); err == nil {
			modelDir = candidate
		}
	}
	if modelDir == "" {
		modelDir = "/opt/models/parakeet-tdt-0.6b-v2-onnx"
	}

	// Validate model files exist.
	melPath := filepath.Join(modelDir, "nemo128.onnx")
	encoderPath := filepath.Join(modelDir, "encoder-model.onnx")
	decoderPath := filepath.Join(modelDir, "decoder_joint-model.onnx")
	vocabPath := filepath.Join(modelDir, "vocab.txt")
	vadPath := filepath.Join(modelDir, "silero_vad.onnx")

	for _, p := range []string{melPath, encoderPath, decoderPath, vocabPath, vadPath} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("asr: nemotron: model file not found: %s", p)
		}
	}

	// Load vocabulary.
	vocab, err := loadVocab(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("asr: nemotron: load vocab: %w", err)
	}
	slog.Info("ASR nemotron: loaded vocabulary", "tokens", len(vocab), "path", vocabPath)

	// Create ONNX sessions.
	// TODO: TensorRT EP support — requires OrtSessionOptionsAppendExecutionProvider_TensorRT_V2
	// and additional model optimization. For now, CUDA EP is used.
	useGPU := cfg.UseGPU

	melSession, err := NewOnnxSession(melPath, useGPU)
	if err != nil {
		return nil, fmt.Errorf("asr: nemotron: create mel session: %w", err)
	}

	encoder, err := NewOnnxSession(encoderPath, useGPU)
	if err != nil {
		melSession.Close()
		return nil, fmt.Errorf("asr: nemotron: create encoder session: %w", err)
	}

	decoder, err := NewOnnxSession(decoderPath, useGPU)
	if err != nil {
		melSession.Close()
		encoder.Close()
		return nil, fmt.Errorf("asr: nemotron: create decoder session: %w", err)
	}

	// Silero VAD runs on CPU — it's a tiny 2.3MB model that runs in microseconds.
	// Running on CPU avoids contending with the encoder/decoder for GPU memory.
	vadSession, err := NewOnnxSession(vadPath, false)
	if err != nil {
		melSession.Close()
		encoder.Close()
		decoder.Close()
		return nil, fmt.Errorf("asr: nemotron: create VAD session: %w", err)
	}

	// Set defaults.
	if cfg.ChunkMs == 0 {
		cfg.ChunkMs = 3000
	}
	if cfg.Language == "" {
		cfg.Language = "en"
	}

	slog.Info("ASR nemotron: backend initialized",
		"modelDir", modelDir,
		"useGPU", useGPU,
		"inferIntervalMs", cfg.ChunkMs,
	)

	b := &NemotronBackend{
		melSession: melSession,
		encoder:    encoder,
		decoder:    decoder,
		vadSession: vadSession,
		resampler:  NewResampler(48000, 2, 16000),
		ringBuf:    NewAudioRingBuf(16000, 30),
		sileroVAD:  NewSileroVAD(vadSession, 0.3, 500),
		vad: NewVAD(VADConfig{
			ThresholdDB: -26,
			HangoverMs:  500,
			SampleRate:  16000,
		}),
		vocab:  vocab,
		cfg:    cfg,
		doneCh: make(chan struct{}),
	}
	b.resetDecoderState()

	return b, nil
}

// Name returns "nemotron".
func (b *NemotronBackend) Name() string { return "nemotron" }

// Start begins the inference loop.
func (b *NemotronBackend) Start(onTranscript func(words []TranscriptWord, isFinal bool)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onTranscript = onTranscript
	b.started = true
	return nil
}

// IngestAudio feeds raw PCM to the backend. Resamples to 16kHz mono,
// writes to the ring buffer, and uses VAD to schedule inference on full
// speech segments. Parakeet TDT is an offline model — it needs long audio
// segments for accurate transcription.
func (b *NemotronBackend) IngestAudio(pcm []float32, pts int64, sampleRate, channels int) {
	mono16k := b.resampler.Process(pcm)
	if len(mono16k) == 0 {
		return
	}

	b.logOnce.Do(func() {
		slog.Info("ASR nemotron: first audio received", "samples", len(mono16k))
	})

	// Use energy-based VAD (same as WhisperBackend) for reliable speech
	// detection on broadcast audio. Silero ONNX VAD was too conservative
	// for compressed broadcast audio with background music.
	b.vad.Process(mono16k)
	nowSpeaking := b.vad.State() == VADSpeaking || b.vad.State() == VADTrailing

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed || !b.started {
		return
	}

	b.ringBuf.Write(mono16k)

	prevSpeaking := b.wasSpeakingPrev
	b.wasSpeakingPrev = nowSpeaking

	now := time.Now()

	switch {
	case !prevSpeaking && nowSpeaking:
		// Speech onset: mark start, reset inference state and decoder.
		b.speechStart = now
		b.lastInfer = now
		b.wasSpeaking = true
		b.inferCount = 0
		b.lastWordCount = 0
		b.resetDecoderState()

	case nowSpeaking:
		// During speech: periodic inference at adaptive intervals using FULL buffer.
		var inferInterval time.Duration
		switch b.inferCount {
		case 0:
			inferInterval = 1000 * time.Millisecond
		case 1:
			inferInterval = 2000 * time.Millisecond
		default:
			inferInterval = time.Duration(b.cfg.ChunkMs) * time.Millisecond
		}

		if b.wasSpeaking && now.Sub(b.lastInfer) >= inferInterval {
			b.lastInfer = now
			b.inferCount++
			go b.runInference(false)
		}

	case prevSpeaking && !nowSpeaking:
		// Speech ended: final inference with full buffer.
		if b.wasSpeaking {
			b.wasSpeaking = false
			go b.runInference(true)
		}
	}
}

// runInference takes a snapshot of the ring buffer and runs the mel + encoder +
// TDT decoder pipeline on the FULL audio segment.
//
// Non-final: the buffer is NOT drained. The model re-transcribes the growing
// speech segment each time. Only NEW words (beyond lastWordCount) are emitted.
//
// Final: the buffer is cleared and all remaining words are emitted.
func (b *NemotronBackend) runInference(isFinal bool) {
	b.inferring.Lock()
	defer b.inferring.Unlock()

	start := time.Now()

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	audio := b.ringBuf.Snapshot()
	audioLen := len(audio)
	prevWordCount := b.lastWordCount
	b.mu.Unlock()

	if audioLen < 1600 {
		return
	}

	// Check audio energy — skip inference if mostly silence/noise.
	var sumSq float64
	for _, s := range audio {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(audioLen))
	if rms < 0.02 {
		return
	}

	// Step 1: Compute mel spectrogram via nemo128.onnx.
	melData, numFrames, err := b.runMelFrontend(audio)
	if err != nil {
		slog.Warn("ASR nemotron: mel frontend failed", "error", err)
		return
	}
	if numFrames == 0 {
		return
	}

	// Step 2: Run encoder.
	encoderOutput, encodedLen, err := b.runEncoder(melData, numFrames)
	if err != nil {
		slog.Warn("ASR nemotron: encoder failed", "error", err)
		return
	}

	// Step 3: Run TDT greedy decode with fresh decoder state.
	// For an offline model, we re-transcribe the entire buffer each time
	// with a clean decoder state. The growing-window dedup handles
	// emitting only new words.
	stateSize := lstmNumLayers * 1 * lstmHiddenSize
	freshState1 := make([]float32, stateSize)
	freshState2 := make([]float32, stateSize)
	var freshTokens []int32

	words, _, _, _ := b.greedyDecode(encoderOutput, encodedLen, freshState1, freshState2, freshTokens)

	slog.Info("ASR nemotron: inference complete",
		"ms", time.Since(start).Milliseconds(),
		"audioSamples", audioLen,
		"melFrames", numFrames,
		"encodedFrames", encodedLen,
		"totalWords", len(words),
		"newWords", max(0, len(words)-prevWordCount),
		"isFinal", isFinal,
	)

	// Text-based dedup: find where the new transcription diverges from
	// what we already emitted. This handles the case where the model
	// re-tokenizes earlier audio differently as the buffer grows.
	b.mu.Lock()
	prevEmitted := b.emittedWords
	cb := b.onTranscript

	// Find the longest common prefix between previous and current words.
	matchLen := 0
	for matchLen < len(prevEmitted) && matchLen < len(words) {
		if words[matchLen].Text != prevEmitted[matchLen] {
			break
		}
		matchLen++
	}

	var newWords []TranscriptWord
	if matchLen < len(words) {
		newWords = words[matchLen:]
	}

	if isFinal {
		b.ringBuf.Clear()
		b.lastWordCount = 0
		b.emittedWords = nil
		b.resetDecoderState()
	} else {
		b.lastWordCount = len(words)
		// Track emitted word texts for next dedup.
		all := make([]string, len(words))
		for i, w := range words {
			all[i] = w.Text
		}
		b.emittedWords = all
	}
	b.mu.Unlock()

	if cb != nil && len(newWords) > 0 {
		cb(newWords, isFinal)
	}
}

// runInferenceOnAudio runs inference directly on provided 16kHz mono audio.
// Used by tests to bypass the IngestAudio/VAD path and validate the
// mel -> encoder -> decoder pipeline with pre-prepared audio.
func (b *NemotronBackend) runInferenceOnAudio(audio []float32, cb func([]TranscriptWord, bool)) {
	b.inferring.Lock()
	defer b.inferring.Unlock()

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	if cb == nil {
		return
	}

	start := time.Now()

	// Step 1: Compute mel spectrogram via nemo128.onnx.
	melData, numFrames, err := b.runMelFrontend(audio)
	if err != nil {
		slog.Warn("ASR nemotron: mel frontend failed", "error", err)
		return
	}
	if numFrames == 0 {
		return
	}

	// Step 2: Run encoder.
	encoderOutput, encodedLen, err := b.runEncoder(melData, numFrames)
	if err != nil {
		slog.Warn("ASR nemotron: encoder failed", "error", err)
		return
	}

	// Step 3: Run TDT greedy decode with fresh state.
	stateSize := lstmNumLayers * 1 * lstmHiddenSize
	freshState1 := make([]float32, stateSize)
	freshState2 := make([]float32, stateSize)
	var freshTokens []int32

	words, _, _, _ := b.greedyDecode(encoderOutput, encodedLen, freshState1, freshState2, freshTokens)

	slog.Info("ASR nemotron: inference complete",
		"ms", time.Since(start).Milliseconds(),
		"audioSamples", len(audio),
		"melFrames", numFrames,
		"encodedFrames", encodedLen,
		"words", len(words),
	)

	if len(words) > 0 {
		cb(words, false)
	}
}

// runMelFrontend runs the nemo128.onnx model to compute mel spectrogram features.
// Input: 16kHz mono float32 audio.
// Output: mel features as flat float32 in [128, numFrames] layout, and numFrames.
// The nemo128.onnx model expects:
//   - "waveforms" [1, N] float32 — raw audio
//   - "waveforms_lens" [1] int64 — number of samples
//
// And returns:
//   - "features" [1, 128, T] — mel features
//   - "features_lens" [1] int64 — number of frames
func (b *NemotronBackend) runMelFrontend(audio []float32) ([]float32, int, error) {
	if len(audio) == 0 {
		return nil, 0, nil
	}

	// Input: waveforms [1, N]
	waveformShape := []int64{1, int64(len(audio))}
	waveformValue, err := b.melSession.NewOnnxValueFloat32(audio, waveformShape)
	if err != nil {
		return nil, 0, fmt.Errorf("create waveform tensor: %w", err)
	}
	defer waveformValue.Close()

	// Input: waveforms_lens [1] — number of samples
	waveformLens := []int64{int64(len(audio))}
	waveformLensShape := []int64{1}
	waveformLensValue, err := b.melSession.NewOnnxValueInt64(waveformLens, waveformLensShape)
	if err != nil {
		return nil, 0, fmt.Errorf("create waveform_lens tensor: %w", err)
	}
	defer waveformLensValue.Close()

	// Run mel frontend.
	outputs, err := b.melSession.Run(
		[]string{"waveforms", "waveforms_lens"},
		[]*OnnxValue{waveformValue, waveformLensValue},
		[]string{"features", "features_lens"},
	)
	if err != nil {
		return nil, 0, fmt.Errorf("mel session run: %w", err)
	}
	defer func() {
		for _, o := range outputs {
			o.Close()
		}
	}()

	// Extract mel features.
	melData, err := outputs[0].GetFloat32Data()
	if err != nil {
		return nil, 0, fmt.Errorf("get mel data: %w", err)
	}

	// Extract number of frames.
	melLens, err := outputs[1].GetInt64Data()
	if err != nil {
		return nil, 0, fmt.Errorf("get mel length: %w", err)
	}
	numFrames := int(melLens[0])

	// The output shape is [1, 128, T]. The encoder expects [1, 128, numFrames]
	// which is the same layout. We just need to strip the batch dimension
	// by copying the data (since it references ONNX-owned memory).
	melCopy := make([]float32, 128*numFrames)
	copy(melCopy, melData[:128*numFrames])

	return melCopy, numFrames, nil
}

// runEncoder runs the encoder ONNX session on a mel spectrogram.
// Returns the encoded features as a flat float32 slice and the encoded length.
func (b *NemotronBackend) runEncoder(melData []float32, numFrames int) ([]float32, int, error) {
	// Input: audio_signal [1, 128, numFrames]
	signalShape := []int64{1, 128, int64(numFrames)}
	signalValue, err := b.encoder.NewOnnxValueFloat32(melData, signalShape)
	if err != nil {
		return nil, 0, fmt.Errorf("create signal tensor: %w", err)
	}
	defer signalValue.Close()

	// Input: length [1]
	lengthData := []int64{int64(numFrames)}
	lengthShape := []int64{1}
	lengthValue, err := b.encoder.NewOnnxValueInt64(lengthData, lengthShape)
	if err != nil {
		return nil, 0, fmt.Errorf("create length tensor: %w", err)
	}
	defer lengthValue.Close()

	// Run encoder.
	outputs, err := b.encoder.Run(
		[]string{"audio_signal", "length"},
		[]*OnnxValue{signalValue, lengthValue},
		[]string{"outputs", "encoded_lengths"},
	)
	if err != nil {
		return nil, 0, fmt.Errorf("encoder run: %w", err)
	}
	defer func() {
		for _, o := range outputs {
			o.Close()
		}
	}()

	// Extract encoded features.
	encodedFeatures, err := outputs[0].GetFloat32Data()
	if err != nil {
		return nil, 0, fmt.Errorf("get encoder output: %w", err)
	}

	// Extract encoded length.
	encodedLengths, err := outputs[1].GetInt64Data()
	if err != nil {
		return nil, 0, fmt.Errorf("get encoded length: %w", err)
	}

	encodedLen := int(encodedLengths[0])

	// Copy the data since it references ONNX-owned memory.
	featuresCopy := make([]float32, len(encodedFeatures))
	copy(featuresCopy, encodedFeatures)

	return featuresCopy, encodedLen, nil
}

// greedyDecode performs TDT greedy decoding on encoder output.
// Takes decoder state as parameters and returns updated state, avoiding
// the need to hold b.mu during the entire decode loop.
func (b *NemotronBackend) greedyDecode(
	encoderOutput []float32, encodedLen int,
	lstmState1, lstmState2 []float32, lastTokens []int32,
) (words []TranscriptWord, newState1, newState2 []float32, newTokens []int32) {
	if encodedLen == 0 {
		return nil, lstmState1, lstmState2, lastTokens
	}

	var tokens []decodedToken
	encFrame := 0

	for encFrame < encodedLen {
		// Run one decoder step.
		jointLogits, updState1, updState2, err := b.runDecoderStep(
			encoderOutput, encodedLen, encFrame, lstmState1, lstmState2, lastTokens)
		if err != nil {
			slog.Warn("ASR nemotron: decoder step failed", "error", err, "frame", encFrame)
			break
		}

		// Token prediction: argmax over first vocabSize entries.
		bestToken := int32(0)
		bestScore := jointLogits[0]
		for i := int32(1); i < int32(vocabSize); i++ {
			if jointLogits[i] > bestScore {
				bestScore = jointLogits[i]
				bestToken = i
			}
		}

		// Compute softmax probability for the winning token.
		// Find max logit for numerical stability.
		var maxLogit float32 = jointLogits[0]
		for i := 1; i < vocabSize; i++ {
			if jointLogits[i] > maxLogit {
				maxLogit = jointLogits[i]
			}
		}
		var sumExp float64
		for i := 0; i < vocabSize; i++ {
			sumExp += math.Exp(float64(jointLogits[i] - maxLogit))
		}
		bestProb := float32(math.Exp(float64(jointLogits[bestToken]-maxLogit)) / sumExp)

		// Duration prediction: argmax over last numDurationBins entries.
		bestDuration := 0
		bestDurScore := jointLogits[vocabSize]
		for d := 1; d < numDurationBins; d++ {
			if jointLogits[vocabSize+d] > bestDurScore {
				bestDurScore = jointLogits[vocabSize+d]
				bestDuration = d
			}
		}

		// Emit token if non-blank.
		if bestToken != blankTokenID {
			tokens = append(tokens, decodedToken{
				id:       bestToken,
				prob:     bestProb,
				framePos: encFrame,
			})
			lastTokens = append(lastTokens, bestToken)
			if len(lastTokens) > maxTokenHistory {
				lastTokens = lastTokens[len(lastTokens)-maxTokenHistory:]
			}
		}

		// Update LSTM states.
		lstmState1 = updState1
		lstmState2 = updState2

		// Advance encoder position by duration (minimum 1 frame).
		skip := bestDuration
		if skip < 1 {
			skip = 1
		}
		encFrame += skip
	}

	// Convert tokens to words.
	return b.tokensToWords(tokens), lstmState1, lstmState2, lastTokens
}

// runDecoderStep runs one step of the TDT decoder-joint network.
// Takes LSTM states and token history as parameters (no b.mu needed).
// Returns joint logits [1030] and updated LSTM states.
func (b *NemotronBackend) runDecoderStep(
	encoderOutput []float32, encodedLen, encFrame int,
	lstmState1, lstmState2 []float32, lastTokens []int32,
) (
	logits []float32, newState1, newState2 []float32, err error,
) {
	// Extract encoder features for this frame: [1, 1024, 1]
	// encoderOutput is [1, 1024, encodedLen] — extract column encFrame.
	encFeatures := make([]float32, encoderDim)
	for d := 0; d < encoderDim; d++ {
		encFeatures[d] = encoderOutput[d*encodedLen+encFrame]
	}

	// Reshape to [1, 1024, 1] for the decoder.
	encShape := []int64{1, int64(encoderDim), 1}
	encValue, err := b.decoder.NewOnnxValueFloat32(encFeatures, encShape)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create encoder features tensor: %w", err)
	}
	defer encValue.Close()

	// Targets: previous tokens [1, targetLen].
	targetLen := len(lastTokens)
	if targetLen == 0 {
		// Initial step: use blank token as seed.
		targetLen = 1
		lastTokens = []int32{blankTokenID}
	}
	targetsShape := []int64{1, int64(targetLen)}
	targetsValue, err := b.decoder.NewOnnxValueInt32(lastTokens, targetsShape)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create targets tensor: %w", err)
	}
	defer targetsValue.Close()

	// Target length [1].
	targetLenData := []int32{int32(targetLen)}
	targetLenShape := []int64{1}
	targetLenValue, err := b.decoder.NewOnnxValueInt32(targetLenData, targetLenShape)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create target_length tensor: %w", err)
	}
	defer targetLenValue.Close()

	// LSTM states: [2, 1, 640].
	stateShape := []int64{int64(lstmNumLayers), 1, int64(lstmHiddenSize)}
	state1Value, err := b.decoder.NewOnnxValueFloat32(lstmState1, stateShape)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create state1 tensor: %w", err)
	}
	defer state1Value.Close()

	state2Value, err := b.decoder.NewOnnxValueFloat32(lstmState2, stateShape)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create state2 tensor: %w", err)
	}
	defer state2Value.Close()

	// Run decoder.
	outputs, err := b.decoder.Run(
		[]string{"encoder_outputs", "targets", "target_length", "input_states_1", "input_states_2"},
		[]*OnnxValue{encValue, targetsValue, targetLenValue, state1Value, state2Value},
		[]string{"outputs", "prednet_lengths", "output_states_1", "output_states_2"},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decoder run: %w", err)
	}
	defer func() {
		for _, o := range outputs {
			o.Close()
		}
	}()

	// Extract joint logits. Output shape is [1, enc_time, target_time, 1030].
	// Since we feed one encoder frame and target sequence, we want logits at
	// position [0, 0, targetLen-1, :] (last target position, first encoder frame).
	rawLogits, err := outputs[0].GetFloat32Data()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get logits: %w", err)
	}

	logitsShape, err := outputs[0].Shape()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get logits shape: %w", err)
	}

	// Shape: [1, encTime, targetTime, 1030]
	if len(logitsShape) != 4 || logitsShape[3] != jointOutputSize {
		return nil, nil, nil, fmt.Errorf("unexpected logits shape: %v", logitsShape)
	}

	encTime := int(logitsShape[1])
	targetTime := int(logitsShape[2])

	// Extract logits at [0, 0, targetTime-1, :].
	// Offset = (0*encTime*targetTime + 0*targetTime + (targetTime-1)) * 1030
	offset := (targetTime - 1) * jointOutputSize
	if encTime > 1 {
		// Should not happen since we feed one frame, but handle gracefully.
		offset = 0*targetTime*jointOutputSize + (targetTime-1)*jointOutputSize
	}

	logits = make([]float32, jointOutputSize)
	if offset+jointOutputSize <= len(rawLogits) {
		copy(logits, rawLogits[offset:offset+jointOutputSize])
	} else {
		return nil, nil, nil, fmt.Errorf("logits offset out of bounds: offset=%d, len=%d", offset, len(rawLogits))
	}

	// Extract new LSTM states.
	rawState1, err := outputs[2].GetFloat32Data()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get state1: %w", err)
	}
	rawState2, err := outputs[3].GetFloat32Data()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get state2: %w", err)
	}

	newState1 = make([]float32, len(rawState1))
	copy(newState1, rawState1)
	newState2 = make([]float32, len(rawState2))
	copy(newState2, rawState2)

	return logits, newState1, newState2, nil
}

// tokensToWords converts decoded tokens (with probabilities and frame positions)
// to TranscriptWord slices with per-word confidence and inter-word gap timing.
// Tokens starting with ▁ (U+2581) indicate a word boundary.
//
// Confidence: average softmax probability of all tokens composing the word.
// GapBefore: milliseconds between the previous token's frame position and this
// word's first token frame position (only set at word boundaries with ▁ prefix).
func (b *NemotronBackend) tokensToWords(tokens []decodedToken) []TranscriptWord {
	if len(tokens) == 0 {
		return nil
	}

	var words []TranscriptWord
	var current strings.Builder
	var sumProb float32
	var tokenCount int
	var wordGapMs int
	var prevFramePos int = -1 // frame position of the last token processed

	for _, tok := range tokens {
		if int(tok.id) >= len(b.vocab) || tok.id == blankTokenID {
			continue
		}

		text := b.vocab[tok.id]

		// ▁ prefix indicates a new word boundary.
		if strings.HasPrefix(text, wordBoundary) {
			// Flush current word.
			if current.Len() > 0 {
				avgProb := sumProb / float32(tokenCount)
				words = append(words, TranscriptWord{
					Text:       current.String(),
					Confidence: avgProb,
					GapBefore:  wordGapMs,
				})
				current.Reset()
				sumProb = 0
				tokenCount = 0
			}

			// Compute gap between this word-boundary token and the previous token.
			wordGapMs = 0
			if prevFramePos >= 0 {
				gapFrames := tok.framePos - prevFramePos
				if gapFrames > 0 {
					wordGapMs = gapFrames * msPerEncoderFrame
				}
			}

			// Strip the word boundary marker.
			text = strings.TrimPrefix(text, wordBoundary)
		}

		if text != "" {
			current.WriteString(text)
			sumProb += tok.prob
			tokenCount++
		}
		prevFramePos = tok.framePos
	}

	// Flush final word.
	if current.Len() > 0 {
		avgProb := sumProb
		if tokenCount > 0 {
			avgProb /= float32(tokenCount)
		}
		words = append(words, TranscriptWord{
			Text:       current.String(),
			Confidence: avgProb,
			GapBefore:  wordGapMs,
		})
	}

	return words
}

// resetDecoderState initializes LSTM states and token history to zeros.
func (b *NemotronBackend) resetDecoderState() {
	stateSize := lstmNumLayers * 1 * lstmHiddenSize
	b.lstmState1 = make([]float32, stateSize)
	b.lstmState2 = make([]float32, stateSize)
	b.lastTokens = nil
}

// Reset clears the audio buffer, VAD, and inference state.
func (b *NemotronBackend) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ringBuf.Clear()
	b.vad.Reset()
	b.sileroVAD.Reset()
	b.resampler.Reset()
	b.resetDecoderState()
	b.wasSpeaking = false
	b.lastWordCount = 0
	b.emittedWords = nil
}

// Stop halts inference but keeps the model loaded.
func (b *NemotronBackend) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onTranscript = nil
	b.started = false
}

// Close releases all resources (ONNX sessions, GPU memory).
// Waits for any in-flight inference to complete before releasing.
func (b *NemotronBackend) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.started = false
	close(b.doneCh)
	b.mu.Unlock()

	// Wait for in-flight inference to finish before releasing ONNX sessions.
	b.inferring.Lock()
	b.inferring.Unlock()

	if b.melSession != nil {
		b.melSession.Close()
	}
	if b.encoder != nil {
		b.encoder.Close()
	}
	if b.decoder != nil {
		b.decoder.Close()
	}
	if b.vadSession != nil {
		b.vadSession.Close()
	}
}

// Models lists available Nemotron models. Currently only one model is supported.
func (b *NemotronBackend) Models() []string {
	return []string{"parakeet-tdt-0.6b-v2"}
}

// SetModel is a no-op since only one model is supported.
func (b *NemotronBackend) SetModel(name string) error {
	if name != "parakeet-tdt-0.6b-v2" {
		return fmt.Errorf("asr: nemotron: unknown model %q", name)
	}
	return nil
}

// ModelName returns the current model name.
func (b *NemotronBackend) ModelName() string {
	return "parakeet-tdt-0.6b-v2"
}

// Config returns the backend configuration as JSON.
func (b *NemotronBackend) Config() json.RawMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, _ := json.Marshal(b.cfg)
	return raw
}

// SetConfig applies backend configuration from JSON.
func (b *NemotronBackend) SetConfig(raw json.RawMessage) error {
	var cfg NemotronConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if cfg.ChunkMs > 0 {
		b.cfg.ChunkMs = cfg.ChunkMs
	}
	if cfg.Language != "" {
		b.cfg.Language = cfg.Language
	}

	return nil
}

// loadVocab reads the vocabulary file. Each line has format "token index".
// Returns a slice where vocab[index] = token.
func loadVocab(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var vocab []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Format: "token index" — split on last space since tokens can contain spaces.
		lastSpace := strings.LastIndex(line, " ")
		if lastSpace < 0 {
			// Fallback: token without index, append sequentially.
			vocab = append(vocab, line)
			continue
		}

		token := line[:lastSpace]
		// We don't need to parse the index — vocab.txt lines are sequential,
		// so append order matches index order.
		vocab = append(vocab, token)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(vocab) == 0 {
		return nil, fmt.Errorf("empty vocabulary file: %s", path)
	}

	return vocab, nil
}
