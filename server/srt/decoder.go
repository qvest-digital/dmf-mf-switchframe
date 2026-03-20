//go:build cgo && !noffmpeg

package srt

/*
#cgo pkg-config: libavcodec libavutil libavformat libswscale libswresample
#include <libavformat/avformat.h>
#include <libavformat/avio.h>
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/channel_layout.h>
#include <libswscale/swscale.h>
#include <libswresample/swresample.h>
#include <stdlib.h>
#include <string.h>

// Forward declarations for Go callback functions.
extern int goSRTRead(void *opaque, uint8_t *buf, int buf_size);
extern void goOnVideoFrame(int id, uint8_t *data, int len, int width, int height, int64_t pts);
extern void goOnAudioFrame(int id, float *data, int samples, int channels, int64_t pts);
extern void goOnCaptionData(int id, uint8_t *data, int size, int64_t pts);
extern void goOnSCTE35Data(int id, uint8_t *data, int size, int64_t pts);

// srtdec_interrupt_cb is called by FFmpeg to check if the operation should be aborted.
// The opaque pointer is an int* pointing to the interrupt flag.
static int srtdec_interrupt_cb(void *opaque) {
    if (opaque) {
        return __atomic_load_n((int*)opaque, __ATOMIC_RELAXED);
    }
    return 0;
}

// srtdec_read_packet is the AVIO read callback that bridges to Go.
// opaque contains the decoder instance ID (cast to intptr_t).
static int srtdec_read_packet(void *opaque, uint8_t *buf, int buf_size) {
    return goSRTRead(opaque, buf, buf_size);
}

// srtdec_t wraps all FFmpeg state for stream decoding.
typedef struct {
    // AVIO
    AVIOContext       *avio_ctx;
    uint8_t           *avio_buffer;
    int                avio_buffer_size;

    // Format
    AVFormatContext   *fmt_ctx;

    // Video
    int                video_stream_idx;
    AVCodecContext    *video_dec_ctx;
    struct SwsContext *sws_ctx;
    enum AVPixelFormat last_sws_fmt;
    int                last_sws_w;
    int                last_sws_h;

    // Audio
    int                audio_stream_idx;
    AVCodecContext    *audio_dec_ctx;
    struct SwrContext *swr_ctx;

    // Data (SCTE-35)
    int                data_stream_idx;

    // Frames
    AVFrame           *dec_frame;
    AVFrame           *sws_frame;
    AVPacket          *pkt;

    // Reusable buffers (avoid per-frame malloc/free)
    uint8_t           *video_buf;
    int                video_buf_size;
    float             *audio_buf;
    int                audio_buf_size;

    // Interrupt
    int                interrupted;
    int                decoder_id;
} srtdec_t;

// srtdec_open sets up the AVIO bridge and opens the input stream.
// decoder_id is used to identify the Go StreamDecoder instance in callbacks.
// max_threads limits per-decoder thread count (0 = auto, capped at 4).
// Returns 0 on success, negative on error.
static int srtdec_open(srtdec_t *h, int decoder_id, int max_threads) {
    memset(h, 0, sizeof(srtdec_t));
    int ret = 0;

    h->decoder_id = decoder_id;
    h->video_stream_idx = -1;
    h->audio_stream_idx = -1;
    h->data_stream_idx = -1;
    h->last_sws_fmt = AV_PIX_FMT_NONE;
    h->video_buf = NULL;
    h->video_buf_size = 0;
    h->audio_buf = NULL;
    h->audio_buf_size = 0;

    // Allocate AVIO buffer (32KB is a good balance for streaming).
    h->avio_buffer_size = 32768;
    h->avio_buffer = (uint8_t*)av_malloc(h->avio_buffer_size);
    if (!h->avio_buffer) {
        return -1;
    }

    // Create custom AVIO context with our read callback.
    // The opaque is the decoder_id cast to a void*.
    h->avio_ctx = avio_alloc_context(
        h->avio_buffer,
        h->avio_buffer_size,
        0,                        // write_flag = 0 (read-only)
        (void*)(intptr_t)decoder_id,
        srtdec_read_packet,
        NULL,                     // write_packet = NULL
        NULL                      // seek = NULL (no seeking)
    );
    if (!h->avio_ctx) {
        av_free(h->avio_buffer);
        h->avio_buffer = NULL;
        return -2;
    }

    // Allocate format context and attach custom IO.
    h->fmt_ctx = avformat_alloc_context();
    if (!h->fmt_ctx) {
        // avio_alloc_context took ownership of avio_buffer on success.
        avio_context_free(&h->avio_ctx);
        return -3;
    }
    h->fmt_ctx->pb = h->avio_ctx;
    h->fmt_ctx->flags |= AVFMT_FLAG_CUSTOM_IO;

    // Set interrupt callback for clean shutdown.
    h->fmt_ctx->interrupt_callback.callback = srtdec_interrupt_cb;
    h->fmt_ctx->interrupt_callback.opaque = &h->interrupted;

    // Open input — NULL filename since we use custom IO.
    // Hint mpegts format since SRT always carries MPEG-TS.
    const AVInputFormat *mpegts_fmt = av_find_input_format("mpegts");
    ret = avformat_open_input(&h->fmt_ctx, NULL, mpegts_fmt, NULL);
    if (ret < 0) {
        // avformat_open_input frees fmt_ctx on failure.
        h->fmt_ctx = NULL;
        avio_context_free(&h->avio_ctx);
        return -4;
    }

    // Limit probe to avoid consuming too much data from the live SRT
    // stream. Default probesize (5MB) causes avformat to read far ahead
    // of real-time. 500 TS packets (~94KB) is enough to find PAT/PMT +
    // video and audio PES headers. 1.5 seconds analyze duration gives
    // enough time to detect both streams reliably.
    h->fmt_ctx->probesize = 188 * 500;
    h->fmt_ctx->max_analyze_duration = 1500000; // 1.5 seconds (µs)

    // Find stream info (probes the stream for codec parameters).
    ret = avformat_find_stream_info(h->fmt_ctx, NULL);
    if (ret < 0) {
        goto fail;
    }

    // Find best video stream.
    h->video_stream_idx = av_find_best_stream(
        h->fmt_ctx, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);

    // Find best audio stream.
    h->audio_stream_idx = av_find_best_stream(
        h->fmt_ctx, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);

    // Find data stream (SCTE-35). May be -1 if none exists.
    h->data_stream_idx = av_find_best_stream(
        h->fmt_ctx, AVMEDIA_TYPE_DATA, -1, -1, NULL, 0);

    if (h->video_stream_idx < 0 && h->audio_stream_idx < 0) {
        ret = -5; // No usable streams found.
        goto fail;
    }

    // Thread count for video decode. We use slice threading (set below)
    // which parallelizes within a single frame without buffering, reducing
    // per-frame decode time. This is critical because the decode loop is
    // single-threaded — while a video frame decodes, no audio packets are
    // read. Faster video decode = shorter audio stall = less bursty delivery.
    int thread_count = max_threads;
    if (thread_count <= 0) {
        thread_count = 1;
    }
    if (thread_count > 4) {
        thread_count = 4;
    }

    // Open video decoder.
    if (h->video_stream_idx >= 0) {
        AVStream *vstream = h->fmt_ctx->streams[h->video_stream_idx];
        const AVCodec *vdec = avcodec_find_decoder(vstream->codecpar->codec_id);
        if (!vdec) {
            // No decoder for this codec; disable video.
            h->video_stream_idx = -1;
        } else {
            h->video_dec_ctx = avcodec_alloc_context3(vdec);
            if (!h->video_dec_ctx) {
                ret = -6;
                goto fail;
            }
            avcodec_parameters_to_context(h->video_dec_ctx, vstream->codecpar);
            h->video_dec_ctx->thread_count = thread_count;
            // Slice threading only: parallelizes within a single frame without
            // buffering. Frame threading (FF_THREAD_FRAME) buffers N frames
            // before producing output, creating N*41ms burst/gap patterns that
            // starve the audio path. Slice threading reduces per-frame decode
            // time (e.g., 25ms → 8ms) so audio packets aren't blocked as long.
            h->video_dec_ctx->thread_type = FF_THREAD_SLICE;
            h->video_dec_ctx->error_concealment = FF_EC_GUESS_MVS | FF_EC_DEBLOCK;

            ret = avcodec_open2(h->video_dec_ctx, vdec, NULL);
            if (ret < 0) {
                avcodec_free_context(&h->video_dec_ctx);
                h->video_dec_ctx = NULL;
                h->video_stream_idx = -1;
            }
        }
    }

    // Open audio decoder.
    if (h->audio_stream_idx >= 0) {
        AVStream *astream = h->fmt_ctx->streams[h->audio_stream_idx];
        const AVCodec *adec = avcodec_find_decoder(astream->codecpar->codec_id);
        if (!adec) {
            h->audio_stream_idx = -1;
        } else {
            h->audio_dec_ctx = avcodec_alloc_context3(adec);
            if (!h->audio_dec_ctx) {
                ret = -7;
                goto fail;
            }
            avcodec_parameters_to_context(h->audio_dec_ctx, astream->codecpar);
            h->audio_dec_ctx->thread_count = 1; // Audio decoders don't benefit from threads.

            ret = avcodec_open2(h->audio_dec_ctx, adec, NULL);
            if (ret < 0) {
                avcodec_free_context(&h->audio_dec_ctx);
                h->audio_dec_ctx = NULL;
                h->audio_stream_idx = -1;
            }
        }
    }

    if (h->video_stream_idx < 0 && h->audio_stream_idx < 0) {
        ret = -5;
        goto fail;
    }

    // Set up swresample for audio normalization (any input -> stereo float32 48kHz).
    if (h->audio_dec_ctx) {
        AVChannelLayout stereo = AV_CHANNEL_LAYOUT_STEREO;
        ret = swr_alloc_set_opts2(&h->swr_ctx,
            &stereo, AV_SAMPLE_FMT_FLT, 48000,
            &h->audio_dec_ctx->ch_layout, h->audio_dec_ctx->sample_fmt,
            h->audio_dec_ctx->sample_rate, 0, NULL);
        if (ret < 0 || !h->swr_ctx) {
            // Audio resampling failed; proceed without audio.
            h->audio_stream_idx = -1;
            if (h->audio_dec_ctx) {
                avcodec_free_context(&h->audio_dec_ctx);
                h->audio_dec_ctx = NULL;
            }
        } else {
            ret = swr_init(h->swr_ctx);
            if (ret < 0) {
                swr_free(&h->swr_ctx);
                h->audio_stream_idx = -1;
                avcodec_free_context(&h->audio_dec_ctx);
                h->audio_dec_ctx = NULL;
            }
        }
    }

    // Allocate frames and packet.
    h->dec_frame = av_frame_alloc();
    h->sws_frame = av_frame_alloc();
    h->pkt = av_packet_alloc();
    if (!h->dec_frame || !h->sws_frame || !h->pkt) {
        ret = -8;
        goto fail;
    }

    return 0;

fail:
    // Cleanup on partial init failure. srtdec_close handles NULL checks.
    if (h->pkt) av_packet_free(&h->pkt);
    if (h->sws_frame) av_frame_free(&h->sws_frame);
    if (h->dec_frame) av_frame_free(&h->dec_frame);
    if (h->swr_ctx) swr_free(&h->swr_ctx);
    if (h->sws_ctx) sws_freeContext(h->sws_ctx);
    if (h->audio_dec_ctx) avcodec_free_context(&h->audio_dec_ctx);
    if (h->video_dec_ctx) avcodec_free_context(&h->video_dec_ctx);
    if (h->fmt_ctx) {
        // When using custom IO, we must not let avformat_close_input free our avio.
        h->fmt_ctx->pb = NULL;
        avformat_close_input(&h->fmt_ctx);
    }
    if (h->avio_ctx) {
        avio_context_free(&h->avio_ctx);
    }
    return ret;
}

// srtdec_is_full_range returns 1 if the frame uses full-range (JPEG) levels.
static int srtdec_is_full_range(AVFrame* f) {
    if (f->format == AV_PIX_FMT_YUVJ420P) return 1;
    if (f->color_range == AVCOL_RANGE_JPEG) return 1;
    return 0;
}

// srtdec_averror_eof returns the AVERROR_EOF constant for use from Go.
// We need this helper because AVERROR_EOF is a C macro that cgo cannot access directly.
static int srtdec_averror_eof(void) {
    return AVERROR_EOF;
}

// srtdec_copy_yuv420 copies a planar YUV420P frame into the srtdec_t's
// reusable video buffer, reallocating only on resolution change.
// Returns the buffer pointer or NULL on failure.
static uint8_t* srtdec_copy_yuv420(srtdec_t *h, AVFrame *frame, int w, int h_val) {
    if (w <= 0 || h_val <= 0 || w > 16384 || h_val > 16384) return NULL;

    int uv_w = w / 2;
    int uv_h = h_val / 2;
    int y_size = w * h_val;
    int uv_size = uv_w * uv_h;
    int total = y_size + 2 * uv_size;

    // Reuse persistent buffer; only reallocate on size increase.
    if (total > h->video_buf_size) {
        free(h->video_buf);
        h->video_buf = (uint8_t*)malloc(total);
        if (!h->video_buf) {
            h->video_buf_size = 0;
            return NULL;
        }
        h->video_buf_size = total;
    }
    uint8_t *buf = h->video_buf;

    // Copy Y plane.
    for (int row = 0; row < h_val; row++) {
        memcpy(buf + row * w,
               frame->data[0] + row * frame->linesize[0], w);
    }
    // Copy U plane.
    for (int row = 0; row < uv_h; row++) {
        memcpy(buf + y_size + row * uv_w,
               frame->data[1] + row * frame->linesize[1], uv_w);
    }
    // Copy V plane.
    for (int row = 0; row < uv_h; row++) {
        memcpy(buf + y_size + uv_size + row * uv_w,
               frame->data[2] + row * frame->linesize[2], uv_w);
    }
    return buf;
}

// srtdec_process_video handles a decoded video frame:
// converts to YUV420P (limited range, BT.709) if needed, deep-copies, and calls Go callback.
static void srtdec_process_video(srtdec_t *h, AVFrame *frame, int64_t pts) {
    int w = frame->width;
    int h_val = frame->height;
    if (w <= 0 || h_val <= 0) return;

    // Ensure even dimensions.
    w = w & ~1;
    h_val = h_val & ~1;
    if (w <= 0 || h_val <= 0) return;

    int total = w * h_val * 3 / 2;

    // Check if we need sws conversion (non-YUV420P input, or full-range→limited-range).
    if (frame->format == AV_PIX_FMT_YUV420P && !srtdec_is_full_range(frame)) {
        // Direct copy from planar YUV420P limited-range — no conversion needed.
        uint8_t *buf = srtdec_copy_yuv420(h, frame, w, h_val);
        if (!buf) return;

        goOnVideoFrame(h->decoder_id, buf, total, w, h_val, pts);
    } else if ((frame->format == AV_PIX_FMT_YUV420P || frame->format == AV_PIX_FMT_YUVJ420P) &&
               srtdec_is_full_range(frame)) {
        // Full-range (JPEG) YUV420P — convert to limited-range via sws.
        // The pipeline operates in BT.709 limited range; passing full-range through
        // would cause crushed blacks and blown highlights.
        if (frame->format != h->last_sws_fmt ||
            frame->width != h->last_sws_w ||
            frame->height != h->last_sws_h) {
            if (h->sws_ctx) {
                sws_freeContext(h->sws_ctx);
                h->sws_ctx = NULL;
            }
            h->sws_ctx = sws_getContext(
                frame->width, frame->height, frame->format,
                w, h_val, AV_PIX_FMT_YUV420P,
                SWS_BILINEAR, NULL, NULL, NULL);
            if (!h->sws_ctx) return;

            // Set source range to full (1) and destination to limited (0).
            // sws_setColorspaceDetails args: inv_table, srcRange, table, dstRange, brightness, contrast, saturation
            const int *inv_table = sws_getCoefficients(SWS_CS_ITU709);
            const int *table     = sws_getCoefficients(SWS_CS_ITU709);
            sws_setColorspaceDetails(h->sws_ctx,
                inv_table, 1,  // source: full range
                table,     0,  // dest: limited range
                0, 1 << 16, 1 << 16);

            h->last_sws_fmt = frame->format;
            h->last_sws_w = frame->width;
            h->last_sws_h = frame->height;

            // Set up sws output frame.
            av_frame_unref(h->sws_frame);
            h->sws_frame->format = AV_PIX_FMT_YUV420P;
            h->sws_frame->width = w;
            h->sws_frame->height = h_val;
            av_frame_get_buffer(h->sws_frame, 0);
        }

        av_frame_make_writable(h->sws_frame);
        sws_scale(h->sws_ctx,
            (const uint8_t* const*)frame->data, frame->linesize,
            0, frame->height,
            h->sws_frame->data, h->sws_frame->linesize);

        // Copy sws output to reusable buffer.
        uint8_t *buf = srtdec_copy_yuv420(h, h->sws_frame, w, h_val);
        if (!buf) return;

        goOnVideoFrame(h->decoder_id, buf, total, w, h_val, pts);
    } else {
        // Need sws conversion — reinit if format/resolution changed.
        if (frame->format != h->last_sws_fmt ||
            frame->width != h->last_sws_w ||
            frame->height != h->last_sws_h) {
            if (h->sws_ctx) {
                sws_freeContext(h->sws_ctx);
            }
            h->sws_ctx = sws_getContext(
                frame->width, frame->height, frame->format,
                w, h_val, AV_PIX_FMT_YUV420P,
                SWS_BILINEAR, NULL, NULL, NULL);
            if (!h->sws_ctx) return;
            h->last_sws_fmt = frame->format;
            h->last_sws_w = frame->width;
            h->last_sws_h = frame->height;

            // Set up sws output frame.
            av_frame_unref(h->sws_frame);
            h->sws_frame->format = AV_PIX_FMT_YUV420P;
            h->sws_frame->width = w;
            h->sws_frame->height = h_val;
            av_frame_get_buffer(h->sws_frame, 0);
        }

        av_frame_make_writable(h->sws_frame);
        sws_scale(h->sws_ctx,
            (const uint8_t* const*)frame->data, frame->linesize,
            0, frame->height,
            h->sws_frame->data, h->sws_frame->linesize);

        // Copy sws output to reusable buffer.
        uint8_t *buf = srtdec_copy_yuv420(h, h->sws_frame, w, h_val);
        if (!buf) return;

        goOnVideoFrame(h->decoder_id, buf, total, w, h_val, pts);
    }
}

// srtdec_process_audio handles a decoded audio frame:
// resamples to stereo float32 48kHz, and calls Go callback.
// Uses the persistent audio_buf to avoid per-frame malloc.
static void srtdec_process_audio(srtdec_t *h, AVFrame *frame, int64_t pts) {
    if (!h->swr_ctx) return;

    // Calculate max output samples for this input.
    int max_out = swr_get_out_samples(h->swr_ctx, frame->nb_samples);
    if (max_out <= 0) max_out = frame->nb_samples * 2 + 256;

    // Reuse persistent audio buffer; only reallocate on size increase.
    int out_channels = 2;
    int buf_samples = max_out;
    int needed = buf_samples * out_channels;
    if (needed > h->audio_buf_size) {
        free(h->audio_buf);
        h->audio_buf = (float*)malloc(needed * sizeof(float));
        if (!h->audio_buf) {
            h->audio_buf_size = 0;
            return;
        }
        h->audio_buf_size = needed;
    }

    uint8_t *out_data[1];
    out_data[0] = (uint8_t*)h->audio_buf;

    int out_samples = swr_convert(h->swr_ctx,
        out_data, buf_samples,
        (const uint8_t**)frame->data, frame->nb_samples);

    if (out_samples > 0) {
        goOnAudioFrame(h->decoder_id, h->audio_buf, out_samples * out_channels, out_channels, pts);
    }
}

// srtdec_run runs the main decode loop until EOF, error, or interrupt.
// Returns 0 on EOF, negative on error.
static int srtdec_run(srtdec_t *h) {
    int ret;

    while (!h->interrupted) {
        ret = av_read_frame(h->fmt_ctx, h->pkt);
        if (ret < 0) {
            break; // EOF or error (including interrupt)
        }

        if (h->pkt->stream_index == h->video_stream_idx && h->video_dec_ctx) {
            // Decode video packet.
            ret = avcodec_send_packet(h->video_dec_ctx, h->pkt);
            if (ret < 0) {
                av_packet_unref(h->pkt);
                continue; // Skip bad packets.
            }

            while (!h->interrupted) {
                ret = avcodec_receive_frame(h->video_dec_ctx, h->dec_frame);
                if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
                    break;
                }
                if (ret < 0) {
                    break;
                }

                int64_t pts = h->dec_frame->pts;
                if (pts == AV_NOPTS_VALUE) {
                    pts = h->dec_frame->best_effort_timestamp;
                }
                srtdec_process_video(h, h->dec_frame, pts);

                // Check for CEA-608/708 closed caption side data (A53 CC).
                AVFrameSideData *cc = av_frame_get_side_data(h->dec_frame, AV_FRAME_DATA_A53_CC);
                if (cc && cc->size > 0) {
                    goOnCaptionData(h->decoder_id, cc->data, cc->size, pts);
                }

                av_frame_unref(h->dec_frame);
            }
        } else if (h->pkt->stream_index == h->audio_stream_idx && h->audio_dec_ctx) {
            // Decode audio packet.
            ret = avcodec_send_packet(h->audio_dec_ctx, h->pkt);
            if (ret < 0) {
                av_packet_unref(h->pkt);
                continue;
            }

            while (!h->interrupted) {
                ret = avcodec_receive_frame(h->audio_dec_ctx, h->dec_frame);
                if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
                    break;
                }
                if (ret < 0) {
                    break;
                }

                int64_t pts = h->dec_frame->pts;
                if (pts == AV_NOPTS_VALUE) {
                    pts = h->dec_frame->best_effort_timestamp;
                }
                srtdec_process_audio(h, h->dec_frame, pts);
                av_frame_unref(h->dec_frame);
            }
        } else if (h->pkt->stream_index == h->data_stream_idx && h->data_stream_idx >= 0) {
            // SCTE-35 data packet — forward raw section data to Go.
            if (h->pkt->size > 0) {
                goOnSCTE35Data(h->decoder_id, h->pkt->data, h->pkt->size, h->pkt->pts);
            }
        }

        av_packet_unref(h->pkt);
    }

    return 0;
}

// srtdec_stop sets the interrupt flag to cause srtdec_run to return.
static void srtdec_stop(srtdec_t *h) {
    __atomic_store_n(&h->interrupted, 1, __ATOMIC_RELAXED);
}

// srtdec_close frees all resources.
static void srtdec_close(srtdec_t *h) {
    if (h->pkt) av_packet_free(&h->pkt);
    if (h->sws_frame) av_frame_free(&h->sws_frame);
    if (h->dec_frame) av_frame_free(&h->dec_frame);
    if (h->swr_ctx) swr_free(&h->swr_ctx);
    if (h->sws_ctx) {
        sws_freeContext(h->sws_ctx);
        h->sws_ctx = NULL;
    }
    if (h->audio_dec_ctx) avcodec_free_context(&h->audio_dec_ctx);
    if (h->video_dec_ctx) avcodec_free_context(&h->video_dec_ctx);
    // Free reusable buffers.
    free(h->video_buf);
    h->video_buf = NULL;
    h->video_buf_size = 0;
    free(h->audio_buf);
    h->audio_buf = NULL;
    h->audio_buf_size = 0;
    if (h->fmt_ctx) {
        // Detach custom IO before closing to prevent double-free.
        h->fmt_ctx->pb = NULL;
        avformat_close_input(&h->fmt_ctx);
    }
    if (h->avio_ctx) {
        // avio_alloc_context took ownership of avio_buffer; freeing
        // the context also frees the buffer.
        avio_context_free(&h->avio_ctx);
    }
}
*/
import "C"

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/zsiec/switchframe/server/codec"
)

