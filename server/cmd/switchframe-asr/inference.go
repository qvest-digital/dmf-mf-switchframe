package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/zsiec/switchframe/server/asr"
)

// modelConfig holds metadata loaded from config.json.
type modelConfig struct {
	Model             string `json:"model"`
	ChunkMs           int    `json:"chunk_ms"`
	SampleRate        int    `json:"sample_rate"`
	MelBins           int    `json:"mel_bins"`
	EncoderDim        int    `json:"encoder_dim"`
	VocabSize         int    `json:"vocab_size"`
	NumDurationBins   int    `json:"num_duration_bins"`
	BlankTokenID      int    `json:"blank_token_id"`
	SubsamplingFactor int    `json:"subsampling_factor"`
}

func loadModelConfig(modelDir string) (*modelConfig, error) {
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	var cfg modelConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// decodedToken represents a single token emitted by the TDT decoder.
type decodedToken struct {
	id       int32
	prob     float32 // softmax probability
	framePos int     // encoder frame position when emitted
}

// tokenAssembler converts a stream of decoded tokens into TranscriptWords.
// It buffers tokens until a SentencePiece word boundary (▁ prefix) is hit,
// then emits the buffered word.
type tokenAssembler struct {
	vocab      []string
	blankID    int32
	pending    []decodedToken // tokens for the current word
	lastFrame  int            // frame position of last emitted word (for gap calculation)
	msPerFrame int            // milliseconds per encoder frame
}

func newTokenAssembler(vocab []string, blankID int32, msPerFrame int) *tokenAssembler {
	return &tokenAssembler{
		vocab:      vocab,
		blankID:    blankID,
		lastFrame:  -1,
		msPerFrame: msPerFrame,
	}
}

// feed processes decoded tokens and returns any completed words.
// A word is complete when the next token starts with ▁ (SentencePiece boundary).
func (ta *tokenAssembler) feed(tokens []decodedToken) []asr.TranscriptWord {
	var words []asr.TranscriptWord
	for _, tok := range tokens {
		if tok.id == ta.blankID {
			continue // skip blank tokens
		}
		if tok.id < 0 || int(tok.id) >= len(ta.vocab) {
			continue // skip invalid tokens
		}

		text := ta.vocab[tok.id]
		isWordStart := len(text) >= 3 && text[0] == '\xe2' && text[1] == '\x96' && text[2] == '\x81' // ▁ in UTF-8

		// If this token starts a new word and we have pending tokens, flush the current word.
		if isWordStart && len(ta.pending) > 0 {
			word := ta.assembleWord()
			words = append(words, word)
		}

		ta.pending = append(ta.pending, tok)
	}
	return words
}

// flush emits any remaining buffered tokens as a final word.
func (ta *tokenAssembler) flush() []asr.TranscriptWord {
	if len(ta.pending) == 0 {
		return nil
	}
	word := ta.assembleWord()
	return []asr.TranscriptWord{word}
}

// assembleWord creates a TranscriptWord from pending tokens and clears the buffer.
func (ta *tokenAssembler) assembleWord() asr.TranscriptWord {
	// Build text by concatenating token texts, stripping ▁ prefix.
	var text string
	for _, tok := range ta.pending {
		t := ta.vocab[tok.id]
		// Strip SentencePiece word boundary marker.
		if len(t) >= 3 && t[0] == '\xe2' && t[1] == '\x96' && t[2] == '\x81' {
			t = t[3:] // strip ▁ (3 bytes in UTF-8)
		}
		text += t
	}

	// Confidence: geometric mean of token probabilities.
	var logSum float64
	for _, tok := range ta.pending {
		if tok.prob > 0 {
			logSum += math.Log(float64(tok.prob))
		}
	}
	confidence := float32(math.Exp(logSum / float64(len(ta.pending))))

	// Gap: time between last emitted word and first token of this word.
	gapBefore := 0
	if ta.lastFrame >= 0 && len(ta.pending) > 0 {
		frameDelta := ta.pending[0].framePos - ta.lastFrame
		if frameDelta > 1 {
			gapBefore = frameDelta * ta.msPerFrame
		}
	}

	// Update lastFrame to the last token's position.
	if len(ta.pending) > 0 {
		ta.lastFrame = ta.pending[len(ta.pending)-1].framePos
	}

	ta.pending = ta.pending[:0]

	return asr.TranscriptWord{
		Text:       text,
		Confidence: confidence,
		GapBefore:  gapBefore,
	}
}

// reset clears the token assembler state.
func (ta *tokenAssembler) reset() {
	ta.pending = ta.pending[:0]
	ta.lastFrame = -1
}

// inferenceEngine orchestrates the batch inference pipeline.
// It runs mel → encoder → greedy decode on complete speech segments
// triggered by VAD speech-end events.
type inferenceEngine struct {
	config    *modelConfig
	vocab     []string
	assembler *tokenAssembler
	backend   inferenceBackend // TRT or stub
}

// inferenceBackend abstracts the actual model inference for batch mode.
type inferenceBackend interface {
	// runMel computes mel spectrogram from raw 16kHz mono audio.
	// Returns mel features as flat float32 and the number of mel frames.
	runMel(audio []float32) (melFeatures []float32, melFrames int, err error)
	// runEncoder runs the batch encoder on mel features.
	// Input: mel features [1, melBins, melFrames], melFrames count.
	// Output: encoded features as flat float32, number of encoded frames.
	runEncoder(melFeatures []float32, melFrames int) (encoded []float32, encodedFrames int, err error)
	// runDecoder runs one TDT decoder step.
	runDecoder(encoderFrame []float32, targets []int32, states1, states2 []float32) (logits []float32, newStates1, newStates2 []float32, err error)
	// gpuStream returns the CUDA stream pointer (nil for stub backends).
	gpuStream() unsafe.Pointer
	// close releases resources.
	close()
}

func newInferenceEngine(modelDir string, deviceID int) (*inferenceEngine, error) {
	cfg, err := loadModelConfig(modelDir)
	if err != nil {
		return nil, err
	}

	vocab, err := loadVocab(filepath.Join(modelDir, "vocab.txt"))
	if err != nil {
		return nil, err
	}

	backend, err := newInferenceBackend(modelDir, deviceID, cfg)
	if err != nil {
		return nil, err
	}

	msPerFrame := cfg.SubsamplingFactor * 10
	if msPerFrame == 0 {
		msPerFrame = cfg.ChunkMs
	}

	return &inferenceEngine{
		config:    cfg,
		vocab:     vocab,
		assembler: newTokenAssembler(vocab, int32(cfg.BlankTokenID), msPerFrame),
		backend:   backend,
	}, nil
}

// Transcribe runs the full mel -> encoder -> decoder pipeline on a complete
// speech segment and returns filtered transcript words.
func (e *inferenceEngine) Transcribe(audio []float32) []asr.TranscriptWord {
	if e.backend == nil || len(audio) < 8000 {
		return nil
	}
	words, err := e.runBatchInference(audio)
	if err != nil {
		slog.Warn("inference failed", "error", err)
		return nil
	}
	return filterWords(words)
}

// runBatchInference runs the full mel -> encoder -> greedy decode pipeline
// on a window of audio samples.
func (e *inferenceEngine) runBatchInference(audio []float32) ([]asr.TranscriptWord, error) {
	// Step 1: Mel spectrogram.
	melFeatures, melFrames, err := e.backend.runMel(audio)
	if err != nil {
		return nil, fmt.Errorf("mel frontend: %w", err)
	}
	if melFrames == 0 {
		return nil, nil
	}

	// Step 2: Encoder.
	encoded, encodedFrames, err := e.backend.runEncoder(melFeatures, melFrames)
	if err != nil {
		return nil, fmt.Errorf("encoder: %w", err)
	}
	if encodedFrames == 0 {
		return nil, nil
	}

	// Step 3: Greedy decode with fresh decoder state.
	words := e.greedyDecode(encoded, encodedFrames)

	return words, nil
}

// greedyDecode performs TDT greedy decoding on encoder output.
// Uses fresh decoder state for each batch window (no streaming state).
func (e *inferenceEngine) greedyDecode(encoded []float32, encodedFrames int) []asr.TranscriptWord {
	var tokens []decodedToken
	var lstmState1, lstmState2 []float32
	var lastTokens []int32
	encFrame := 0

	for encFrame < encodedFrames {
		// Extract single encoder frame.
		frameStart := encFrame * e.config.EncoderDim
		frameEnd := frameStart + e.config.EncoderDim
		if frameEnd > len(encoded) {
			break
		}
		frame := encoded[frameStart:frameEnd]

		logits, newS1, newS2, err := e.backend.runDecoder(frame, lastTokens, lstmState1, lstmState2)
		if err != nil {
			slog.Warn("decoder failed", "error", err)
			break
		}
		lstmState1 = newS1
		lstmState2 = newS2

		vocabSize := e.config.VocabSize

		// Argmax over vocab for token.
		bestToken := int32(0)
		bestScore := logits[0]
		for i := int32(1); i < int32(vocabSize); i++ {
			if logits[i] > bestScore {
				bestScore = logits[i]
				bestToken = i
			}
		}

		// Softmax probability for confidence.
		maxLogit := logits[0]
		for i := 1; i < vocabSize; i++ {
			if logits[i] > maxLogit {
				maxLogit = logits[i]
			}
		}
		var sumExp float64
		for i := 0; i < vocabSize; i++ {
			sumExp += math.Exp(float64(logits[i] - maxLogit))
		}
		prob := float32(math.Exp(float64(logits[bestToken]-maxLogit)) / sumExp)

		// Duration prediction (last numDurationBins entries).
		bestDur := 0
		bestDurScore := logits[vocabSize]
		for d := 1; d < e.config.NumDurationBins; d++ {
			if logits[vocabSize+d] > bestDurScore {
				bestDurScore = logits[vocabSize+d]
				bestDur = d
			}
		}

		if bestToken != int32(e.config.BlankTokenID) {
			tokens = append(tokens, decodedToken{
				id:       bestToken,
				prob:     prob,
				framePos: encFrame,
			})
			lastTokens = append(lastTokens, bestToken)
			if len(lastTokens) > 128 {
				lastTokens = lastTokens[len(lastTokens)-128:]
			}
		}

		skip := bestDur
		if skip < 1 {
			skip = 1
		}
		encFrame += skip
	}

	// Convert tokens to words using a temporary assembler (fresh state per window).
	tmpAssembler := newTokenAssembler(e.vocab, int32(e.config.BlankTokenID), e.assembler.msPerFrame)
	words := tmpAssembler.feed(tokens)
	flushed := tmpAssembler.flush()
	words = append(words, flushed...)

	return words
}

// resetState clears all inference state, allowing a fresh start.
func (e *inferenceEngine) resetState() {
	e.assembler.reset()
}

func loadVocab(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load vocab: %w", err)
	}
	var vocab []string
	for _, line := range splitLines(data) {
		if line != "" {
			vocab = append(vocab, line)
		}
	}
	return vocab, nil
}

func splitLines(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}
