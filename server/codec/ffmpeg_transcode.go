//go:build cgo && !noffmpeg

package codec

/*
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/audio_fifo.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/channel_layout.h>
#include <libavutil/mathematics.h>
#include <libavutil/timestamp.h>
#include <libswscale/swscale.h>
#include <libswresample/swresample.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	int width;
	int height;
	int64_t duration_ms;
	double fps;
	int sample_rate;
	int channels;
	int video_frames;
	int64_t audio_samples; // samples accepted by the audio encoder
} transcode_result_t;

// transcode_ctx_t carries all state from the open/setup phase
// (ff_transcode_open) into the frame loop (ff_transcode_run). The split exists
// so the Go caller can hold FFmpegOpenMu across just the open phase — which
// performs every avformat_open_input/avcodec_open2 of the transcode — without
// serializing the long-running frame loop behind the global open lock.
typedef struct {
	// Input state
	AVFormatContext* ifmt_ctx;
	int video_stream_idx;
	int audio_stream_idx;
	AVCodecContext* video_dec_ctx;
	AVCodecContext* audio_dec_ctx;

	// Output state
	AVFormatContext* ofmt_ctx;
	AVCodecContext* video_enc_ctx;
	AVCodecContext* audio_enc_ctx;
	AVStream* out_video_stream;
	AVStream* out_audio_stream;
	int out_video_idx;
	int out_audio_idx;

	// Conversion state
	struct SwsContext* sws_ctx;
	struct SwrContext* swr_ctx;
	AVAudioFifo* afifo;     // re-chunks resampled audio to aac_frame_size
	uint8_t** conv_samples; // temp buffer between swr_convert and the FIFO
	int conv_capacity;
	int aac_frame_size;

	// Frame/packet state
	AVFrame* dec_frame;
	AVFrame* sws_frame;
	AVFrame* swr_frame;
	AVPacket* pkt;
	AVPacket* enc_pkt;
} transcode_ctx_t;

// ff_transcode_push_audio resamples decoded audio (or, with in==NULL, flushes
// the resampler) into the audio FIFO, growing the temporary conversion buffer
// as needed. The FIFO decouples the source's decoded frame size from the AAC
// encoder's fixed frame_size: sending short frames straight to the encoder
// would make it treat the first one as the final frame and reject the rest.
// Returns the number of samples pushed (>= 0), or a negative AVERROR.
static int ff_transcode_push_audio(struct SwrContext* swr_ctx, AVAudioFifo* afifo,
                                   const uint8_t** in, int in_count, int nb_channels,
                                   uint8_t*** conv_samples, int* conv_capacity) {
	int max_out = swr_get_out_samples(swr_ctx, in_count);
	if (max_out <= 0) {
		if (in_count <= 0) {
			return 0; // nothing buffered, nothing to flush
		}
		max_out = in_count + 256;
	}
	if (max_out > *conv_capacity) {
		if (*conv_samples) {
			av_freep(&(*conv_samples)[0]);
			av_freep(conv_samples);
		}
		*conv_capacity = 0;
		int ret = av_samples_alloc_array_and_samples(conv_samples, NULL, nb_channels,
		                                             max_out, AV_SAMPLE_FMT_FLTP, 0);
		if (ret < 0) {
			return ret;
		}
		*conv_capacity = max_out;
	}
	int out_samples = swr_convert(swr_ctx, *conv_samples, max_out, in, in_count);
	if (out_samples <= 0) {
		return out_samples;
	}
	if (av_audio_fifo_write(afifo, (void**)*conv_samples, out_samples) < out_samples) {
		return AVERROR(ENOMEM);
	}
	return out_samples;
}

// ff_transcode_drain_audio_fifo sends full frame_size chunks from the FIFO to
// the audio encoder and muxes the resulting packets. When final is non-zero
// the trailing partial frame is sent as well (valid only once, at end of
// stream). audio_samples_sent is incremented by the samples the encoder
// accepted. Returns 0 on success, negative on a fatal encode/mux error.
static int ff_transcode_drain_audio_fifo(AVAudioFifo* afifo, int frame_size, int final,
                                         AVFrame* swr_frame, int64_t* audio_pts,
                                         int64_t* audio_samples_sent,
                                         AVCodecContext* audio_enc_ctx,
                                         AVFormatContext* ofmt_ctx,
                                         AVStream* out_audio_stream, int out_audio_idx,
                                         AVPacket* enc_pkt) {
	while (av_audio_fifo_size(afifo) >= frame_size ||
	       (final && av_audio_fifo_size(afifo) > 0)) {
		int n = av_audio_fifo_size(afifo);
		if (n > frame_size) {
			n = frame_size;
		}

		// Restore full capacity before make_writable so a reallocation
		// (encoder still holds a reference) keeps room for frame_size samples.
		swr_frame->nb_samples = frame_size;
		int ret = av_frame_make_writable(swr_frame);
		if (ret < 0) {
			return ret;
		}
		if (av_audio_fifo_read(afifo, (void**)swr_frame->data, n) < n) {
			return AVERROR(EINVAL);
		}
		swr_frame->nb_samples = n;
		swr_frame->pts = *audio_pts;
		*audio_pts += n;

		ret = avcodec_send_frame(audio_enc_ctx, swr_frame);
		if (ret < 0) {
			return ret;
		}
		*audio_samples_sent += n;

		while (1) {
			ret = avcodec_receive_packet(audio_enc_ctx, enc_pkt);
			if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
				break;
			}
			if (ret < 0) {
				return ret;
			}
			enc_pkt->stream_index = out_audio_idx;
			av_packet_rescale_ts(enc_pkt,
				audio_enc_ctx->time_base,
				out_audio_stream->time_base);
			ret = av_interleaved_write_frame(ofmt_ctx, enc_pkt);
			av_packet_unref(enc_pkt);
			if (ret < 0) {
				return ret;
			}
		}
	}
	return 0;
}

// ff_transcode_free releases every resource held by ctx. Safe to call on a
// zeroed ctx, after a failed open, or after a completed run; it is idempotent
// because every release nulls the corresponding pointer.
static void ff_transcode_free(transcode_ctx_t* ctx) {
	if (ctx->enc_pkt) av_packet_free(&ctx->enc_pkt);
	if (ctx->pkt) av_packet_free(&ctx->pkt);
	if (ctx->swr_frame) av_frame_free(&ctx->swr_frame);
	if (ctx->sws_frame) av_frame_free(&ctx->sws_frame);
	if (ctx->dec_frame) av_frame_free(&ctx->dec_frame);
	if (ctx->afifo) {
		av_audio_fifo_free(ctx->afifo);
		ctx->afifo = NULL;
	}
	if (ctx->conv_samples) {
		av_freep(&ctx->conv_samples[0]);
		av_freep(&ctx->conv_samples);
	}
	ctx->conv_capacity = 0;
	if (ctx->swr_ctx) swr_free(&ctx->swr_ctx);
	if (ctx->sws_ctx) {
		sws_freeContext(ctx->sws_ctx);
		ctx->sws_ctx = NULL;
	}
	if (ctx->audio_enc_ctx) avcodec_free_context(&ctx->audio_enc_ctx);
	if (ctx->video_enc_ctx) avcodec_free_context(&ctx->video_enc_ctx);
	if (ctx->audio_dec_ctx) avcodec_free_context(&ctx->audio_dec_ctx);
	if (ctx->video_dec_ctx) avcodec_free_context(&ctx->video_dec_ctx);
	if (ctx->ofmt_ctx) {
		if (!(ctx->ofmt_ctx->oformat->flags & AVFMT_NOFILE) && ctx->ofmt_ctx->pb) {
			avio_closep(&ctx->ofmt_ctx->pb);
		}
		avformat_free_context(ctx->ofmt_ctx);
		ctx->ofmt_ctx = NULL;
		ctx->out_video_stream = NULL;
		ctx->out_audio_stream = NULL;
	}
	if (ctx->ifmt_ctx) avformat_close_input(&ctx->ifmt_ctx);
}

// ff_transcode_open performs the open/setup phase of a transcode into ctx:
// input demuxer, decoders, output muxer, encoders, scaler/resampler, and
// frame/packet allocations. It contains every avformat_open_input and
// avcodec_open2 call of the transcode (including h264_nvenc, which
// initializes CUDA inside avcodec_open2), so the Go caller must hold
// FFmpegOpenMu across this call only — ff_transcode_run performs no opens.
//
// Error codes:
//   -1: cannot open input
//   -2: no video stream found
//   -3: cannot open video decoder
//   -4: cannot create output context
//   -5: cannot open video encoder
//   -6: cannot open audio encoder
//   -7: cannot open output file
//   -8: transcode loop error (returned by ff_transcode_run / setup allocs)
//   -9: sws_getContext failed
//  -10: swr init failed
//
// On failure all partially-acquired resources are freed and ctx is safe to
// pass to ff_transcode_free again (it is idempotent).
static int ff_transcode_open(const char* input_path, const char* output_path,
                             const char* encoder_name, int bitrate,
                             transcode_ctx_t* ctx) {
	memset(ctx, 0, sizeof(*ctx));
	ctx->video_stream_idx = -1;
	ctx->audio_stream_idx = -1;
	ctx->out_video_idx = -1;
	ctx->out_audio_idx = -1;
	ctx->aac_frame_size = 1024;

	int ret = 0;

	// --- Open input ---
	ret = avformat_open_input(&ctx->ifmt_ctx, input_path, NULL, NULL);
	if (ret < 0) {
		ret = -1;
		goto fail;
	}

	ret = avformat_find_stream_info(ctx->ifmt_ctx, NULL);
	if (ret < 0) {
		goto fail;
	}

	// --- Find best streams ---
	ctx->video_stream_idx = av_find_best_stream(ctx->ifmt_ctx, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
	if (ctx->video_stream_idx < 0) {
		ret = -2;
		goto fail;
	}

	ctx->audio_stream_idx = av_find_best_stream(ctx->ifmt_ctx, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);

	// --- Open video decoder ---
	{
		AVStream* in_vstream = ctx->ifmt_ctx->streams[ctx->video_stream_idx];
		const AVCodec* vdec = avcodec_find_decoder(in_vstream->codecpar->codec_id);
		if (!vdec) {
			ret = -3;
			goto fail;
		}
		ctx->video_dec_ctx = avcodec_alloc_context3(vdec);
		if (!ctx->video_dec_ctx) {
			ret = -3;
			goto fail;
		}
		avcodec_parameters_to_context(ctx->video_dec_ctx, in_vstream->codecpar);
		ctx->video_dec_ctx->thread_count = 0; // auto
		ret = avcodec_open2(ctx->video_dec_ctx, vdec, NULL);
		if (ret < 0) {
			ret = -3;
			goto fail;
		}
	}

	// --- Open audio decoder (optional) ---
	if (ctx->audio_stream_idx >= 0) {
		AVStream* in_astream = ctx->ifmt_ctx->streams[ctx->audio_stream_idx];
		const AVCodec* adec = avcodec_find_decoder(in_astream->codecpar->codec_id);
		if (adec) {
			ctx->audio_dec_ctx = avcodec_alloc_context3(adec);
			if (ctx->audio_dec_ctx) {
				avcodec_parameters_to_context(ctx->audio_dec_ctx, in_astream->codecpar);
				ret = avcodec_open2(ctx->audio_dec_ctx, adec, NULL);
				if (ret < 0) {
					avcodec_free_context(&ctx->audio_dec_ctx);
					ctx->audio_dec_ctx = NULL;
					ctx->audio_stream_idx = -1; // proceed without audio
				}
			}
		}
	}

	// --- Create output context ---
	ret = avformat_alloc_output_context2(&ctx->ofmt_ctx, NULL, "mpegts", output_path);
	if (ret < 0 || !ctx->ofmt_ctx) {
		ret = -4;
		goto fail;
	}

	// --- Create output video stream + encoder ---
	{
		const AVCodec* venc = NULL;
		if (encoder_name && encoder_name[0] != '\0') {
			venc = avcodec_find_encoder_by_name(encoder_name);
		}
		if (!venc) {
			venc = avcodec_find_encoder(AV_CODEC_ID_H264);
		}
		if (!venc) {
			ret = -5;
			goto fail;
		}

		ctx->out_video_stream = avformat_new_stream(ctx->ofmt_ctx, NULL);
		if (!ctx->out_video_stream) {
			ret = -5;
			goto fail;
		}
		ctx->out_video_idx = ctx->out_video_stream->index;

		ctx->video_enc_ctx = avcodec_alloc_context3(venc);
		if (!ctx->video_enc_ctx) {
			ret = -5;
			goto fail;
		}

		// Resolution from input, even-aligned
		int enc_w = ctx->video_dec_ctx->width & ~1;
		int enc_h = ctx->video_dec_ctx->height & ~1;
		if (enc_w <= 0) enc_w = 2;
		if (enc_h <= 0) enc_h = 2;

		ctx->video_enc_ctx->width = enc_w;
		ctx->video_enc_ctx->height = enc_h;
		ctx->video_enc_ctx->pix_fmt = AV_PIX_FMT_YUV420P;

		// time_base from input stream
		AVStream* in_vstream = ctx->ifmt_ctx->streams[ctx->video_stream_idx];
		if (in_vstream->avg_frame_rate.num > 0 && in_vstream->avg_frame_rate.den > 0) {
			ctx->video_enc_ctx->time_base = av_inv_q(in_vstream->avg_frame_rate);
			ctx->video_enc_ctx->framerate = in_vstream->avg_frame_rate;
		} else if (in_vstream->r_frame_rate.num > 0 && in_vstream->r_frame_rate.den > 0) {
			ctx->video_enc_ctx->time_base = av_inv_q(in_vstream->r_frame_rate);
			ctx->video_enc_ctx->framerate = in_vstream->r_frame_rate;
		} else {
			ctx->video_enc_ctx->time_base = (AVRational){1, 30};
			ctx->video_enc_ctx->framerate = (AVRational){30, 1};
		}

		// GOP = fps * 2
		double fps_val = av_q2d(ctx->video_enc_ctx->framerate);
		if (fps_val < 1.0) fps_val = 30.0;
		ctx->video_enc_ctx->gop_size = (int)(fps_val * 2.0);
		ctx->video_enc_ctx->max_b_frames = 0;

		// Bitrate: use param or auto based on resolution
		if (bitrate > 0) {
			ctx->video_enc_ctx->bit_rate = bitrate;
		} else {
			if (enc_w >= 3840) {
				ctx->video_enc_ctx->bit_rate = 20000000;
			} else if (enc_w >= 1920) {
				ctx->video_enc_ctx->bit_rate = 10000000;
			} else if (enc_w >= 1280) {
				ctx->video_enc_ctx->bit_rate = 5000000;
			} else {
				ctx->video_enc_ctx->bit_rate = 2000000;
			}
		}

		// BT.709 colorspace signaling
		ctx->video_enc_ctx->color_primaries = AVCOL_PRI_BT709;
		ctx->video_enc_ctx->color_trc = AVCOL_TRC_BT709;
		ctx->video_enc_ctx->colorspace = AVCOL_SPC_BT709;
		ctx->video_enc_ctx->color_range = AVCOL_RANGE_MPEG;

		// Codec-specific options
		if (strcmp(venc->name, "libx264") == 0) {
			av_opt_set(ctx->video_enc_ctx->priv_data, "preset", "medium", 0);
			av_opt_set(ctx->video_enc_ctx->priv_data, "profile", "high", 0);
		} else {
			// Reasonable defaults for other encoders
			ctx->video_enc_ctx->thread_count = 0;
		}

		if (ctx->ofmt_ctx->oformat->flags & AVFMT_GLOBALHEADER) {
			ctx->video_enc_ctx->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
		}

		ret = avcodec_open2(ctx->video_enc_ctx, venc, NULL);
		if (ret < 0) {
			ret = -5;
			goto fail;
		}

		avcodec_parameters_from_context(ctx->out_video_stream->codecpar, ctx->video_enc_ctx);
		ctx->out_video_stream->time_base = ctx->video_enc_ctx->time_base;
		ctx->out_video_stream->codecpar->codec_tag = 0;
	}

	// --- Create output audio stream + encoder (if input has audio) ---
	if (ctx->audio_dec_ctx) {
		const AVCodec* aenc = avcodec_find_encoder(AV_CODEC_ID_AAC);
		if (!aenc) {
			ret = -6;
			goto fail;
		}

		ctx->out_audio_stream = avformat_new_stream(ctx->ofmt_ctx, NULL);
		if (!ctx->out_audio_stream) {
			ret = -6;
			goto fail;
		}
		ctx->out_audio_idx = ctx->out_audio_stream->index;

		ctx->audio_enc_ctx = avcodec_alloc_context3(aenc);
		if (!ctx->audio_enc_ctx) {
			ret = -6;
			goto fail;
		}

		ctx->audio_enc_ctx->sample_rate = 48000;
		ctx->audio_enc_ctx->sample_fmt = AV_SAMPLE_FMT_FLTP;
		ctx->audio_enc_ctx->bit_rate = 128000;
		ctx->audio_enc_ctx->time_base = (AVRational){1, 48000};

		// Set stereo channel layout
		AVChannelLayout stereo = AV_CHANNEL_LAYOUT_STEREO;
		av_channel_layout_copy(&ctx->audio_enc_ctx->ch_layout, &stereo);

		if (ctx->ofmt_ctx->oformat->flags & AVFMT_GLOBALHEADER) {
			ctx->audio_enc_ctx->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
		}

		ret = avcodec_open2(ctx->audio_enc_ctx, aenc, NULL);
		if (ret < 0) {
			ret = -6;
			goto fail;
		}

		avcodec_parameters_from_context(ctx->out_audio_stream->codecpar, ctx->audio_enc_ctx);
		ctx->out_audio_stream->time_base = ctx->audio_enc_ctx->time_base;
		ctx->out_audio_stream->codecpar->codec_tag = 0;

		if (ctx->audio_enc_ctx->frame_size > 0) {
			ctx->aac_frame_size = ctx->audio_enc_ctx->frame_size;
		}
	}

	// --- Open output file ---
	if (!(ctx->ofmt_ctx->oformat->flags & AVFMT_NOFILE)) {
		ret = avio_open(&ctx->ofmt_ctx->pb, output_path, AVIO_FLAG_WRITE);
		if (ret < 0) {
			ret = -7;
			goto fail;
		}
	}

	ret = avformat_write_header(ctx->ofmt_ctx, NULL);
	if (ret < 0) {
		ret = -7;
		goto fail;
	}

	// --- Create sws context ---
	{
		enum AVPixelFormat src_pix_fmt = ctx->video_dec_ctx->pix_fmt;
		// If the decoder hasn't determined the pixel format yet, default to YUV420P
		if (src_pix_fmt == AV_PIX_FMT_NONE) {
			src_pix_fmt = AV_PIX_FMT_YUV420P;
		}
		ctx->sws_ctx = sws_getContext(
			ctx->video_dec_ctx->width, ctx->video_dec_ctx->height, src_pix_fmt,
			ctx->video_enc_ctx->width, ctx->video_enc_ctx->height, AV_PIX_FMT_YUV420P,
			SWS_BILINEAR, NULL, NULL, NULL);
		if (!ctx->sws_ctx) {
			ret = -9;
			goto fail;
		}
	}

	// --- Create swr context (if audio) ---
	if (ctx->audio_dec_ctx && ctx->audio_enc_ctx) {
		ret = swr_alloc_set_opts2(&ctx->swr_ctx,
			&ctx->audio_enc_ctx->ch_layout, AV_SAMPLE_FMT_FLTP, 48000,
			&ctx->audio_dec_ctx->ch_layout, ctx->audio_dec_ctx->sample_fmt,
			ctx->audio_dec_ctx->sample_rate, 0, NULL);
		if (ret < 0 || !ctx->swr_ctx) {
			ret = -10;
			goto fail;
		}
		ret = swr_init(ctx->swr_ctx);
		if (ret < 0) {
			ret = -10;
			goto fail;
		}

		ctx->afifo = av_audio_fifo_alloc(AV_SAMPLE_FMT_FLTP,
			ctx->audio_enc_ctx->ch_layout.nb_channels, ctx->aac_frame_size * 4);
		if (!ctx->afifo) {
			ret = -10;
			goto fail;
		}
	}

	// --- Allocate frames and packets ---
	ctx->dec_frame = av_frame_alloc();
	ctx->sws_frame = av_frame_alloc();
	ctx->swr_frame = av_frame_alloc();
	ctx->pkt = av_packet_alloc();
	ctx->enc_pkt = av_packet_alloc();
	if (!ctx->dec_frame || !ctx->sws_frame || !ctx->swr_frame || !ctx->pkt || !ctx->enc_pkt) {
		ret = -8;
		goto fail;
	}

	// Set up sws output frame
	ctx->sws_frame->format = AV_PIX_FMT_YUV420P;
	ctx->sws_frame->width = ctx->video_enc_ctx->width;
	ctx->sws_frame->height = ctx->video_enc_ctx->height;
	ret = av_frame_get_buffer(ctx->sws_frame, 0);
	if (ret < 0) {
		ret = -8;
		goto fail;
	}

	// Set up swr output frame (if audio). Sized to exactly one encoder frame;
	// the FIFO above guarantees we always fill it with aac_frame_size samples
	// (except for the final partial frame).
	if (ctx->audio_enc_ctx) {
		ctx->swr_frame->format = AV_SAMPLE_FMT_FLTP;
		ctx->swr_frame->sample_rate = 48000;
		av_channel_layout_copy(&ctx->swr_frame->ch_layout, &ctx->audio_enc_ctx->ch_layout);
		ctx->swr_frame->nb_samples = ctx->aac_frame_size;
		ret = av_frame_get_buffer(ctx->swr_frame, 0);
		if (ret < 0) {
			ret = -8;
			goto fail;
		}
	}

	return 0;

fail:
	ff_transcode_free(ctx);
	return ret;
}

// ff_transcode_run executes the frame loop of a transcode prepared by
// ff_transcode_open: demux, decode, scale/resample, encode, mux, flush, and
// trailer. It performs no avformat_open_input/avcodec_open2 calls, so it must
// run WITHOUT FFmpegOpenMu held — it can take minutes for long inputs and
// would otherwise block every other FFmpeg open in the process. The caller
// must free ctx with ff_transcode_free afterwards. Returns 0 on success or
// -8 if the transcode loop aborted on a fatal decode/encode/mux error.
static int ff_transcode_run(transcode_ctx_t* ctx, int* progress_pct,
                            transcode_result_t* result) {
	memset(result, 0, sizeof(transcode_result_t));

	int ret = 0;
	int loop_err = 0;
	int video_frames = 0;
	int64_t video_pts = 0;
	int64_t audio_pts = 0;
	int64_t audio_samples_sent = 0;
	enum AVPixelFormat last_sws_fmt = AV_PIX_FMT_NONE;
	int last_sws_w = 0, last_sws_h = 0;

	AVFormatContext* ifmt_ctx = ctx->ifmt_ctx;
	int video_stream_idx = ctx->video_stream_idx;
	int audio_stream_idx = ctx->audio_stream_idx;
	AVCodecContext* video_dec_ctx = ctx->video_dec_ctx;
	AVCodecContext* audio_dec_ctx = ctx->audio_dec_ctx;
	AVFormatContext* ofmt_ctx = ctx->ofmt_ctx;
	AVCodecContext* video_enc_ctx = ctx->video_enc_ctx;
	AVCodecContext* audio_enc_ctx = ctx->audio_enc_ctx;
	AVStream* out_video_stream = ctx->out_video_stream;
	AVStream* out_audio_stream = ctx->out_audio_stream;
	int out_video_idx = ctx->out_video_idx;
	int out_audio_idx = ctx->out_audio_idx;
	struct SwrContext* swr_ctx = ctx->swr_ctx;
	AVAudioFifo* afifo = ctx->afifo;
	int aac_frame_size = ctx->aac_frame_size;
	AVFrame* dec_frame = ctx->dec_frame;
	AVFrame* sws_frame = ctx->sws_frame;
	AVFrame* swr_frame = ctx->swr_frame;
	AVPacket* pkt = ctx->pkt;
	AVPacket* enc_pkt = ctx->enc_pkt;

	// --- Main transcode loop ---
	{
		while (1) {
			ret = av_read_frame(ifmt_ctx, pkt);
			if (ret < 0) {
				break; // EOF or error
			}

			if (pkt->stream_index == video_stream_idx) {
				// Update progress percentage based on packet PTS vs input duration.
				// Uses __atomic_store_n with relaxed ordering so that the Go side
				// can safely read via atomic.LoadInt32 without a data race.
				if (progress_pct && ifmt_ctx->duration > 0 && pkt->pts != AV_NOPTS_VALUE) {
					int64_t pts_us = av_rescale_q(pkt->pts,
						ifmt_ctx->streams[video_stream_idx]->time_base,
						(AVRational){1, AV_TIME_BASE});
					int pct = (int)(pts_us * 100 / ifmt_ctx->duration);
					if (pct < 0) pct = 0;
					if (pct > 100) pct = 100;
					__atomic_store_n(progress_pct, pct, __ATOMIC_RELAXED);
				}

				// Decode video
				ret = avcodec_send_packet(video_dec_ctx, pkt);
				av_packet_unref(pkt);
				if (ret < 0) {
					continue; // skip bad packets
				}

				while (1) {
					ret = avcodec_receive_frame(video_dec_ctx, dec_frame);
					if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
						break;
					}
					if (ret < 0) {
						goto loop_fail;
					}

					// Reinitialize sws only when decoder format/resolution actually changes.
					if (dec_frame->format != AV_PIX_FMT_NONE &&
					    (dec_frame->format != last_sws_fmt ||
					     dec_frame->width  != last_sws_w   ||
					     dec_frame->height != last_sws_h)) {
						struct SwsContext* new_sws = sws_getContext(
							dec_frame->width, dec_frame->height, dec_frame->format,
							video_enc_ctx->width, video_enc_ctx->height, AV_PIX_FMT_YUV420P,
							SWS_BILINEAR, NULL, NULL, NULL);
						if (new_sws) {
							sws_freeContext(ctx->sws_ctx);
							ctx->sws_ctx = new_sws;
							last_sws_fmt = dec_frame->format;
							last_sws_w   = dec_frame->width;
							last_sws_h   = dec_frame->height;
						}
					}

					// Scale/convert
					av_frame_make_writable(sws_frame);
					sws_scale(ctx->sws_ctx,
						(const uint8_t* const*)dec_frame->data, dec_frame->linesize,
						0, dec_frame->height,
						sws_frame->data, sws_frame->linesize);

					sws_frame->pts = video_pts++;
					av_frame_unref(dec_frame);

					// Encode video
					ret = avcodec_send_frame(video_enc_ctx, sws_frame);
					if (ret < 0) {
						continue;
					}

					while (1) {
						ret = avcodec_receive_packet(video_enc_ctx, enc_pkt);
						if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
							break;
						}
						if (ret < 0) {
							goto loop_fail;
						}

						enc_pkt->stream_index = out_video_idx;
						av_packet_rescale_ts(enc_pkt,
							video_enc_ctx->time_base,
							out_video_stream->time_base);
						ret = av_interleaved_write_frame(ofmt_ctx, enc_pkt);
						av_packet_unref(enc_pkt);
						if (ret < 0) {
							goto loop_fail;
						}
						video_frames++;
					}
				}
			} else if (pkt->stream_index == audio_stream_idx && audio_dec_ctx && audio_enc_ctx) {
				// Decode audio
				ret = avcodec_send_packet(audio_dec_ctx, pkt);
				av_packet_unref(pkt);
				if (ret < 0) {
					continue;
				}

				while (1) {
					ret = avcodec_receive_frame(audio_dec_ctx, dec_frame);
					if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
						break;
					}
					if (ret < 0) {
						goto loop_fail;
					}

					// Resample into the FIFO and encode in exact aac_frame_size
					// chunks (the AAC encoder rejects mid-stream short frames).
					ret = ff_transcode_push_audio(swr_ctx, afifo,
						(const uint8_t**)dec_frame->data, dec_frame->nb_samples,
						audio_enc_ctx->ch_layout.nb_channels,
						&ctx->conv_samples, &ctx->conv_capacity);
					av_frame_unref(dec_frame);
					if (ret < 0) {
						continue; // skip unconvertible frames
					}

					ret = ff_transcode_drain_audio_fifo(afifo, aac_frame_size, 0,
						swr_frame, &audio_pts, &audio_samples_sent,
						audio_enc_ctx, ofmt_ctx, out_audio_stream,
						out_audio_idx, enc_pkt);
					if (ret < 0) {
						goto loop_fail;
					}
				}
			} else {
				av_packet_unref(pkt);
			}
		}
	}

	goto loop_done;

loop_fail:
	// A fatal mid-loop error (decode/encode/mux failure) aborted the transcode.
	// Remember it so the function reports failure instead of silently returning
	// a truncated output as success. ret itself is reused as scratch by the
	// flush paths below, so the error must be latched here.
	loop_err = -8;

loop_done:

	// --- Flush video decoder ---
	// With thread_count=0 (auto), the decoder uses frame threading which
	// buffers multiple frames internally. Sending a NULL packet drains them.
	avcodec_send_packet(video_dec_ctx, NULL);
	while (1) {
		ret = avcodec_receive_frame(video_dec_ctx, dec_frame);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
			break;
		}
		if (ret < 0) {
			break;
		}

		// Reinitialize sws if decoder format/resolution changed.
		if (dec_frame->format != AV_PIX_FMT_NONE &&
		    (dec_frame->format != last_sws_fmt ||
		     dec_frame->width  != last_sws_w   ||
		     dec_frame->height != last_sws_h)) {
			struct SwsContext* new_sws = sws_getContext(
				dec_frame->width, dec_frame->height, dec_frame->format,
				video_enc_ctx->width, video_enc_ctx->height, AV_PIX_FMT_YUV420P,
				SWS_BILINEAR, NULL, NULL, NULL);
			if (new_sws) {
				sws_freeContext(ctx->sws_ctx);
				ctx->sws_ctx = new_sws;
				last_sws_fmt = dec_frame->format;
				last_sws_w   = dec_frame->width;
				last_sws_h   = dec_frame->height;
			}
		}

		av_frame_make_writable(sws_frame);
		sws_scale(ctx->sws_ctx,
			(const uint8_t* const*)dec_frame->data, dec_frame->linesize,
			0, dec_frame->height,
			sws_frame->data, sws_frame->linesize);

		sws_frame->pts = video_pts++;
		av_frame_unref(dec_frame);

		ret = avcodec_send_frame(video_enc_ctx, sws_frame);
		if (ret < 0) {
			continue;
		}

		while (1) {
			ret = avcodec_receive_packet(video_enc_ctx, enc_pkt);
			if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
				break;
			}
			if (ret < 0) {
				break;
			}

			enc_pkt->stream_index = out_video_idx;
			av_packet_rescale_ts(enc_pkt,
				video_enc_ctx->time_base,
				out_video_stream->time_base);
			av_interleaved_write_frame(ofmt_ctx, enc_pkt);
			av_packet_unref(enc_pkt);
			video_frames++;
		}
	}

	// --- Flush video encoder ---
	avcodec_send_frame(video_enc_ctx, NULL);
	while (1) {
		ret = avcodec_receive_packet(video_enc_ctx, enc_pkt);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
			break;
		}
		if (ret < 0) {
			break;
		}
		enc_pkt->stream_index = out_video_idx;
		av_packet_rescale_ts(enc_pkt,
			video_enc_ctx->time_base,
			out_video_stream->time_base);
		av_interleaved_write_frame(ofmt_ctx, enc_pkt);
		av_packet_unref(enc_pkt);
		video_frames++;
	}

	// --- Flush audio decoder ---
	if (audio_dec_ctx && audio_enc_ctx) {
		avcodec_send_packet(audio_dec_ctx, NULL);
		while (1) {
			ret = avcodec_receive_frame(audio_dec_ctx, dec_frame);
			if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
				break;
			}
			if (ret < 0) {
				break;
			}

			ret = ff_transcode_push_audio(swr_ctx, afifo,
				(const uint8_t**)dec_frame->data, dec_frame->nb_samples,
				audio_enc_ctx->ch_layout.nb_channels,
				&ctx->conv_samples, &ctx->conv_capacity);
			av_frame_unref(dec_frame);
			if (ret < 0) {
				continue;
			}

			if (ff_transcode_drain_audio_fifo(afifo, aac_frame_size, 0,
				swr_frame, &audio_pts, &audio_samples_sent,
				audio_enc_ctx, ofmt_ctx, out_audio_stream,
				out_audio_idx, enc_pkt) < 0) {
				break;
			}
		}
	}

	// --- Flush audio encoder ---
	if (audio_enc_ctx) {
		// Flush remaining samples from the resampler into the FIFO, then
		// drain everything including the trailing partial frame.
		if (swr_ctx && afifo) {
			ff_transcode_push_audio(swr_ctx, afifo, NULL, 0,
				audio_enc_ctx->ch_layout.nb_channels,
				&ctx->conv_samples, &ctx->conv_capacity);
			ff_transcode_drain_audio_fifo(afifo, aac_frame_size, 1,
				swr_frame, &audio_pts, &audio_samples_sent,
				audio_enc_ctx, ofmt_ctx, out_audio_stream,
				out_audio_idx, enc_pkt);
		}

		avcodec_send_frame(audio_enc_ctx, NULL);
		while (1) {
			ret = avcodec_receive_packet(audio_enc_ctx, enc_pkt);
			if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) break;
			if (ret < 0) break;
			enc_pkt->stream_index = out_audio_idx;
			av_packet_rescale_ts(enc_pkt,
				audio_enc_ctx->time_base,
				out_audio_stream->time_base);
			av_interleaved_write_frame(ofmt_ctx, enc_pkt);
			av_packet_unref(enc_pkt);
		}
	}

	// --- Write trailer ---
	av_write_trailer(ofmt_ctx);

	// --- Fill result ---
	{
		double fps_val = av_q2d(video_enc_ctx->framerate);
		if (fps_val < 0.01) fps_val = 0.0;

		result->width = video_enc_ctx->width;
		result->height = video_enc_ctx->height;
		result->fps = fps_val;
		result->video_frames = video_frames;

		if (video_frames > 0 && fps_val > 0.01) {
			result->duration_ms = (int64_t)((double)video_frames / fps_val * 1000.0);
		} else {
			// Fall back to input duration
			if (ifmt_ctx->duration > 0) {
				result->duration_ms = ifmt_ctx->duration / (AV_TIME_BASE / 1000);
			}
		}

		if (audio_enc_ctx) {
			result->sample_rate = audio_enc_ctx->sample_rate;
			result->channels = audio_enc_ctx->ch_layout.nb_channels;
			result->audio_samples = audio_samples_sent;
		}
	}

	// Mark progress complete after successful flush.
	if (progress_pct && loop_err == 0) {
		__atomic_store_n(progress_pct, 100, __ATOMIC_RELAXED);
	}

	return loop_err; // 0 on success, -8 if the transcode loop aborted
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// TranscodeResult holds metadata from the transcode operation.
type TranscodeResult struct {
	Width       int
	Height      int
	DurationMs  int64
	FPS         float64
	SampleRate  int
	Channels    int
	VideoFrames int
	// AudioSamples is the number of audio samples (at the output sample rate)
	// accepted by the audio encoder.
	AudioSamples int64
}

// transcodeErrors maps C error codes to descriptive messages.
var transcodeErrors = map[C.int]string{
	-1:  "cannot open input file",
	-2:  "no video stream found",
	-3:  "cannot open video decoder",
	-4:  "cannot create output context",
	-5:  "cannot open video encoder",
	-6:  "cannot open audio encoder",
	-7:  "cannot open output file",
	-8:  "transcode processing error",
	-9:  "pixel format conversion init failed",
	-10: "audio resampling init failed",
}

// TranscodeFile transcodes any media file supported by FFmpeg's avformat into
// H.264+AAC MPEG-TS. The encoderName should come from ProbeEncoders() (e.g.,
// "libx264", "h264_videotoolbox"). Pass "" to auto-select H.264 encoder.
// Bitrate of 0 auto-selects based on resolution.
func TranscodeFile(inputPath, outputPath, encoderName string, bitrate int) (*TranscodeResult, error) {
	return TranscodeFileWithProgress(inputPath, outputPath, encoderName, bitrate, nil)
}

// TranscodeFileWithProgress is like TranscodeFile but accepts an optional
// progress pointer. When non-nil, the C transcode loop atomically writes
// 0-100 into *progressPct as packets are processed.
func TranscodeFileWithProgress(inputPath, outputPath, encoderName string, bitrate int, progressPct *int32) (*TranscodeResult, error) {
	initFFmpegLogLevel()

	cInput := C.CString(inputPath)
	defer C.free(unsafe.Pointer(cInput))

	cOutput := C.CString(outputPath)
	defer C.free(unsafe.Pointer(cOutput))

	var cEncoder *C.char
	if encoderName != "" {
		cEncoder = C.CString(encoderName)
		defer C.free(unsafe.Pointer(cEncoder))
	}

	var cProgress *C.int
	if progressPct != nil {
		cProgress = (*C.int)(unsafe.Pointer(progressPct))
	}

	// The open phase performs every avformat_open_input/avcodec_open2 of the
	// transcode (including h264_nvenc, which initializes CUDA inside
	// avcodec_open2), so it must hold FFmpegOpenMu like every other open site.
	// The frame loop below runs without the lock: it does no opens and can
	// take minutes, and holding the global open lock for its duration would
	// block every other FFmpeg open (e.g. source connects) in the process.
	var tctx C.transcode_ctx_t
	FFmpegOpenMu.Lock()
	rc := C.ff_transcode_open(cInput, cOutput, cEncoder, C.int(bitrate), &tctx)
	FFmpegOpenMu.Unlock()
	if rc != 0 {
		return nil, transcodeError(rc)
	}
	defer C.ff_transcode_free(&tctx)

	var result C.transcode_result_t
	rc = C.ff_transcode_run(&tctx, cProgress, &result)
	if rc != 0 {
		return nil, transcodeError(rc)
	}

	return &TranscodeResult{
		Width:        int(result.width),
		Height:       int(result.height),
		DurationMs:   int64(result.duration_ms),
		FPS:          float64(result.fps),
		SampleRate:   int(result.sample_rate),
		Channels:     int(result.channels),
		VideoFrames:  int(result.video_frames),
		AudioSamples: int64(result.audio_samples),
	}, nil
}

// transcodeError maps a negative C return code to a descriptive error.
func transcodeError(rc C.int) error {
	msg := transcodeErrors[rc]
	if msg == "" {
		msg = "unknown error"
	}
	return fmt.Errorf("transcode failed: %s (code %d)", msg, int(rc))
}
