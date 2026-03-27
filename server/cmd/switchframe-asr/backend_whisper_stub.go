//go:build !cgo || !cuda || !tensorrt

package main

import (
	"fmt"

	"github.com/zsiec/switchframe/server/asr"
)

type whisperASRBackend struct{}

func newWhisperASRBackend(modelsDir string, useGPU bool) (*whisperASRBackend, error) {
	return nil, fmt.Errorf("whisper not available (non-cgo build)")
}

func (b *whisperASRBackend) Transcribe([]float32) []asr.TranscriptWord { return nil }
func (b *whisperASRBackend) Name() string                               { return "whisper" }
func (b *whisperASRBackend) Models() []string                           { return nil }
func (b *whisperASRBackend) ModelName() string                          { return "" }
func (b *whisperASRBackend) SetModel(string) error {
	return fmt.Errorf("whisper not available")
}
func (b *whisperASRBackend) Config() map[string]interface{} { return nil }
func (b *whisperASRBackend) SetConfig(map[string]interface{})                {}
func (b *whisperASRBackend) Close()                                          {}
