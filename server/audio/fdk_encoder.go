//go:build cgo

package audio

/*
#include <fdk-aac/aacenc_lib.h>
#include <string.h>

// aacenc wraps the FDK AAC encoder handle and state.
typedef struct {
	HANDLE_AACENCODER enc;
	int channels;
	int sample_rate;
	int frame_size;    // samples per channel per frame (1024 for AAC-LC)
	int max_out_bytes; // max output buffer size
} aacenc_t;

static int aacenc_open(aacenc_t* h, int sample_rate, int channels, int bitrate) {
	AACENC_ERROR err;
	h->channels = channels;
	h->sample_rate = sample_rate;

	if ((err = aacEncOpen(&h->enc, 0, channels)) != AACENC_OK) {
		return (int)err;
	}

	// AAC-LC
	if ((err = aacEncoder_SetParam(h->enc, AACENC_AOT, 2)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	if ((err = aacEncoder_SetParam(h->enc, AACENC_SAMPLERATE, sample_rate)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	// Channel mode: MODE_1 for mono, MODE_2 for stereo, etc.
	CHANNEL_MODE mode;
	switch (channels) {
		case 1: mode = MODE_1; break;
		case 2: mode = MODE_2; break;
		case 3: mode = MODE_1_2; break;
		case 4: mode = MODE_1_2_1; break;
		case 5: mode = MODE_1_2_2; break;
		case 6: mode = MODE_1_2_2_1; break;
		default:
			aacEncClose(&h->enc);
			return -1;
	}
	if ((err = aacEncoder_SetParam(h->enc, AACENC_CHANNELMODE, mode)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	// WAV channel ordering (interleaved L,R for stereo).
	if ((err = aacEncoder_SetParam(h->enc, AACENC_CHANNELORDER, 1)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	if ((err = aacEncoder_SetParam(h->enc, AACENC_BITRATE, bitrate)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	// ADTS transport (type 2) — includes sync headers for the decoder.
	if ((err = aacEncoder_SetParam(h->enc, AACENC_TRANSMUX, 2)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	// Enable afterburner for better quality.
	if ((err = aacEncoder_SetParam(h->enc, AACENC_AFTERBURNER, 1)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	// Initialize the encoder.
	if ((err = aacEncEncode(h->enc, NULL, NULL, NULL, NULL)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	// Get encoder info.
	AACENC_InfoStruct info;
	if ((err = aacEncInfo(h->enc, &info)) != AACENC_OK) {
		aacEncClose(&h->enc);
		return (int)err;
	}

	h->frame_size = info.frameLength;
	h->max_out_bytes = info.maxOutBufBytes;

	return 0;
}

static void aacenc_close(aacenc_t* h) {
	if (h->enc) {
		aacEncClose(&h->enc);
		h->enc = NULL;
	}
}

// aacenc_encode encodes one frame of 16-bit PCM to AAC (ADTS).
// pcm contains frame_size * channels interleaved INT_PCM samples.
// aac_out must be pre-allocated to hold at least max_out_bytes.
// On success, *out_bytes is set to the number of encoded bytes.
static int aacenc_encode(aacenc_t* h, INT_PCM* pcm, int num_samples,
                         unsigned char* aac_out, int aac_out_size, int* out_bytes) {
	AACENC_ERROR err;

	INT in_identifier = IN_AUDIO_DATA;
	INT in_elem_size = sizeof(INT_PCM);
	INT in_buf_size = num_samples * sizeof(INT_PCM);
	void* in_buf = pcm;

	AACENC_BufDesc in_desc = {0};
	in_desc.numBufs = 1;
	in_desc.bufs = &in_buf;
	in_desc.bufferIdentifiers = &in_identifier;
	in_desc.bufSizes = &in_buf_size;
	in_desc.bufElSizes = &in_elem_size;

	INT out_identifier = OUT_BITSTREAM_DATA;
	INT out_elem_size = 1;
	INT out_buf_size = aac_out_size;
	void* out_buf = aac_out;

	AACENC_BufDesc out_desc = {0};
	out_desc.numBufs = 1;
	out_desc.bufs = &out_buf;
	out_desc.bufferIdentifiers = &out_identifier;
	out_desc.bufSizes = &out_buf_size;
	out_desc.bufElSizes = &out_elem_size;

	AACENC_InArgs in_args = {0};
	in_args.numInSamples = num_samples;

	AACENC_OutArgs out_args = {0};

	err = aacEncEncode(h->enc, &in_desc, &out_desc, &in_args, &out_args);
	if (err != AACENC_OK) {
		return (int)err;
	}

	*out_bytes = out_args.numOutBytes;
	return 0;
}

static int aacenc_frame_size(aacenc_t* h) {
	return h->frame_size;
}

static int aacenc_max_out_bytes(aacenc_t* h) {
	return h->max_out_bytes;
}
*/
import "C"

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"
)

