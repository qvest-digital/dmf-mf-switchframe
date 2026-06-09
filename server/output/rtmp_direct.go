//go:build cgo && !noffmpeg

package output

/*
#cgo pkg-config: libavformat libavcodec libavutil
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavcodec/defs.h>
#include <libavutil/opt.h>
#include <libavutil/error.h>
#include <libavutil/channel_layout.h>
#include <stdlib.h>
#include <string.h>

// AV_PROFILE_AAC_LOW was introduced in FFmpeg 6.1 (libavcodec 60.31).
// Older versions (e.g. Ubuntu 24.04's libavcodec60) use FF_PROFILE_AAC_LOW.
#ifndef AV_PROFILE_AAC_LOW
#define AV_PROFILE_AAC_LOW FF_PROFILE_AAC_LOW
#endif

typedef struct {
	AVFormatContext* out_ctx;
	int video_idx;
	int audio_idx;
	int header_written;
} rtmp_flv_ctx_t;

// --- Minimal H.264 SPS parser for extracting width/height ---

// Bit reader for exp-Golomb and fixed-width reads.
typedef struct {
	const uint8_t* data;
	int            size;    // total bytes
	int            bit_pos; // current bit offset
} bitreader_t;

static int br_read_bits(bitreader_t* br, int n) {
	int val = 0;
	for (int i = 0; i < n; i++) {
		int byte_idx = br->bit_pos / 8;
		int bit_idx  = 7 - (br->bit_pos % 8);
		if (byte_idx >= br->size) return 0;
		val = (val << 1) | ((br->data[byte_idx] >> bit_idx) & 1);
		br->bit_pos++;
	}
	return val;
}

// Unsigned exp-Golomb (ISO 14496-10 section 9.1).
static unsigned br_read_ue(bitreader_t* br) {
	int leading_zeros = 0;
	while (br->bit_pos / 8 < br->size && br_read_bits(br, 1) == 0)
		leading_zeros++;
	if (leading_zeros > 31) return 0;
	return ((1u << leading_zeros) - 1) + (unsigned)br_read_bits(br, leading_zeros);
}

// Signed exp-Golomb.
static int br_read_se(bitreader_t* br) {
	unsigned v = br_read_ue(br);
	return (v & 1) ? (int)((v + 1) / 2) : -(int)(v / 2);
}

// nalu_to_rbsp strips emulation prevention bytes (00 00 03 → 00 00) from
// a raw NALU to produce the Raw Byte Sequence Payload. Output buffer must
// be at least nalu_len bytes. Returns the RBSP length.
static int nalu_to_rbsp(const uint8_t* nalu, int nalu_len, uint8_t* rbsp) {
	int j = 0;
	for (int i = 0; i < nalu_len; i++) {
		if (i + 2 < nalu_len &&
			nalu[i] == 0 && nalu[i+1] == 0 && nalu[i+2] == 3) {
			rbsp[j++] = 0;
			rbsp[j++] = 0;
			i += 2; // skip 00 00 03; loop increment lands on byte after 03
		} else {
			rbsp[j++] = nalu[i];
		}
	}
	return j;
}

// sps_get_dimensions parses an H.264 SPS NALU to extract width and height.
// sps must include the NALU type byte (0x67). Falls back to 1920x1080
// if parsing fails (never causes rtmp_flv_open to fail).
static void sps_get_dimensions(const uint8_t* sps, int sps_len,
	int* out_width, int* out_height) {
	*out_width  = 1920;
	*out_height = 1080;
	if (sps_len < 4) return;

	// Strip emulation prevention bytes before parsing.
	uint8_t* rbsp = (uint8_t*)malloc(sps_len);
	if (!rbsp) return;
	int rbsp_len = nalu_to_rbsp(sps, sps_len, rbsp);

	bitreader_t br = { rbsp, rbsp_len, 8 }; // skip NALU type byte

	int profile_idc = br_read_bits(&br, 8);
	br_read_bits(&br, 8);  // constraint_set_flags
	br_read_bits(&br, 8);  // level_idc
	br_read_ue(&br);       // seq_parameter_set_id

	// High-profile family: chroma_format_idc and related fields.
	int chroma_format_idc = 1; // default
	if (profile_idc == 100 || profile_idc == 110 || profile_idc == 122 ||
		profile_idc == 244 || profile_idc == 44  || profile_idc == 83  ||
		profile_idc == 86  || profile_idc == 118 || profile_idc == 128 ||
		profile_idc == 138 || profile_idc == 139 || profile_idc == 134 ||
		profile_idc == 135) {
		chroma_format_idc = (int)br_read_ue(&br);
		if (chroma_format_idc == 3) {
			br_read_bits(&br, 1); // separate_colour_plane_flag
		}
		br_read_ue(&br); // bit_depth_luma_minus8
		br_read_ue(&br); // bit_depth_chroma_minus8
		br_read_bits(&br, 1); // qpprime_y_zero_transform_bypass_flag
		int scaling_matrix_present = br_read_bits(&br, 1);
		if (scaling_matrix_present) {
			int cnt = (chroma_format_idc != 3) ? 8 : 12;
			for (int i = 0; i < cnt; i++) {
				if (br_read_bits(&br, 1)) { // scaling_list_present_flag
					int size = (i < 6) ? 16 : 64;
					int last = 8, next = 8;
					for (int j = 0; j < size; j++) {
						if (next != 0) {
							int delta = br_read_se(&br);
							next = (last + delta + 256) % 256;
						}
						last = (next == 0) ? last : next;
					}
				}
			}
		}
	}

	br_read_ue(&br); // log2_max_frame_num_minus4
	unsigned poc_type = br_read_ue(&br);
	if (poc_type == 0) {
		br_read_ue(&br); // log2_max_pic_order_cnt_lsb_minus4
	} else if (poc_type == 1) {
		br_read_bits(&br, 1); // delta_pic_order_always_zero_flag
		br_read_se(&br);      // offset_for_non_ref_pic
		br_read_se(&br);      // offset_for_top_to_bottom_field
		unsigned n = br_read_ue(&br); // num_ref_frames_in_pic_order_cnt_cycle
		for (unsigned i = 0; i < n && i < 256; i++)
			br_read_se(&br);  // offset_for_ref_frame
	}

	br_read_ue(&br);       // max_num_ref_frames
	br_read_bits(&br, 1);  // gaps_in_frame_num_value_allowed_flag

	unsigned pic_width_in_mbs_minus1        = br_read_ue(&br);
	unsigned pic_height_in_map_units_minus1 = br_read_ue(&br);
	int frame_mbs_only_flag                 = br_read_bits(&br, 1);

	int w = (int)(pic_width_in_mbs_minus1 + 1) * 16;
	int h = (2 - frame_mbs_only_flag) * (int)(pic_height_in_map_units_minus1 + 1) * 16;

	if (!frame_mbs_only_flag) {
		br_read_bits(&br, 1); // mb_adaptive_frame_field_flag
	}
	br_read_bits(&br, 1); // direct_8x8_inference_flag

	// Frame cropping — adjusts final dimensions.
	int crop_flag = br_read_bits(&br, 1);
	if (crop_flag) {
		int sub_width_c  = (chroma_format_idc == 1 || chroma_format_idc == 2) ? 2 : 1;
		int sub_height_c = (chroma_format_idc == 1) ? 2 : 1;
		int crop_unit_x  = (chroma_format_idc == 0) ? 1 : sub_width_c;
		int crop_unit_y  = (chroma_format_idc == 0) ? (2 - frame_mbs_only_flag) :
		                   sub_height_c * (2 - frame_mbs_only_flag);
		unsigned crop_left   = br_read_ue(&br);
		unsigned crop_right  = br_read_ue(&br);
		unsigned crop_top    = br_read_ue(&br);
		unsigned crop_bottom = br_read_ue(&br);
		w -= (int)(crop_left + crop_right) * crop_unit_x;
		h -= (int)(crop_top + crop_bottom) * crop_unit_y;
	}

	free(rbsp);

	// Sanity check: smallest plausible RTMP resolution is ~128x96.
	// If the parser ran off the end of a truncated SPS, it reads zeros
	// and produces implausibly small dimensions — reject those.
	if (w >= 128 && h >= 96 && w <= 7680 && h <= 4320) {
		*out_width  = w;
		*out_height = h;
	}
}

// rtmp_flv_open opens an RTMP connection and sets up FLV muxing with
// pre-configured H.264 video and AAC audio streams.
// SPS/PPS are used to build the AVCDecoderConfigurationRecord extradata.
// Width/height are parsed from the SPS for correct FLV metadata.
// Returns 0 on success, negative on error.
static int rtmp_flv_open(rtmp_flv_ctx_t* ctx, const char* url,
	const unsigned char* sps, int sps_len,
	const unsigned char* pps, int pps_len,
	int sample_rate, int channels) {

	int rc = avformat_alloc_output_context2(&ctx->out_ctx, NULL, "flv", url);
	if (rc < 0 || !ctx->out_ctx) return -1;

	av_opt_set(ctx->out_ctx->priv_data, "flvflags", "no_duration_filesize", 0);

	// Parse resolution from SPS.
	int vid_width, vid_height;
	sps_get_dimensions(sps, sps_len, &vid_width, &vid_height);

	// --- Video stream ---
	AVStream* vst = avformat_new_stream(ctx->out_ctx, NULL);
	if (!vst) {
		avformat_free_context(ctx->out_ctx);
		ctx->out_ctx = NULL;
		return -2;
	}
	ctx->video_idx = vst->index;
	vst->codecpar->codec_type = AVMEDIA_TYPE_VIDEO;
	vst->codecpar->codec_id = AV_CODEC_ID_H264;
	vst->codecpar->width = vid_width;
	vst->codecpar->height = vid_height;
	vst->time_base = (AVRational){1, 90000};

	// Build AVCDecoderConfigurationRecord:
	// version(1) + profile(1) + compat(1) + level(1) + 0xFF(1) + 0xE1(1) +
	// sps_len(2) + sps_data + numPPS(1) + pps_len(2) + pps_data
	int extradata_size = 6 + 2 + sps_len + 1 + 2 + pps_len;
	uint8_t* extradata = (uint8_t*)av_malloc(extradata_size + AV_INPUT_BUFFER_PADDING_SIZE);
	if (!extradata) {
		avformat_free_context(ctx->out_ctx);
		ctx->out_ctx = NULL;
		return -3;
	}
	memset(extradata, 0, extradata_size + AV_INPUT_BUFFER_PADDING_SIZE);
	int pos = 0;
	extradata[pos++] = 1;               // version
	extradata[pos++] = sps_len > 1 ? sps[1] : 66;  // profile
	extradata[pos++] = sps_len > 2 ? sps[2] : 192;  // compat
	extradata[pos++] = sps_len > 3 ? sps[3] : 30;   // level
	extradata[pos++] = 0xFF;            // lengthSizeMinusOne = 3
	extradata[pos++] = 0xE1;            // numSPS = 1
	extradata[pos++] = (sps_len >> 8) & 0xFF;  // SPS length (big-endian)
	extradata[pos++] = sps_len & 0xFF;
	memcpy(extradata + pos, sps, sps_len);
	pos += sps_len;
	extradata[pos++] = 1;               // numPPS
	extradata[pos++] = (pps_len >> 8) & 0xFF;  // PPS length (big-endian)
	extradata[pos++] = pps_len & 0xFF;
	memcpy(extradata + pos, pps, pps_len);

	vst->codecpar->extradata = extradata;
	vst->codecpar->extradata_size = extradata_size;

	// --- Audio stream ---
	AVStream* ast = avformat_new_stream(ctx->out_ctx, NULL);
	if (!ast) {
		avformat_free_context(ctx->out_ctx);
		ctx->out_ctx = NULL;
		return -4;
	}
	ctx->audio_idx = ast->index;
	ast->codecpar->codec_type = AVMEDIA_TYPE_AUDIO;
	ast->codecpar->codec_id = AV_CODEC_ID_AAC;
	ast->codecpar->sample_rate = sample_rate;
	av_channel_layout_default(&ast->codecpar->ch_layout, channels);
	ast->codecpar->profile = AV_PROFILE_AAC_LOW;
	ast->time_base = (AVRational){1, 90000};

	// Build 2-byte AudioSpecificConfig:
	// AOT(5 bits) | srIdx(4 bits) | chanCfg(4 bits) | padding(3 bits)
	int sr_idx;
	switch (sample_rate) {
		case 96000: sr_idx = 0; break;
		case 88200: sr_idx = 1; break;
		case 64000: sr_idx = 2; break;
		case 48000: sr_idx = 3; break;
		case 44100: sr_idx = 4; break;
		case 32000: sr_idx = 5; break;
		case 24000: sr_idx = 6; break;
		case 22050: sr_idx = 7; break;
		case 16000: sr_idx = 8; break;
		case 12000: sr_idx = 9; break;
		case 11025: sr_idx = 10; break;
		case 8000:  sr_idx = 11; break;
		case 7350:  sr_idx = 12; break;
		default:    sr_idx = 3; break; // default to 48kHz
	}
	int aot = 2; // AAC-LC
	int chan_cfg = channels;
	uint8_t* audio_extra = (uint8_t*)av_malloc(2 + AV_INPUT_BUFFER_PADDING_SIZE);
	if (!audio_extra) {
		avformat_free_context(ctx->out_ctx);
		ctx->out_ctx = NULL;
		return -5;
	}
	memset(audio_extra, 0, 2 + AV_INPUT_BUFFER_PADDING_SIZE);
	audio_extra[0] = (uint8_t)((aot << 3) | (sr_idx >> 1));
	audio_extra[1] = (uint8_t)(((sr_idx & 1) << 7) | (chan_cfg << 3));

	ast->codecpar->extradata = audio_extra;
	ast->codecpar->extradata_size = 2;

	// Open RTMP connection.
	rc = avio_open(&ctx->out_ctx->pb, url, AVIO_FLAG_WRITE);
	if (rc < 0) {
		avformat_free_context(ctx->out_ctx);
		ctx->out_ctx = NULL;
		return -6;
	}

	// Write FLV header.
	rc = avformat_write_header(ctx->out_ctx, NULL);
	if (rc < 0) {
		avio_closep(&ctx->out_ctx->pb);
		avformat_free_context(ctx->out_ctx);
		ctx->out_ctx = NULL;
		return -7;
	}

	ctx->header_written = 1;
	return 0;
}

// rtmp_flv_write_video writes a video packet to the FLV/RTMP output.
// data is AVC1-format (length-prefixed NALUs).
// Returns 0 on success, negative on error.
static int rtmp_flv_write_video(rtmp_flv_ctx_t* ctx,
	const unsigned char* data, int size,
	int64_t pts_90k, int is_keyframe) {

	AVPacket* pkt = av_packet_alloc();
	if (!pkt) return -1;

	int rc = av_new_packet(pkt, size);
	if (rc < 0) {
		av_packet_free(&pkt);
		return -2;
	}
	memcpy(pkt->data, data, size);

	pkt->pts = pts_90k;
	pkt->dts = pts_90k;
	pkt->stream_index = ctx->video_idx;
	if (is_keyframe) {
		pkt->flags |= AV_PKT_FLAG_KEY;
	}

	rc = av_interleaved_write_frame(ctx->out_ctx, pkt);
	av_packet_free(&pkt);
	return rc < 0 ? -3 : 0;
}

// rtmp_flv_write_audio writes an audio packet to the FLV/RTMP output.
// data should be raw AAC (ADTS header stripped).
// Returns 0 on success, negative on error.
static int rtmp_flv_write_audio(rtmp_flv_ctx_t* ctx,
	const unsigned char* data, int size,
	int64_t pts_90k) {

	AVPacket* pkt = av_packet_alloc();
	if (!pkt) return -1;

	int rc = av_new_packet(pkt, size);
	if (rc < 0) {
		av_packet_free(&pkt);
		return -2;
	}
	memcpy(pkt->data, data, size);

	pkt->pts = pts_90k;
	pkt->dts = pts_90k;
	pkt->stream_index = ctx->audio_idx;

	rc = av_interleaved_write_frame(ctx->out_ctx, pkt);
	av_packet_free(&pkt);
	return rc < 0 ? -3 : 0;
}

// rtmp_flv_close writes the FLV trailer and closes the RTMP connection.
static void rtmp_flv_close(rtmp_flv_ctx_t* ctx) {
	if (ctx->out_ctx) {
		if (ctx->header_written && ctx->out_ctx->pb) {
			av_write_trailer(ctx->out_ctx);
		}
		if (ctx->out_ctx->pb) {
			avio_closep(&ctx->out_ctx->pb);
		}
		avformat_free_context(ctx->out_ctx);
		ctx->out_ctx = NULL;
	}
	ctx->header_written = 0;
}
*/
import "C"

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal/backoff"
)

