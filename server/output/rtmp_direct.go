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

typedef struct {
	AVFormatContext* out_ctx;
	int video_idx;
	int audio_idx;
	int header_written;
} rtmp_flv_ctx_t;

// rtmp_flv_open opens an RTMP connection and sets up FLV muxing with
// pre-configured H.264 video and AAC audio streams.
// SPS/PPS are used to build the AVCDecoderConfigurationRecord extradata.
// Returns 0 on success, negative on error.
static int rtmp_flv_open(rtmp_flv_ctx_t* ctx, const char* url,
	const unsigned char* sps, int sps_len,
	const unsigned char* pps, int pps_len,
	int sample_rate, int channels) {

	int rc = avformat_alloc_output_context2(&ctx->out_ctx, NULL, "flv", url);
	if (rc < 0 || !ctx->out_ctx) return -1;

	av_opt_set(ctx->out_ctx->priv_data, "flvflags", "no_duration_filesize", 0);

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
	vst->codecpar->width = 1920;
	vst->codecpar->height = 1080;
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
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

const (
	rtmpVideoChSize = 100
	rtmpAudioChSize = 100
)

// RTMPDirect pushes program output to an RTMP ingest server using direct
// FLV muxing. Unlike RTMPCaller (which demuxes TS then remuxes to FLV),
// RTMPDirect receives raw H.264 and AAC frames and writes them directly
// to the FLV container, eliminating the TS demuxer ring buffer that caused
// permanent audio corruption on ring underrun.
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

	videoCh chan *media.VideoFrame
	audioCh chan *media.AudioFrame

	initialized atomic.Bool
	ptsBase     int64
	ptsBaseSet  bool

	state        atomic.Pointer[AdapterState]
	lastError    atomic.Pointer[string]
	reconnecting atomic.Bool
	pendingIDR   atomic.Bool
	bytesWritten atomic.Int64
	videoDropped atomic.Int64
	audioDropped atomic.Int64
	startedAt    time.Time
	backoff      time.Duration

	// connectFn can be overridden in tests to avoid real RTMP connections.
	connectFn func(url string) error

	// onReconnect is called after a successful reconnection.
	onReconnect func()
}

// NewRTMPDirect creates an RTMP direct adapter.
func NewRTMPDirect(config RTMPCallerConfig) (*RTMPDirect, error) {
	if config.URL == "" {
		return nil, fmt.Errorf("RTMP URL is required")
	}

	r := &RTMPDirect{
		config:  config,
		backoff: rtmpInitialBackoff,
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

	r.mu.Lock()
	r.closeLocked()
	r.mu.Unlock()

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

	if !r.initialized.Load() {
		if !frame.IsKeyframe {
			return // drop non-keyframes before init
		}

		// Test mode: just mark initialized without calling C.
		if r.connectFn != nil {
			r.initialized.Store(true)
			r.ptsBase = frame.PTS
			r.ptsBaseSet = true
			return
		}

		if err := r.initConnection(frame); err != nil {
			slog.Warn("RTMP direct init failed, entering reconnect",
				"url", rtmpMaskURL(r.config.URL), "err", err)
			r.enterReconnect(err.Error())
			return
		}
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
		C.int(48000), // default sample rate
		C.int(2))     // default stereo
	if rc < 0 {
		return fmt.Errorf("rtmp_flv_open failed (code %d)", int(rc))
	}

	r.ptsBase = frame.PTS
	r.ptsBaseSet = true
	r.initialized.Store(true)
	return nil
}

// enterReconnect transitions to reconnect state and spawns the reconnect loop.
func (r *RTMPDirect) enterReconnect(errMsg string) {
	r.state.Store(ptrTo(StateReconnecting))
	r.lastError.Store(ptrTo(errMsg))
	if r.reconnecting.CompareAndSwap(false, true) {
		go r.reconnectLoop()
	}
}

// reconnectLoop retries the RTMP connection with exponential backoff.
func (r *RTMPDirect) reconnectLoop() {
	defer r.reconnecting.Store(false)

	for {
		select {
		case <-r.goCtx.Done():
			return
		default:
		}

		backoff := r.nextBackoff()
		slog.Info("RTMP direct reconnecting", "backoff", backoff, "url", rtmpMaskURL(r.config.URL))

		select {
		case <-r.goCtx.Done():
			return
		case <-time.After(backoff):
		}

		r.mu.Lock()
		r.closeLocked()
		r.mu.Unlock()

		// Reset state for new connection — will be initialized on next keyframe.
		r.initialized.Store(false)
		r.ptsBaseSet = false
		r.pendingIDR.Store(true)
		r.state.Store(ptrTo(StateActive))
		r.lastError.Store(ptrTo(""))
		r.resetBackoff()

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

// nextBackoff returns the current backoff duration and advances to the next level.
func (r *RTMPDirect) nextBackoff() time.Duration {
	r.mu.Lock()
	current := r.backoff
	next := current * 2
	if next > rtmpMaxBackoff {
		next = rtmpMaxBackoff
	}
	r.backoff = next
	r.mu.Unlock()
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(current) * jitter)
}

// resetBackoff resets the backoff duration to the initial value.
func (r *RTMPDirect) resetBackoff() {
	r.mu.Lock()
	r.backoff = rtmpInitialBackoff
	r.mu.Unlock()
}

// Compile-time check that RTMPDirect satisfies the FrameAdapter interface.
var _ FrameAdapter = (*RTMPDirect)(nil)
