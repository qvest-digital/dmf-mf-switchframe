package main

import (
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// decoderFactory returns a factory that creates video decoders using the
// auto-detected codec (NVENC/VA-API/VideoToolbox/libx264/OpenH264).
func decoderFactory() transition.DecoderFactory {
	return func() (transition.VideoDecoder, error) {
		return codec.NewVideoDecoder()
	}
}

// decoderFactoryForColorFormat returns a decoder factory appropriate for the
// pipeline color format. When the pipeline is in professional mode (10-bit 422),
// decoders preserve native 10-bit output to avoid the lossy round-trip of
// downconverting to 8-bit 420 then upconverting back.
func decoderFactoryForColorFormat(colorFmt switcher.PipelineColorFormat) transition.DecoderFactory {
	if colorFmt == switcher.ColorYUV422_10bit {
		return func() (transition.VideoDecoder, error) {
			return codec.NewVideoDecoderNative10bit()
		}
	}
	return decoderFactory()
}

// encoderFactory returns a factory that creates AVC (H.264) video encoders using
// the auto-detected codec. The encoder always uses cVBR (ABR + tight VBV).
func encoderFactory() transition.EncoderFactory {
	return func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return codec.NewVideoEncoder(w, h, bitrate, fpsNum, fpsDen)
	}
}

// encoderFactoryForCodec returns an encoder factory for the specified pipeline
// codec and color format. For HEVC with 10-bit 4:2:2, it returns a 10-bit HEVC
// encoder factory. For HEVC with 8-bit, a standard HEVC factory. For AVC
// (default), the standard H.264 encoder factory.
func encoderFactoryForCodec(pipelineCodec switcher.PipelineCodec, colorFmt ...switcher.PipelineColorFormat) transition.EncoderFactory {
	if pipelineCodec == switcher.CodecHEVC {
		// Check if 10-bit mode is requested.
		if len(colorFmt) > 0 && colorFmt[0] == switcher.ColorYUV422_10bit {
			return func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
				return codec.NewHEVCVideoEncoder10bit(w, h, bitrate, fpsNum, fpsDen)
			}
		}
		return func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return codec.NewHEVCVideoEncoder(w, h, bitrate, fpsNum, fpsDen)
		}
	}
	return encoderFactory()
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