// decoderRegistry maps opaque IDs to StreamDecoder instances for C callbacks.
// Uses RWMutex because lookupDecoder is called 30+ times/sec per source from
// C callbacks, while register/unregister are infrequent lifecycle operations.
var (
	decoderMu       sync.RWMutex
	decoderRegistry = map[int]*StreamDecoder{}
	decoderNextID   int
)

// registerDecoder stores a decoder in the global registry and returns its ID.
func registerDecoder(d *StreamDecoder) int {
	decoderMu.Lock()
	defer decoderMu.Unlock()
	decoderNextID++
	id := decoderNextID
	decoderRegistry[id] = d
	return id
}

// unregisterDecoder removes a decoder from the global registry.
func unregisterDecoder(id int) {
	decoderMu.Lock()
	defer decoderMu.Unlock()
	delete(decoderRegistry, id)
}

// lookupDecoder retrieves a decoder from the global registry.
// Uses RLock for concurrent read access from C callbacks.
func lookupDecoder(id int) *StreamDecoder {
	decoderMu.RLock()
	defer decoderMu.RUnlock()
	return decoderRegistry[id]
}

// StreamDecoderConfig configures a StreamDecoder for demuxing and decoding
// an MPEG-TS stream from an io.Reader.
type StreamDecoderConfig struct {
	Reader     io.Reader
	MaxThreads int // default 4
	OnVideo    func(yuv []byte, width, height int, pts int64)
	OnAudio    func(pcm []float32, pts int64, sampleRate, channels int)
	// OnCaptions is called when CEA-608/708 closed caption data is extracted
	// from H.264 SEI NALUs (A53 CC side data) during video decode.
	OnCaptions func(data []byte, pts int64) // optional

	// OnSCTE35 is called when SCTE-35 splice_info_section data is found on
	// a data PID in the MPEG-TS stream.
	OnSCTE35 func(data []byte, pts int64) // optional
}

