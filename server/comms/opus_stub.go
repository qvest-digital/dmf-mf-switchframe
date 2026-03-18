//go:build !cgo || noopus

package comms

import (
	"errors"
	"log/slog"
)

const opusAvailable = false

var errOpusNotAvailable = errors.New("opus codec unavailable: built without cgo or with noopus tag")

func init() {
	slog.Warn("comms: built without opus — voice communication codec disabled")
}

// opusEncoder is a stub for builds without Opus support.
type opusEncoder struct{}

// newOpusEncoder returns an error when Opus is not available.
func newOpusEncoder(sampleRate, channels int) (*opusEncoder, error) {
	return nil, errOpusNotAvailable
}

// Encode always returns an error when Opus is not available.
func (e *opusEncoder) Encode(pcm []int16, frameSize int, buf []byte) (int, error) {
	return 0, errOpusNotAvailable
}

// opusDecoder is a stub for builds without Opus support.
type opusDecoder struct{}

// newOpusDecoder returns an error when Opus is not available.
func newOpusDecoder(sampleRate, channels int) (*opusDecoder, error) {
	return nil, errOpusNotAvailable
}

// Decode always returns an error when Opus is not available.
func (d *opusDecoder) Decode(data []byte, pcm []int16, frameSize int) (int, error) {
	return 0, errOpusNotAvailable
}