const (
	rtmpVideoChSize = 100
	rtmpAudioChSize = 100
)

// RTMPDirect pushes program output to an RTMP ingest server using direct
// FLV muxing. It receives raw H.264 and AAC frames and writes them directly
// to the FLV container via libavformat, avoiding any intermediate TS
// demux/remux step.
//
// Implements the FrameAdapter interface.
type RTMPDirect struct {
	config RTMPCallerConfig

	mu     sync.Mutex
	ctx    C.rtmp_flv_ctx_t
	goCtx  context.Context
	cancel context.CancelFunc
	stopCh chan struct{}
	doneCh chan struct{}

	// reconnectDone is closed by reconnectLoop on exit so Close can join
	// the goroutine; nil when no loop is running. Guarded by mu.
	reconnectDone chan struct{}

	videoCh chan *media.VideoFrame
	audioCh chan *media.AudioFrame

	initialized atomic.Bool
	ptsBase     int64
	lastSPS     []byte // cached for reconnect (written only from writeLoop)
	lastPPS     []byte // cached for reconnect (written only from writeLoop)

	state        atomic.Pointer[AdapterState]
	lastError    atomic.Pointer[string]
	reconnecting atomic.Bool
	pendingIDR   atomic.Bool
	bytesWritten atomic.Int64
	videoDropped atomic.Int64
	audioDropped atomic.Int64
	startedAt    time.Time
	bo           backoff.Backoff

	// connectFn can be overridden in tests to avoid real RTMP connections.
	connectFn func(url string) error

	// onReconnect is called after a successful reconnection.
	onReconnect func()

	// transcoder decodes HEVC→YUV→H.264 when the pipeline codec is HEVC.
	// Created lazily on the first HEVC frame; nil when pipeline is H.264.
	transcoder *rtmpTranscoder

	// transcoderFactory creates a transcoder for a given resolution.
	// Overridden in tests to inject mock decoders/encoders.
	transcoderFactory func(width, height int) (*rtmpTranscoder, error)
}

