//go:build cgo

package audio

/*
#include <fdk-aac/aacdecoder_lib.h>
#include <string.h>

// aacdec wraps the FDK AAC decoder handle and state.
typedef struct {
	HANDLE_AACDECODER dec;
	int channels;
	int sample_rate;
	CStreamInfo* info;
} aacdec_t;

static int aacdec_open(aacdec_t* h, int sample_rate, int channels) {
	h->channels = channels;
	h->sample_rate = sample_rate;
	h->info = NULL;

	// Use ADTS transport — the encoder produces ADTS frames.
	h->dec = aacDecoder_Open(TT_MP4_ADTS, 1);
	if (!h->dec) {
		return -1;
	}
	// Pin PCM channel ordering to WAV order (L/R/C/LFE/Ls/Rs per ITU-R
	// BS.2051-3) so the Go-side surround downmix coefficients line up with
	// the actual decoder output regardless of FDK version defaults. Return
	// code is ignored: ancient FDK builds reject the param but default to
	// WAV order anyway, so a failure here is indistinguishable from success.
	aacDecoder_SetParam(h->dec, AAC_PCM_OUTPUT_CHANNEL_MAPPING, 1);
	return 0;
}

static void aacdec_close(aacdec_t* h) {
	if (h->dec) {
		aacDecoder_Close(h->dec);
		h->dec = NULL;
	}
}

// aacdec_decode fills the decoder buffer then decodes one frame.
// pcm_out must be pre-allocated to hold at least pcm_out_size INT_PCM samples.
// On success, *decoded_samples is set to the number of decoded PCM samples
// (total, all channels) and *decoded_channels to the interleaved channel count.
static int aacdec_decode(aacdec_t* h, unsigned char* aac_data, int aac_size,
                         INT_PCM* pcm_out, int pcm_out_size,
                         int* decoded_samples, int* decoded_channels) {
	UCHAR* buf = aac_data;
	UINT buf_size = (UINT)aac_size;
	UINT bytes_valid = buf_size;

	AAC_DECODER_ERROR err = aacDecoder_Fill(h->dec, &buf, &buf_size, &bytes_valid);
	if (err != AAC_DEC_OK) {
		return (int)err;
	}

	err = aacDecoder_DecodeFrame(h->dec, pcm_out, pcm_out_size, 0);
	if (err != AAC_DEC_OK) {
		return (int)err;
	}

	h->info = aacDecoder_GetStreamInfo(h->dec);
	if (h->info) {
		*decoded_samples = h->info->frameSize * h->info->numChannels;
		*decoded_channels = h->info->numChannels;
	} else {
		*decoded_samples = 0;
		*decoded_channels = 0;
	}
	return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

// FDKDecoder wraps the FDK AAC decoder (ADTS mode) and implements Decoder.
// It decodes ADTS-framed AAC data to interleaved float32 PCM.
//
// The decoder reuses internal buffers across calls. Callers must copy or
// consume the returned []float32 before the next Decode() call.
type FDKDecoder struct {
	handle       C.aacdec_t
	channels     int        // configured/expected channel count (for buffer sizing)
	lastChannels atomic.Int32 // channel count reported by the most recent successful Decode
	closed       atomic.Bool
	closeOnce    sync.Once
	pcmBuf       []int16   // reusable C decode buffer
	outBuf       []float32 // reusable float32 output buffer
	frameCount   uint32    // counts decoded frames (0-sample priming frames are expected early)
}

// NewFDKDecoder creates a new FDK AAC decoder for the given sample rate and channel count.
func NewFDKDecoder(sampleRate, channels int) (*FDKDecoder, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate: %d", sampleRate)
	}
	if channels < 1 || channels > 8 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1-8)", channels)
	}

	const maxSamples = 2048 * 8
	d := &FDKDecoder{
		channels: channels,
		pcmBuf:   make([]int16, maxSamples),
	}
	rc := C.aacdec_open(&d.handle, C.int(sampleRate), C.int(channels))
	if rc != 0 {
		return nil, fmt.Errorf("failed to open FDK AAC decoder: code %d", int(rc))
	}
	return d, nil
}

// Decode decodes an ADTS-framed AAC frame into interleaved float32 PCM.
func (d *FDKDecoder) Decode(aacFrame []byte) ([]float32, error) {
	if d.closed.Load() {
		return nil, errors.New("decoder is closed")
	}
	if len(aacFrame) == 0 {
		return nil, errors.New("empty AAC frame")
	}

	// Reuse pre-allocated pcmBuf (sized for 2048*8 samples at construction).
	var decodedSamples C.int
	var decodedChannels C.int

	rc := C.aacdec_decode(
		&d.handle,
		(*C.uchar)(unsafe.Pointer(&aacFrame[0])),
		C.int(len(aacFrame)),
		(*C.INT_PCM)(unsafe.Pointer(&d.pcmBuf[0])),
		C.int(len(d.pcmBuf)),
		&decodedSamples,
		&decodedChannels,
	)
	if rc != 0 {
		return nil, fmt.Errorf("FDK AAC decode error: code %d", int(rc))
	}

	n := int(decodedSamples)
	d.frameCount++
	if n == 0 {
		// AAC decoders need 1-2 "priming" frames to initialize internal
		// MDCT state. Return nil (not error) for the first 2 frames so
		// callers can skip gracefully instead of logging decode errors.
		if d.frameCount <= 2 {
			return nil, nil
		}
		return nil, errors.New("decoder produced no samples")
	}
	d.lastChannels.Store(int32(decodedChannels))

	// Convert int16 PCM to float32 [-1.0, 1.0] using reusable output buffer.
	// Callers must copy the result before the next Decode() call.
	if cap(d.outBuf) >= n {
		d.outBuf = d.outBuf[:n]
	} else {
		d.outBuf = make([]float32, n)
	}
	for i := 0; i < n; i++ {
		d.outBuf[i] = normalizeInt16(int16(d.pcmBuf[i]))
	}
	return d.outBuf, nil
}

// Channels returns the interleaved channel count of the most recent
// successful Decode. Returns 0 before the first non-empty decode.
func (d *FDKDecoder) Channels() int {
	return int(d.lastChannels.Load())
}

// Close releases the decoder resources. Safe to call multiple times
// and concurrently from multiple goroutines.
func (d *FDKDecoder) Close() error {
	d.closeOnce.Do(func() {
		d.closed.Store(true)
		C.aacdec_close(&d.handle)
	})
	return nil
}
