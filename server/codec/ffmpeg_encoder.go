//go:build cgo && !noffmpeg

package codec

/*
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/version.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// AV_FRAME_FLAG_KEY was added in FFmpeg 6.1 (libavutil 58.29).
// For older versions (e.g. Debian Bookworm's FFmpeg 5.1), use key_frame field.
#if LIBAVUTIL_VERSION_INT < AV_VERSION_INT(58, 29, 100)
#define COMPAT_SET_KEY_FRAME(frame, is_key) ((frame)->key_frame = (is_key))
#else
#define COMPAT_SET_KEY_FRAME(frame, is_key) do { \
    if (is_key) (frame)->flags |= AV_FRAME_FLAG_KEY; \
    else (frame)->flags &= ~AV_FRAME_FLAG_KEY; \
} while(0)
#endif

// ffenc_t wraps an FFmpeg encoder context with its associated frame and packet.
typedef struct {
	AVCodecContext* ctx;
	AVFrame*        frame;
	AVPacket*       pkt;
	int             width;
	int             height;
} ffenc_t;

// ffenc_open initializes the encoder with the given codec name and parameters.
// hwDeviceCtx is currently unused (reserved for future HW accel).
// The encoder always uses constrained VBR (ABR + tight VBV) for predictable
// bitrate suitable for SRT transport, while maintaining quality flexibility.
// Returns 0 on success, negative on error.
static int ffenc_open(ffenc_t* h, const char* codec_name,
                      int width, int height, int bitrate,
                      int fps_num, int fps_den,
                      int gop_secs, void* hwDeviceCtx) {
	memset(h, 0, sizeof(ffenc_t));

	// av_log_set_level is called once from Go via initFFmpegLogLevel().

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
	h->ctx->time_base = (AVRational){fps_den, fps_num};
	h->ctx->framerate = (AVRational){fps_num, fps_den};

	// Constrained VBR (cVBR): ABR target with tight VBV ceiling.
	// The encoder targets the specified bitrate on average, with a 1.2x peak
	// ceiling enforced by VBV. This matches broadcast standard practice
	// (Haivision KB, AWS MediaLive) and produces predictable output suitable
	// for SRT transport while preserving per-frame quality flexibility.
	h->ctx->bit_rate = bitrate;
	h->ctx->rc_max_rate = bitrate + bitrate / 5; // 1.2x target
	h->ctx->rc_buffer_size = bitrate + bitrate / 5; // 1-second VBV at peak rate

	h->ctx->gop_size = fps_num * gop_secs / fps_den;
	h->ctx->max_b_frames = 0;
	h->ctx->pix_fmt = AV_PIX_FMT_YUV420P;

	// Signal BT.709 colorspace in VUI parameters.
	h->ctx->color_primaries = AVCOL_PRI_BT709;
	h->ctx->color_trc = AVCOL_TRC_BT709;
	h->ctx->colorspace = AVCOL_SPC_BT709;
	h->ctx->color_range = AVCOL_RANGE_MPEG; // limited range (16-235)

	// Thread count for sliced threading: determines how many slices per frame.
	// 4 slices at 1080p = 270 lines each. Zero added pipeline latency.
	// Above 4, gains are sublinear and slice boundary artifacts increase.
	int ncpu = (int)sysconf(_SC_NPROCESSORS_ONLN);
	if (ncpu < 2) ncpu = 2;
	if (ncpu > 4) ncpu = 4;
	h->ctx->thread_count = ncpu;

	// Set explicit H.264 level for downstream decoder compatibility.
	float fps_f = (float)fps_num / (float)fps_den;
	int level;
	if (width <= 1280 && height <= 720) {
		level = 31; // Level 3.1
	} else if (width <= 1920 && height <= 1080 && fps_f <= 30.5f) {
		level = 40; // Level 4.0
	} else {
		level = 42; // Level 4.2
	}

	// Codec-specific options.
	if (strcmp(codec_name, "libx264") == 0) {
		av_opt_set(h->ctx->priv_data, "preset", "superfast", 0);
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		// zerolatency: sliced threading (zero frame buffer), no mbtree,
		// sync-lookahead=0, rc-lookahead=0, force-cfr=1.
		// Eliminates ~100ms internal encoder buffering from frame threading.
		av_opt_set(h->ctx->priv_data, "tune", "zerolatency", 0);
		// Auto-variance AQ adapts per-frame between temporal and spatial
		// redistribution — better than mode 2 for mixed content (static →
		// dissolve → stinger → camera motion).
		av_opt_set(h->ctx->priv_data, "aq-mode", "3", 0);
		av_opt_set(h->ctx->priv_data, "aq-strength", "1.2", 0);
		// Disable scene-change detection: transitions ARE the content change.
		av_opt_set(h->ctx->priv_data, "sc_threshold", "0", 0);
		char level_str[8];
		snprintf(level_str, sizeof(level_str), "%d", level);
		av_opt_set(h->ctx->priv_data, "level", level_str, 0);
		// Enable Access Unit Delimiters for MPEG-TS compliance.
		av_opt_set(h->ctx->priv_data, "aud", "1", 0);
		// Smart weighted prediction improves dissolve quality (~5% CPU cost).
		// Exploits linear fade relationship between frames during mix transitions.
		av_opt_set(h->ctx->priv_data, "weightp", "2", 0);
		// Psychovisual RD: preserves detail in graphics overlays and text.
		// psy-trellis=0.15 keeps high-frequency detail (score bugs, lower thirds).
		av_opt_set(h->ctx->priv_data, "psy-rd", "1.0:0.15", 0);
		// Slightly reduce deblocking to preserve fine detail at broadcast bitrates.
		av_opt_set(h->ctx->priv_data, "deblock", "-1:-1", 0);
	} else if (strcmp(codec_name, "h264_nvenc") == 0) {
		av_opt_set(h->ctx->priv_data, "preset", "p4", 0);
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		av_opt_set(h->ctx->priv_data, "delay", "0", 0);
		av_opt_set_int(h->ctx->priv_data, "spatial-aq", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "aq-strength", 8, 0);
		av_opt_set_int(h->ctx->priv_data, "no-scenecut", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "forced-idr", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "level", level, 0);
		// NVENC CBR is hardware-native and works correctly.
		// Note: with rc=cbr, NVENC uses bit_rate as the target and ignores
		// rc_max_rate. The VBV buffer (rc_buffer_size) is still applied.
		av_opt_set(h->ctx->priv_data, "rc", "cbr", 0);
		// temporal-aq is incompatible with CBR on NVENC.
		av_opt_set_int(h->ctx->priv_data, "temporal-aq", 0, 0);
	} else if (strcmp(codec_name, "h264_vaapi") == 0) {
		av_opt_set_int(h->ctx->priv_data, "profile", 100, 0); // HIGH
		h->ctx->level = level;
	} else if (strcmp(codec_name, "h264_videotoolbox") == 0) {
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		av_opt_set_int(h->ctx->priv_data, "realtime", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "prio_speed", 1, 0);
		// Force frame-at-a-time output — no internal encoder frame buffering.
		h->ctx->max_b_frames = 0;
		av_opt_set_int(h->ctx->priv_data, "allow_b_frames", 0, 0);
		av_opt_set_int(h->ctx->priv_data, "level", level, 0);
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

	return 0;
}

// ffenc_encode encodes one YUV420 frame.
// yuv_data points to packed planar YUV420 (Y: w*h, U: w/2*h/2, V: w/2*h/2).
// If force_idr is non-zero, the frame is forced to be an IDR keyframe.
// input_pts is the presentation timestamp passed through from the pipeline
// (90 kHz MPEG-TS time base) for correct A/V sync.
// On success (return 0): out_buf/out_len point directly into pkt->data.
// Caller must copy the data before calling ffenc_unref_packet().
// Returns 0 on success, 1 if EAGAIN (need more input), negative on error.
static int ffenc_encode(ffenc_t* h, unsigned char* yuv_data, int force_idr,
                        int64_t input_pts,
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
	int ht = h->height;
	int y_size = w * ht;
	int uv_w = w / 2;
	int uv_h = ht / 2;

	// Y plane
	for (int row = 0; row < ht; row++) {
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

	h->frame->pts = input_pts;

	if (force_idr) {
		h->frame->pict_type = AV_PICTURE_TYPE_I;
		COMPAT_SET_KEY_FRAME(h->frame, 1);
	} else {
		h->frame->pict_type = AV_PICTURE_TYPE_NONE;
		COMPAT_SET_KEY_FRAME(h->frame, 0);
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

	// Return pointer directly into pkt->data — no intermediate malloc+memcpy.
	// Caller must copy the data (via C.GoBytes) before calling ffenc_unref_packet.
	*out_buf = h->pkt->data;
	*out_len = h->pkt->size;
	*is_idr = (h->pkt->flags & AV_PKT_FLAG_KEY) ? 1 : 0;

	return 0;
}

// ffenc_unref_packet releases the packet data after the caller has copied it.
static void ffenc_unref_packet(ffenc_t* h) {
	av_packet_unref(h->pkt);
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
// width, height, bitrate, fpsNum, and fpsDen configure the output stream.
// fpsNum/fpsDen express the frame rate as a rational number (e.g. 30000/1001 for 29.97fps).
// gopSecs sets the IDR keyframe interval in seconds.
// hwDeviceCtx is reserved for future hardware acceleration (pass nil for software).
//
// The encoder always uses constrained VBR (cVBR): ABR with a tight 1.2x VBV
// ceiling. This produces predictable bitrate for SRT transport while preserving
// per-frame quality flexibility. Transport-level CBR padding is handled by the
// CBR pacer in the output layer, not by the encoder.
func NewFFmpegEncoder(codecName string, width, height, bitrate, fpsNum, fpsDen, gopSecs int, hwDeviceCtx unsafe.Pointer) (*FFmpegEncoder, error) {
	initFFmpegLogLevel()

	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d", width, height)
	}
	if bitrate <= 0 {
		return nil, fmt.Errorf("invalid bitrate: %d", bitrate)
	}
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil, fmt.Errorf("invalid fps: %d/%d", fpsNum, fpsDen)
	}

	cName := C.CString(codecName)
	defer C.free(unsafe.Pointer(cName))

	if gopSecs <= 0 {
		return nil, fmt.Errorf("invalid gopSecs: %d", gopSecs)
	}

	e := &FFmpegEncoder{}
	rc := C.ffenc_open(&e.handle, cName,
		C.int(width), C.int(height), C.int(bitrate),
		C.int(fpsNum), C.int(fpsDen),
		C.int(gopSecs), hwDeviceCtx)
	if rc != 0 {
		desc := map[int]string{
			-1: "codec not found",
			-2: "context allocation failed",
			-3: "avcodec_open2 failed",
			-4: "frame allocation failed",
			-5: "frame buffer allocation failed",
			-6: "packet allocation failed",
		}
		msg := desc[int(rc)]
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("failed to create FFmpeg encoder %q: %s (code %d)", codecName, msg, int(rc))
	}
	return e, nil
}

// Encode encodes a packed YUV420 planar frame to Annex B H.264 data.
// pts is the presentation timestamp in 90 kHz MPEG-TS units, passed through
// to the encoded bitstream for A/V sync.
// If forceIDR is true, the encoder forces an IDR keyframe.
// Returns the encoded bitstream, whether the frame is a keyframe, and any error.
func (e *FFmpegEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
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
		C.int64_t(pts),
		&outBuf, &outLen, &isIDR,
	)
	if rc < 0 {
		return nil, false, fmt.Errorf("FFmpeg encode error: code %d", int(rc))
	}
	if rc == 1 {
		// EAGAIN: encoder needs more input before producing output.
		// This is normal for hardware encoders (e.g. VideoToolbox) that
		// buffer a few frames during warmup. Return nil data, no error.
		return nil, false, nil
	}

	n := int(outLen)
	if n == 0 || outBuf == nil {
		return nil, false, fmt.Errorf("encoder produced no output")
	}

	// GoBytes copies pkt->data into Go memory; then unref releases the AVPacket.
	result := C.GoBytes(unsafe.Pointer(outBuf), outLen)
	C.ffenc_unref_packet(&e.handle)

	return result, isIDR != 0, nil
}

// Close releases the encoder resources. Safe to call multiple times.
func (e *FFmpegEncoder) Close() {
	if !e.closed {
		C.ffenc_close(&e.handle)
		e.closed = true
	}
}
