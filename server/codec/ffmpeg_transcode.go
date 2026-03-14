//go:build cgo && !noffmpeg

package codec

/*
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
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
} transcode_result_t;

// ff_transcode_file transcodes any media file FFmpeg can handle into H.264+AAC MPEG-TS.
//
// Error codes:
//   -1: cannot open input
//   -2: no video stream found
//   -3: cannot open video decoder
//   -4: cannot create output context
//   -5: cannot open video encoder
//   -6: cannot open audio encoder
//   -7: cannot open output file
//   -8: transcode loop error
//   -9: sws_getContext failed
//  -10: swr init failed
static int ff_transcode_file(const char* input_path, const char* output_path,
                              const char* encoder_name, int bitrate,
                              int* progress_pct,
                              transcode_result_t* result) {
	memset(result, 0, sizeof(transcode_result_t));

	int ret = 0;
	int video_frames = 0;

	// Input state
	AVFormatContext* ifmt_ctx = NULL;
	int video_stream_idx = -1;
	int audio_stream_idx = -1;
	AVCodecContext* video_dec_ctx = NULL;
	AVCodecContext* audio_dec_ctx = NULL;

	// Output state
	AVFormatContext* ofmt_ctx = NULL;
	AVCodecContext* video_enc_ctx = NULL;
	AVCodecContext* audio_enc_ctx = NULL;
	AVStream* out_video_stream = NULL;
	AVStream* out_audio_stream = NULL;
	int out_video_idx = -1;
	int out_audio_idx = -1;

	// Conversion state
	struct SwsContext* sws_ctx = NULL;
	struct SwrContext* swr_ctx = NULL;

	// Frame/packet state
	AVFrame* dec_frame = NULL;
	AVFrame* sws_frame = NULL;
	AVFrame* swr_frame = NULL;
	AVPacket* pkt = NULL;
	AVPacket* enc_pkt = NULL;

	// --- Open input ---
	ret = avformat_open_input(&ifmt_ctx, input_path, NULL, NULL);
	if (ret < 0) {
		return -1;
	}

	ret = avformat_find_stream_info(ifmt_ctx, NULL);
	if (ret < 0) {
		goto cleanup;
	}

	// --- Find best streams ---
	video_stream_idx = av_find_best_stream(ifmt_ctx, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
	if (video_stream_idx < 0) {
		ret = -2;
		goto cleanup;
	}

	audio_stream_idx = av_find_best_stream(ifmt_ctx, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);

	// --- Open video decoder ---
	{
		AVStream* in_vstream = ifmt_ctx->streams[video_stream_idx];
		const AVCodec* vdec = avcodec_find_decoder(in_vstream->codecpar->codec_id);
		if (!vdec) {
			ret = -3;
			goto cleanup;
		}
		video_dec_ctx = avcodec_alloc_context3(vdec);
		if (!video_dec_ctx) {
			ret = -3;
			goto cleanup;
		}
		avcodec_parameters_to_context(video_dec_ctx, in_vstream->codecpar);
		video_dec_ctx->thread_count = 0; // auto
		ret = avcodec_open2(video_dec_ctx, vdec, NULL);
		if (ret < 0) {
			ret = -3;
			goto cleanup;
		}
	}

	// --- Open audio decoder (optional) ---
	if (audio_stream_idx >= 0) {
		AVStream* in_astream = ifmt_ctx->streams[audio_stream_idx];
		const AVCodec* adec = avcodec_find_decoder(in_astream->codecpar->codec_id);
		if (adec) {
			audio_dec_ctx = avcodec_alloc_context3(adec);
			if (audio_dec_ctx) {
				avcodec_parameters_to_context(audio_dec_ctx, in_astream->codecpar);
				ret = avcodec_open2(audio_dec_ctx, adec, NULL);
				if (ret < 0) {
					avcodec_free_context(&audio_dec_ctx);
					audio_dec_ctx = NULL;
					audio_stream_idx = -1; // proceed without audio
				}
			}
		}
	}

	// --- Create output context ---
	ret = avformat_alloc_output_context2(&ofmt_ctx, NULL, "mpegts", output_path);
	if (ret < 0 || !ofmt_ctx) {
		ret = -4;
		goto cleanup;
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
			goto cleanup;
		}

		out_video_stream = avformat_new_stream(ofmt_ctx, NULL);
		if (!out_video_stream) {
			ret = -5;
			goto cleanup;
		}
		out_video_idx = out_video_stream->index;

		video_enc_ctx = avcodec_alloc_context3(venc);
		if (!video_enc_ctx) {
			ret = -5;
			goto cleanup;
		}

		// Resolution from input, even-aligned
		int enc_w = video_dec_ctx->width & ~1;
		int enc_h = video_dec_ctx->height & ~1;
		if (enc_w <= 0) enc_w = 2;
		if (enc_h <= 0) enc_h = 2;

		video_enc_ctx->width = enc_w;
		video_enc_ctx->height = enc_h;
		video_enc_ctx->pix_fmt = AV_PIX_FMT_YUV420P;

		// time_base from input stream
		AVStream* in_vstream = ifmt_ctx->streams[video_stream_idx];
		if (in_vstream->avg_frame_rate.num > 0 && in_vstream->avg_frame_rate.den > 0) {
			video_enc_ctx->time_base = av_inv_q(in_vstream->avg_frame_rate);
			video_enc_ctx->framerate = in_vstream->avg_frame_rate;
		} else if (in_vstream->r_frame_rate.num > 0 && in_vstream->r_frame_rate.den > 0) {
			video_enc_ctx->time_base = av_inv_q(in_vstream->r_frame_rate);
			video_enc_ctx->framerate = in_vstream->r_frame_rate;
		} else {
			video_enc_ctx->time_base = (AVRational){1, 30};
			video_enc_ctx->framerate = (AVRational){30, 1};
		}

		// GOP = fps * 2
		double fps_val = av_q2d(video_enc_ctx->framerate);
		if (fps_val < 1.0) fps_val = 30.0;
		video_enc_ctx->gop_size = (int)(fps_val * 2.0);
		video_enc_ctx->max_b_frames = 0;

		// Bitrate: use param or auto based on resolution
		if (bitrate > 0) {
			video_enc_ctx->bit_rate = bitrate;
		} else {
			if (enc_w >= 3840) {
				video_enc_ctx->bit_rate = 20000000;
			} else if (enc_w >= 1920) {
				video_enc_ctx->bit_rate = 10000000;
			} else if (enc_w >= 1280) {
				video_enc_ctx->bit_rate = 5000000;
			} else {
				video_enc_ctx->bit_rate = 2000000;
			}
		}

		// BT.709 colorspace signaling
		video_enc_ctx->color_primaries = AVCOL_PRI_BT709;
		video_enc_ctx->color_trc = AVCOL_TRC_BT709;
		video_enc_ctx->colorspace = AVCOL_SPC_BT709;
		video_enc_ctx->color_range = AVCOL_RANGE_MPEG;

		// Codec-specific options
		if (strcmp(venc->name, "libx264") == 0) {
			av_opt_set(video_enc_ctx->priv_data, "preset", "medium", 0);
			av_opt_set(video_enc_ctx->priv_data, "profile", "high", 0);
		} else {
			// Reasonable defaults for other encoders
			video_enc_ctx->thread_count = 0;
		}

		if (ofmt_ctx->oformat->flags & AVFMT_GLOBALHEADER) {
			video_enc_ctx->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
		}

		ret = avcodec_open2(video_enc_ctx, venc, NULL);
		if (ret < 0) {
			ret = -5;
			goto cleanup;
		}

		avcodec_parameters_from_context(out_video_stream->codecpar, video_enc_ctx);
		out_video_stream->time_base = video_enc_ctx->time_base;
		out_video_stream->codecpar->codec_tag = 0;
	}

	// --- Create output audio stream + encoder (if input has audio) ---
	if (audio_dec_ctx) {
		const AVCodec* aenc = avcodec_find_encoder(AV_CODEC_ID_AAC);
		if (!aenc) {
			ret = -6;
			goto cleanup;
		}

		out_audio_stream = avformat_new_stream(ofmt_ctx, NULL);
		if (!out_audio_stream) {
			ret = -6;
			goto cleanup;
		}
		out_audio_idx = out_audio_stream->index;

		audio_enc_ctx = avcodec_alloc_context3(aenc);
		if (!audio_enc_ctx) {
			ret = -6;
			goto cleanup;
		}

		audio_enc_ctx->sample_rate = 48000;
		audio_enc_ctx->sample_fmt = AV_SAMPLE_FMT_FLTP;
		audio_enc_ctx->bit_rate = 128000;
		audio_enc_ctx->time_base = (AVRational){1, 48000};

		// Set stereo channel layout
		AVChannelLayout stereo = AV_CHANNEL_LAYOUT_STEREO;
		av_channel_layout_copy(&audio_enc_ctx->ch_layout, &stereo);

		if (ofmt_ctx->oformat->flags & AVFMT_GLOBALHEADER) {
			audio_enc_ctx->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
		}

		ret = avcodec_open2(audio_enc_ctx, aenc, NULL);
		if (ret < 0) {
			ret = -6;
			goto cleanup;
		}

		avcodec_parameters_from_context(out_audio_stream->codecpar, audio_enc_ctx);
		out_audio_stream->time_base = audio_enc_ctx->time_base;
		out_audio_stream->codecpar->codec_tag = 0;
	}

	// --- Open output file ---
	if (!(ofmt_ctx->oformat->flags & AVFMT_NOFILE)) {
		ret = avio_open(&ofmt_ctx->pb, output_path, AVIO_FLAG_WRITE);
		if (ret < 0) {
			ret = -7;
			goto cleanup;
		}
	}

	ret = avformat_write_header(ofmt_ctx, NULL);
	if (ret < 0) {
		ret = -7;
		goto cleanup;
	}

	// --- Create sws context ---
	{
		enum AVPixelFormat src_pix_fmt = video_dec_ctx->pix_fmt;
		// If the decoder hasn't determined the pixel format yet, default to YUV420P
		if (src_pix_fmt == AV_PIX_FMT_NONE) {
			src_pix_fmt = AV_PIX_FMT_YUV420P;
		}
		sws_ctx = sws_getContext(
			video_dec_ctx->width, video_dec_ctx->height, src_pix_fmt,
			video_enc_ctx->width, video_enc_ctx->height, AV_PIX_FMT_YUV420P,
			SWS_BILINEAR, NULL, NULL, NULL);
		if (!sws_ctx) {
			ret = -9;
			goto cleanup;
		}
	}

	// --- Create swr context (if audio) ---
	if (audio_dec_ctx && audio_enc_ctx) {
		ret = swr_alloc_set_opts2(&swr_ctx,
			&audio_enc_ctx->ch_layout, AV_SAMPLE_FMT_FLTP, 48000,
			&audio_dec_ctx->ch_layout, audio_dec_ctx->sample_fmt,
			audio_dec_ctx->sample_rate, 0, NULL);
		if (ret < 0 || !swr_ctx) {
			ret = -10;
			goto cleanup;
		}
		ret = swr_init(swr_ctx);
		if (ret < 0) {
			ret = -10;
			goto cleanup;
		}
	}

	// --- Allocate frames and packets ---
	dec_frame = av_frame_alloc();
	sws_frame = av_frame_alloc();
	swr_frame = av_frame_alloc();
	pkt = av_packet_alloc();
	enc_pkt = av_packet_alloc();
	if (!dec_frame || !sws_frame || !swr_frame || !pkt || !enc_pkt) {
		ret = -8;
		goto cleanup;
	}

	// Set up sws output frame
	sws_frame->format = AV_PIX_FMT_YUV420P;
	sws_frame->width = video_enc_ctx->width;
	sws_frame->height = video_enc_ctx->height;
	ret = av_frame_get_buffer(sws_frame, 0);
	if (ret < 0) {
		ret = -8;
		goto cleanup;
	}

	// Set up swr output frame (if audio)
	if (audio_enc_ctx) {
		swr_frame->format = AV_SAMPLE_FMT_FLTP;
		swr_frame->sample_rate = 48000;
		av_channel_layout_copy(&swr_frame->ch_layout, &audio_enc_ctx->ch_layout);
		swr_frame->nb_samples = audio_enc_ctx->frame_size;
		if (swr_frame->nb_samples <= 0) {
			swr_frame->nb_samples = 1024;
		}
		ret = av_frame_get_buffer(swr_frame, 0);
		if (ret < 0) {
			ret = -8;
			goto cleanup;
		}
	}

	// --- Main transcode loop ---
	{
		int64_t video_pts = 0;
		int64_t audio_pts = 0;

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
						goto loop_done;
					}

					// Lazily reinitialize sws if decoder pixel format changed
					if (dec_frame->format != AV_PIX_FMT_NONE) {
						struct SwsContext* new_sws = sws_getContext(
							dec_frame->width, dec_frame->height, dec_frame->format,
							video_enc_ctx->width, video_enc_ctx->height, AV_PIX_FMT_YUV420P,
							SWS_BILINEAR, NULL, NULL, NULL);
						if (new_sws) {
							sws_freeContext(sws_ctx);
							sws_ctx = new_sws;
						}
					}

					// Scale/convert
					av_frame_make_writable(sws_frame);
					sws_scale(sws_ctx,
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
							goto loop_done;
						}

						enc_pkt->stream_index = out_video_idx;
						av_packet_rescale_ts(enc_pkt,
							video_enc_ctx->time_base,
							out_video_stream->time_base);
						ret = av_interleaved_write_frame(ofmt_ctx, enc_pkt);
						av_packet_unref(enc_pkt);
						if (ret < 0) {
							goto loop_done;
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
						goto loop_done;
					}

					// Resample audio
					int out_samples = swr_convert(swr_ctx,
						swr_frame->data, swr_frame->nb_samples,
						(const uint8_t**)dec_frame->data, dec_frame->nb_samples);
					av_frame_unref(dec_frame);

					if (out_samples <= 0) {
						continue;
					}

					swr_frame->nb_samples = out_samples;
					swr_frame->pts = audio_pts;
					audio_pts += out_samples;

					// Encode audio
					ret = avcodec_send_frame(audio_enc_ctx, swr_frame);
					if (ret < 0) {
						continue;
					}

					while (1) {
						ret = avcodec_receive_packet(audio_enc_ctx, enc_pkt);
						if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
							break;
						}
						if (ret < 0) {
							goto loop_done;
						}

						enc_pkt->stream_index = out_audio_idx;
						av_packet_rescale_ts(enc_pkt,
							audio_enc_ctx->time_base,
							out_audio_stream->time_base);
						ret = av_interleaved_write_frame(ofmt_ctx, enc_pkt);
						av_packet_unref(enc_pkt);
						if (ret < 0) {
							goto loop_done;
						}
					}
				}
			} else {
				av_packet_unref(pkt);
			}
		}
	}

loop_done:

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

	// --- Flush audio encoder ---
	if (audio_enc_ctx) {
		// Flush remaining samples from resampler
		if (swr_ctx) {
			int flush_samples = swr_convert(swr_ctx,
				swr_frame->data, swr_frame->nb_samples,
				NULL, 0);
			if (flush_samples > 0) {
				swr_frame->nb_samples = flush_samples;
				avcodec_send_frame(audio_enc_ctx, swr_frame);
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
		}
	}

	// Mark progress complete after successful flush.
	if (progress_pct) {
		__atomic_store_n(progress_pct, 100, __ATOMIC_RELAXED);
	}

	ret = 0; // success

cleanup:
	if (enc_pkt) av_packet_free(&enc_pkt);
	if (pkt) av_packet_free(&pkt);
	if (swr_frame) av_frame_free(&swr_frame);
	if (sws_frame) av_frame_free(&sws_frame);
	if (dec_frame) av_frame_free(&dec_frame);
	if (swr_ctx) swr_free(&swr_ctx);
	if (sws_ctx) sws_freeContext(sws_ctx);
	if (audio_enc_ctx) avcodec_free_context(&audio_enc_ctx);
	if (video_enc_ctx) avcodec_free_context(&video_enc_ctx);
	if (audio_dec_ctx) avcodec_free_context(&audio_dec_ctx);
	if (video_dec_ctx) avcodec_free_context(&video_dec_ctx);
	if (ofmt_ctx) {
		if (!(ofmt_ctx->oformat->flags & AVFMT_NOFILE) && ofmt_ctx->pb) {
			avio_closep(&ofmt_ctx->pb);
		}
		avformat_free_context(ofmt_ctx);
	}
	if (ifmt_ctx) avformat_close_input(&ifmt_ctx);

	return ret;
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

	var result C.transcode_result_t
	rc := C.ff_transcode_file(cInput, cOutput, cEncoder, C.int(bitrate), cProgress, &result)
	if rc != 0 {
		msg := transcodeErrors[rc]
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("transcode failed: %s (code %d)", msg, int(rc))
	}

	return &TranscodeResult{
		Width:       int(result.width),
		Height:      int(result.height),
		DurationMs:  int64(result.duration_ms),
		FPS:         float64(result.fps),
		SampleRate:  int(result.sample_rate),
		Channels:    int(result.channels),
		VideoFrames: int(result.video_frames),
	}, nil
}