// NewRTMPDirect creates an RTMP direct adapter.
func NewRTMPDirect(config RTMPCallerConfig) (*RTMPDirect, error) {
	if config.URL == "" {
		return nil, fmt.Errorf("RTMP URL is required")
	}
	if config.SampleRate <= 0 {
		config.SampleRate = 48000
	}
	if config.Channels <= 0 {
		config.Channels = 2
	}

	r := &RTMPDirect{
		config: config,
		bo:     backoff.Backoff{JitterPct: 0.20},
	}
	r.state.Store(ptrTo(StateStopped))
	r.lastError.Store(ptrTo(""))
	return r, nil
}

// ID returns the adapter identifier.
func (r *RTMPDirect) ID() string { return "rtmp-direct" }

// Start initializes channels and spawns the write loop. The actual RTMP
// connection is deferred until the first keyframe arrives.
func (r *RTMPDirect) Start(ctx context.Context) error {
	r.videoCh = make(chan *media.VideoFrame, rtmpVideoChSize)
	r.audioCh = make(chan *media.AudioFrame, rtmpAudioChSize)
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})
	r.goCtx, r.cancel = context.WithCancel(ctx)
	r.startedAt = time.Now()
	r.bytesWritten.Store(0)
	r.state.Store(ptrTo(StateActive))
	r.lastError.Store(ptrTo(""))

	go r.writeLoop()
	return nil
}

