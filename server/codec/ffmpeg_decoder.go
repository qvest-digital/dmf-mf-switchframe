//go:build cgo && !noffmpeg

package codec

/*
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/hwcontext.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

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

	// av_log_set_level is called once from Go via initFFmpegLogLevel().

	const AVCodec* codec = avcodec_find_decoder(AV_CODEC_ID_H264);
	if (!codec) {
		return -1; // codec not found
	}

	h->ctx = avcodec_alloc_context3(codec);
	if (!h->ctx) {
		return -2; // alloc failed
	}

	// No AV_EF_CAREFUL: allow best-effort error concealment for frames with
	// missing references (expected during transition warmup and source changes).
	// FF_EC_GUESS_MVS uses surrounding motion vectors to conceal damaged
	// macroblocks, producing fewer visible glitches than simple frame copy.
	h->ctx->error_concealment = FF_EC_GUESS_MVS | FF_EC_DEBLOCK;

	int ncpu = (int)sysconf(_SC_NPROCESSORS_ONLN);
	if (ncpu < 2) ncpu = 2;
	if (ncpu > 8) ncpu = 8;
	h->ctx->thread_count = ncpu;

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

// Lookup tables for YUVJ420P (full-range 0-255) → YUV420P (limited-range) conversion.
// Y:  full [0,255] → limited [16,235]:  Y_lim  = 16 + Y_full  * 219 / 255
// UV: full [0,255] → limited [16,240]:  UV_lim = 16 + UV_full * 224 / 255
static unsigned char full_to_limited_y[256];
static unsigned char full_to_limited_uv[256];

// init_range_tables populates the full→limited range lookup tables.
// Must be called exactly once before first use. Synchronization is handled
// on the Go side via sync.Once (initRangeTablesOnce).
static void init_range_tables(void) {
	for (int i = 0; i < 256; i++) {
		full_to_limited_y[i]  = (unsigned char)(16 + i * 219 / 255);
		full_to_limited_uv[i] = (unsigned char)(16 + i * 224 / 255);
	}
}

// remap_row applies a 256-byte lookup table to each pixel in a row.
static void remap_row(unsigned char* dst, const unsigned char* src,
                      int len, const unsigned char* lut) {
	for (int i = 0; i < len; i++) {
		dst[i] = lut[src[i]];
	}
}

// ffdec_is_full_range returns 1 if the decoded frame uses full-range (JPEG) levels.
// Checks both the deprecated YUVJ420P pixel format and the modern color_range field.
static int ffdec_is_full_range(AVFrame* f) {
	if (f->format == AV_PIX_FMT_YUVJ420P) return 1;
	if (f->color_range == AVCOL_RANGE_JPEG) return 1;
	return 0;
}

// ffdec_copy_frame copies YUV420 planes from src_frame into dst, stripping stride padding.
// dst must have capacity >= w*h*3/2. When remap_to_limited is non-zero, pixel values
// are converted from full-range (0-255) to limited-range (Y:16-235, UV:16-240).
static void ffdec_copy_frame(AVFrame* src_frame, unsigned char* dst,
                             int w, int h_val, int remap_to_limited) {
	int uv_w = w / 2;
	int uv_h = h_val / 2;
	int y_size = w * h_val;
	int uv_size = uv_w * uv_h;

	if (remap_to_limited) {
		// Range tables are initialized once from Go via initRangeTablesOnce.
		for (int row = 0; row < h_val; row++) {
			remap_row(dst + row * w,
			          src_frame->data[0] + row * src_frame->linesize[0],
			          w, full_to_limited_y);
		}
		for (int row = 0; row < uv_h; row++) {
			remap_row(dst + y_size + row * uv_w,
			          src_frame->data[1] + row * src_frame->linesize[1],
			          uv_w, full_to_limited_uv);
		}
		for (int row = 0; row < uv_h; row++) {
			remap_row(dst + y_size + uv_size + row * uv_w,
			          src_frame->data[2] + row * src_frame->linesize[2],
			          uv_w, full_to_limited_uv);
		}
	} else {
		for (int row = 0; row < h_val; row++) {
			memcpy(dst + row * w,
			       src_frame->data[0] + row * src_frame->linesize[0], w);
		}
		for (int row = 0; row < uv_h; row++) {
			memcpy(dst + y_size + row * uv_w,
			       src_frame->data[1] + row * src_frame->linesize[1], uv_w);
		}
		for (int row = 0; row < uv_h; row++) {
			memcpy(dst + y_size + uv_size + row * uv_w,
			       src_frame->data[2] + row * src_frame->linesize[2], uv_w);
		}
	}
}

// ffdec_decode decodes one packet of Annex B H.264 data to packed YUV420.
// If dst_buf is non-NULL and dst_cap >= the required size, the frame is
// written directly into dst_buf (zero-copy to caller). Otherwise a buffer
// is malloc'd and returned via out_buf (caller must free with free()).
// On success (return 0): out_buf/out_len/out_width/out_height are set.
// Returns 0 on success, 1 if EAGAIN (buffering), negative on error.
static int ffdec_decode(ffdec_t* h, unsigned char* data, int data_len,
                        unsigned char* dst_buf, int dst_cap,
                        unsigned char** out_buf, int* out_len, int* out_width, int* out_height) {
	*out_buf = NULL;
	*out_len = 0;
	*out_width = 0;
	*out_height = 0;

	// Reset the packet before reuse. av_packet_unref frees side_data arrays
	// and unrefs any attached buf references from the previous decode call.
	// Without this, side_data accumulates across calls since we directly
	// assign data/size below (bypassing av_packet_make_writable).
	av_packet_unref(h->pkt);

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

	AVFrame* src_frame = h->frame;

	if (h->frame->format != AV_PIX_FMT_YUV420P &&
	    h->frame->format != AV_PIX_FMT_YUVJ420P) {
		if (h->frame->hw_frames_ctx) {
			rc = av_hwframe_transfer_data(h->sw_frame, h->frame, 0);
			if (rc < 0) {
				av_frame_unref(h->frame);
				return -3;
			}
			src_frame = h->sw_frame;
			if (src_frame->format != AV_PIX_FMT_YUV420P &&
			    src_frame->format != AV_PIX_FMT_YUVJ420P) {
				av_frame_unref(h->frame);
				av_frame_unref(h->sw_frame);
				return -4;
			}
		} else {
			av_frame_unref(h->frame);
			return -4;
		}
	}

	int w = src_frame->width;
	int h_val = src_frame->height;
	int total = w * h_val * 3 / 2;

	unsigned char* buf;
	if (dst_buf && dst_cap >= total) {
		buf = dst_buf;
	} else {
		buf = (unsigned char*)malloc(total);
		if (!buf) {
			av_frame_unref(h->frame);
			if (src_frame == h->sw_frame) {
				av_frame_unref(h->sw_frame);
			}
			return -5;
		}
	}

	ffdec_copy_frame(src_frame, buf, w, h_val, ffdec_is_full_range(src_frame));

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

// ffdec_flush resets the decoder state, clearing reference frames and
// internal buffers. The decoder remains usable for new input.
static void ffdec_flush(ffdec_t* h) {
	if (h->ctx) {
		avcodec_flush_buffers(h->ctx);
	}
}

// ffdec_send_eos signals end-of-stream so remaining buffered frames can be drained.
static int ffdec_send_eos(ffdec_t* h) {
	return avcodec_send_packet(h->ctx, NULL);
}

// ffdec_receive_only receives a decoded frame without sending new input.
// Used to drain remaining frames after all input has been sent or after EOS.
// Same dst_buf/dst_cap semantics as ffdec_decode.
// Returns 0 on success, 1 if no more frames (EAGAIN/EOF), negative on error.
static int ffdec_receive_only(ffdec_t* h, unsigned char* dst_buf, int dst_cap,
                              unsigned char** out_buf, int* out_len,
                              int* out_width, int* out_height) {
	*out_buf = NULL;
	*out_len = 0;
	*out_width = 0;
	*out_height = 0;

	int rc = avcodec_receive_frame(h->ctx, h->frame);
	if (rc == AVERROR(EAGAIN) || rc == AVERROR_EOF) {
		return 1;
	}
	if (rc < 0) {
		return -2;
	}

	AVFrame* src_frame = h->frame;

	if (h->frame->format != AV_PIX_FMT_YUV420P &&
	    h->frame->format != AV_PIX_FMT_YUVJ420P) {
		if (h->frame->hw_frames_ctx) {
			rc = av_hwframe_transfer_data(h->sw_frame, h->frame, 0);
			if (rc < 0) {
				av_frame_unref(h->frame);
				return -3;
			}
			src_frame = h->sw_frame;
			if (src_frame->format != AV_PIX_FMT_YUV420P &&
			    src_frame->format != AV_PIX_FMT_YUVJ420P) {
				av_frame_unref(h->frame);
				av_frame_unref(h->sw_frame);
				return -4;
			}
		} else {
			av_frame_unref(h->frame);
			return -4;
		}
	}

	int w = src_frame->width;
	int h_val = src_frame->height;
	int total = w * h_val * 3 / 2;

	unsigned char* buf;
	if (dst_buf && dst_cap >= total) {
		buf = dst_buf;
	} else {
		buf = (unsigned char*)malloc(total);
		if (!buf) {
			av_frame_unref(h->frame);
			if (src_frame == h->sw_frame) {
				av_frame_unref(h->sw_frame);
			}
			return -5;
		}
	}

	ffdec_copy_frame(src_frame, buf, w, h_val, ffdec_is_full_range(src_frame));

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
	"sync"
	"unsafe"

	"github.com/zsiec/switchframe/server/transition"
)

// Compile-time check that FFmpegDecoder implements transition.VideoDecoder.
var _ transition.VideoDecoder = (*FFmpegDecoder)(nil)

// initRangeTablesOnce ensures the YUVJ420P→YUV420P range conversion lookup
// tables are populated exactly once, preventing a data race when multiple
// decoders initialize concurrently.
var initRangeTablesOnce sync.Once

// FFmpegDecoder wraps an FFmpeg libavcodec H.264 decoder and implements transition.VideoDecoder.
// It decodes Annex B H.264 bitstream to packed YUV420 planar.
//
// FFmpegDecoder is NOT safe for concurrent use. Callers must synchronize access externally.
type FFmpegDecoder struct {
	handle C.ffdec_t
	closed bool
	yuvBuf []byte // reusable buffer for decoded YUV output
}

// NewFFmpegDecoder creates a new FFmpeg H.264 decoder.
// hwDeviceCtx is reserved for future hardware acceleration (pass nil for software).
func NewFFmpegDecoder(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	initFFmpegLogLevel()
	initRangeTablesOnce.Do(func() {
		C.init_range_tables()
	})

	d := &FFmpegDecoder{}
	rc := C.ffdec_open(&d.handle, hwDeviceCtx)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create FFmpeg decoder: code %d", int(rc))
	}
	return d, nil
}

// Decode decodes Annex B encoded H.264 data into packed YUV420 planar bytes.
// Returns the YUV buffer (Y: w*h, U: w/2*h/2, V: w/2*h/2), width, height, and any error.
//
// WARNING: The returned YUV byte slice aliases the decoder's internal buffer.
// It is only valid until the next call to Decode or ReceiveFrame. Callers
// that need the data beyond that point must copy it before the next decode.
func (d *FFmpegDecoder) Decode(data []byte) ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, fmt.Errorf("decoder is closed")
	}
	if len(data) == 0 {
		return nil, 0, 0, fmt.Errorf("empty input data")
	}

	var dstBuf *C.uchar
	var dstCap C.int
	if len(d.yuvBuf) > 0 {
		dstBuf = (*C.uchar)(unsafe.Pointer(&d.yuvBuf[0]))
		dstCap = C.int(cap(d.yuvBuf))
	}

	var outBuf *C.uchar
	var outLen, outWidth, outHeight C.int

	rc := C.ffdec_decode(
		&d.handle,
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(len(data)),
		dstBuf, dstCap,
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

	return d.adoptOrCopy(outBuf, outLen, int(outWidth), int(outHeight))
}

// adoptOrCopy handles the output from ffdec_decode/ffdec_receive_only.
// If outBuf points into d.yuvBuf (the Go-provided buffer was used), it re-slices
// d.yuvBuf to the output length. Otherwise, the C side malloc'd a buffer because
// d.yuvBuf was too small or nil, so we copy via GoBytes and free the C buffer.
// In both cases, d.yuvBuf is updated for the next call.
func (d *FFmpegDecoder) adoptOrCopy(outBuf *C.uchar, outLen C.int, w, h int) ([]byte, int, int, error) {
	n := int(outLen)
	needed := w * h * 3 / 2

	if len(d.yuvBuf) > 0 && outBuf == (*C.uchar)(unsafe.Pointer(&d.yuvBuf[0])) {
		// C wrote directly into our Go buffer — no copy needed.
		return d.yuvBuf[:n], w, h, nil
	}

	// C malloc'd its own buffer (first call or resolution changed).
	result := C.GoBytes(unsafe.Pointer(outBuf), outLen)
	C.free(unsafe.Pointer(outBuf))

	// Cache the buffer for future frames at this resolution.
	if cap(d.yuvBuf) < needed {
		d.yuvBuf = make([]byte, needed)
	} else {
		d.yuvBuf = d.yuvBuf[:needed]
	}

	return result, w, h, nil
}

// Flush resets the decoder's internal state (reference frames, reorder buffer)
// without destroying it. Use when the input source changes to prevent stale
// reference frame warnings. The decoder can immediately accept new input.
func (d *FFmpegDecoder) Flush() {
	if !d.closed {
		C.ffdec_flush(&d.handle)
	}
}

// SendEOS signals end-of-stream to the decoder so buffered frames
// (from B-frame reordering) can be drained via ReceiveFrame().
func (d *FFmpegDecoder) SendEOS() error {
	if d.closed {
		return fmt.Errorf("decoder is closed")
	}
	rc := C.ffdec_send_eos(&d.handle)
	if rc < 0 {
		return fmt.Errorf("send EOS failed: code %d", int(rc))
	}
	return nil
}

// ReceiveFrame receives a decoded frame without sending new input.
// Returns the YUV buffer, width, height, and any error.
// Returns an error when no more frames are available (EAGAIN/EOF).
//
// WARNING: The returned YUV byte slice aliases the decoder's internal buffer.
// It is only valid until the next call to Decode or ReceiveFrame. Callers
// that need the data beyond that point must copy it before the next decode.
func (d *FFmpegDecoder) ReceiveFrame() ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, fmt.Errorf("decoder is closed")
	}

	var dstBuf *C.uchar
	var dstCap C.int
	if len(d.yuvBuf) > 0 {
		dstBuf = (*C.uchar)(unsafe.Pointer(&d.yuvBuf[0]))
		dstCap = C.int(cap(d.yuvBuf))
	}

	var outBuf *C.uchar
	var outLen, outWidth, outHeight C.int

	rc := C.ffdec_receive_only(
		&d.handle,
		dstBuf, dstCap,
		&outBuf, &outLen, &outWidth, &outHeight,
	)
	if rc == 1 {
		return nil, 0, 0, fmt.Errorf("no more frames")
	}
	if rc < 0 {
		return nil, 0, 0, fmt.Errorf("receive frame error: code %d", int(rc))
	}

	n := int(outLen)
	if n == 0 || outBuf == nil {
		return nil, 0, 0, fmt.Errorf("decoder produced no output")
	}

	return d.adoptOrCopy(outBuf, outLen, int(outWidth), int(outHeight))
}

// Close releases the decoder resources. Safe to call multiple times.
func (d *FFmpegDecoder) Close() {
	if !d.closed {
		C.ffdec_close(&d.handle)
		d.closed = true
	}
}
