package main

import "github.com/zsiec/switchframe/server/asr"

// asrBackend abstracts an ASR inference engine inside the sidecar.
type asrBackend interface {
	Transcribe(audio []float32) []asr.TranscriptWord
	Name() string
	Models() []string
	ModelName() string
	SetModel(name string) error
	Config() map[string]interface{}
	SetConfig(cfg map[string]interface{})
	Close()
}