// Close stops the write loop and releases FFmpeg resources.
func (r *RTMPDirect) Close() error {
	if r.cancel != nil {
		r.cancel()
	}

	if r.stopCh != nil {
		select {
		case <-r.stopCh:
		default:
			close(r.stopCh)
		}
	}

	if r.doneCh != nil {
		<-r.doneCh
	}

	// Join the reconnect goroutine so it cannot reopen the RTMP connection
	// (leaking the FFmpeg context) or resurrect StateActive after Close.
	// The writeLoop has exited, so no new reconnect loop can be spawned
	// once this snapshot is taken.
	r.mu.Lock()
	reconnectDone := r.reconnectDone
	r.mu.Unlock()
	if reconnectDone != nil {
		<-reconnectDone
	}

	r.mu.Lock()
	r.closeLocked()
	r.mu.Unlock()

	// Close transcoder if active.
	if r.transcoder != nil {
		r.transcoder.Close()
		r.transcoder = nil
	}

	r.state.Store(ptrTo(StateStopped))
	return nil
}

// WriteVideo enqueues a video frame for writing. Non-blocking: drops the
// frame if the channel is full.
func (r *RTMPDirect) WriteVideo(frame *media.VideoFrame) error {
	state := *r.state.Load()
	if state == StateStopped || state == StateError {
		return fmt.Errorf("RTMP direct not active (state: %s)", state)
	}

	// Silently drop during reconnect or starting.
	select {
	case r.videoCh <- frame:
	default:
		r.videoDropped.Add(1)
	}
	return nil
}

