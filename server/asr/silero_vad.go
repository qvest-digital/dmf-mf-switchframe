//go:build cgo && cuda

package asr

import (
	"fmt"
	"log/slog"
)

const (
	// sileroChunkSize is the fixed input size for the Silero VAD model.
	// The model expects exactly 512 samples (32ms at 16kHz).
	sileroChunkSize = 512

	// sileroStateSize is the recurrent hidden state size: [2, 1, 128].
	sileroStateSize = 2 * 1 * 128

	// sileroSampleRate is the expected sample rate for the Silero VAD model.
	sileroSampleRate = 16000

	// sileroChunkDurationMs is the duration of one 512-sample chunk in milliseconds.
	sileroChunkDurationMs = 32 // 512 / 16000 * 1000
)

// SileroVAD implements voice activity detection using the Silero VAD ONNX model.
// It processes 16kHz mono audio in 512-sample (32ms) chunks and maintains
// recurrent hidden state across calls for temporal context.
//
// The detector is not goroutine-safe; callers must synchronize access.
type SileroVAD struct {
	session   *OnnxSession
	state     []float32 // [2, 1, 128] recurrent state
	buf       []float32 // accumulates samples until 512
	threshold float64   // speech probability threshold (default 0.5)

	speaking   bool
	hangover   int // remaining hangover in milliseconds
	hangoverMs int // configured hangover duration
	debugCount int // for periodic debug logging
}

// NewSileroVAD creates a new Silero VAD instance.
// The session should be loaded from silero_vad.onnx (CPU execution is recommended).
// threshold is the speech probability threshold (0.0-1.0, default 0.5).
// hangoverMs is how long to stay in speech state after probability drops (default 500ms).
func NewSileroVAD(session *OnnxSession, threshold float64, hangoverMs int) *SileroVAD {
	if threshold <= 0 {
		threshold = 0.5
	}
	if hangoverMs <= 0 {
		hangoverMs = 500
	}
	return &SileroVAD{
		session:    session,
		state:      make([]float32, sileroStateSize),
		threshold:  threshold,
		hangoverMs: hangoverMs,
	}
}

// Process feeds 16kHz mono samples to the VAD. It buffers samples internally
// and runs ONNX inference for each complete 512-sample chunk. The IsSpeaking()
// result is updated after each chunk.
func (v *SileroVAD) Process(samples []float32) {
	if len(samples) == 0 {
		return
	}

	v.buf = append(v.buf, samples...)

	// Process all complete 512-sample chunks.
	for len(v.buf) >= sileroChunkSize {
		chunk := v.buf[:sileroChunkSize]
		v.buf = v.buf[sileroChunkSize:]

		prob, err := v.infer(chunk)
		if err != nil {
			slog.Warn("ASR silero VAD: inference failed", "error", err)
			continue
		}

		// Log every ~1 second (31 chunks * 32ms ≈ 1s) when prob > 0.01
		// to diagnose VAD sensitivity on broadcast audio.
		v.debugCount++
		if v.debugCount%31 == 0 {
			slog.Info("ASR silero VAD probe", "prob", fmt.Sprintf("%.6f", prob), "threshold", v.threshold, "speaking", v.speaking)
		}

		if prob > v.threshold {
			v.speaking = true
			v.hangover = v.hangoverMs
		} else if v.speaking {
			v.hangover -= sileroChunkDurationMs
			if v.hangover <= 0 {
				v.speaking = false
				v.hangover = 0
			}
		}
	}
}

// IsSpeaking returns whether speech is currently detected.
// This includes the hangover period after speech probability drops.
func (v *SileroVAD) IsSpeaking() bool {
	return v.speaking
}

// Reset clears the recurrent state, sample buffer, and speech state.
func (v *SileroVAD) Reset() {
	v.state = make([]float32, sileroStateSize)
	v.buf = v.buf[:0]
	v.speaking = false
	v.hangover = 0
}

// infer runs the Silero VAD model on a single 512-sample chunk.
// Returns the speech probability (0.0-1.0).
func (v *SileroVAD) infer(chunk []float32) (float64, error) {
	if len(chunk) != sileroChunkSize {
		return 0, fmt.Errorf("silero VAD: expected %d samples, got %d", sileroChunkSize, len(chunk))
	}

	// Input: [1, 512] float32.
	inputShape := []int64{1, sileroChunkSize}
	inputValue, err := v.session.NewOnnxValueFloat32(chunk, inputShape)
	if err != nil {
		return 0, fmt.Errorf("create input tensor: %w", err)
	}
	defer inputValue.Close()

	// State: [2, 1, 128] float32.
	stateShape := []int64{2, 1, 128}
	stateValue, err := v.session.NewOnnxValueFloat32(v.state, stateShape)
	if err != nil {
		return 0, fmt.Errorf("create state tensor: %w", err)
	}
	defer stateValue.Close()

	// Sample rate: scalar int64 = 16000.
	srData := []int64{sileroSampleRate}
	srShape := []int64{1}
	srValue, err := v.session.NewOnnxValueInt64(srData, srShape)
	if err != nil {
		return 0, fmt.Errorf("create sr tensor: %w", err)
	}
	defer srValue.Close()

	// Run inference.
	outputs, err := v.session.Run(
		[]string{"input", "state", "sr"},
		[]*OnnxValue{inputValue, stateValue, srValue},
		[]string{"output", "stateN"},
	)
	if err != nil {
		return 0, fmt.Errorf("silero VAD run: %w", err)
	}
	defer func() {
		for _, o := range outputs {
			o.Close()
		}
	}()

	// Extract speech probability from output [1, 1].
	probData, err := outputs[0].GetFloat32Data()
	if err != nil {
		return 0, fmt.Errorf("get output data: %w", err)
	}
	if len(probData) == 0 {
		return 0, fmt.Errorf("empty output tensor")
	}

	// Update recurrent state from stateN [2, 1, 128].
	newState, err := outputs[1].GetFloat32Data()
	if err != nil {
		return 0, fmt.Errorf("get state data: %w", err)
	}
	if len(newState) >= sileroStateSize {
		copy(v.state, newState[:sileroStateSize])
	}

	return float64(probData[0]), nil
}