// FDKEncoder wraps the FDK AAC encoder (ADTS output) and implements Encoder.
// It encodes interleaved float32 PCM to ADTS-framed AAC data.
type FDKEncoder struct {
	handle    C.aacenc_t
	channels  int
	closed    atomic.Bool
	closeOnce sync.Once
	pcm16Buf  []int16 // reused across Encode() calls
	outBuf    []byte  // reused across Encode() calls
}

// NewFDKEncoder creates a new FDK AAC-LC encoder for the given sample rate and channel count.
// Bitrate is auto-selected: 128kbps for stereo, 64kbps for mono.
func NewFDKEncoder(sampleRate, channels int) (*FDKEncoder, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate: %d", sampleRate)
	}
	if channels < 1 || channels > 6 {
		return nil, fmt.Errorf("invalid channel count: %d (must be 1-6)", channels)
	}

	// Choose a reasonable bitrate: ~64kbps per channel.
	bitrate := 64000 * channels

	e := &FDKEncoder{channels: channels}
	rc := C.aacenc_open(&e.handle, C.int(sampleRate), C.int(channels), C.int(bitrate))
	if rc != 0 {
		return nil, fmt.Errorf("failed to open FDK AAC encoder: code %d", int(rc))
	}

	// Pre-allocate reusable buffers to eliminate per-frame allocations.
	frameSize := int(C.aacenc_frame_size(&e.handle))
	e.pcm16Buf = make([]int16, frameSize*channels)
	maxOut := int(C.aacenc_max_out_bytes(&e.handle))
	if maxOut <= 0 {
		maxOut = 8192
	}
	e.outBuf = make([]byte, maxOut)

	return e, nil
}

// Encode encodes interleaved float32 PCM into an ADTS-framed AAC frame.
// The input must contain exactly frameSize * channels samples (1024 * channels for AAC-LC).
func (e *FDKEncoder) Encode(pcm []float32) ([]byte, error) {
	if e.closed.Load() {
		return nil, fmt.Errorf("encoder is closed")
	}

	frameSize := int(C.aacenc_frame_size(&e.handle))
	expectedSamples := frameSize * e.channels
	if len(pcm) != expectedSamples {
		return nil, fmt.Errorf("PCM must be %d samples (%d * %d channels), got %d",
			expectedSamples, frameSize, e.channels, len(pcm))
	}

	// Convert float32 [-1.0, 1.0] to int16, reusing pre-allocated buffer.
	pcm16 := e.pcm16Buf[:expectedSamples]
	for i, s := range pcm {
		// Clamp to [-1.0, 1.0].
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		pcm16[i] = int16(s * float32(math.MaxInt16))
	}

	outBuf := e.outBuf
	var outBytes C.int

	rc := C.aacenc_encode(
		&e.handle,
		(*C.INT_PCM)(unsafe.Pointer(&pcm16[0])),
		C.int(len(pcm16)),
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(len(outBuf)),
		&outBytes,
	)
	if rc != 0 {
		return nil, fmt.Errorf("FDK AAC encode error: code %d", int(rc))
	}

	n := int(outBytes)
	if n == 0 {
		// Encoder is priming; return empty but not nil.
		return []byte{}, nil
	}
	// Return a copy so callers own the slice (outBuf is reused next call).
	return append([]byte(nil), outBuf[:n]...), nil
}

// Close releases the encoder resources. Safe to call multiple times
// and concurrently from multiple goroutines.
func (e *FDKEncoder) Close() error {
	e.closeOnce.Do(func() {
		e.closed.Store(true)
		C.aacenc_close(&e.handle)
	})
	return nil
}
