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
	int             pix_fmt_10bit; // non-zero if YUV422P10LE (10-bit 4:2:2)
} ffenc_t;

// ffenc_open initializes the encoder with the given codec name and parameters.
// hwDeviceCtx is currently unused (reserved for future HW accel).
// The encoder always uses constrained VBR (ABR + tight VBV) for predictable
// bitrate suitable for SRT transport, while maintaining quality flexibility.
// Returns 0 on success, negative on error.
static int ffenc_open(ffenc_t* h, const char* codec_name,
                      int width, int height, int bitrate,
                      int fps_num, int fps_den,
                      int gop_secs, void* hwDeviceCtx,
                      int pix_fmt_10bit,
                      const char* preset_override) {
	memset(h, 0, sizeof(ffenc_t));
	h->pix_fmt_10bit = pix_fmt_10bit;

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
	h->ctx->pix_fmt = pix_fmt_10bit ? AV_PIX_FMT_YUV422P10LE : AV_PIX_FMT_YUV420P;

	// Signal BT.709 colorspace in VUI parameters.
	h->ctx->color_primaries = AVCOL_PRI_BT709;
	h->ctx->color_trc = AVCOL_TRC_BT709;
	h->ctx->colorspace = AVCOL_SPC_BT709;
	h->ctx->color_range = AVCOL_RANGE_MPEG; // limited range (16-235)
	h->ctx->chroma_sample_location = AVCHROMA_LOC_LEFT;

	// Thread count for sliced threading (zerolatency tune): determines how
	// many slices per frame. More threads = faster per-frame encode at the
	// cost of mild slice-boundary artifacts. At 1080p, 8 slices = 135 rows
	// each (8.4 macroblock rows) — negligible quality impact.
	// Must be high enough to sustain real-time encode when CPU is shared
	// with per-source decoder goroutines (3×60fps + 1×30fps = 210 decodes/sec).
	int ncpu = (int)sysconf(_SC_NPROCESSORS_ONLN);
	if (ncpu < 2) ncpu = 2;
	if (ncpu > 8) ncpu = 8;
	h->ctx->thread_count = ncpu;

	// Set explicit H.264 level for downstream decoder compatibility.
	// HEVC uses a different level numbering scheme (see HEVC section below).
	float fps_f = (float)fps_num / (float)fps_den;
	int avc_level;
	if (width <= 1280 && height <= 720) {
		avc_level = 31; // Level 3.1
	} else if (width <= 1920 && height <= 1080 && fps_f <= 30.5f) {
		avc_level = 40; // Level 4.0
	} else {
		avc_level = 42; // Level 4.2
	}

	// HEVC level: ITU-T H.265 Table A.6. Level values are 30x the level number.
	// Level 3.1 = 93, Level 4.0 = 120, Level 4.1 = 123.
	int hevc_level;
	if (width <= 1280 && height <= 720) {
		hevc_level = 93;  // Level 3.1
	} else if (width <= 1920 && height <= 1080 && fps_f <= 30.5f) {
		hevc_level = 120; // Level 4.0
	} else {
		hevc_level = 123; // Level 4.1
	}

	// --- H.264 (AVC) codec-specific options ---
	if (strcmp(codec_name, "libx264") == 0) {
		av_opt_set(h->ctx->priv_data, "preset",
			(preset_override && preset_override[0]) ? preset_override : "superfast", 0);
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		// zerolatency: sliced threading (zero frame buffer), no mbtree,
		// sync-lookahead=0, rc-lookahead=0, force-cfr=1.
		// Eliminates ~100ms internal encoder buffering from frame threading.
		// Throughput relies on slice parallelism (thread_count above) instead
		// of pipelining multiple frames.
		av_opt_set(h->ctx->priv_data, "tune", "zerolatency", 0);
		// Auto-variance AQ adapts per-frame between temporal and spatial
		// redistribution — better than mode 2 for mixed content (static →
		// dissolve → stinger → camera motion).
		av_opt_set(h->ctx->priv_data, "aq-mode", "3", 0);
		av_opt_set(h->ctx->priv_data, "aq-strength", "1.2", 0);
		// Disable scene-change detection: transitions ARE the content change.
		av_opt_set(h->ctx->priv_data, "sc_threshold", "0", 0);
		char level_str[8];
		snprintf(level_str, sizeof(level_str), "%d", avc_level);
		av_opt_set(h->ctx->priv_data, "level", level_str, 0);
		// Enable Access Unit Delimiters for MPEG-TS compliance.
		av_opt_set(h->ctx->priv_data, "aud", "1", 0);
		// Slightly reduce deblocking to preserve fine detail at broadcast bitrates.
		av_opt_set(h->ctx->priv_data, "deblock", "-1:-1", 0);
		// NOTE: weightp and psy-rd omitted. With superfast (subme=1), psy-rd
		// has negligible quality impact but adds CPU overhead. weightp=2 does
		// per-frame reference analysis that costs ~5-10% CPU — unaffordable
		// when sharing cores with 4+ source decoder goroutines at 60fps.
	} else if (strcmp(codec_name, "h264_nvenc") == 0) {
		av_opt_set(h->ctx->priv_data, "preset",
			(preset_override && preset_override[0]) ? preset_override : "p4", 0);
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		av_opt_set(h->ctx->priv_data, "delay", "0", 0);
		av_opt_set_int(h->ctx->priv_data, "spatial-aq", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "aq-strength", 8, 0);
		av_opt_set_int(h->ctx->priv_data, "no-scenecut", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "forced-idr", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "level", avc_level, 0);
		// NVENC CBR is hardware-native and works correctly.
		// Note: with rc=cbr, NVENC uses bit_rate as the target and ignores
		// rc_max_rate. The VBV buffer (rc_buffer_size) is still applied.
		av_opt_set(h->ctx->priv_data, "rc", "cbr", 0);
		// temporal-aq is incompatible with CBR on NVENC.
		av_opt_set_int(h->ctx->priv_data, "temporal-aq", 0, 0);
	} else if (strcmp(codec_name, "h264_vaapi") == 0) {
		av_opt_set_int(h->ctx->priv_data, "profile", 100, 0); // HIGH
		h->ctx->level = avc_level;
	} else if (strcmp(codec_name, "h264_videotoolbox") == 0) {
		av_opt_set(h->ctx->priv_data, "profile", "high", 0);
		av_opt_set_int(h->ctx->priv_data, "realtime", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "prio_speed", 1, 0);
		// Force frame-at-a-time output — no internal encoder frame buffering.
		h->ctx->max_b_frames = 0;
		av_opt_set_int(h->ctx->priv_data, "allow_b_frames", 0, 0);
		av_opt_set_int(h->ctx->priv_data, "level", avc_level, 0);
	}
	// --- H.265 (HEVC) codec-specific options ---
	// Mirrors the libx264 configuration above for broadcast-quality live encoding:
	// superfast preset (not ultrafast — measurable quality improvement at negligible
	// speed cost with zerolatency tune), AQ mode 3 for mixed content, AUDs for
	// MPEG-TS compliance, closed GOP for random access, deblock tuning for detail.
	else if (strcmp(codec_name, "libx265") == 0) {
		// superfast matches our libx264 preset. With zerolatency tune, the quality
		// delta between ultrafast and superfast is real but the speed cost is <5%.
		av_opt_set(h->ctx->priv_data, "preset",
			(preset_override && preset_override[0]) ? preset_override : "superfast", 0);
		// zerolatency: disables frame parallelism (zero internal buffer), uses
		// sliced threading via thread_count. Eliminates encoder buffering latency.
		av_opt_set(h->ctx->priv_data, "tune", "zerolatency", 0);
		if (pix_fmt_10bit) {
			av_opt_set(h->ctx->priv_data, "profile", "main422-10", 0);
		} else {
			av_opt_set(h->ctx->priv_data, "profile", "main", 0);
		}
		// Auto-variance AQ adapts per-frame between temporal and spatial
		// redistribution — better than mode 2 for mixed content (static →
		// dissolve → stinger → camera motion). Matches libx264 aq-mode=3.
		// x265-params is the only way to pass x265-specific options via FFmpeg.
		av_opt_set(h->ctx->priv_data, "x265-params",
			"aq-mode=3:aq-strength=1.2"    // auto-variance AQ (matches x264)
			":no-scenecut=1"                // transitions are the content change
			":no-open-gop=1"                // closed GOP for MPEG-TS random access
			":aud=1"                        // AUDs mandatory for MPEG-TS compliance
			":repeat-headers=1"             // VPS/SPS/PPS on every IDR for mid-stream join
			":deblock=-1,-1"                // mild deblock reduction (preserve detail)
			, 0);
		av_opt_set_int(h->ctx->priv_data, "level-idc", hevc_level, 0);
	} else if (strcmp(codec_name, "hevc_nvenc") == 0) {
		av_opt_set(h->ctx->priv_data, "preset",
			(preset_override && preset_override[0]) ? preset_override : "p4", 0);
		av_opt_set(h->ctx->priv_data, "profile", pix_fmt_10bit ? "main10" : "main", 0);
		av_opt_set(h->ctx->priv_data, "delay", "0", 0);
		av_opt_set_int(h->ctx->priv_data, "spatial-aq", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "aq-strength", 8, 0);
		av_opt_set_int(h->ctx->priv_data, "no-scenecut", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "forced-idr", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "level", hevc_level, 0);
		av_opt_set(h->ctx->priv_data, "rc", "cbr", 0);
		av_opt_set_int(h->ctx->priv_data, "temporal-aq", 0, 0);
	} else if (strcmp(codec_name, "hevc_vaapi") == 0) {
		if (pix_fmt_10bit) {
			av_opt_set_int(h->ctx->priv_data, "profile", 2, 0); // Main 10 (FF_PROFILE_HEVC_MAIN_10)
		} else {
			av_opt_set_int(h->ctx->priv_data, "profile", 1, 0); // Main
		}
		h->ctx->level = hevc_level;
	} else if (strcmp(codec_name, "hevc_videotoolbox") == 0) {
		if (pix_fmt_10bit) {
			av_opt_set(h->ctx->priv_data, "profile", "main10", 0);
		} else {
			av_opt_set(h->ctx->priv_data, "profile", "main", 0);
		}
		av_opt_set_int(h->ctx->priv_data, "realtime", 1, 0);
		av_opt_set_int(h->ctx->priv_data, "prio_speed", 1, 0);
		h->ctx->max_b_frames = 0;
		av_opt_set_int(h->ctx->priv_data, "allow_b_frames", 0, 0);
		av_opt_set_int(h->ctx->priv_data, "level", hevc_level, 0);
	}

	// Enable Access Unit Delimiters for hardware encoders and software encoders
	// that don't set it above. libx264 sets aud=1 above; for others set it here.
	// av_opt_set returns error if unsupported — safe to ignore.
	if (strcmp(codec_name, "libx264") != 0 && strcmp(codec_name, "libx265") != 0) {
		av_opt_set(h->ctx->priv_data, "aud", "1", 0);
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
	h->frame->format = pix_fmt_10bit ? AV_PIX_FMT_YUV422P10LE : AV_PIX_FMT_YUV420P;
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

// ffenc_open_preview initializes a lightweight preview encoder.
// Uses libx264 with the given preset, baseline profile, zerolatency tune.
// Designed for low-bitrate preview proxy encoding — hardware encoders are
// reserved for the program output path.
// Returns 0 on success, negative on error.
static int ffenc_open_preview(ffenc_t* h, int width, int height, int bitrate,
                              int fps_num, int fps_den, int gop_secs,
                              const char* preset) {
	memset(h, 0, sizeof(ffenc_t));

	const AVCodec* codec = avcodec_find_encoder_by_name("libx264");
	if (!codec) {
		return -1;
	}

	h->ctx = avcodec_alloc_context3(codec);
	if (!h->ctx) {
		return -2;
	}

	h->width = width;
	h->height = height;

	h->ctx->width = width;
	h->ctx->height = height;
	h->ctx->time_base = (AVRational){fps_den, fps_num};
	h->ctx->framerate = (AVRational){fps_num, fps_den};

	// Constrained VBR: same formula as production encoder.
	h->ctx->bit_rate = bitrate;
	h->ctx->rc_max_rate = bitrate + bitrate / 5; // 1.2x target
	h->ctx->rc_buffer_size = bitrate + bitrate / 5;

	h->ctx->gop_size = fps_num * gop_secs / fps_den;
	h->ctx->max_b_frames = 0;
	h->ctx->pix_fmt = AV_PIX_FMT_YUV420P;
	h->ctx->thread_count = 2;

	// BT.709 colorspace signaling, limited range.
	h->ctx->color_primaries = AVCOL_PRI_BT709;
	h->ctx->color_trc = AVCOL_TRC_BT709;
	h->ctx->colorspace = AVCOL_SPC_BT709;
	h->ctx->color_range = AVCOL_RANGE_MPEG;
	h->ctx->chroma_sample_location = AVCHROMA_LOC_LEFT;

	// NOTE: No AV_CODEC_FLAG_GLOBAL_HEADER. SPS/PPS are emitted inline
	// in the Annex B bitstream on keyframes, matching the production encoder
	// pattern. The MoQ relay extracts them from AVC1 NALUs directly.

	// baseline profile, zerolatency tune. Preset set by caller via parameter.
	av_opt_set(h->ctx->priv_data, "preset", preset, 0);
	av_opt_set(h->ctx->priv_data, "profile", "baseline", 0);
	av_opt_set(h->ctx->priv_data, "tune", "zerolatency", 0);
	av_opt_set(h->ctx->priv_data, "sc_threshold", "0", 0);
	av_opt_set(h->ctx->priv_data, "aud", "1", 0);
	// pic-struct=1 sets pic_struct_present_flag in the SPS VUI. Without it, a
	// pic_timing SEI carrying SMPTE timecode is not advertised as present, so a
	// strict decoder/parser (incl. the operator UI's timecode.js) skips it and
	// the embedded timecode is lost. The preview's SMPTE timecode passthrough
	// (preview.rewriteAVC1WithTimecode) relies on this flag being set.
	av_opt_set(h->ctx->priv_data, "x264-params", "pic-struct=1", 0);

	int rc = avcodec_open2(h->ctx, codec, NULL);
	if (rc < 0) {
		avcodec_free_context(&h->ctx);
		return -3;
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

	// Copy packed YUV input into the AVFrame planes, respecting linesize.
	int w = h->width;
	int ht = h->height;

	if (h->pix_fmt_10bit) {
		// YUV422P10LE: Y plane = w*h*2 bytes (uint16 per sample),
		// Cb plane = w/2*h*2 bytes, Cr plane = w/2*h*2 bytes.
		int y_row_bytes = w * 2;
		int uv_w = w / 2;
		int uv_row_bytes = uv_w * 2;
		int y_plane_size = y_row_bytes * ht;
		int uv_plane_size = uv_row_bytes * ht; // full height for 4:2:2

		// Y plane
		for (int row = 0; row < ht; row++) {
			memcpy(h->frame->data[0] + row * h->frame->linesize[0],
			       yuv_data + row * y_row_bytes, y_row_bytes);
		}
		// Cb plane
		for (int row = 0; row < ht; row++) {
			memcpy(h->frame->data[1] + row * h->frame->linesize[1],
			       yuv_data + y_plane_size + row * uv_row_bytes, uv_row_bytes);
		}
		// Cr plane
		for (int row = 0; row < ht; row++) {
			memcpy(h->frame->data[2] + row * h->frame->linesize[2],
			       yuv_data + y_plane_size + uv_plane_size + row * uv_row_bytes, uv_row_bytes);
		}
	} else {
		// YUV420P: Y plane = w*h bytes, Cb = w/2*h/2, Cr = w/2*h/2.
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
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// Compile-time check that FFmpegEncoder implements transition.VideoEncoder.
var _ transition.VideoEncoder = (*FFmpegEncoder)(nil)

// FFmpegEncoder wraps an FFmpeg libavcodec encoder and implements transition.VideoEncoder.
// It encodes packed YUV420 planar frames to Annex B H.264 bitstream.
//
// FFmpegEncoder is NOT safe for concurrent use. Callers must synchronize access externally.
type FFmpegEncoder struct {
	// mu only serializes concurrent Close calls so that freeing the C context
	// and setting closed is idempotent. It does NOT make Encode/Extradata safe
	// to call concurrently with Close (that would be a use-after-free of the
	// AVCodecContext) — the documented external-synchronization contract above
	// is what keeps the hot path safe.
	mu     sync.Mutex
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
func NewFFmpegEncoder(codecName string, width, height, bitrate, fpsNum, fpsDen, gopSecs int, hwDeviceCtx unsafe.Pointer, opts ...*EncoderOptions) (*FFmpegEncoder, error) {
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

	// Apply opts overrides.
	var encOpts *EncoderOptions
	if len(opts) > 0 {
		encOpts = opts[0]
	}
	if encOpts != nil && encOpts.GOPSecs > 0 {
		gopSecs = encOpts.GOPSecs
	}

	if gopSecs <= 0 {
		return nil, fmt.Errorf("invalid gopSecs: %d", gopSecs)
	}

	// Resolve preset override for the codec.
	var cPreset *C.char
	if encOpts != nil && encOpts.Preset != "" {
		var presetStr string
		switch {
		case strings.HasSuffix(codecName, "_nvenc"):
			presetStr = encOpts.Preset.NVENCPreset()
		case codecName == "libx264":
			presetStr = encOpts.Preset.X264Preset()
		case codecName == "libx265":
			presetStr = encOpts.Preset.X265Preset()
		}
		if presetStr != "" {
			cPreset = C.CString(presetStr)
			defer C.free(unsafe.Pointer(cPreset))
		}
	}

	e := &FFmpegEncoder{}
	FFmpegOpenMu.Lock()
	rc := C.ffenc_open(&e.handle, cName,
		C.int(width), C.int(height), C.int(bitrate),
		C.int(fpsNum), C.int(fpsDen),
		C.int(gopSecs), hwDeviceCtx, C.int(0), cPreset)
	FFmpegOpenMu.Unlock()
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

// NewFFmpegEncoder10bit creates a 10-bit YUV422P10LE encoder.
// Uses the same codec selection as NewFFmpegEncoder but configures the encoder
// for 10-bit 4:2:2 input (professional pipeline profile).
// For libx265: profile=main422-10. For NVENC: profile=main10.
func NewFFmpegEncoder10bit(codecName string, width, height, bitrate, fpsNum, fpsDen, gopSecs int, hwDeviceCtx unsafe.Pointer, opts ...*EncoderOptions) (*FFmpegEncoder, error) {
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

	// Apply opts overrides.
	var encOpts *EncoderOptions
	if len(opts) > 0 {
		encOpts = opts[0]
	}
	if encOpts != nil && encOpts.GOPSecs > 0 {
		gopSecs = encOpts.GOPSecs
	}

	if gopSecs <= 0 {
		return nil, fmt.Errorf("invalid gopSecs: %d", gopSecs)
	}

	cName := C.CString(codecName)
	defer C.free(unsafe.Pointer(cName))

	// Resolve preset override for the codec.
	var cPreset *C.char
	if encOpts != nil && encOpts.Preset != "" {
		var presetStr string
		switch {
		case strings.HasSuffix(codecName, "_nvenc"):
			presetStr = encOpts.Preset.NVENCPreset()
		case codecName == "libx265":
			presetStr = encOpts.Preset.X265Preset()
		}
		if presetStr != "" {
			cPreset = C.CString(presetStr)
			defer C.free(unsafe.Pointer(cPreset))
		}
	}

	e := &FFmpegEncoder{}
	FFmpegOpenMu.Lock()
	rc := C.ffenc_open(&e.handle, cName,
		C.int(width), C.int(height), C.int(bitrate),
		C.int(fpsNum), C.int(fpsDen),
		C.int(gopSecs), hwDeviceCtx, C.int(1), cPreset)
	FFmpegOpenMu.Unlock()
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
		return nil, fmt.Errorf("failed to create 10-bit FFmpeg encoder %q: %s (code %d)", codecName, msg, int(rc))
	}
	return e, nil
}

// NewFFmpegPreviewEncoder creates a lightweight preview encoder using libx264
// with the given preset (e.g. "ultrafast", "veryfast") and baseline profile.
// It always uses software encoding — hardware encoders are reserved for the
// program output path.
//
// width, height, bitrate, fpsNum, and fpsDen configure the output stream.
// gopSecs sets the IDR keyframe interval in seconds.
func NewFFmpegPreviewEncoder(width, height, bitrate, fpsNum, fpsDen, gopSecs int, preset ...string) (*FFmpegEncoder, error) {
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
	if gopSecs <= 0 {
		return nil, fmt.Errorf("invalid gopSecs: %d", gopSecs)
	}

	p := "ultrafast"
	if len(preset) > 0 && preset[0] != "" {
		p = preset[0]
	}
	cPreset := C.CString(p)
	defer C.free(unsafe.Pointer(cPreset))

	e := &FFmpegEncoder{}
	FFmpegOpenMu.Lock()
	rc := C.ffenc_open_preview(&e.handle,
		C.int(width), C.int(height), C.int(bitrate),
		C.int(fpsNum), C.int(fpsDen),
		C.int(gopSecs), cPreset)
	FFmpegOpenMu.Unlock()
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
		return nil, fmt.Errorf("failed to create FFmpeg preview encoder: %s (code %d)", msg, int(rc))
	}
	return e, nil
}

// Extradata returns the encoder's extradata (SPS/PPS) when AV_CODEC_FLAG_GLOBAL_HEADER
// is set. Returns nil if no extradata is available.
func (e *FFmpegEncoder) Extradata() []byte {
	if e.closed || e.handle.ctx == nil {
		return nil
	}
	size := int(e.handle.ctx.extradata_size)
	if size <= 0 || e.handle.ctx.extradata == nil {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(e.handle.ctx.extradata), C.int(size))
}

// Encode encodes a packed YUV420 planar frame to Annex B H.264 data.
// pts is the presentation timestamp in 90 kHz MPEG-TS units, passed through
// to the encoded bitstream for A/V sync.
// If forceIDR is true, the encoder forces an IDR keyframe.
// Returns the encoded bitstream, whether the frame is a keyframe, and any error.
func (e *FFmpegEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	if e.closed {
		return nil, false, errors.New("encoder is closed")
	}

	w := int(e.handle.width)
	h := int(e.handle.height)
	// The expected input size depends on the encoder's pixel format: the C
	// side copies w*h*4 bytes for YUV422P10LE (10-bit) and w*h*3/2 bytes for
	// YUV420P (8-bit). Validating against the wrong size would either reject
	// every valid frame or let the C side read past the end of the slice.
	if e.handle.pix_fmt_10bit != 0 {
		expected := video.YUV422_10bitFrameSize(w, h)
		if len(yuv) != expected {
			return nil, false, fmt.Errorf("YUV buffer must be %d bytes (%dx%d*4, YUV422P10LE), got %d",
				expected, w, h, len(yuv))
		}
	} else {
		expected := video.YUV420FrameSize(w, h)
		if len(yuv) != expected {
			return nil, false, fmt.Errorf("YUV buffer must be %d bytes (%dx%d*3/2), got %d",
				expected, w, h, len(yuv))
		}
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
		// A packet was successfully received (rc==0); release it before
		// returning, mirroring the success path so no buffer is stranded.
		C.ffenc_unref_packet(&e.handle)
		return nil, false, errors.New("encoder produced no output")
	}

	// GoBytes copies pkt->data into Go memory; then unref releases the AVPacket.
	result := C.GoBytes(unsafe.Pointer(outBuf), outLen)
	C.ffenc_unref_packet(&e.handle)

	return result, isIDR != 0, nil
}

// Close releases the encoder resources. Safe to call multiple times.
func (e *FFmpegEncoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		C.ffenc_close(&e.handle)
		e.closed = true
	}
}