// WriteAudio enqueues an audio frame for writing. Non-blocking: drops the
// frame if the channel is full.
func (r *RTMPDirect) WriteAudio(frame *media.AudioFrame) error {
	state := *r.state.Load()
	if state == StateStopped || state == StateError {
		return fmt.Errorf("RTMP direct not active (state: %s)", state)
	}

	select {
	case r.audioCh <- frame:
	default:
		r.audioDropped.Add(1)
	}
	return nil
}

// Status returns the current adapter status.
func (r *RTMPDirect) Status() AdapterStatus {
	state := *r.state.Load()
	errStr := *r.lastError.Load()
	return AdapterStatus{
		State:        state,
		BytesWritten: r.bytesWritten.Load(),
		StartedAt:    r.startedAt,
		Error:        errStr,
	}
}

// RTMPStatusSnapshot returns an RTMPStatus for ControlRoomState.
func (r *RTMPDirect) RTMPStatusSnapshot() RTMPStatus {
	status := r.Status()
	return RTMPStatus{
		Active:       status.State == StateActive || status.State == StateReconnecting,
		URL:          rtmpMaskURL(r.config.URL),
		State:        string(status.State),
		BytesWritten: status.BytesWritten,
		Error:        status.Error,
	}
}

// writeLoop drains the video and audio channels and writes frames to RTMP.
// Video is given priority to prevent audio from starving keyframe delivery.
func (r *RTMPDirect) writeLoop() {
	defer close(r.doneCh)

	for {
		// Priority: always drain video first.
		select {
		case frame := <-r.videoCh:
			r.handleVideo(frame)
			continue
		default:
		}

		select {
		case frame := <-r.videoCh:
			r.handleVideo(frame)
		case frame := <-r.audioCh:
			r.handleAudio(frame)
		case <-r.stopCh:
			return
		case <-r.goCtx.Done():
			return
		}
	}
}

