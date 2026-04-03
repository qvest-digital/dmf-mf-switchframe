package main

import "github.com/zsiec/switchframe/server/asr"

// trtASRBackend wraps the TensorRT-based inferenceEngine as an asrBackend.
type trtASRBackend struct {
	engine *inferenceEngine
}

func newTRTASRBackend(modelDir string, deviceID int) (*trtASRBackend, error) {
	engine, err := newInferenceEngine(modelDir, deviceID)
	if err != nil {
		return nil, err
	}
	return &trtASRBackend{engine: engine}, nil
}

func (b *trtASRBackend) Transcribe(audio []float32) []asr.TranscriptWord {
	return b.engine.Transcribe(audio)
}

func (b *trtASRBackend) Name() string      { return "parakeet" }
func (b *trtASRBackend) Models() []string  { return []string{"parakeet-tdt-1.1b"} }
func (b *trtASRBackend) ModelName() string { return "parakeet-tdt-1.1b" }
func (b *trtASRBackend) SetModel(string) error {
	return nil
}

func (b *trtASRBackend) Config() map[string]interface{}   { return nil }
func (b *trtASRBackend) SetConfig(map[string]interface{}) {}

func (b *trtASRBackend) Close() { b.engine.backend.close() }
