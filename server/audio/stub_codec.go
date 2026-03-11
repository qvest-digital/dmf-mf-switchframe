//go:build !cgo

package audio

import (
	"errors"
	"log/slog"
)

var errCGODisabled = errors.New("audio codec unavailable: built without cgo (install libfdk-aac-dev for full functionality)")

func init() {
	slog.Warn("audio: built without cgo — AAC decode/encode disabled")
}

// NewFDKDecoder returns an error when cgo is not available.
func NewFDKDecoder(sampleRate, channels int) (Decoder, error) {
	return nil, errCGODisabled
}

// NewFDKEncoder returns an error when cgo is not available.
func NewFDKEncoder(sampleRate, channels int) (Encoder, error) {
	return nil, errCGODisabled
}
