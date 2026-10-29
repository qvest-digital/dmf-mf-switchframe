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
	int             output_10bit_422;  // when 1, preserve 10-bit 422 output instead of downconverting
} ffdec_t;

// Forward declaration.
static int ffdec_open_codec(ffdec_t* h, void* hwDeviceCtx, int thread_count,
                             enum AVCodecID codec_id);

// ffdec_open initializes an H.264 decoder (convenience wrapper for ffdec_open_codec).
// hwDeviceCtx is reserved for future hardware acceleration (pass NULL for software).
// thread_count controls multithreading: 0 = auto (ncpu, 2-4), 1 = single-threaded.
// Single-threaded avoids frame-level buffering delay (important for clip decoders
// where immediate output is needed per input frame).
// Returns 0 on success, negative on error.
static int ffdec_open(ffdec_t* h, void* hwDeviceCtx, int thread_count) {
	return ffdec_open_codec(h, hwDeviceCtx, thread_count, AV_CODEC_ID_H264);
}

// ffdec_open_codec initializes a decoder for the specified codec ID.
// Supports AV_CODEC_ID_H264 and AV_CODEC_ID_HEVC.
static int ffdec_open_codec(ffdec_t* h, void* hwDeviceCtx, int thread_count,
                             enum AVCodecID codec_id) {
	memset(h, 0, sizeof(ffdec_t));

	// av_log_set_level is called once from Go via initFFmpegLogLevel().

	const AVCodec* codec = avcodec_find_decoder(codec_id);
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

	if (thread_count <= 0) {
		int ncpu = (int)sysconf(_SC_NPROCESSORS_ONLN);
		if (ncpu < 2) ncpu = 2;
		if (ncpu > 4) ncpu = 4;
		h->ctx->thread_count = ncpu;
	} else {
		h->ctx->thread_count = thread_count;
	}

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

// ffdec_is_planar_420 returns 1 if the frame is YUV420P or YUVJ420P (3-plane 8-bit planar).
static int ffdec_is_planar_420(AVFrame* f) {
	return f->format == AV_PIX_FMT_YUV420P || f->format == AV_PIX_FMT_YUVJ420P;
}

// ffdec_is_planar_420_10bit returns 1 if the frame is YUV420P10LE or YUV420P10BE (10-bit planar).
static int ffdec_is_planar_420_10bit(AVFrame* f) {
	return f->format == AV_PIX_FMT_YUV420P10LE || f->format == AV_PIX_FMT_YUV420P10BE;
}

// ffdec_is_planar_422 returns 1 if the frame is YUV422P or YUVJ422P (8-bit 4:2:2 planar).
static int ffdec_is_planar_422(AVFrame* f) {
	return f->format == AV_PIX_FMT_YUV422P || f->format == AV_PIX_FMT_YUVJ422P;
}

// ffdec_is_planar_422_10bit returns 1 if the frame is YUV422P10LE or YUV422P10BE (10-bit 4:2:2 planar).
static int ffdec_is_planar_422_10bit(AVFrame* f) {
	return f->format == AV_PIX_FMT_YUV422P10LE || f->format == AV_PIX_FMT_YUV422P10BE;
}

// ffdec_is_supported_format returns 1 if the decoder can handle this pixel format.
static int ffdec_is_supported_format(AVFrame* f) {
	return ffdec_is_planar_420(f) || ffdec_is_planar_420_10bit(f)
	    || ffdec_is_planar_422(f) || ffdec_is_planar_422_10bit(f)
	    || f->format == AV_PIX_FMT_NV12;
}

// ffdec_copy_10bit_to_8bit copies a 10-bit YUV420P plane to 8-bit by right-shifting.
// src contains uint16_t samples (2 bytes each), dst contains uint8_t.
static void ffdec_copy_10bit_plane(unsigned char* dst, const unsigned char* src,
                                    int src_linesize, int width, int height,
                                    int is_big_endian, const unsigned char* remap_lut) {
	for (int row = 0; row < height; row++) {
		const unsigned char* s = src + row * src_linesize;
		unsigned char* d = dst + row * width;
		for (int col = 0; col < width; col++) {
			unsigned short val;
			if (is_big_endian) {
				val = ((unsigned short)s[col * 2] << 8) | s[col * 2 + 1];
			} else {
				val = s[col * 2] | ((unsigned short)s[col * 2 + 1] << 8);
			}
			// 10-bit (0-1023) to 8-bit (0-255): shift right by 2.
			unsigned char v8 = (unsigned char)(val >> 2);
			d[col] = remap_lut ? remap_lut[v8] : v8;
		}
	}
}

// ffdec_copy_10bit copies 10-bit YUV420P into packed 8-bit YUV420P dst buffer.
static void ffdec_copy_10bit(AVFrame* src_frame, unsigned char* dst,
                              int w, int h_val, int remap_to_limited) {
	int uv_w = w / 2;
	int uv_h = h_val / 2;
	int y_size = w * h_val;
	int uv_size = uv_w * uv_h;
	int is_be = (src_frame->format == AV_PIX_FMT_YUV420P10BE);

	const unsigned char* y_lut  = remap_to_limited ? full_to_limited_y  : NULL;
	const unsigned char* uv_lut = remap_to_limited ? full_to_limited_uv : NULL;

	ffdec_copy_10bit_plane(dst, src_frame->data[0],
	                        src_frame->linesize[0], w, h_val, is_be, y_lut);
	ffdec_copy_10bit_plane(dst + y_size, src_frame->data[1],
	                        src_frame->linesize[1], uv_w, uv_h, is_be, uv_lut);
	ffdec_copy_10bit_plane(dst + y_size + uv_size, src_frame->data[2],
	                        src_frame->linesize[2], uv_w, uv_h, is_be, uv_lut);
}

// ffdec_copy_422p10le_plane copies a 10-bit planar plane directly, preserving uint16 LE samples.
// Handles stride padding by copying exactly width samples per row.
static void ffdec_copy_422p10le_plane(unsigned char* dst, const unsigned char* src,
                                       int src_linesize, int width_samples, int height,
                                       int is_big_endian) {
	int row_bytes = width_samples * 2; // 2 bytes per sample (uint16)
	if (!is_big_endian) {
		// Already LE — just copy rows (handling stride).
		for (int row = 0; row < height; row++) {
			memcpy(dst + row * row_bytes, src + row * src_linesize, row_bytes);
		}
	} else {
		// BE → LE byte-swap each sample.
		for (int row = 0; row < height; row++) {
			const unsigned char* s = src + row * src_linesize;
			unsigned char* d = dst + row * row_bytes;
			for (int col = 0; col < width_samples; col++) {
				// Swap bytes: BE [hi,lo] → LE [lo,hi]
				d[col * 2]     = s[col * 2 + 1];
				d[col * 2 + 1] = s[col * 2];
			}
		}
	}
}

// ffdec_copy_422p10le copies a YUV422P10LE/BE frame into packed YUV422P10LE dst buffer.
// Output layout: Y plane (w*h*2 bytes), Cb plane (w/2*h*2 bytes), Cr plane (w/2*h*2 bytes).
// Total = w*h*4 bytes.
static void ffdec_copy_422p10le(AVFrame* src_frame, unsigned char* dst,
                                 int w, int h_val) {
	int chroma_w = w / 2;
	int y_size = w * h_val * 2;          // Y: w*h samples, 2 bytes each
	int uv_size = chroma_w * h_val * 2;  // each chroma: w/2*h samples, 2 bytes each
	int is_be = (src_frame->format == AV_PIX_FMT_YUV422P10BE);

	ffdec_copy_422p10le_plane(dst, src_frame->data[0],
	                           src_frame->linesize[0], w, h_val, is_be);
	ffdec_copy_422p10le_plane(dst + y_size, src_frame->data[1],
	                           src_frame->linesize[1], chroma_w, h_val, is_be);
	ffdec_copy_422p10le_plane(dst + y_size + uv_size, src_frame->data[2],
	                           src_frame->linesize[2], chroma_w, h_val, is_be);
}

// ffdec_copy_420p10_to_422p10le copies a 10-bit YUV420P frame to YUV422P10LE output.
// This upsamples chroma vertically (420→422) while preserving 10-bit precision.
// Even rows: direct copy. Odd rows: average of adjacent source rows.
static void ffdec_copy_420p10_to_422p10le(AVFrame* src_frame, unsigned char* dst,
                                           int w, int h_val) {
	int chroma_w = w / 2;
	int chroma_h_420 = h_val / 2;
	int y_size = w * h_val * 2;          // Y: w*h samples, 2 bytes each
	int uv_size = chroma_w * h_val * 2;  // each 422 chroma: w/2*h samples, 2 bytes each
	int is_be = (src_frame->format == AV_PIX_FMT_YUV420P10BE);

	// Y plane: direct copy, width*height samples.
	ffdec_copy_422p10le_plane(dst, src_frame->data[0],
	                           src_frame->linesize[0], w, h_val, is_be);

	// Chroma planes: upsample 420→422 in 10-bit domain.
	for (int plane = 1; plane <= 2; plane++) {
		const unsigned char* csrc = src_frame->data[plane];
		int csrc_linesize = src_frame->linesize[plane];
		unsigned char* cdst = dst + y_size + (plane - 1) * uv_size;

		for (int dst_row = 0; dst_row < h_val; dst_row++) {
			int src_row = dst_row / 2;
			unsigned char* d = cdst + dst_row * chroma_w * 2;

			if (dst_row % 2 == 0) {
				// Even row: direct copy from source row.
				const unsigned char* s = csrc + src_row * csrc_linesize;
				if (!is_be) {
					memcpy(d, s, chroma_w * 2);
				} else {
					for (int col = 0; col < chroma_w; col++) {
						d[col * 2]     = s[col * 2 + 1];
						d[col * 2 + 1] = s[col * 2];
					}
				}
			} else {
				// Odd row: average src_row and src_row+1.
				int next_row = src_row + 1;
				if (next_row >= chroma_h_420) next_row = src_row;
				const unsigned char* s0 = csrc + src_row * csrc_linesize;
				const unsigned char* s1 = csrc + next_row * csrc_linesize;

				for (int col = 0; col < chroma_w; col++) {
					unsigned short a, b;
					if (is_be) {
						a = ((unsigned short)s0[col * 2] << 8) | s0[col * 2 + 1];
						b = ((unsigned short)s1[col * 2] << 8) | s1[col * 2 + 1];
					} else {
						a = s0[col * 2] | ((unsigned short)s0[col * 2 + 1] << 8);
						b = s1[col * 2] | ((unsigned short)s1[col * 2 + 1] << 8);
					}
					unsigned short avg = (unsigned short)((a + b + 1) >> 1);
					// Output as LE
					d[col * 2]     = (unsigned char)(avg & 0xFF);
					d[col * 2 + 1] = (unsigned char)(avg >> 8);
				}
			}
		}
	}
}

// ffdec_copy_frame_10bit copies decoded frame into YUV422P10LE dst buffer
// when the output_10bit_422 flag is set. Handles:
// - YUV422P10LE/BE: direct copy with byte-order normalization
// - YUV420P10LE/BE: copy Y + upsample chroma 420→422 in 10-bit
// Returns 1 if the frame was copied as 10-bit, 0 if the frame format is not
// 10-bit (caller should fall back to 8-bit path).
static int ffdec_copy_frame_10bit(AVFrame* src_frame, unsigned char* dst, int w, int h_val) {
	if (ffdec_is_planar_422_10bit(src_frame)) {
		ffdec_copy_422p10le(src_frame, dst, w, h_val);
		return 1;
	}
	if (ffdec_is_planar_420_10bit(src_frame)) {
		ffdec_copy_420p10_to_422p10le(src_frame, dst, w, h_val);
		return 1;
	}
	return 0; // not a 10-bit format — fall back to 8-bit path
}

// ffdec_is_full_range returns 1 if the decoded frame uses full-range (JPEG) levels.
// Checks both the deprecated YUVJ pixel formats and the modern color_range field.
static int ffdec_is_full_range(AVFrame* f) {
	if (f->format == AV_PIX_FMT_YUVJ420P) return 1;
	if (f->format == AV_PIX_FMT_YUVJ422P) return 1;
	if (f->color_range == AVCOL_RANGE_JPEG) return 1;
	return 0;
}

// ffdec_copy_planar copies 3-plane YUV420P/YUVJ420P from src_frame into dst.
// dst must have capacity >= video.YUV420FrameSize(w,h). When remap_to_limited is non-zero, pixel values
// are converted from full-range (0-255) to limited-range (Y:16-235, UV:16-240).
static void ffdec_copy_planar(AVFrame* src_frame, unsigned char* dst,
                              int w, int h_val, int remap_to_limited) {
	int uv_w = w / 2;
	int uv_h = h_val / 2;
	int y_size = w * h_val;
	int uv_size = uv_w * uv_h;

	if (remap_to_limited) {
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

// ffdec_copy_nv12 copies NV12 (semi-planar) from src_frame into packed YUV420P dst.
// NV12 has Y in data[0] and interleaved UV in data[1]. This de-interleaves UV
// into separate Cb and Cr planes.
static void ffdec_copy_nv12(AVFrame* src_frame, unsigned char* dst,
                            int w, int h_val, int remap_to_limited) {
	int uv_w = w / 2;
	int uv_h = h_val / 2;
	int y_size = w * h_val;
	int uv_size = uv_w * uv_h;
	unsigned char* u_dst = dst + y_size;
	unsigned char* v_dst = dst + y_size + uv_size;

	// Copy Y plane
	if (remap_to_limited) {
		for (int row = 0; row < h_val; row++) {
			remap_row(dst + row * w,
			          src_frame->data[0] + row * src_frame->linesize[0],
			          w, full_to_limited_y);
		}
	} else {
		for (int row = 0; row < h_val; row++) {
			memcpy(dst + row * w,
			       src_frame->data[0] + row * src_frame->linesize[0], w);
		}
	}

	// De-interleave UV plane (UVUVUV... → separate U and V)
	for (int row = 0; row < uv_h; row++) {
		const unsigned char* uv_src = src_frame->data[1] + row * src_frame->linesize[1];
		unsigned char* u_row = u_dst + row * uv_w;
		unsigned char* v_row = v_dst + row * uv_w;
		if (remap_to_limited) {
			for (int col = 0; col < uv_w; col++) {
				u_row[col] = full_to_limited_uv[uv_src[col * 2]];
				v_row[col] = full_to_limited_uv[uv_src[col * 2 + 1]];
			}
		} else {
			for (int col = 0; col < uv_w; col++) {
				u_row[col] = uv_src[col * 2];
				v_row[col] = uv_src[col * 2 + 1];
			}
		}
	}
}

// ffdec_copy_422p_to_420p copies 8-bit YUV422P into packed YUV420P dst buffer,
// downsampling chroma vertically by averaging adjacent row pairs.
// dst must have capacity >= w*h*3/2.
static void ffdec_copy_422p_to_420p(AVFrame* src_frame, unsigned char* dst,
                                     int w, int h_val, int remap_to_limited) {
	int uv_w = w / 2;
	int uv_h = h_val / 2;
	int y_size = w * h_val;
	int uv_size = uv_w * uv_h;

	// Copy Y plane (same for 422 and 420).
	if (remap_to_limited) {
		for (int row = 0; row < h_val; row++) {
			remap_row(dst + row * w,
			          src_frame->data[0] + row * src_frame->linesize[0],
			          w, full_to_limited_y);
		}
	} else {
		for (int row = 0; row < h_val; row++) {
			memcpy(dst + row * w,
			       src_frame->data[0] + row * src_frame->linesize[0], w);
		}
	}

	// Downsample chroma: 422 has full-height chroma, 420 has half-height.
	// Average pairs of adjacent chroma rows.
	for (int plane = 1; plane <= 2; plane++) {
		unsigned char* plane_dst = dst + y_size + (plane - 1) * uv_size;
		for (int row = 0; row < uv_h; row++) {
			const unsigned char* row0 = src_frame->data[plane] + (row * 2) * src_frame->linesize[plane];
			const unsigned char* row1 = src_frame->data[plane] + (row * 2 + 1) * src_frame->linesize[plane];
			unsigned char* d = plane_dst + row * uv_w;
			if (remap_to_limited) {
				for (int col = 0; col < uv_w; col++) {
					unsigned char avg = (unsigned char)(((unsigned int)row0[col] + (unsigned int)row1[col] + 1) >> 1);
					d[col] = full_to_limited_uv[avg];
				}
			} else {
				for (int col = 0; col < uv_w; col++) {
					d[col] = (unsigned char)(((unsigned int)row0[col] + (unsigned int)row1[col] + 1) >> 1);
				}
			}
		}
	}
}

// ffdec_copy_422p_to_422p10le promotes 8-bit YUV422P to YUV422P10LE output.
// Each 8-bit sample is left-shifted by 2 to fill the 10-bit range.
// dst must have capacity >= w*h*4.
static void ffdec_copy_422p_to_422p10le(AVFrame* src_frame, unsigned char* dst,
                                         int w, int h_val) {
	int y_size_bytes = w * h_val * 2;       // Y: w*h samples, 2 bytes each
	int uv_size_bytes = (w / 2) * h_val * 2; // each chroma: w/2*h samples, 2 bytes each

	// Y plane: promote 8-bit to 10-bit LE
	for (int row = 0; row < h_val; row++) {
		const unsigned char* src = src_frame->data[0] + row * src_frame->linesize[0];
		unsigned char* d = dst + row * w * 2;
		for (int col = 0; col < w; col++) {
			unsigned short val10 = (unsigned short)src[col] << 2;
			d[col * 2]     = (unsigned char)(val10 & 0xFF);
			d[col * 2 + 1] = (unsigned char)(val10 >> 8);
		}
	}

	// U plane
	int uv_w = w / 2;
	for (int row = 0; row < h_val; row++) {
		const unsigned char* src = src_frame->data[1] + row * src_frame->linesize[1];
		unsigned char* d = dst + y_size_bytes + row * uv_w * 2;
		for (int col = 0; col < uv_w; col++) {
			unsigned short val10 = (unsigned short)src[col] << 2;
			d[col * 2]     = (unsigned char)(val10 & 0xFF);
			d[col * 2 + 1] = (unsigned char)(val10 >> 8);
		}
	}

	// V plane
	for (int row = 0; row < h_val; row++) {
		const unsigned char* src = src_frame->data[2] + row * src_frame->linesize[2];
		unsigned char* d = dst + y_size_bytes + uv_size_bytes + row * uv_w * 2;
		for (int col = 0; col < uv_w; col++) {
			unsigned short val10 = (unsigned short)src[col] << 2;
			d[col * 2]     = (unsigned char)(val10 & 0xFF);
			d[col * 2 + 1] = (unsigned char)(val10 >> 8);
		}
	}
}

// ffdec_copy_frame copies decoded frame into packed YUV420P dst buffer.
// Handles planar YUV420P/YUVJ420P, YUV422P/YUVJ422P (with 422→420 downsample),
// semi-planar NV12, and 10-bit YUV420P10 inputs.
static void ffdec_copy_frame(AVFrame* src_frame, unsigned char* dst,
                             int w, int h_val, int remap_to_limited) {
	if (ffdec_is_planar_420_10bit(src_frame)) {
		ffdec_copy_10bit(src_frame, dst, w, h_val, remap_to_limited);
	} else if (ffdec_is_planar_422(src_frame)) {
		ffdec_copy_422p_to_420p(src_frame, dst, w, h_val, remap_to_limited);
	} else if (src_frame->format == AV_PIX_FMT_NV12) {
		ffdec_copy_nv12(src_frame, dst, w, h_val, remap_to_limited);
	} else {
		ffdec_copy_planar(src_frame, dst, w, h_val, remap_to_limited);
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

	if (!ffdec_is_supported_format(h->frame)) {
		if (h->frame->hw_frames_ctx) {
			rc = av_hwframe_transfer_data(h->sw_frame, h->frame, 0);
			if (rc < 0) {
				// A failed transfer can leave sw_frame with a partial buffer
				// reference; unref both to stay symmetric with the
				// unsupported-format branch below.
				av_frame_unref(h->sw_frame);
				av_frame_unref(h->frame);
				return -3;
			}
			src_frame = h->sw_frame;
			if (!ffdec_is_supported_format(src_frame)) {
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

	// When output_10bit_422 is set and the source is 10-bit or 8-bit 422,
	// output YUV422P10LE. Total bytes: w*h*4.
	if (h->output_10bit_422 &&
	    (ffdec_is_planar_422_10bit(src_frame) || ffdec_is_planar_420_10bit(src_frame)
	     || ffdec_is_planar_422(src_frame))) {
		int total_10bit = w * h_val * 4;

		unsigned char* buf;
		if (dst_buf && dst_cap >= total_10bit) {
			buf = dst_buf;
		} else {
			buf = (unsigned char*)malloc(total_10bit);
			if (!buf) {
				av_frame_unref(h->frame);
				if (src_frame == h->sw_frame) {
					av_frame_unref(h->sw_frame);
				}
				return -5;
			}
		}

		if (ffdec_is_planar_422(src_frame)) {
			ffdec_copy_422p_to_422p10le(src_frame, buf, w, h_val);
		} else {
			ffdec_copy_frame_10bit(src_frame, buf, w, h_val);
		}

		*out_buf = buf;
		*out_len = total_10bit;
		*out_width = w;
		*out_height = h_val;

		av_frame_unref(h->frame);
		if (src_frame == h->sw_frame) {
			av_frame_unref(h->sw_frame);
		}
		return 0;
	}

	// Standard 8-bit YUV420P output path.
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

	if (!ffdec_is_supported_format(h->frame)) {
		if (h->frame->hw_frames_ctx) {
			rc = av_hwframe_transfer_data(h->sw_frame, h->frame, 0);
			if (rc < 0) {
				// A failed transfer can leave sw_frame with a partial buffer
				// reference; unref both to stay symmetric with the
				// unsupported-format branch below.
				av_frame_unref(h->sw_frame);
				av_frame_unref(h->frame);
				return -3;
			}
			src_frame = h->sw_frame;
			if (!ffdec_is_supported_format(src_frame)) {
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

	// When output_10bit_422 is set and the source is 10-bit or 8-bit 422,
	// output YUV422P10LE. Total bytes: w*h*4.
	if (h->output_10bit_422 &&
	    (ffdec_is_planar_422_10bit(src_frame) || ffdec_is_planar_420_10bit(src_frame)
	     || ffdec_is_planar_422(src_frame))) {
		int total_10bit = w * h_val * 4;

		unsigned char* buf;
		if (dst_buf && dst_cap >= total_10bit) {
			buf = dst_buf;
		} else {
			buf = (unsigned char*)malloc(total_10bit);
			if (!buf) {
				av_frame_unref(h->frame);
				if (src_frame == h->sw_frame) {
					av_frame_unref(h->sw_frame);
				}
				return -5;
			}
		}

		if (ffdec_is_planar_422(src_frame)) {
			ffdec_copy_422p_to_422p10le(src_frame, buf, w, h_val);
		} else {
			ffdec_copy_frame_10bit(src_frame, buf, w, h_val);
		}

		*out_buf = buf;
		*out_len = total_10bit;
		*out_width = w;
		*out_height = h_val;

		av_frame_unref(h->frame);
		if (src_frame == h->sw_frame) {
			av_frame_unref(h->sw_frame);
		}
		return 0;
	}

	// Standard 8-bit YUV420P output path.
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

// ffdec_create_hw_device_ctx attempts to create a hardware device context
// for the given type name (e.g. "videotoolbox", "cuda", "vaapi").
// Returns the AVBufferRef* (cast to void*) on success, NULL on failure.
static void* ffdec_create_hw_device_ctx(const char* type_name) {
	enum AVHWDeviceType type = av_hwdevice_find_type_by_name(type_name);
	if (type == AV_HWDEVICE_TYPE_NONE) {
		return NULL;
	}
	AVBufferRef* ctx = NULL;
	int rc = av_hwdevice_ctx_create(&ctx, type, NULL, NULL, 0);
	if (rc < 0) {
		return NULL;
	}
	return (void*)ctx;
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
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// Compile-time check that FFmpegDecoder implements transition.VideoDecoder.
var _ transition.VideoDecoder = (*FFmpegDecoder)(nil)

// initRangeTablesOnce ensures the YUVJ420P→YUV420P range conversion lookup
// tables are populated exactly once, preventing a data race when multiple
// decoders initialize concurrently.
var initRangeTablesOnce sync.Once

// FFmpegDecoder wraps an FFmpeg libavcodec decoder and implements transition.VideoDecoder.
// It decodes Annex B H.264 or H.265/HEVC bitstream to packed YUV420 planar.
//
// When output10bit422 is true and the source provides 10-bit content, the decoder
// outputs YUV422P10LE (w*h*4 bytes) instead of downconverting to YUV420P 8-bit.
// This preserves quality when the pipeline is in professional (10-bit 422) mode.
//
// FFmpegDecoder is NOT safe for concurrent use. Callers must synchronize access externally.
type FFmpegDecoder struct {
	// closeMu only serializes concurrent Close calls so that freeing the C
	// context and setting closed is idempotent. It does NOT make Decode/
	// ReceiveFrame safe to call concurrently with Close (that would be a
	// use-after-free of the AVCodecContext) — the documented external-
	// synchronization contract above is what keeps the hot path safe.
	closeMu        sync.Mutex
	handle         C.ffdec_t
	closed         bool
	output10bit422 bool   // when true, preserve 10-bit 422 output for 10-bit sources
	yuvBuf         []byte // reusable buffer for decoded YUV output
}

// NewFFmpegDecoder creates a new FFmpeg H.264 decoder with auto thread count.
// hwDeviceCtx is reserved for future hardware acceleration (pass nil for software).
func NewFFmpegDecoder(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	return NewFFmpegDecoderWithThreads(hwDeviceCtx, 0)
}

// NewFFmpegDecoderWithThreads creates an FFmpeg H.264 decoder with explicit thread count.
// threadCount=0 means auto (ncpu, 2..4). threadCount=1 disables frame-level multithreading,
// which eliminates buffering delay (important for clip decoders that need immediate output).
func NewFFmpegDecoderWithThreads(hwDeviceCtx unsafe.Pointer, threadCount int) (*FFmpegDecoder, error) {
	initFFmpegLogLevel()
	initRangeTablesOnce.Do(func() {
		C.init_range_tables()
	})

	d := &FFmpegDecoder{}
	FFmpegOpenMu.Lock()
	rc := C.ffdec_open(&d.handle, hwDeviceCtx, C.int(threadCount))
	FFmpegOpenMu.Unlock()
	if rc != 0 {
		return nil, fmt.Errorf("failed to create FFmpeg decoder: code %d", int(rc))
	}
	return d, nil
}

// NewFFmpegDecoderNative10bit creates an H.264 decoder that preserves 10-bit
// 422 output. When the decoded frame is YUV422P (8-bit) or YUV420P10/YUV422P10,
// the decoder outputs YUV422P10LE (w*h*4 bytes) instead of downconverting.
func NewFFmpegDecoderNative10bit(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	return NewFFmpegDecoderNative10bitWithThreads(hwDeviceCtx, 0)
}

// NewFFmpegDecoderNative10bitWithThreads creates an H.264 decoder with explicit
// thread count and native 10-bit output support.
func NewFFmpegDecoderNative10bitWithThreads(hwDeviceCtx unsafe.Pointer, threadCount int) (*FFmpegDecoder, error) {
	initFFmpegLogLevel()
	initRangeTablesOnce.Do(func() {
		C.init_range_tables()
	})

	d := &FFmpegDecoder{output10bit422: true}
	FFmpegOpenMu.Lock()
	rc := C.ffdec_open(&d.handle, hwDeviceCtx, C.int(threadCount))
	FFmpegOpenMu.Unlock()
	if rc != 0 {
		return nil, fmt.Errorf("failed to create FFmpeg decoder (native 10-bit): code %d", int(rc))
	}
	d.handle.output_10bit_422 = 1
	return d, nil
}

// NewFFmpegHEVCDecoder creates a new FFmpeg HEVC decoder with auto thread count.
// hwDeviceCtx is reserved for future hardware acceleration (pass nil for software).
func NewFFmpegHEVCDecoder(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	return NewFFmpegHEVCDecoderWithThreads(hwDeviceCtx, 0)
}

// NewFFmpegHEVCDecoderWithThreads creates an FFmpeg HEVC decoder with explicit thread count.
// threadCount=0 means auto (ncpu, 2..4). threadCount=1 disables frame-level multithreading.
func NewFFmpegHEVCDecoderWithThreads(hwDeviceCtx unsafe.Pointer, threadCount int) (*FFmpegDecoder, error) {
	initFFmpegLogLevel()
	initRangeTablesOnce.Do(func() {
		C.init_range_tables()
	})

	d := &FFmpegDecoder{}
	FFmpegOpenMu.Lock()
	rc := C.ffdec_open_codec(&d.handle, hwDeviceCtx, C.int(threadCount), C.AV_CODEC_ID_HEVC)
	FFmpegOpenMu.Unlock()
	if rc != 0 {
		return nil, fmt.Errorf("failed to create FFmpeg HEVC decoder: code %d", int(rc))
	}
	return d, nil
}

// NewFFmpegHEVCDecoderNative10bit creates an HEVC decoder that preserves 10-bit
// 422 output. When the decoded frame is YUV422P10LE or YUV420P10LE, the decoder
// outputs YUV422P10LE (w*h*4 bytes) instead of downconverting to 8-bit 420.
// For 8-bit sources, falls back to standard YUV420P 8-bit output (w*h*3/2).
func NewFFmpegHEVCDecoderNative10bit(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	return NewFFmpegHEVCDecoderNative10bitWithThreads(hwDeviceCtx, 0)
}

// NewFFmpegHEVCDecoderNative10bitWithThreads creates an HEVC decoder with explicit
// thread count and native 10-bit output support.
func NewFFmpegHEVCDecoderNative10bitWithThreads(hwDeviceCtx unsafe.Pointer, threadCount int) (*FFmpegDecoder, error) {
	initFFmpegLogLevel()
	initRangeTablesOnce.Do(func() {
		C.init_range_tables()
	})

	d := &FFmpegDecoder{output10bit422: true}
	FFmpegOpenMu.Lock()
	rc := C.ffdec_open_codec(&d.handle, hwDeviceCtx, C.int(threadCount), C.AV_CODEC_ID_HEVC)
	FFmpegOpenMu.Unlock()
	if rc != 0 {
		return nil, fmt.Errorf("failed to create FFmpeg HEVC decoder (native 10-bit): code %d", int(rc))
	}
	d.handle.output_10bit_422 = 1
	return d, nil
}

// Decode decodes Annex B encoded H.264 data into packed YUV420 planar bytes.
// Returns the YUV buffer (Y: w*h, U: w/2*h/2, V: w/2*h/2), width, height, and any error.
//
// The returned byte slice is an independent copy that is safe to retain
// across subsequent Decode or ReceiveFrame calls.
func (d *FFmpegDecoder) Decode(data []byte) ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, errors.New("decoder is closed")
	}
	if len(data) == 0 {
		return nil, 0, 0, errors.New("empty input data")
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
		return nil, 0, 0, errors.New("no output frame yet (buffering)")
	}
	if rc < 0 {
		return nil, 0, 0, fmt.Errorf("FFmpeg decode error: code %d", int(rc))
	}

	n := int(outLen)
	if n == 0 || outBuf == nil {
		return nil, 0, 0, errors.New("decoder produced no output")
	}

	return d.adoptOrCopy(outBuf, outLen, int(outWidth), int(outHeight))
}

// adoptOrCopy handles the output from ffdec_decode/ffdec_receive_only.
// If outBuf points into d.yuvBuf (the Go-provided buffer was used), it deep-copies
// the data into a new slice to prevent aliasing. Otherwise, the C side malloc'd a
// buffer because d.yuvBuf was too small or nil, so we copy via GoBytes and free the
// C buffer. In both cases, d.yuvBuf is updated for the next call and the returned
// slice is always an independent copy safe for the caller to retain.
func (d *FFmpegDecoder) adoptOrCopy(outBuf *C.uchar, outLen C.int, w, h int) ([]byte, int, int, error) {
	return d.adoptOrCopyInto(outBuf, outLen, w, h, nil)
}

// adoptOrCopyInto handles the output from ffdec_decode/ffdec_receive_only,
// optionally writing into a caller-provided dst buffer to avoid allocation.
// If dst is large enough, the decoded frame is copied directly into dst.
// If dst is nil or too small, falls back to fresh allocation.
func (d *FFmpegDecoder) adoptOrCopyInto(outBuf *C.uchar, outLen C.int, w, h int, dst []byte) ([]byte, int, int, error) {
	n := int(outLen)
	// Use the actual output length for buffer caching. When the decoder produces
	// 10-bit 422 output (w*h*4), we need the yuvBuf to be large enough for that.
	needed := n
	if needed < video.YUV420FrameSize(w, h) {
		needed = video.YUV420FrameSize(w, h)
	}

	if len(d.yuvBuf) > 0 && outBuf == (*C.uchar)(unsafe.Pointer(&d.yuvBuf[0])) {
		// C wrote directly into our Go buffer — copy to dst or allocate.
		if len(dst) >= n {
			copy(dst[:n], d.yuvBuf[:n])
			return dst[:n], w, h, nil
		}
		// dst too small or nil, allocate fresh.
		result := make([]byte, n)
		copy(result, d.yuvBuf[:n])
		return result, w, h, nil
	}

	// C malloc'd its own buffer (first call or resolution changed).
	if len(dst) >= n {
		// Copy directly into caller's buffer via C.memcpy.
		C.memcpy(unsafe.Pointer(&dst[0]), unsafe.Pointer(outBuf), C.size_t(n))
		C.free(unsafe.Pointer(outBuf))
		// Cache the buffer for future frames at this resolution.
		if cap(d.yuvBuf) < needed {
			d.yuvBuf = make([]byte, needed)
		} else {
			d.yuvBuf = d.yuvBuf[:needed]
		}
		return dst[:n], w, h, nil
	}

	// Fallback: C.GoBytes (existing path).
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

// DecodeInto decodes H.264 data, writing the result into dst if it fits.
// If dst is large enough for the decoded YUV420, the frame is copied directly
// into dst (eliminating the intermediate allocation). If dst is nil or too
// small, falls back to standard allocation behavior.
// Returns the YUV buffer (which may or may not be dst), width, height, error.
func (d *FFmpegDecoder) DecodeInto(data []byte, dst []byte) ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, errors.New("decoder is closed")
	}
	if len(data) == 0 {
		return nil, 0, 0, errors.New("empty input data")
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
		return nil, 0, 0, errors.New("no output frame yet (buffering)")
	}
	if rc < 0 {
		return nil, 0, 0, fmt.Errorf("FFmpeg decode error: code %d", int(rc))
	}

	n := int(outLen)
	if n == 0 || outBuf == nil {
		return nil, 0, 0, errors.New("decoder produced no output")
	}

	return d.adoptOrCopyInto(outBuf, outLen, int(outWidth), int(outHeight), dst)
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
		return errors.New("decoder is closed")
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
// The returned byte slice is an independent copy that is safe to retain
// across subsequent Decode or ReceiveFrame calls.
func (d *FFmpegDecoder) ReceiveFrame() ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, errors.New("decoder is closed")
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
		return nil, 0, 0, errors.New("no more frames")
	}
	if rc < 0 {
		return nil, 0, 0, fmt.Errorf("receive frame error: code %d", int(rc))
	}

	n := int(outLen)
	if n == 0 || outBuf == nil {
		return nil, 0, 0, errors.New("decoder produced no output")
	}

	return d.adoptOrCopy(outBuf, outLen, int(outWidth), int(outHeight))
}

// ThreadCount returns the number of threads the decoder was opened with.
func (d *FFmpegDecoder) ThreadCount() int {
	return int(d.handle.ctx.thread_count)
}

// Close releases the decoder resources. Safe to call multiple times.
func (d *FFmpegDecoder) Close() {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	if !d.closed {
		C.ffdec_close(&d.handle)
		d.closed = true
	}
}

// CreateHWDeviceCtx attempts to create a hardware device context for the
// given type (e.g. "videotoolbox", "cuda", "vaapi"). Returns the context
// pointer on success, nil on failure. The caller must not free the returned
// pointer — it is managed by AVBufferRef reference counting.
func CreateHWDeviceCtx(typeName string) unsafe.Pointer {
	cType := C.CString(typeName)
	defer C.free(unsafe.Pointer(cType))
	return C.ffdec_create_hw_device_ctx(cType)
}
