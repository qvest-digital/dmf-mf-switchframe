package transition

/*
#include <wels/codec_api.h>
#include <wels/codec_def.h>
#include <wels/codec_app_def.h>
#include <string.h>
#include <stdlib.h>

// oh264dec wraps an OpenH264 decoder instance.
typedef struct {
	ISVCDecoder* dec;
} oh264dec_t;

static int oh264dec_open(oh264dec_t* h) {
	long rc = WelsCreateDecoder(&h->dec);
	if (rc != 0 || h->dec == NULL) {
		return -1;
	}

	// Suppress verbose OpenH264 logging.
	int logLevel = WELS_LOG_QUIET;
	(*h->dec)->SetOption(h->dec, DECODER_OPTION_TRACE_LEVEL, &logLevel);

	SDecodingParam param;
	memset(&param, 0, sizeof(SDecodingParam));
	param.sVideoProperty.eVideoBsType = VIDEO_BITSTREAM_AVC;
	param.eEcActiveIdc = ERROR_CON_SLICE_COPY;

	rc = (*h->dec)->Initialize(h->dec, &param);
	if (rc != 0) {
		WelsDestroyDecoder(h->dec);
		h->dec = NULL;
		return -2;
	}

	return 0;
}

static void oh264dec_close(oh264dec_t* h) {
	if (h->dec) {
		(*h->dec)->Uninitialize(h->dec);
		WelsDestroyDecoder(h->dec);
		h->dec = NULL;
	}
}

// oh264dec_decode decodes Annex B data into YUV420 planes.
// On success, dst_y/dst_u/dst_v point to decoder-owned buffers (valid until next call),
// and width/height/y_stride/uv_stride are set.
// Returns 0 on success with frame data, 1 if no output yet, negative on error.
static int oh264dec_decode(oh264dec_t* h,
                           unsigned char* src, int src_len,
                           unsigned char** dst_y, unsigned char** dst_u, unsigned char** dst_v,
                           int* width, int* height, int* y_stride, int* uv_stride) {
	unsigned char* ptrs[3] = {NULL, NULL, NULL};
	SBufferInfo buf_info;
	memset(&buf_info, 0, sizeof(SBufferInfo));

	DECODING_STATE state = (*h->dec)->DecodeFrameNoDelay(h->dec, src, src_len, ptrs, &buf_info);
	if (state != dsErrorFree && state != dsDataErrorConcealed) {
		return -1;
	}

	if (buf_info.iBufferStatus != 1) {
		return 1; // no output frame yet
	}

	*dst_y = buf_info.pDst[0];
	*dst_u = buf_info.pDst[1];
	*dst_v = buf_info.pDst[2];
	*width = buf_info.UsrData.sSystemBuffer.iWidth;
	*height = buf_info.UsrData.sSystemBuffer.iHeight;
	*y_stride = buf_info.UsrData.sSystemBuffer.iStride[0];
	*uv_stride = buf_info.UsrData.sSystemBuffer.iStride[1];
	return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Compile-time check that OpenH264Decoder implements VideoDecoder.
var _ VideoDecoder = (*OpenH264Decoder)(nil)

// OpenH264Decoder wraps the OpenH264 decoder and implements VideoDecoder.
// It decodes Annex B H.264 bitstream to packed YUV420 planar.
type OpenH264Decoder struct {
	handle C.oh264dec_t
	closed bool
}

// NewOpenH264Decoder creates a new OpenH264 decoder instance.
func NewOpenH264Decoder() (*OpenH264Decoder, error) {
	d := &OpenH264Decoder{}
	rc := C.oh264dec_open(&d.handle)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create OpenH264 decoder: code %d", int(rc))
	}
	return d, nil
}

// Decode decodes Annex B encoded H.264 data into packed YUV420 planar bytes.
// Returns the YUV buffer, width, height, and any error.
// The decoder output may have padded strides, so this method copies to a
// tightly-packed planar layout (Y: w*h, U: w/2*h/2, V: w/2*h/2).
func (d *OpenH264Decoder) Decode(data []byte) ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, fmt.Errorf("decoder is closed")
	}
	if len(data) == 0 {
		return nil, 0, 0, fmt.Errorf("empty input data")
	}

	var dstY, dstU, dstV *C.uchar
	var width, height, yStride, uvStride C.int

	rc := C.oh264dec_decode(
		&d.handle,
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(len(data)),
		&dstY, &dstU, &dstV,
		&width, &height, &yStride, &uvStride,
	)
	if rc > 0 {
		// No output frame yet (buffering). Not an error but no data.
		return nil, 0, 0, fmt.Errorf("no output frame yet (buffering)")
	}
	if rc < 0 {
		return nil, 0, 0, fmt.Errorf("OpenH264 decode error: code %d", int(rc))
	}

	w := int(width)
	h := int(height)
	ys := int(yStride)
	uvs := int(uvStride)

	if dstY == nil || dstU == nil || dstV == nil {
		return nil, 0, 0, fmt.Errorf("decoder returned nil plane pointers")
	}

	// Copy strided decoder output to tightly-packed planar buffer.
	ySize := w * h
	uvW := w / 2
	uvH := h / 2
	uvSize := uvW * uvH
	out := make([]byte, ySize+2*uvSize)

	// Copy Y plane.
	srcY := unsafe.Slice((*byte)(unsafe.Pointer(dstY)), ys*h)
	for row := 0; row < h; row++ {
		copy(out[row*w:(row+1)*w], srcY[row*ys:row*ys+w])
	}

	// Copy U plane.
	srcU := unsafe.Slice((*byte)(unsafe.Pointer(dstU)), uvs*uvH)
	for row := 0; row < uvH; row++ {
		copy(out[ySize+row*uvW:ySize+(row+1)*uvW], srcU[row*uvs:row*uvs+uvW])
	}

	// Copy V plane.
	srcV := unsafe.Slice((*byte)(unsafe.Pointer(dstV)), uvs*uvH)
	for row := 0; row < uvH; row++ {
		copy(out[ySize+uvSize+row*uvW:ySize+uvSize+(row+1)*uvW], srcV[row*uvs:row*uvs+uvW])
	}

	return out, w, h, nil
}

// Close releases the decoder resources. Safe to call multiple times.
func (d *OpenH264Decoder) Close() {
	if !d.closed {
		C.oh264dec_close(&d.handle)
		d.closed = true
	}
}
