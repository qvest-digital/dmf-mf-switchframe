//go:build cgo && !noopus

package comms

import (
	"errors"
	"fmt"

	"gopkg.in/hraban/opus.v2"
)

var errOpusNotAvailable = errors.New("opus codec unavailable: built without cgo or with noopus tag")

const opusAvailable = true

// opusEncoder wraps the hraban/opus encoder for voice communication.
type opusEncoder struct {
	enc *opus.Encoder
}

// newOpusEncoder creates a new Opus encoder configured for voice communication.
// Sample rate must be 8000, 12000, 16000, 24000, or 48000.
// Channels must be 1 (mono) or 2 (stereo).
func newOpusEncoder(sampleRate, channels int) (*opusEncoder, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate: %d", sampleRate)
	}
	if channels < 1 || channels > 2 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1 or 2)", channels)
	}

	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("failed to create opus encoder: %w", err)
	}

	if err := enc.SetBitrate(32000); err != nil {
		return nil, fmt.Errorf("failed to set opus bitrate: %w", err)
	}

	return &opusEncoder{enc: enc}, nil
}

// Encode encodes PCM int16 samples into Opus. frameSize is the number of
// samples per channel. buf receives the encoded data. Returns the number
// of bytes written to buf.
func (e *opusEncoder) Encode(pcm []int16, frameSize int, buf []byte) (int, error) {
	return e.enc.Encode(pcm, buf)
}

// opusDecoder wraps the hraban/opus decoder for voice communication.
type opusDecoder struct {
	dec *opus.Decoder
}

// newOpusDecoder creates a new Opus decoder.
// Sample rate must be 8000, 12000, 16000, 24000, or 48000.
// Channels must be 1 (mono) or 2 (stereo).
func newOpusDecoder(sampleRate, channels int) (*opusDecoder, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate: %d", sampleRate)
	}
	if channels < 1 || channels > 2 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1 or 2)", channels)
	}

	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("failed to create opus decoder: %w", err)
	}

	return &opusDecoder{dec: dec}, nil
}

// Decode decodes Opus data into PCM int16 samples. frameSize is the number
// of samples per channel that the output buffer can hold. Returns the number
// of decoded samples per channel.
func (d *opusDecoder) Decode(data []byte, pcm []int16, frameSize int) (int, error) {
	return d.dec.Decode(data, pcm)
}
