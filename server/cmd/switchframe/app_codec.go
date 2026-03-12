package main

import (
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/mxl"
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
// auto-detected codec. The encoder always uses cVBR (ABR + tight VBV).
func encoderFactory() transition.EncoderFactory {
	return func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return codec.NewVideoEncoder(w, h, bitrate, fpsNum, fpsDen)
	}
}

// audioDecoderFactory returns a factory that creates FDK AAC decoders.
func audioDecoderFactory() func(sampleRate, channels int) (audio.Decoder, error) {
	return func(sampleRate, channels int) (audio.Decoder, error) {
		return audio.NewFDKDecoder(sampleRate, channels)
	}
}

// audioEncoderFactory returns a factory that creates FDK AAC encoders.
func audioEncoderFactory() func(sampleRate, channels int) (audio.Encoder, error) {
	return func(sampleRate, channels int) (audio.Encoder, error) {
		return audio.NewFDKEncoder(sampleRate, channels)
	}
}

// audioEncoderFactoryForMXL returns a factory compatible with mxl.AudioEnc.
// audio.Encoder satisfies mxl.AudioEnc (same Encode/Close methods).
func audioEncoderFactoryForMXL() func(sampleRate, channels int) (mxl.AudioEnc, error) {
	return func(sampleRate, channels int) (mxl.AudioEnc, error) {
		return audio.NewFDKEncoder(sampleRate, channels)
	}
}