// StreamDecoder bridges an io.Reader to FFmpeg's avformat/avcodec for live
// MPEG-TS demuxing and decoding. It uses a custom AVIO context that reads
// from the Go io.Reader via a C callback.
type StreamDecoder struct {
	cfg    StreamDecoderConfig
	handle C.srtdec_t
	id     int
	closed atomic.Bool

	// Reusable Go-side buffers for C→Go copies, avoiding per-frame allocation
	// (~3.1 MB/frame at 1080p for video, ~8 KB/frame for audio).
	videoGoBuf []byte
	audioGoBuf []float32
}

// NewStreamDecoder creates a StreamDecoder that will demux and decode
// MPEG-TS data from cfg.Reader. Call Run() to start the decode loop.
func NewStreamDecoder(cfg StreamDecoderConfig) (*StreamDecoder, error) {
	if cfg.Reader == nil {
		return nil, errors.New("reader is required")
	}
	if cfg.OnVideo == nil {
		return nil, errors.New("OnVideo callback is required")
	}
	if cfg.OnAudio == nil {
		return nil, errors.New("OnAudio callback is required")
	}

	initFFmpegLog()

	d := &StreamDecoder{cfg: cfg}
	d.id = registerDecoder(d)

	maxThreads := cfg.MaxThreads
	if maxThreads <= 0 {
		maxThreads = 4
	}

	codec.FFmpegOpenMu.Lock()
	rc := C.srtdec_open(&d.handle, C.int(d.id), C.int(maxThreads))
	codec.FFmpegOpenMu.Unlock()
	if rc != 0 {
		unregisterDecoder(d.id)
		return nil, errors.New("failed to open stream decoder")
	}

	return d, nil
}

