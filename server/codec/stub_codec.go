//go:build !cgo || !openh264

package codec

import (
	"errors"
	"log/slog"
)

var errOpenH264Disabled = errors.New("OpenH264 codec unavailable: built without openh264 build tag (use -tags openh264)")

func init() {
	slog.Debug("codec: OpenH264 not available (built without openh264 tag)")
}

// OpenH264Decoder is a stub for builds without the openh264 build tag.
type OpenH264Decoder struct{}

// NewOpenH264Decoder returns an error when the openh264 build tag is not set.
func NewOpenH264Decoder() (*OpenH264Decoder, error) {
	return nil, errOpenH264Disabled
}

// Decode is a stub that always returns an error.
func (d *OpenH264Decoder) Decode(data []byte) ([]byte, int, int, error) {
	return nil, 0, 0, errOpenH264Disabled
}

// Close is a no-op stub.
func (d *OpenH264Decoder) Close() {}

// OpenH264Encoder is a stub for builds without the openh264 build tag.
type OpenH264Encoder struct{}

// NewOpenH264Encoder returns an error when the openh264 build tag is not set.
func NewOpenH264Encoder(width, height, bitrate int, fps float32) (*OpenH264Encoder, error) {
	return nil, errOpenH264Disabled
}

// Encode is a stub that always returns an error.
func (e *OpenH264Encoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	return nil, false, errOpenH264Disabled
}

// Close is a no-op stub.
func (e *OpenH264Encoder) Close() {}
