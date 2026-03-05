//go:build !cgo

package transition

import (
	"errors"
	"log/slog"
)

var errCGODisabled = errors.New("video codec unavailable: built without cgo (install libopenh264-dev for full functionality)")

func init() {
	slog.Warn("transition: built without cgo — H.264 decode/encode disabled")
}

// NewOpenH264Decoder returns an error when cgo is not available.
func NewOpenH264Decoder() (VideoDecoder, error) {
	return nil, errCGODisabled
}

// NewOpenH264Encoder returns an error when cgo is not available.
func NewOpenH264Encoder(width, height, bitrate int, fps float32) (VideoEncoder, error) {
	return nil, errCGODisabled
}
