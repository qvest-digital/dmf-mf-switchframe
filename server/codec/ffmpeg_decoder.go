//go:build cgo && !noffmpeg

package codec

/*
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/hwcontext.h>
#include <stdlib.h>
#include <string.h>

// ffdec_t wraps an FFmpeg decoder context with its associated frames and packet.
typedef struct {
	AVCodecContext* ctx;
	AVFrame*        frame;
	AVFrame*        sw_frame;  // for HW→SW transfer
	AVPacket*       pkt;
} ffdec_t;

// ffdec_open initializes an H.264 decoder.
// hwDeviceCtx is reserved for future hardware acceleration (pass NULL for software).
// Returns 0 on success, negative on error.
static int ffdec_open(ffdec_t* h, void* hwDeviceCtx) {
	memset(h, 0, sizeof(ffdec_t));

	// Suppress verbose FFmpeg logging (only show errors).
	av_log_set_level(AV_LOG_ERROR);

	const AVCodec* codec = avcodec_find_decoder(AV_CODEC_ID_H264);
	if (!codec) {
		return -1; // codec not found
	}

	h->ctx = avcodec_alloc_context3(codec);
	if (!h->ctx) {
		return -2; // alloc failed
	}

	h->ctx->err_recognition = AV_EF_CAREFUL;
	h->ctx->thread_count = 1;

	if (hwDeviceCtx) {
		h->ctx->hw_device_ctx = av_buffer_ref((AVBufferRef*)hwDeviceCtx);
	}

	int rc = avcodec_open2(h->ctx, codec, NULL);
	if (rc < 0) {
		avcodec_free_context(&h->ctx);
		return -3; // open failed
	}

	h->frame = av_frame_alloc();
	if (!h->frame) {
		avcodec_free_context(&h->ctx);
		return -4;
	}

	h->sw_frame = av_frame_alloc();
	if (!h->sw_frame) {
		av_frame_free(&h->frame);
		avcodec_free_context(&h->ctx);
		return -5;
	}

	h->pkt = av_packet_alloc();
	if (!h->pkt) {
		av_frame_free(&h->sw_frame);
		av_frame_free(&h->frame);
		avcodec_free_context(&h->ctx);
		return -6;
	}

	return 0;
}

// ffdec_decode decodes one packet of Annex B H.264 data to packed YUV420.
// On success (return 0): out_buf is malloc'd with packed YUV420 data, out_len/width/height are set.
// The caller must free out_buf with free().
// Returns 0 on success, 1 if EAGAIN (buffering), negative on error.
static int ffdec_decode(ffdec_t* h, unsigned char* data, int data_len,
                        unsigned char** out_buf, int* out_len, int* out_width, int* out_height) {
	*out_buf = NULL;
	*out_len = 0;
	*out_width = 0;
	*out_height = 0;

	h->pkt->data = data;
	h->pkt->size = data_len;

	int rc = avcodec_send_packet(h->ctx, h->pkt);
	if (rc < 0) {
		return -1; // send failed
	}

	rc = avcodec_receive_frame(h->ctx, h->frame);
	if (rc == AVERROR(EAGAIN)) {
		return 1; // need more input
	}
	if (rc < 0) {
		return -2; // receive failed
	}

	// Determine which frame to read from (handle HW transfer if needed).
	AVFrame* src_frame = h->frame;

	if (h->frame->format != AV_PIX_FMT_YUV420P &&
	    h->frame->format != AV_PIX_FMT_YUVJ420P) {
		// Check if this is a hardware frame that needs transfer.
		if (h->frame->hw_frames_ctx) {
			rc = av_hwframe_transfer_data(h->sw_frame, h->frame, 0);
			if (rc < 0) {
				av_frame_unref(h->frame);
				return -3; // HW transfer failed
			}
			src_frame = h->sw_frame;
			// Verify transferred frame is YUV420P.
			if (src_frame->format != AV_PIX_FMT_YUV420P &&
			    src_frame->format != AV_PIX_FMT_YUVJ420P) {
				av_frame_unref(h->frame);
				av_frame_unref(h->sw_frame);
				return -4; // unexpected pixel format after HW transfer
			}
		} else {
			av_frame_unref(h->frame);
			return -4; // unexpected pixel format
		}
	}

	int w = src_frame->width;
	int h_val = src_frame->height;
	int uv_w = w / 2;
	int uv_h = h_val / 2;
	int y_size = w * h_val;
	int uv_size = uv_w * uv_h;
	int total = y_size + 2 * uv_size;

	unsigned char* buf = (unsigned char*)malloc(total);
	if (!buf) {
		av_frame_unref(h->frame);
		if (src_frame == h->sw_frame) {
			av_frame_unref(h->sw_frame);
		}
		return -5; // malloc failed
	}

	// Copy Y plane (row-by-row to handle linesize padding).
	for (int row = 0; row < h_val; row++) {
		memcpy(buf + row * w,
		       src_frame->data[0] + row * src_frame->linesize[0], w);
	}
	// Copy U plane.
	for (int row = 0; row < uv_h; row++) {
		memcpy(buf + y_size + row * uv_w,
		       src_frame->data[1] + row * src_frame->linesize[1], uv_w);
	}
	// Copy V plane.
	for (int row = 0; row < uv_h; row++) {
		memcpy(buf + y_size + uv_size + row * uv_w,
		       src_frame->data[2] + row * src_frame->linesize[2], uv_w);
	}

	*out_buf = buf;
	*out_len = total;
	*out_width = w;
	*out_height = h_val;

	av_frame_unref(h->frame);
	if (src_frame == h->sw_frame) {
		av_frame_unref(h->sw_frame);
	}

	return 0;
}

// ffdec_close frees all decoder resources.
static void ffdec_close(ffdec_t* h) {
	if (h->pkt) {
		av_packet_free(&h->pkt);
	}
	if (h->sw_frame) {
		av_frame_free(&h->sw_frame);
	}
	if (h->frame) {
		av_frame_free(&h->frame);
	}
	if (h->ctx) {
		avcodec_free_context(&h->ctx);
	}
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/zsiec/switchframe/server/transition"
)

// Compile-time check that FFmpegDecoder implements transition.VideoDecoder.
var _ transition.VideoDecoder = (*FFmpegDecoder)(nil)

// FFmpegDecoder wraps an FFmpeg libavcodec H.264 decoder and implements transition.VideoDecoder.
// It decodes Annex B H.264 bitstream to packed YUV420 planar.
//
// FFmpegDecoder is NOT safe for concurrent use. Callers must synchronize access externally.
type FFmpegDecoder struct {
	handle C.ffdec_t
	closed bool
}

// NewFFmpegDecoder creates a new FFmpeg H.264 decoder.
// hwDeviceCtx is reserved for future hardware acceleration (pass nil for software).
func NewFFmpegDecoder(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	d := &FFmpegDecoder{}
	rc := C.ffdec_open(&d.handle, hwDeviceCtx)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create FFmpeg decoder: code %d", int(rc))
	}
	return d, nil
}

// Decode decodes Annex B encoded H.264 data into packed YUV420 planar bytes.
// Returns the YUV buffer (Y: w*h, U: w/2*h/2, V: w/2*h/2), width, height, and any error.
func (d *FFmpegDecoder) Decode(data []byte) ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, fmt.Errorf("decoder is closed")
	}
	if len(data) == 0 {
		return nil, 0, 0, fmt.Errorf("empty input data")
	}

	var outBuf *C.uchar
	var outLen, outWidth, outHeight C.int

	rc := C.ffdec_decode(
		&d.handle,
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(len(data)),
		&outBuf, &outLen, &outWidth, &outHeight,
	)
	if rc == 1 {
		return nil, 0, 0, fmt.Errorf("no output frame yet (buffering)")
	}
	if rc < 0 {
		return nil, 0, 0, fmt.Errorf("FFmpeg decode error: code %d", int(rc))
	}

	n := int(outLen)
	if n == 0 || outBuf == nil {
		return nil, 0, 0, fmt.Errorf("decoder produced no output")
	}

	// Copy from C-allocated buffer to Go slice, then free.
	result := C.GoBytes(unsafe.Pointer(outBuf), outLen)
	C.free(unsafe.Pointer(outBuf))

	return result, int(outWidth), int(outHeight), nil
}

// Close releases the decoder resources. Safe to call multiple times.
func (d *FFmpegDecoder) Close() {
	if !d.closed {
		C.ffdec_close(&d.handle)
		d.closed = true
	}
}