// Run blocks and runs the decode loop until EOF, error, or Stop() is called.
// Decoded video frames are delivered via OnVideo, audio via OnAudio.
func (d *StreamDecoder) Run() {
	if d.closed.Load() {
		return
	}
	C.srtdec_run(&d.handle)
	d.close()
}

// Stop signals the decoder to stop. Run() will return shortly after.
func (d *StreamDecoder) Stop() {
	if !d.closed.Load() {
		C.srtdec_stop(&d.handle)
		// Also close the reader if it supports it, to unblock any pending reads.
		if closer, ok := d.cfg.Reader.(io.Closer); ok {
			_ = closer.Close()
		}
	}
}

// close releases all FFmpeg resources and unregisters from the global map.
func (d *StreamDecoder) close() {
	if d.closed.Swap(true) {
		return // already closed
	}
	C.srtdec_close(&d.handle)
	unregisterDecoder(d.id)
}

// initFFmpegLog ensures FFmpeg log level is set once.
// We call this from the srt package to avoid depending on codec package init order.
var ffmpegLogOnce sync.Once

func initFFmpegLog() {
	ffmpegLogOnce.Do(func() {
		C.av_log_set_level(C.AV_LOG_FATAL)
	})
}

// --- C callback implementations ---

//export goSRTRead
func goSRTRead(opaque unsafe.Pointer, buf *C.uint8_t, bufSize C.int) C.int {
	id := int(uintptr(opaque))
	d := lookupDecoder(id)
	if d == nil {
		return C.int(-1) // AVERROR_EOF-like
	}

	goBuf := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(bufSize))
	n, err := d.cfg.Reader.Read(goBuf)
	if n > 0 {
		return C.int(n)
	}
	if err != nil {
		if err == io.EOF {
			// Return AVERROR_EOF so FFmpeg treats this as a clean end-of-stream
			// rather than a generic I/O error.
			return C.int(C.srtdec_averror_eof())
		}
		// Other errors (closed pipe, network failure, etc.) — return generic error.
		return C.int(-1)
	}
	// n == 0 && err == nil: spurious empty read. Return 0 and let FFmpeg's
	// AVIO layer retry. This is safe — AVIO treats 0 as "no data yet".
	return C.int(0)
}

