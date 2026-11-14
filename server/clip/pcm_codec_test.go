//go:build cgo

package clip

import "github.com/zsiec/switchframe/server/audio"

// testPCMCodec is the codec the engine decodes clips with (see
// internal/filepcm, which imports this package).
var testPCMCodec = PCMCodec{
	NewDecoder: func(sampleRate, channels int) (PCMDecoder, error) {
		return audio.NewFDKFileDecoder(sampleRate, channels)
	},
	NewResampler: func(srcRate, dstRate, channels int) PCMResampler {
		return audio.NewResampler(srcRate, dstRate, channels)
	},
}
