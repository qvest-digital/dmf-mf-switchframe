package main

import (
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// decoderFactory returns a factory that creates video decoders using the
// auto-detected codec (NVENC/VA-API/VideoToolbox/libx264/OpenH264).
func decoderFactory() transition.DecoderFactory {
	return func() (transition.VideoDecoder, error) {
		return codec.NewVideoDecoder()
	}
}

// encoderFactory returns a factory that creates video encoders using the
// auto-detected codec.
func encoderFactory() transition.EncoderFactory {
	return func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
		return codec.NewVideoEncoder(w, h, bitrate, fps)
	}
}

// audioDecoderFactory returns a factory that creates FDK AAC decoders.
func audioDecoderFactory() func(sampleRate, channels int) (audio.AudioDecoder, error) {
	return func(sampleRate, channels int) (audio.AudioDecoder, error) {
		return audio.NewFDKDecoder(sampleRate, channels)
	}
}

// audioEncoderFactory returns a factory that creates FDK AAC encoders.
func audioEncoderFactory() func(sampleRate, channels int) (audio.AudioEncoder, error) {
	return func(sampleRate, channels int) (audio.AudioEncoder, error) {
		return audio.NewFDKEncoder(sampleRate, channels)
	}
}