//export goOnVideoFrame
func goOnVideoFrame(id C.int, data *C.uint8_t, length C.int, width C.int, height C.int, pts C.int64_t) {
	d := lookupDecoder(int(id))
	if d == nil || d.cfg.OnVideo == nil {
		return
	}

	// Copy YUV data from C to a reusable Go buffer to avoid per-frame allocation
	// (~3.1 MB at 1080p, ~93 MB/s per source at 30fps). The callback must not
	// retain the slice beyond the call — all downstream consumers (IngestRawVideo,
	// IngestFillYUV, IngestSourceFrame, IngestRawFrame) deep-copy before async use.
	n := int(length)
	if cap(d.videoGoBuf) < n {
		d.videoGoBuf = make([]byte, n)
	}
	d.videoGoBuf = d.videoGoBuf[:n]
	copy(d.videoGoBuf, unsafe.Slice((*byte)(unsafe.Pointer(data)), n))

	d.cfg.OnVideo(d.videoGoBuf, int(width), int(height), int64(pts))
}

//export goOnAudioFrame
func goOnAudioFrame(id C.int, data *C.float, samples C.int, channels C.int, pts C.int64_t) {
	d := lookupDecoder(int(id))
	if d == nil || d.cfg.OnAudio == nil {
		return
	}

	// Copy PCM data from C to a reusable Go buffer to avoid per-frame allocation.
	// The callback must not retain the slice beyond the call — IngestPCM and the
	// audio encoder consume the data synchronously.
	n := int(samples)
	if cap(d.audioGoBuf) < n {
		d.audioGoBuf = make([]float32, n)
	}
	d.audioGoBuf = d.audioGoBuf[:n]
	cSlice := unsafe.Slice((*float32)(unsafe.Pointer(data)), n)
	copy(d.audioGoBuf, cSlice)

	// The swr output is stereo float32 at 48kHz.
	d.cfg.OnAudio(d.audioGoBuf, int64(pts), 48000, int(channels))
}

//export goOnCaptionData
func goOnCaptionData(id C.int, data *C.uint8_t, size C.int, pts C.int64_t) {
	d := lookupDecoder(int(id))
	if d == nil || d.cfg.OnCaptions == nil {
		return
	}
	goData := C.GoBytes(unsafe.Pointer(data), size)
	d.cfg.OnCaptions(goData, int64(pts))
}

//export goOnSCTE35Data
func goOnSCTE35Data(id C.int, data *C.uint8_t, size C.int, pts C.int64_t) {
	d := lookupDecoder(int(id))
	if d == nil || d.cfg.OnSCTE35 == nil {
		return
	}
	goData := C.GoBytes(unsafe.Pointer(data), size)
	d.cfg.OnSCTE35(goData, int64(pts))
}
