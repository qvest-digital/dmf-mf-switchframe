//go:build cgo && !noffmpeg

package codec

/*
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <stdlib.h>
#include <string.h>

// ffenc_t wraps an FFmpeg encoder context with its associated frame and packet.
typedef struct {
	AVCodecContext* ctx;
	AVFrame*        frame;
	AVPacket*       pkt;
	int             width;
	int             height;
	int64_t         pts;
} ffenc_t;

// ffenc_open initializes the encoder with the given codec name and parameters.
// hwDeviceCtx is currently unused (reserved for future HW accel).
// Returns 0 on success, negative on error.
static int ffenc_open(ffenc_t* h, const char* codec_name,
                      int width, int height, int bitrate, float fps,
                      void* hwDeviceCtx) {
	memset(h, 0, sizeof(ffenc_t));

	// Suppress verbose FFmpeg/x264 logging (only show errors).
	av_log_set_level(AV_LOG_ERROR);

	const AVCodec* codec = avcodec_find_encoder_by_name(codec_name);
	if (!codec) {
		return -1; // codec not found
	}

	h->ctx = avcodec_alloc_context3(codec);
	if (!h->ctx) {
		return -2; // alloc failed
	}

	h->width = width;
	h->height = height;

	h->ctx->width = width;
	h->ctx->height = height;
	h->ctx->time_base = (AVRational){1, (int)(fps + 0.5f)};
	h->ctx->framerate = (AVRational){(int)(fps + 0.5f), 1};
	h->ctx->bit_rate = bitrate;
	h->ctx->gop_size = (int)(fps + 0.5f) * 2; // IDR every 2 seconds
	h->ctx->max_b_frames = 0;
	h->ctx->pix_fmt = AV_PIX_FMT_YUV420P;
	h->ctx->thread_count = 1;

	// Codec-specific options for low-latency encoding.
	if (strcmp(codec_name, "libx264") == 0) {
		av_opt_set(h->ctx->priv_data, "preset", "medium", 0);
		av_opt_set(h->ctx->priv_data, "tune", "zerolatency", 0);
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		// Disable scene-change detection: transitions ARE the content change.
		av_opt_set(h->ctx->priv_data, "sc_threshold", "0", 0);
	} else if (strcmp(codec_name, "h264_nvenc") == 0) {
		av_opt_set(h->ctx->priv_data, "preset", "p4", 0);
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		av_opt_set(h->ctx->priv_data, "rc", "vbr", 0);
	} else if (strcmp(codec_name, "h264_vaapi") == 0) {
		av_opt_set_int(h->ctx->priv_data, "profile", 100, 0); // HIGH
	} else if (strcmp(codec_name, "h264_videotoolbox") == 0) {
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		av_opt_set_int(h->ctx->priv_data, "allow_sw", 0, 0);
		av_opt_set_int(h->ctx->priv_data, "realtime", 1, 0);
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
	h->frame->format = AV_PIX_FMT_YUV420P;
	h->frame->width = width;
	h->frame->height = height;

	rc = av_frame_get_buffer(h->frame, 0);
	if (rc < 0) {
		av_frame_free(&h->frame);
		avcodec_free_context(&h->ctx);
		return -5;
	}

	h->pkt = av_packet_alloc();
	if (!h->pkt) {
		av_frame_free(&h->frame);
		avcodec_free_context(&h->ctx);
		return -6;
	}

	h->pts = 0;
	return 0;
}

// ffenc_encode encodes one YUV420 frame.
// yuv_data points to packed planar YUV420 (Y: w*h, U: w/2*h/2, V: w/2*h/2).
// If force_idr is non-zero, the frame is forced to be an IDR keyframe.
// On success (return 0): out_buf/out_len contain the Annex B bitstream, is_idr is set.
// The caller must free out_buf with free().
// Returns 0 on success, 1 if EAGAIN (need more input), negative on error.
static int ffenc_encode(ffenc_t* h, unsigned char* yuv_data, int force_idr,
                        unsigned char** out_buf, int* out_len, int* is_idr) {
	*out_buf = NULL;
	*out_len = 0;
	*is_idr = 0;

	// Make the frame writable (in case it's referenced by the encoder).
	int rc = av_frame_make_writable(h->frame);
	if (rc < 0) {
		return -1;
	}

	// Copy packed YUV420 input into the AVFrame planes, respecting linesize.
	int w = h->width;
	int hw = h->height;
	int y_size = w * hw;
	int uv_w = w / 2;
	int uv_h = hw / 2;

	// Y plane
	for (int row = 0; row < hw; row++) {
		memcpy(h->frame->data[0] + row * h->frame->linesize[0],
		       yuv_data + row * w, w);
	}
	// U plane
	for (int row = 0; row < uv_h; row++) {
		memcpy(h->frame->data[1] + row * h->frame->linesize[1],
		       yuv_data + y_size + row * uv_w, uv_w);
	}
	// V plane
	for (int row = 0; row < uv_h; row++) {
		memcpy(h->frame->data[2] + row * h->frame->linesize[2],
		       yuv_data + y_size + uv_w * uv_h + row * uv_w, uv_w);
	}

	h->frame->pts = h->pts++;

	if (force_idr) {
		h->frame->pict_type = AV_PICTURE_TYPE_I;
		h->frame->flags |= AV_FRAME_FLAG_KEY;
	} else {
		h->frame->pict_type = AV_PICTURE_TYPE_NONE;
		h->frame->flags &= ~AV_FRAME_FLAG_KEY;
	}

	rc = avcodec_send_frame(h->ctx, h->frame);
	if (rc < 0) {
		return -2; // send failed
	}

	rc = avcodec_receive_packet(h->ctx, h->pkt);
	if (rc == AVERROR(EAGAIN)) {
		return 1; // need more input
	}
	if (rc < 0) {
		return -3; // receive failed
	}

	// Copy packet data to a malloc'd buffer for the Go side.
	unsigned char* buf = (unsigned char*)malloc(h->pkt->size);
	if (!buf) {
		av_packet_unref(h->pkt);
		return -4;
	}
	memcpy(buf, h->pkt->data, h->pkt->size);
	*out_buf = buf;
	*out_len = h->pkt->size;
	*is_idr = (h->pkt->flags & AV_PKT_FLAG_KEY) ? 1 : 0;

	av_packet_unref(h->pkt);
	return 0;
}

// ffenc_close frees all encoder resources.
static void ffenc_close(ffenc_t* h) {
	if (h->pkt) {
		av_packet_free(&h->pkt);
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

// Compile-time check that FFmpegEncoder implements transition.VideoEncoder.
var _ transition.VideoEncoder = (*FFmpegEncoder)(nil)

// FFmpegEncoder wraps an FFmpeg libavcodec encoder and implements transition.VideoEncoder.
// It encodes packed YUV420 planar frames to Annex B H.264 bitstream.
//
// FFmpegEncoder is NOT safe for concurrent use. Callers must synchronize access externally.
type FFmpegEncoder struct {
	handle C.ffenc_t
	closed bool
}

// NewFFmpegEncoder creates a new FFmpeg encoder using the named codec.
//
// codecName is the FFmpeg encoder name (e.g. "libx264", "h264_videotoolbox").
// width, height, bitrate, and fps configure the output stream.
// hwDeviceCtx is reserved for future hardware acceleration (pass nil for software).
func NewFFmpegEncoder(codecName string, width, height, bitrate int, fps float32, hwDeviceCtx unsafe.Pointer) (*FFmpegEncoder, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d", width, height)
	}
	if bitrate <= 0 {
		return nil, fmt.Errorf("invalid bitrate: %d", bitrate)
	}
	if fps <= 0 {
		return nil, fmt.Errorf("invalid fps: %f", fps)
	}

	cName := C.CString(codecName)
	defer C.free(unsafe.Pointer(cName))

	e := &FFmpegEncoder{}
	rc := C.ffenc_open(&e.handle, cName,
		C.int(width), C.int(height), C.int(bitrate), C.float(fps),
		hwDeviceCtx)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create FFmpeg encoder %q: code %d", codecName, int(rc))
	}
	return e, nil
}

// Encode encodes a packed YUV420 planar frame to Annex B H.264 data.
// If forceIDR is true, the encoder forces an IDR keyframe.
// Returns the encoded bitstream, whether the frame is a keyframe, and any error.
func (e *FFmpegEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	if e.closed {
		return nil, false, fmt.Errorf("encoder is closed")
	}

	w := int(e.handle.width)
	h := int(e.handle.height)
	expected := w * h * 3 / 2
	if len(yuv) != expected {
		return nil, false, fmt.Errorf("YUV buffer must be %d bytes (%dx%d*3/2), got %d",
			expected, w, h, len(yuv))
	}

	forceIDRInt := C.int(0)
	if forceIDR {
		forceIDRInt = C.int(1)
	}

	var outBuf *C.uchar
	var outLen C.int
	var isIDR C.int

	rc := C.ffenc_encode(
		&e.handle,
		(*C.uchar)(unsafe.Pointer(&yuv[0])),
		forceIDRInt,
		&outBuf, &outLen, &isIDR,
	)
	if rc < 0 {
		return nil, false, fmt.Errorf("FFmpeg encode error: code %d", int(rc))
	}
	if rc == 1 {
		// EAGAIN: encoder needs more input before producing output.
		return nil, false, fmt.Errorf("encoder needs more input (EAGAIN)")
	}

	n := int(outLen)
	if n == 0 || outBuf == nil {
		return nil, false, fmt.Errorf("encoder produced no output")
	}

	// Copy from C-allocated buffer to Go slice, then free.
	result := C.GoBytes(unsafe.Pointer(outBuf), outLen)
	C.free(unsafe.Pointer(outBuf))

	return result, isIDR != 0, nil
}

// Close releases the encoder resources. Safe to call multiple times.
func (e *FFmpegEncoder) Close() {
	if !e.closed {
		C.ffenc_close(&e.handle)
		e.closed = true
	}
}
