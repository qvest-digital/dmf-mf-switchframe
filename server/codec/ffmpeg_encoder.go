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
// When cbr is non-zero, the encoder uses constant bitrate mode with filler NALUs
// instead of quality-driven variable bitrate (CRF/VBR).
// Returns 0 on success, negative on error.
static int ffenc_open(ffenc_t* h, const char* codec_name,
                      int width, int height, int bitrate,
                      int fps_num, int fps_den,
                      int gop_secs, int cbr, void* hwDeviceCtx) {
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

	// Broadcast-quality rate control: target constant quality, not constant
	// bitrate. The encoder spends whatever bits are needed for each frame's
	// complexity — steady shots use low bitrate, transitions (wipes, stingers,
	// dissolves) burst high. A VBV ceiling prevents runaway bitrate.
	//
	// bit_rate is set as a hint for HW encoders that require it, but libx264
	// uses CRF mode which ignores it (quality-driven, not bitrate-driven).
	h->ctx->bit_rate = bitrate;

	// VBV ceiling: 2x source bitrate with 1-second buffer. Generous enough
	// that the rate controller doesn't crush quality during transitions,
	// but tight enough to prevent encoder-internal buffering from adding
	// latency. Larger VBV buffers let the encoder defer bits across more
	// frames, which helps quality but adds delay.
	h->ctx->rc_max_rate = bitrate * 2;
	h->ctx->rc_buffer_size = bitrate; // 1 second

	h->ctx->gop_size = fps_num * gop_secs / fps_den;
	h->ctx->max_b_frames = 0;
	h->ctx->pix_fmt = AV_PIX_FMT_YUV420P;

	// Signal BT.709 colorspace in VUI parameters.
	h->ctx->color_primaries = AVCOL_PRI_BT709;
	h->ctx->color_trc = AVCOL_TRC_BT709;
	h->ctx->colorspace = AVCOL_SPC_BT709;
	h->ctx->color_range = AVCOL_RANGE_MPEG; // limited range (16-235)

	// Derive thread count from CPU cores, clamped to [2, 8].
	int ncpu = (int)sysconf(_SC_NPROCESSORS_ONLN);
	if (ncpu < 2) ncpu = 2;
	if (ncpu > 8) ncpu = 8;
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
		av_opt_set(h->ctx->priv_data, "preset", "fast", 0);
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		// Variance-based AQ redistributes bits toward high-detail regions
		// (wipe boundaries, stinger edges) instead of uniform areas.
		av_opt_set(h->ctx->priv_data, "aq-mode", "2", 0);
		// Disable sync-lookahead (threaded lookahead adds latency).
		av_opt_set(h->ctx->priv_data, "sync-lookahead", "0", 0);
		// Disable mbtree — it needs deep lookahead to be effective and
		// adds frame-buffering latency in low-lookahead configurations.
		av_opt_set(h->ctx->priv_data, "mbtree", "0", 0);
		// Disable scene-change detection: transitions ARE the content change.
		av_opt_set(h->ctx->priv_data, "sc_threshold", "0", 0);
		char level_str[8];
		snprintf(level_str, sizeof(level_str), "%d", level);
		av_opt_set(h->ctx->priv_data, "level", level_str, 0);
		// Enable Access Unit Delimiters for MPEG-TS compliance.
		av_opt_set(h->ctx->priv_data, "aud", "1", 0);

		if (cbr) {
			// CBR: constant bitrate with HRD signaling and filler NALUs.
			// No CRF — incompatible with CBR rate control.
			av_opt_set(h->ctx->priv_data, "nal-hrd", "cbr", 0);
			h->ctx->rc_min_rate = bitrate; // floor = ceiling = target
			h->ctx->rc_max_rate = bitrate;
			h->ctx->rc_buffer_size = bitrate; // 1s VBV
			// CBR rate controller needs more planning horizon than CRF.
			av_opt_set(h->ctx->priv_data, "rc-lookahead", "10", 0);
			// Less bit redistribution pressure under CBR budget.
			av_opt_set(h->ctx->priv_data, "aq-strength", "1.0", 0);
		} else {
			// CRF (Constant Rate Factor): quality-targeted encoding.
			// CRF 22 balances quality with realtime encode speed. Lower values
			// (16-18) produce better quality but VideoToolbox/software encoders
			// can't sustain them at 60fps. 22 is visually clean for broadcast
			// while keeping encode times under the frame budget.
			av_opt_set(h->ctx->priv_data, "crf", "22", 0);
			av_opt_set(h->ctx->priv_data, "aq-strength", "1.2", 0);
			// Low-latency lookahead: 3 frames gives AQ enough context for
			// good bit allocation without adding significant delay. The
			// "fast" preset defaults to a higher lookahead which is
			// unacceptable for live switching.
			av_opt_set(h->ctx->priv_data, "rc-lookahead", "3", 0);
		}
	} else if (strcmp(codec_name, "h264_nvenc") == 0) {
		av_opt_set(h->ctx->priv_data, "preset", "p4", 0);
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		av_opt_set(h->ctx->priv_data, "delay", "0", 0);
		av_opt_set_int(h->ctx->priv_data, "spatial-aq", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "aq-strength", 8, 0);
		av_opt_set_int(h->ctx->priv_data, "no-scenecut", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "forced-idr", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "level", level, 0);

		if (cbr) {
			// CBR: constant bitrate mode.
			av_opt_set(h->ctx->priv_data, "rc", "cbr", 0);
			// temporal-aq is incompatible with CBR.
			av_opt_set_int(h->ctx->priv_data, "temporal-aq", 0, 0);
		} else {
			// VBR with constant quality target: NVENC's closest equivalent to CRF.
			// cq=22 targets quality similar to x264 CRF 22.
			av_opt_set(h->ctx->priv_data, "rc", "vbr", 0);
			av_opt_set(h->ctx->priv_data, "cq", "22", 0);
			// Temporal AQ for better bit distribution across frames during transitions.
			av_opt_set_int(h->ctx->priv_data, "temporal-aq", 1, 0);
		}
	} else if (strcmp(codec_name, "h264_vaapi") == 0) {
		av_opt_set_int(h->ctx->priv_data, "profile", 100, 0); // HIGH
		h->ctx->level = level;

		if (cbr) {
			// CBR: set min rate = target for constant bitrate.
			h->ctx->rc_min_rate = bitrate;
		}
	} else if (strcmp(codec_name, "h264_videotoolbox") == 0) {
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		av_opt_set_int(h->ctx->priv_data, "realtime", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "prio_speed", 1, 0);
		// Force frame-at-a-time output — no internal encoder frame buffering.
		// Without this, VT can hold 1-3 frames for rate control lookahead.
		h->ctx->max_b_frames = 0;
		av_opt_set_int(h->ctx->priv_data, "allow_b_frames", 0, 0);
		av_opt_set_int(h->ctx->priv_data, "level", level, 0);

		if (cbr) {
			// CBR: enable constant bitrate mode.
			av_opt_set(h->ctx->priv_data, "constant_bit_rate", "true", 0);
		} else {
			// Constant quality via capped VBR — higher quality than pure ABR.
			av_opt_set(h->ctx->priv_data, "constant_bit_rate", "false", 0);
		}
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
// cbr enables constant bitrate mode with filler NALUs. When false, the encoder
// uses CRF/VBR (quality-driven, variable bitrate).
// hwDeviceCtx is reserved for future hardware acceleration (pass nil for software).
func NewFFmpegEncoder(codecName string, width, height, bitrate, fpsNum, fpsDen, gopSecs int, cbr bool, hwDeviceCtx unsafe.Pointer) (*FFmpegEncoder, error) {
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

	cbrInt := C.int(0)
	if cbr {
		cbrInt = C.int(1)
	}

	e := &FFmpegEncoder{}
	rc := C.ffenc_open(&e.handle, cName,
		C.int(width), C.int(height), C.int(bitrate),
		C.int(fpsNum), C.int(fpsDen),
		C.int(gopSecs), cbrInt, hwDeviceCtx)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create FFmpeg encoder %q: code %d", codecName, int(rc))
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
