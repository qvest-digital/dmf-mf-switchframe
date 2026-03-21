//go:build cgo && cuda && !noffmpeg

package codec

/*
#cgo CFLAGS: -I/usr/local/cuda/include
#cgo LDFLAGS: -L/usr/local/cuda/lib64 -lcuda
#include <cuda.h>
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/version.h>
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_cuda.h>
#include <stdlib.h>
#include <string.h>

// AV_FRAME_FLAG_KEY was added in FFmpeg 6.1 (libavutil 58.29).
// For older versions (e.g. Debian Bookworm's FFmpeg 5.1), use key_frame field.
#if LIBAVUTIL_VERSION_INT < AV_VERSION_INT(58, 29, 100)
#define COMPAT_SET_KEY_FRAME_HW(frame, is_key) ((frame)->key_frame = (is_key))
#else
#define COMPAT_SET_KEY_FRAME_HW(frame, is_key) do { \
    if (is_key) (frame)->flags |= AV_FRAME_FLAG_KEY; \
    else (frame)->flags &= ~AV_FRAME_FLAG_KEY; \
} while(0)
#endif

// ffenc_base_t mirrors ffenc_t from ffmpeg_encoder.go for struct compatibility.
typedef struct {
	AVCodecContext* ctx;
	AVFrame*        frame;
	AVPacket*       pkt;
	int             width;
	int             height;
} ffenc_base_t;

// ffenc_hw_frames_t extends ffenc_base_t with hw_frames_ctx for CUDA→NVENC zero-copy.
typedef struct {
	ffenc_base_t     base;
	AVBufferRef*     hw_device_ref;   // AVHWDeviceContext (CUDA)
	AVBufferRef*     hw_frames_ref;   // AVHWFramesContext (NV12 on CUDA)
	AVFrame*         hw_frame;        // reusable AV_PIX_FMT_CUDA frame
} ffenc_hw_frames_t;

// ffenc_open_cuda_hw opens an NVENC encoder with hw_frames_ctx so that
// CUDA device pointers can be passed directly without CPU download.
// cuda_ctx is the CUcontext from the GPU pipeline.
// Returns 0 on success, negative on error.
static int ffenc_open_cuda_hw(ffenc_hw_frames_t* h, int width, int height,
                               int bitrate, int fps_num, int fps_den,
                               int gop_secs, void* cuda_ctx) {
	memset(h, 0, sizeof(ffenc_hw_frames_t));

	const AVCodec* codec = avcodec_find_encoder_by_name("h264_nvenc");
	if (!codec) return -1;

	h->base.ctx = avcodec_alloc_context3(codec);
	if (!h->base.ctx) return -2;

	h->base.width = width;
	h->base.height = height;
	h->base.ctx->width = width;
	h->base.ctx->height = height;
	h->base.ctx->time_base = (AVRational){fps_den, fps_num};
	h->base.ctx->framerate = (AVRational){fps_num, fps_den};
	h->base.ctx->bit_rate = bitrate;
	h->base.ctx->rc_max_rate = bitrate + bitrate / 5;
	h->base.ctx->rc_buffer_size = bitrate + bitrate / 5;
	h->base.ctx->gop_size = fps_num * gop_secs / fps_den;
	h->base.ctx->max_b_frames = 0;
	// Input pixel format is AV_PIX_FMT_CUDA — NVENC reads NV12 from device memory.
	h->base.ctx->pix_fmt = AV_PIX_FMT_CUDA;

	// BT.709 colorspace signaling
	h->base.ctx->color_primaries = AVCOL_PRI_BT709;
	h->base.ctx->color_trc = AVCOL_TRC_BT709;
	h->base.ctx->colorspace = AVCOL_SPC_BT709;
	h->base.ctx->color_range = AVCOL_RANGE_MPEG;

	// H.264 level
	float fps_f = (float)fps_num / (float)fps_den;
	int level;
	if (width <= 1280 && height <= 720)
		level = 31;
	else if (width <= 1920 && height <= 1080 && fps_f <= 30.5f)
		level = 40;
	else
		level = 42;

	// NVENC options (matching ffenc_open in ffmpeg_encoder.go)
	av_opt_set(h->base.ctx->priv_data, "preset", "p4", 0);
	av_opt_set(h->base.ctx->priv_data, "profile", "high", 0);
	av_opt_set(h->base.ctx->priv_data, "delay", "0", 0);
	av_opt_set_int(h->base.ctx->priv_data, "spatial-aq", 1, 0);
	av_opt_set_int(h->base.ctx->priv_data, "aq-strength", 8, 0);
	av_opt_set_int(h->base.ctx->priv_data, "no-scenecut", 1, 0);
	av_opt_set_int(h->base.ctx->priv_data, "forced-idr", 1, 0);
	av_opt_set_int(h->base.ctx->priv_data, "level", level, 0);
	av_opt_set(h->base.ctx->priv_data, "rc", "cbr", 0);
	av_opt_set_int(h->base.ctx->priv_data, "temporal-aq", 0, 0);
	av_opt_set(h->base.ctx->priv_data, "aud", "1", 0);

	// --- Create CUDA hw_device_ctx from existing CUcontext ---
	h->hw_device_ref = av_hwdevice_ctx_alloc(AV_HWDEVICE_TYPE_CUDA);
	if (!h->hw_device_ref) {
		avcodec_free_context(&h->base.ctx);
		return -10;
	}
	AVHWDeviceContext* device_ctx = (AVHWDeviceContext*)h->hw_device_ref->data;
	AVCUDADeviceContext* cuda_device = (AVCUDADeviceContext*)device_ctx->hwctx;
	cuda_device->cuda_ctx = (CUcontext)cuda_ctx;
	// Let FFmpeg manage the CUDA stream internally.
	cuda_device->stream = NULL;

	int rc = av_hwdevice_ctx_init(h->hw_device_ref);
	if (rc < 0) {
		av_buffer_unref(&h->hw_device_ref);
		avcodec_free_context(&h->base.ctx);
		return -11;
	}

	// --- Create hw_frames_ctx for NV12 on CUDA ---
	h->hw_frames_ref = av_hwframe_ctx_alloc(h->hw_device_ref);
	if (!h->hw_frames_ref) {
		av_buffer_unref(&h->hw_device_ref);
		avcodec_free_context(&h->base.ctx);
		return -12;
	}
	AVHWFramesContext* frames_ctx = (AVHWFramesContext*)h->hw_frames_ref->data;
	frames_ctx->format    = AV_PIX_FMT_CUDA;
	frames_ctx->sw_format = AV_PIX_FMT_NV12;
	frames_ctx->width     = width;
	frames_ctx->height    = height;
	// initial_pool_size=0: we provide our own device pointers, no FFmpeg pool needed.
	frames_ctx->initial_pool_size = 0;

	rc = av_hwframe_ctx_init(h->hw_frames_ref);
	if (rc < 0) {
		av_buffer_unref(&h->hw_frames_ref);
		av_buffer_unref(&h->hw_device_ref);
		avcodec_free_context(&h->base.ctx);
		return -13;
	}

	h->base.ctx->hw_frames_ctx = av_buffer_ref(h->hw_frames_ref);
	if (!h->base.ctx->hw_frames_ctx) {
		av_buffer_unref(&h->hw_frames_ref);
		av_buffer_unref(&h->hw_device_ref);
		avcodec_free_context(&h->base.ctx);
		return -14;
	}

	rc = avcodec_open2(h->base.ctx, codec, NULL);
	if (rc < 0) {
		av_buffer_unref(&h->hw_frames_ref);
		av_buffer_unref(&h->hw_device_ref);
		avcodec_free_context(&h->base.ctx);
		return -3;
	}

	// Allocate reusable CUDA AVFrame
	h->hw_frame = av_frame_alloc();
	if (!h->hw_frame) {
		av_buffer_unref(&h->hw_frames_ref);
		av_buffer_unref(&h->hw_device_ref);
		avcodec_free_context(&h->base.ctx);
		return -4;
	}

	h->base.pkt = av_packet_alloc();
	if (!h->base.pkt) {
		av_frame_free(&h->hw_frame);
		av_buffer_unref(&h->hw_frames_ref);
		av_buffer_unref(&h->hw_device_ref);
		avcodec_free_context(&h->base.ctx);
		return -6;
	}

	return 0;
}

// ffenc_encode_nv12_cuda encodes one NV12 frame from CUDA device memory.
// y_dev_ptr points to Y plane, uv_dev_ptr points to UV plane (both device pointers).
// pitch is the row stride in bytes.
// cuda_ctx is the CUcontext to push before encoding (cgo goroutines may migrate threads).
// Returns 0 on success, 1 if EAGAIN, negative on error.
static int ffenc_encode_nv12_cuda(ffenc_hw_frames_t* h,
                                   void* y_dev_ptr, void* uv_dev_ptr,
                                   int pitch, int64_t input_pts, int force_idr,
                                   void* cuda_ctx,
                                   unsigned char** out_buf, int* out_len, int* is_idr) {
	*out_buf = NULL;
	*out_len = 0;
	*is_idr  = 0;

	// Ensure CUDA context is active on this thread. cgo goroutines may
	// migrate to threads that haven't called cudaSetDevice(0).
	CUcontext prev_ctx;
	cuCtxPushCurrent((CUcontext)cuda_ctx);

	AVFrame* frame = h->hw_frame;

	// Set up the CUDA frame: point data[] to device memory
	frame->format         = AV_PIX_FMT_CUDA;
	frame->width          = h->base.width;
	frame->height         = h->base.height;
	frame->data[0]        = (uint8_t*)y_dev_ptr;
	frame->data[1]        = (uint8_t*)uv_dev_ptr;
	frame->linesize[0]    = pitch;
	frame->linesize[1]    = pitch;
	frame->pts            = input_pts;

	// Attach hw_frames_ctx to the frame (required for NVENC to know the CUDA context).
	// Unref previous attachment first if present.
	av_buffer_unref(&frame->hw_frames_ctx);
	frame->hw_frames_ctx = av_buffer_ref(h->hw_frames_ref);
	if (!frame->hw_frames_ctx) {
		cuCtxPopCurrent(&prev_ctx);
		return -7;
	}

	if (force_idr) {
		frame->pict_type = AV_PICTURE_TYPE_I;
		COMPAT_SET_KEY_FRAME_HW(frame, 1);
	} else {
		frame->pict_type = AV_PICTURE_TYPE_NONE;
		COMPAT_SET_KEY_FRAME_HW(frame, 0);
	}

	int rc = avcodec_send_frame(h->base.ctx, frame);
	if (rc < 0) {
		cuCtxPopCurrent(&prev_ctx);
		return rc; // return actual FFmpeg error code (negative)
	}

	rc = avcodec_receive_packet(h->base.ctx, h->base.pkt);
	cuCtxPopCurrent(&prev_ctx);

	if (rc == AVERROR(EAGAIN)) return 1;
	if (rc < 0) return -3;

	*out_buf = h->base.pkt->data;
	*out_len = h->base.pkt->size;
	*is_idr  = (h->base.pkt->flags & AV_PKT_FLAG_KEY) ? 1 : 0;
	return 0;
}

// ffenc_close_hw_frames frees all hw_frames encoder resources.
static void ffenc_close_hw_frames(ffenc_hw_frames_t* h) {
	if (h->hw_frame) {
		av_buffer_unref(&h->hw_frame->hw_frames_ctx);
		av_frame_free(&h->hw_frame);
	}
	if (h->base.pkt) {
		av_packet_free(&h->base.pkt);
	}
	if (h->base.ctx) {
		avcodec_free_context(&h->base.ctx);
	}
	if (h->hw_frames_ref) {
		av_buffer_unref(&h->hw_frames_ref);
	}
	if (h->hw_device_ref) {
		av_buffer_unref(&h->hw_device_ref);
	}
}

// ffenc_unref_packet_hw unrefs the packet after Go has copied the data.
static void ffenc_unref_packet_hw(ffenc_hw_frames_t* h) {
	av_packet_unref(h->base.pkt);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// FFmpegHWFramesEncoder wraps an NVENC encoder with hw_frames_ctx for
// direct GPU encode from CUDA device memory (zero-copy NV12 -> H.264).
//
// FFmpegHWFramesEncoder is NOT safe for concurrent use. Callers must
// synchronize access externally.
type FFmpegHWFramesEncoder struct {
	handle  C.ffenc_hw_frames_t
	cudaCtx unsafe.Pointer // CUcontext for thread-safe CUDA access
	closed  bool
}

// NewFFmpegHWFramesEncoder creates an NVENC encoder that reads NV12 directly
// from CUDA device memory via hw_frames_ctx. cudaCtx is the CUcontext from
// the GPU pipeline (obtained via gpu.Context).
//
// Returns an error if NVENC or CUDA hw context creation fails. The caller
// should fall back to the standard GPU->CPU download path in that case.
func NewFFmpegHWFramesEncoder(cudaCtx unsafe.Pointer, width, height, bitrate, fpsNum, fpsDen, gopSecs int) (*FFmpegHWFramesEncoder, error) {
	initFFmpegLogLevel()

	if cudaCtx == nil {
		return nil, fmt.Errorf("cuda context is nil")
	}
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

	e := &FFmpegHWFramesEncoder{}
	FFmpegOpenMu.Lock()
	rc := C.ffenc_open_cuda_hw(&e.handle,
		C.int(width), C.int(height), C.int(bitrate),
		C.int(fpsNum), C.int(fpsDen),
		C.int(gopSecs), cudaCtx)
	FFmpegOpenMu.Unlock()
	if rc != 0 {
		desc := map[int]string{
			-1:  "NVENC codec not found",
			-2:  "context allocation failed",
			-3:  "avcodec_open2 failed",
			-4:  "frame allocation failed",
			-6:  "packet allocation failed",
			-10: "CUDA hw_device_ctx alloc failed",
			-11: "CUDA hw_device_ctx init failed",
			-12: "hw_frames_ctx alloc failed",
			-13: "hw_frames_ctx init failed",
			-14: "hw_frames_ctx ref failed",
		}
		msg := desc[int(rc)]
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("failed to create NVENC hw_frames encoder: %s (code %d)", msg, int(rc))
	}
	e.cudaCtx = cudaCtx
	return e, nil
}

// EncodeNV12CUDA encodes one NV12 frame from CUDA device memory.
// yDevPtr is the device pointer to the Y plane, uvDevPtr to the UV plane.
// pitch is the row stride in bytes for both planes.
// pts is the presentation timestamp in 90 kHz MPEG-TS units.
// Returns the encoded Annex B H.264 bitstream, whether it's an IDR, and any error.
func (e *FFmpegHWFramesEncoder) EncodeNV12CUDA(yDevPtr, uvDevPtr unsafe.Pointer, pitch int, pts int64, forceIDR bool) ([]byte, bool, error) {
	if e.closed {
		return nil, false, errors.New("encoder is closed")
	}

	forceIDRInt := C.int(0)
	if forceIDR {
		forceIDRInt = 1
	}

	var outBuf *C.uchar
	var outLen, isIDR C.int

	rc := C.ffenc_encode_nv12_cuda(&e.handle,
		yDevPtr, uvDevPtr,
		C.int(pitch), C.int64_t(pts), forceIDRInt,
		e.cudaCtx,
		&outBuf, &outLen, &isIDR)
	if rc < 0 {
		return nil, false, fmt.Errorf("NVENC hw_frames encode error: code %d", int(rc))
	}
	if rc == 1 {
		// EAGAIN -- need more input
		return nil, false, nil
	}

	if outLen == 0 || outBuf == nil {
		return nil, false, errors.New("NVENC hw_frames encoder produced no output")
	}

	result := C.GoBytes(unsafe.Pointer(outBuf), outLen)
	C.ffenc_unref_packet_hw(&e.handle)

	return result, isIDR != 0, nil
}

// Close releases the encoder resources. Safe to call multiple times.
func (e *FFmpegHWFramesEncoder) Close() {
	if !e.closed {
		C.ffenc_close_hw_frames(&e.handle)
		e.closed = true
	}
}
