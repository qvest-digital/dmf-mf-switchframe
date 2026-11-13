// Package filepcm gives the clip package the decoder and resampler it
// decodes a clip's audio to PCM with.
package filepcm

import (
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clip"
)

// Codec decodes with the FDK decoder that adds no delay of its own
// (audio.NewFDKFileDecoder) and resamples with audio.Resampler.
func Codec() clip.PCMCodec {
	return clip.PCMCodec{
		NewDecoder: func(sampleRate, channels int) (clip.PCMDecoder, error) {
			return audio.NewFDKFileDecoder(sampleRate, channels)
		},
		NewResampler: func(srcRate, dstRate, channels int) clip.PCMResampler {
			return audio.NewResampler(srcRate, dstRate, channels)
		},
	}
}