// handleVideo processes a single video frame.
func (r *RTMPDirect) handleVideo(frame *media.VideoFrame) {
	state := *r.state.Load()
	if state != StateActive {
		return
	}

	// HEVC→H.264 transcoding: standard RTMP only supports H.264.
	// When HEVC frames arrive, transcode them to H.264 before writing.
	if frame.Codec == "h265" {
		frame = r.transcodeHEVC(frame)
		if frame == nil {
			return // transcode failed or buffering
		}
	}

	if !r.initialized.Load() {
		if !frame.IsKeyframe {
			return // drop non-keyframes before init
		}

		// Test mode: just mark initialized without calling C.
		if r.connectFn != nil {
			r.initialized.Store(true)
			r.ptsBase = frame.PTS

			return
		}

		if err := r.initConnection(frame); err != nil {
			slog.Warn("RTMP direct init failed, entering reconnect",
				"url", rtmpMaskURL(r.config.URL), "err", err)
			r.enterReconnect(err.Error())
			return
		}
	}

	// Cache SPS/PPS from keyframes for eager reconnect.
	if frame.IsKeyframe && len(frame.SPS) > 0 && len(frame.PPS) > 0 {
		r.lastSPS = append(r.lastSPS[:0], frame.SPS...)
		r.lastPPS = append(r.lastPPS[:0], frame.PPS...)
	}

	// After reconnect, drop P-frames until the first keyframe. The new
	// RTMP connection has a fresh sequence header but the decoder needs
	// an IDR to start. Also rebase PTS for the new connection.
	if r.pendingIDR.Load() {
		if !frame.IsKeyframe {
			return
		}
		r.pendingIDR.Store(false)
		r.ptsBase = frame.PTS // rebase for new connection
	}

	// In test mode, skip the C write call.
	if r.connectFn != nil {
		r.bytesWritten.Add(int64(len(frame.WireData)))
		return
	}

	if len(frame.WireData) == 0 {
		return
	}

	pts := frame.PTS - r.ptsBase
	if pts < 0 {
		pts = 0
	}

	isKey := C.int(0)
	if frame.IsKeyframe {
		isKey = 1
	}

	r.mu.Lock()
	rc := C.rtmp_flv_write_video(&r.ctx,
		(*C.uchar)(unsafe.Pointer(&frame.WireData[0])),
		C.int(len(frame.WireData)),
		C.int64_t(pts),
		isKey)
	r.mu.Unlock()

	if rc < 0 {
		slog.Warn("RTMP direct video write error, entering reconnect",
			"url", rtmpMaskURL(r.config.URL), "rc", int(rc))
		r.enterReconnect("RTMP video write failed")
		return
	}

	r.bytesWritten.Add(int64(len(frame.WireData)))
}

// handleAudio processes a single audio frame.
func (r *RTMPDirect) handleAudio(frame *media.AudioFrame) {
	state := *r.state.Load()
	if state != StateActive || !r.initialized.Load() {
		return
	}

	data := stripADTS(frame.Data)
	if len(data) == 0 {
		return
	}

	// In test mode, skip the C write call.
	if r.connectFn != nil {
		r.bytesWritten.Add(int64(len(data)))
		return
	}

	pts := frame.PTS - r.ptsBase
	if pts < 0 {
		pts = 0
	}

	r.mu.Lock()
	rc := C.rtmp_flv_write_audio(&r.ctx,
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(len(data)),
		C.int64_t(pts))
	r.mu.Unlock()

	if rc < 0 {
		slog.Warn("RTMP direct audio write error, entering reconnect",
			"url", rtmpMaskURL(r.config.URL), "rc", int(rc))
		r.enterReconnect("RTMP audio write failed")
		return
	}

	r.bytesWritten.Add(int64(len(data)))
}

