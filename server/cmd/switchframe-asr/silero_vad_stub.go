//go:build !cgo || !cuda || !tensorrt

package main

import "unsafe"

type sileroVAD struct {
	sileroVADState
	buf []float32
}

func newSileroVAD(modelPath string, stream unsafe.Pointer, threshold float64, hangoverMs int) (*sileroVAD, error) {
	return &sileroVAD{
		sileroVADState: newSileroVADState(threshold, hangoverMs),
	}, nil
}

func (v *sileroVAD) process(samples []float32) {
	v.buf = append(v.buf, samples...)
	for len(v.buf) >= sileroChunkSize {
		v.buf = v.buf[sileroChunkSize:]
	}
}

func (v *sileroVAD) isSpeaking() bool { return v.speaking }

func (v *sileroVAD) reset() {
	v.buf = v.buf[:0]
	v.speaking = false
	v.hangover = 0
}

func (v *sileroVAD) close() {}