// stripADTS removes an ADTS header if present, returning raw AAC data.
func stripADTS(data []byte) []byte {
	if codec.IsADTS(data) {
		hdrLen := codec.ADTSHeaderLen(data)
		if hdrLen > 0 && hdrLen < len(data) {
			return data[hdrLen:]
		}
	}
	return data
}

// initConnection opens the RTMP connection using the SPS/PPS from the
// first keyframe to configure the FLV muxer.
func (r *RTMPDirect) initConnection(frame *media.VideoFrame) error {
	if len(frame.SPS) == 0 || len(frame.PPS) == 0 {
		return fmt.Errorf("keyframe missing SPS/PPS")
	}

	cURL := C.CString(r.config.URL)
	defer C.free(unsafe.Pointer(cURL))

	r.mu.Lock()
	defer r.mu.Unlock()

	rc := C.rtmp_flv_open(&r.ctx, cURL,
		(*C.uchar)(unsafe.Pointer(&frame.SPS[0])), C.int(len(frame.SPS)),
		(*C.uchar)(unsafe.Pointer(&frame.PPS[0])), C.int(len(frame.PPS)),
		C.int(r.config.SampleRate),
		C.int(r.config.Channels))
	if rc < 0 {
		return fmt.Errorf("rtmp_flv_open failed (code %d)", int(rc))
	}

	r.ptsBase = frame.PTS
	r.initialized.Store(true)
	return nil
}

// enterReconnect transitions to reconnect state and spawns the reconnect loop.
func (r *RTMPDirect) enterReconnect(errMsg string) {
	r.state.Store(ptrTo(StateReconnecting))
	r.lastError.Store(ptrTo(errMsg))
	// CAS guard prevents duplicate reconnect goroutines. Create the done
	// channel under r.mu so Close can observe it atomically.
	r.mu.Lock()
	if r.reconnecting.CompareAndSwap(false, true) {
		r.reconnectDone = make(chan struct{})
		go r.reconnectLoop()
	}
	r.mu.Unlock()
}

// reconnectLoop retries the RTMP connection with exponential backoff.
// If cached SPS/PPS are available (from a prior keyframe), it eagerly
// re-opens the RTMP connection. Otherwise it resets state and waits for
// the next keyframe to trigger initConnection from the writeLoop.
func (r *RTMPDirect) reconnectLoop() {
	// Capture (and lazily create, for direct test invocation) the done
	// channel so Close can join this goroutine. Closing it on exit lets a
	// concurrent Close know the loop has finished and will not reopen the
	// RTMP connection.
	r.mu.Lock()
	if r.reconnectDone == nil {
		r.reconnectDone = make(chan struct{})
	}
	done := r.reconnectDone
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.reconnectDone == done {
			r.reconnectDone = nil
		}
		r.mu.Unlock()
		close(done)
		r.reconnecting.Store(false)
		// Re-arm check: this loop stores StateActive and calls onReconnect
		// BEFORE the Store(false) above. A write failure inside that window
		// calls enterReconnect, which stores StateReconnecting but loses
		// the CompareAndSwap against the still-set flag, spawning nothing.
		// Without this re-check the adapter would be stuck dropping all
		// frames in StateReconnecting with no reconnect goroutine.
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.goCtx != nil && r.goCtx.Err() == nil &&
			*r.state.Load() == StateReconnecting &&
			r.reconnecting.CompareAndSwap(false, true) {
			r.reconnectDone = make(chan struct{})
			go r.reconnectLoop()
		}
	}()

	for {
		select {
		case <-r.goCtx.Done():
			return
		default:
		}

		wait := r.bo.Next()
		slog.Info("RTMP direct reconnecting", "backoff", wait, "url", rtmpMaskURL(r.config.URL))

		select {
		case <-r.goCtx.Done():
			return
		case <-time.After(wait):
		}

		r.mu.Lock()
		// Re-check after the backoff timer: Close may have run while this
		// loop was sleeping (or won the select race above). Reopening the
		// connection now would leak the FFmpeg context and resurrect
		// StateActive after Close.
		if r.goCtx.Err() != nil {
			r.mu.Unlock()
			return
		}
		r.closeLocked()

		// Try eager reconnect if we have cached SPS/PPS.
		if len(r.lastSPS) > 0 && len(r.lastPPS) > 0 {
			cURL := C.CString(r.config.URL)
			rc := C.rtmp_flv_open(&r.ctx, cURL,
				(*C.uchar)(unsafe.Pointer(&r.lastSPS[0])), C.int(len(r.lastSPS)),
				(*C.uchar)(unsafe.Pointer(&r.lastPPS[0])), C.int(len(r.lastPPS)),
				C.int(r.config.SampleRate),
				C.int(r.config.Channels))
			C.free(unsafe.Pointer(cURL))

			if rc == 0 {
				r.mu.Unlock()
				// Eagerly reconnected — need a keyframe to resume cleanly.
				r.initialized.Store(true)
				r.pendingIDR.Store(true)
				r.state.Store(ptrTo(StateActive))
				r.lastError.Store(ptrTo(""))
				r.bo.Reset()

				slog.Info("RTMP direct reconnected", "url", rtmpMaskURL(r.config.URL))
				if r.onReconnect != nil {
					r.onReconnect()
				}
				return
			}
			// Eager reconnect failed — retry with backoff.
			slog.Warn("RTMP direct eager reconnect failed", "rc", int(rc),
				"url", rtmpMaskURL(r.config.URL))
			r.mu.Unlock()
			r.lastError.Store(ptrTo("RTMP reconnect failed"))
			continue
		}

		r.mu.Unlock()

		// No cached SPS/PPS — reset and wait for next keyframe.
		r.initialized.Store(false)
		r.pendingIDR.Store(true)
		r.state.Store(ptrTo(StateActive))
		r.lastError.Store(ptrTo(""))
		r.bo.Reset()

		slog.Info("RTMP direct ready for reconnect (waiting for keyframe)",
			"url", rtmpMaskURL(r.config.URL))
		if r.onReconnect != nil {
			r.onReconnect()
		}
		return
	}
}

// closeLocked tears down the FFmpeg FLV context. Must be called with r.mu held.
func (r *RTMPDirect) closeLocked() {
	C.rtmp_flv_close(&r.ctx)
}

// parseSPSDimensions extracts width and height from an H.264 SPS NALU.
// Exported for testing. Returns (1920, 1080) as fallback on parse failure.
func parseSPSDimensions(sps []byte) (width, height int) {
	if len(sps) == 0 {
		return 1920, 1080
	}
	var w, h C.int
	C.sps_get_dimensions((*C.uint8_t)(unsafe.Pointer(&sps[0])), C.int(len(sps)), &w, &h)
	return int(w), int(h)
}

// transcodeHEVC lazily creates a transcoder and converts an HEVC frame to H.264.
// Returns the transcoded H.264 frame, or nil if transcoding fails or the
// decoder is still buffering.
func (r *RTMPDirect) transcodeHEVC(frame *media.VideoFrame) *media.VideoFrame {
	// Lazy init: create transcoder on first HEVC frame.
	if r.transcoder == nil {
		if !frame.IsKeyframe {
			// Can't create transcoder without a keyframe (need SPS/PPS for decoder init).
			return nil
		}

		var err error
		if r.transcoderFactory != nil {
			// Use injected factory (testing).
			r.transcoder, err = r.transcoderFactory(1920, 1080)
		} else {
			r.transcoder, err = r.createDefaultTranscoder()
		}
		if err != nil {
			slog.Warn("RTMP direct: failed to create HEVC→H.264 transcoder",
				"url", rtmpMaskURL(r.config.URL), "err", err)
			return nil
		}
		slog.Info("RTMP direct: created HEVC→H.264 transcoder",
			"url", rtmpMaskURL(r.config.URL))
	}

	h264Frame, err := r.transcoder.Transcode(frame)
	if err != nil {
		slog.Debug("RTMP direct: transcode failed, dropping frame",
			"url", rtmpMaskURL(r.config.URL), "err", err)
		return nil
	}
	return h264Frame
}

// createDefaultTranscoder creates a real HEVC decoder + H.264 encoder pair
// using the codec package. Called when no test factory is injected.
func (r *RTMPDirect) createDefaultTranscoder() (*rtmpTranscoder, error) {
	return nil, fmt.Errorf("HEVC→H.264 transcoder requires codec factory (not yet wired)")
}

// Compile-time check that RTMPDirect satisfies the FrameAdapter interface.
var _ FrameAdapter = (*RTMPDirect)(nil)
