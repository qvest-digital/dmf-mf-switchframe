//go:build cgo && !noffmpeg

package output

/*
#cgo pkg-config: libavformat libavcodec libavutil
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/opt.h>
#include <libavutil/error.h>
#include <libavutil/log.h>
#include <stdlib.h>
#include <string.h>

// rtmp_ctx_t wraps the input (TS demux) and output (FLV/RTMP mux) contexts.
typedef struct {
	AVFormatContext* in_ctx;
	AVIOContext*     in_avio;
	unsigned char*   in_buf;

	AVFormatContext* out_ctx;

	int video_in_idx;
	int audio_in_idx;
	int video_out_idx;
	int audio_out_idx;

	int header_written;
	int initialized;
} rtmp_ctx_t;

// Ring buffer for feeding TS data from Go Write() to the avformat read callback.
// Sized to hold several seconds of muxed TS.
#define RTMP_RING_SIZE (2 * 1024 * 1024)
#define RTMP_AVIO_BUF  (32 * 1024)

typedef struct {
	unsigned char data[RTMP_RING_SIZE];
	int           write_pos;
	int           read_pos;
	int           count;
	int           eof;
} rtmp_ring_t;

static int rtmp_ring_write(rtmp_ring_t* r, const unsigned char* buf, int len) {
	int avail = RTMP_RING_SIZE - r->count;
	if (len > avail) {
		// Reject the entire write rather than accepting a partial chunk.
		// Partial writes would split 188-byte TS packets and corrupt the
		// demuxer's state. Callers treat a 0-byte return as ring-full overflow.
		return 0;
	}
	for (int i = 0; i < len; i++) {
		r->data[r->write_pos] = buf[i];
		r->write_pos = (r->write_pos + 1) % RTMP_RING_SIZE;
	}
	r->count += len;
	return len;
}

static int rtmp_ring_read(rtmp_ring_t* r, unsigned char* buf, int len) {
	if (len > r->count) {
		len = r->count;
	}
	for (int i = 0; i < len; i++) {
		buf[i] = r->data[r->read_pos];
		r->read_pos = (r->read_pos + 1) % RTMP_RING_SIZE;
	}
	r->count -= len;
	return len;
}

// avio_read_callback is the custom read function for the TS demuxer input.
// opaque points to an rtmp_ring_t.
//
// avformat_find_stream_info and avformat_open_input are synchronous and do
// NOT handle EAGAIN from the read callback. We must either return data or
// EOF. Since the Go side feeds data into the ring under a mutex and the
// drain loop calls avformat functions under the same mutex, we return
// AVERROR_EOF when no data is available -- the caller (drainLoop) ensures
// sufficient data is buffered before invoking avformat_find_stream_info,
// and av_read_frame handles EOF gracefully (returns "try again later"
// equivalent via its own return code).
static int avio_read_callback(void* opaque, uint8_t* buf, int buf_size) {
	rtmp_ring_t* ring = (rtmp_ring_t*)opaque;
	if (ring->count == 0) {
		return AVERROR_EOF;
	}
	int n = rtmp_ring_read(ring, buf, buf_size);
	return n;
}

// rtmp_open_output opens the RTMP output context (FLV muxer to rtmp:// URL).
// Returns 0 on success, negative on error.
static int rtmp_open_output(rtmp_ctx_t* ctx, const char* url) {
	int rc = avformat_alloc_output_context2(&ctx->out_ctx, NULL, "flv", url);
	if (rc < 0 || !ctx->out_ctx) return -1;

	// Disable FLV duration/filesize seek-back (not possible over RTMP).
	av_opt_set(ctx->out_ctx->priv_data, "flvflags", "no_duration_filesize", 0);

	// Open the RTMP connection.
	rc = avio_open(&ctx->out_ctx->pb, url, AVIO_FLAG_WRITE);
	if (rc < 0) {
		avformat_free_context(ctx->out_ctx);
		ctx->out_ctx = NULL;
		return -2;
	}

	return 0;
}

// rtmp_open_input opens the TS demuxer reading from the ring buffer.
// Returns 0 on success, negative on error.
static int rtmp_open_input(rtmp_ctx_t* ctx, rtmp_ring_t* ring) {
	ctx->in_buf = (unsigned char*)av_malloc(RTMP_AVIO_BUF);
	if (!ctx->in_buf) return -1;

	ctx->in_avio = avio_alloc_context(
		ctx->in_buf, RTMP_AVIO_BUF,
		0,          // write_flag = 0 (read)
		ring,       // opaque
		avio_read_callback,
		NULL,       // write
		NULL        // seek
	);
	if (!ctx->in_avio) {
		av_free(ctx->in_buf);
		ctx->in_buf = NULL;
		return -2;
	}

	ctx->in_ctx = avformat_alloc_context();
	if (!ctx->in_ctx) {
		avio_context_free(&ctx->in_avio);
		ctx->in_buf = NULL;
		return -3;
	}
	ctx->in_ctx->pb = ctx->in_avio;

	const AVInputFormat* fmt = av_find_input_format("mpegts");
	AVDictionary* opts = NULL;
	// Minimal probing: we know the format is MPEG-TS with H.264+AAC.
	// Keep probesize small to avoid needing excessive buffering, but large
	// enough for at least one GOP's worth of data to detect streams reliably.
	av_dict_set(&opts, "probesize", "131072", 0);
	av_dict_set(&opts, "analyzeduration", "1000000", 0);

	int rc = avformat_open_input(&ctx->in_ctx, NULL, fmt, &opts);
	av_dict_free(&opts);
	if (rc < 0) {
		ctx->in_ctx = NULL;
		avio_context_free(&ctx->in_avio);
		ctx->in_buf = NULL;
		return -4;
	}

	return 0;
}

// rtmp_find_streams probes the TS input for streams and creates
// corresponding output streams in the FLV muxer.
// FLV supports: H.264 (AVC) video, AAC audio.
// Enhanced RTMP adds HEVC, AV1, VP9 — but standard ingest endpoints
// (YouTube, Twitch, Facebook) require H.264 + AAC.
// Returns 0 on success, negative on error.
static int rtmp_find_streams(rtmp_ctx_t* ctx) {
	int rc = avformat_find_stream_info(ctx->in_ctx, NULL);
	if (rc < 0) return -1;

	ctx->video_in_idx = -1;
	ctx->audio_in_idx = -1;

	for (unsigned i = 0; i < ctx->in_ctx->nb_streams; i++) {
		AVStream* in_stream = ctx->in_ctx->streams[i];
		enum AVMediaType type = in_stream->codecpar->codec_type;

		if (type == AVMEDIA_TYPE_VIDEO && ctx->video_in_idx < 0) {
			enum AVCodecID vcodec = in_stream->codecpar->codec_id;
			if (vcodec != AV_CODEC_ID_H264 && vcodec != AV_CODEC_ID_HEVC) {
				continue; // FLV only supports H.264 (and HEVC via Enhanced RTMP)
			}
			AVStream* out_stream = avformat_new_stream(ctx->out_ctx, NULL);
			if (!out_stream) return -2;
			avcodec_parameters_copy(out_stream->codecpar, in_stream->codecpar);
			out_stream->codecpar->codec_tag = 0;
			ctx->video_in_idx = (int)i;
			ctx->video_out_idx = out_stream->index;
		} else if (type == AVMEDIA_TYPE_AUDIO && ctx->audio_in_idx < 0) {
			enum AVCodecID acodec = in_stream->codecpar->codec_id;
			if (acodec != AV_CODEC_ID_AAC && acodec != AV_CODEC_ID_MP3) {
				continue; // FLV supports AAC and MP3; skip unsupported codecs
			}
			AVStream* out_stream = avformat_new_stream(ctx->out_ctx, NULL);
			if (!out_stream) return -3;
			avcodec_parameters_copy(out_stream->codecpar, in_stream->codecpar);
			out_stream->codecpar->codec_tag = 0;
			ctx->audio_in_idx = (int)i;
			ctx->audio_out_idx = out_stream->index;
		}
	}

	if (ctx->video_in_idx < 0) return -4;

	rc = avformat_write_header(ctx->out_ctx, NULL);
	if (rc < 0) return -5;

	ctx->header_written = 1;
	ctx->initialized = 1;
	return 0;
}

// rtmp_read_write_packet reads one packet from the TS demuxer and writes
// it to the RTMP output, performing timestamp rescaling.
// Returns 0 on success, 1 if no more packets (EAGAIN), negative on error.
static int rtmp_read_write_packet(rtmp_ctx_t* ctx) {
	AVPacket* pkt = av_packet_alloc();
	if (!pkt) return -1;

	int rc = av_read_frame(ctx->in_ctx, pkt);
	if (rc < 0) {
		av_packet_free(&pkt);
		if (rc == AVERROR(EAGAIN)) return 1;
		if (rc == AVERROR_EOF) return 1;
		return -1;
	}

	int out_idx = -1;
	if (pkt->stream_index == ctx->video_in_idx) {
		out_idx = ctx->video_out_idx;
	} else if (pkt->stream_index == ctx->audio_in_idx) {
		out_idx = ctx->audio_out_idx;
	}

	if (out_idx < 0) {
		av_packet_free(&pkt);
		return 0;
	}

	AVStream* in_stream = ctx->in_ctx->streams[pkt->stream_index];
	AVStream* out_stream = ctx->out_ctx->streams[out_idx];

	pkt->stream_index = out_idx;
	av_packet_rescale_ts(pkt, in_stream->time_base, out_stream->time_base);
	pkt->pos = -1;

	rc = av_interleaved_write_frame(ctx->out_ctx, pkt);
	av_packet_free(&pkt);
	if (rc < 0) return -2;

	return 0;
}

// rtmp_close_output writes the FLV trailer and closes the RTMP connection.
// Errors from av_write_trailer are ignored since we're tearing down.
static void rtmp_close_output(rtmp_ctx_t* ctx) {
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
	ctx->initialized = 0;
}

// rtmp_close_input closes the TS demuxer input.
// avformat_close_input frees the AVIOContext and its buffer when ownership
// was transferred via avformat_open_input, so we must NOT double-free.
static void rtmp_close_input(rtmp_ctx_t* ctx) {
	if (ctx->in_ctx) {
		// avformat_close_input frees in_ctx, its pb (in_avio), and the avio buffer.
		avformat_close_input(&ctx->in_ctx);
		ctx->in_avio = NULL;
		ctx->in_buf = NULL;
	} else {
		// avformat_open_input was never called or failed — we still own these.
		if (ctx->in_avio) {
			av_freep(&ctx->in_avio->buffer);
			avio_context_free(&ctx->in_avio);
		}
		ctx->in_buf = NULL;
	}
}

// rtmp_ring_available returns the number of bytes available to read.
static int rtmp_ring_available(rtmp_ring_t* r) {
	return r->count;
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
)

const (
	rtmpInitialBackoff = 1 * time.Second
	rtmpMaxBackoff     = 30 * time.Second
	rtmpDrainBatch     = 64
)

// rtmpMaskURL masks the stream key portion of an RTMP URL for safe logging.
// "rtmp://host/app/secret-key" -> "rtmp://host/app/****"
func rtmpMaskURL(rawURL string) string {
	// Find the third slash (after scheme://host/).
	slashCount := 0
	lastSlash := -1
	for i, ch := range rawURL {
		if ch == '/' {
			slashCount++
			if slashCount >= 4 {
				lastSlash = i
				break
			}
		}
	}
	if lastSlash > 0 && lastSlash < len(rawURL)-1 {
		return rawURL[:lastSlash+1] + "****"
	}
	return rawURL
}

// RTMPStatus holds runtime RTMP connection info for ControlRoomState.
type RTMPStatus struct {
	Active       bool   `json:"active"`
	URL          string `json:"url,omitempty"`
	State        string `json:"state,omitempty"`
	BytesWritten int64  `json:"bytesWritten,omitempty"`
	Error        string `json:"error,omitempty"`
}

// RTMPCaller pushes program output to an RTMP ingest server.
// It demuxes the incoming MPEG-TS stream and remuxes into FLV over RTMP
// using libavformat via cgo. Implements the Adapter interface.
type RTMPCaller struct {
	config RTMPCallerConfig

	mu           sync.Mutex
	ring         *C.rtmp_ring_t
	ctx          C.rtmp_ctx_t
	goCtx        context.Context
	cancel       context.CancelFunc
	drainStop    chan struct{}
	drainDone    chan struct{}
	reconnecting atomic.Bool
	pendingIDR   atomic.Bool
	bytesWritten atomic.Int64
	state        atomic.Pointer[AdapterState]
	lastError    atomic.Pointer[string]
	startedAt    time.Time
	backoff      time.Duration

	// connectFn can be overridden in tests.
	connectFn func(url string) error

	// onReconnect is called after a successful reconnection.
	onReconnect func()
}

// NewRTMPCaller creates an RTMP caller adapter.
func NewRTMPCaller(config RTMPCallerConfig) (*RTMPCaller, error) {
	if config.URL == "" {
		return nil, fmt.Errorf("RTMP URL is required")
	}

	c := &RTMPCaller{
		config:  config,
		backoff: rtmpInitialBackoff,
	}
	c.state.Store(ptrTo(StateStopped))
	c.lastError.Store(ptrTo(""))

	ring := (*C.rtmp_ring_t)(C.calloc(1, C.size_t(unsafe.Sizeof(C.rtmp_ring_t{}))))
	if ring == nil {
		return nil, fmt.Errorf("failed to allocate RTMP ring buffer")
	}
	c.ring = ring

	return c, nil
}

// ID returns the adapter identifier.
func (c *RTMPCaller) ID() string { return "rtmp" }

// Start opens the RTMP connection and begins the demux/remux loop.
func (c *RTMPCaller) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.goCtx, c.cancel = context.WithCancel(ctx)
	c.startedAt = time.Now()
	c.bytesWritten.Store(0)
	c.state.Store(ptrTo(StateStarting))
	c.lastError.Store(ptrTo(""))
	c.drainStop = make(chan struct{})
	c.drainDone = make(chan struct{})

	if err := c.openLocked(); err != nil {
		c.state.Store(ptrTo(StateReconnecting))
		c.lastError.Store(ptrTo(err.Error()))
		c.reconnecting.Store(true)
		go c.reconnectLoop()
		go c.drainLoop()
		return nil
	}

	c.state.Store(ptrTo(StateActive))
	c.resetBackoffLocked()
	go c.drainLoop()
	return nil
}

// openLocked opens the RTMP output connection and TS demuxer input.
// Must be called with c.mu held.
func (c *RTMPCaller) openLocked() error {
	if c.connectFn != nil {
		return c.connectFn(c.config.URL)
	}

	cURL := C.CString(c.config.URL)
	defer C.free(unsafe.Pointer(cURL))

	rc := C.rtmp_open_output(&c.ctx, cURL)
	if rc < 0 {
		if rc == -2 && len(c.config.URL) >= 8 && c.config.URL[:8] == "rtmps://" {
			return fmt.Errorf("RTMPS connect failed — ensure FFmpeg was built with TLS support (--enable-openssl or --enable-gnutls)")
		}
		return fmt.Errorf("RTMP connect failed (code %d)", int(rc))
	}

	rc = C.rtmp_open_input(&c.ctx, c.ring)
	if rc < 0 {
		C.rtmp_close_output(&c.ctx)
		return fmt.Errorf("TS demuxer init failed (code %d)", int(rc))
	}

	return nil
}

// Write sends TS data to the RTMP adapter's ring buffer.
func (c *RTMPCaller) Write(tsData []byte) (int, error) {
	state := *c.state.Load()

	switch state {
	case StateActive:
		if c.pendingIDR.Load() {
			if !containsKeyframe(tsData) {
				return len(tsData), nil
			}
			c.pendingIDR.Store(false)
		}

		c.mu.Lock()
		n := C.rtmp_ring_write(c.ring, (*C.uchar)(unsafe.Pointer(&tsData[0])), C.int(len(tsData)))
		c.mu.Unlock()
		if int(n) == 0 && len(tsData) > 0 {
			// Ring buffer full -- data is dropped entirely (no partial TS packets).
			// Report len(tsData) so the caller doesn't retry; the drain loop
			// will consume data and make space.
			return len(tsData), nil
		}
		c.bytesWritten.Add(int64(n))
		return int(n), nil

	case StateReconnecting, StateStarting:
		return len(tsData), nil

	default:
		return 0, fmt.Errorf("RTMP caller not active (state: %s)", state)
	}
}

// drainLoop reads packets from the TS demuxer and writes them to RTMP.
func (c *RTMPCaller) drainLoop() {
	defer close(c.drainDone)

	for {
		select {
		case <-c.drainStop:
			return
		case <-c.goCtx.Done():
			return
		default:
		}

		state := *c.state.Load()
		if state != StateActive {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		c.mu.Lock()
		if c.ctx.initialized == 0 {
			avail := C.rtmp_ring_available(c.ring)
			if avail > 192*1024 {
				rc := C.rtmp_find_streams(&c.ctx)
				if rc < 0 {
					c.mu.Unlock()
					time.Sleep(10 * time.Millisecond)
					continue
				}
			} else {
				c.mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				continue
			}
		}

		wrote := false
		for i := 0; i < rtmpDrainBatch; i++ {
			rc := C.rtmp_read_write_packet(&c.ctx)
			if rc == 1 {
				break
			}
			if rc < 0 {
				c.mu.Unlock()
				slog.Warn("RTMP write error, entering reconnect", "url", rtmpMaskURL(c.config.URL))
				c.state.Store(ptrTo(StateReconnecting))
				c.lastError.Store(ptrTo("RTMP write failed"))
				if c.reconnecting.CompareAndSwap(false, true) {
					go c.reconnectLoop()
				}
				goto waitLoop
			}
			wrote = true
		}
		c.mu.Unlock()

		if !wrote {
			time.Sleep(5 * time.Millisecond)
		}
		continue

	waitLoop:
		time.Sleep(100 * time.Millisecond)
	}
}

// Close stops the RTMP caller and releases resources.
func (c *RTMPCaller) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}

	if c.drainStop != nil {
		select {
		case <-c.drainStop:
		default:
			close(c.drainStop)
		}
	}

	if c.drainDone != nil {
		c.mu.Unlock()
		<-c.drainDone
		c.mu.Lock()
	}

	c.closeLocked()

	if c.ring != nil {
		C.free(unsafe.Pointer(c.ring))
		c.ring = nil
	}

	c.state.Store(ptrTo(StateStopped))
	return nil
}

// closeLocked tears down FFmpeg contexts. Must be called with c.mu held.
func (c *RTMPCaller) closeLocked() {
	C.rtmp_close_output(&c.ctx)
	C.rtmp_close_input(&c.ctx)
}

// Status returns the current RTMP caller status.
func (c *RTMPCaller) Status() AdapterStatus {
	state := *c.state.Load()
	errStr := *c.lastError.Load()
	return AdapterStatus{
		State:        state,
		BytesWritten: c.bytesWritten.Load(),
		StartedAt:    c.startedAt,
		Error:        errStr,
	}
}

// RTMPStatusSnapshot returns an RTMPStatus for ControlRoomState.
func (c *RTMPCaller) RTMPStatusSnapshot() RTMPStatus {
	status := c.Status()
	return RTMPStatus{
		Active:       status.State == StateActive || status.State == StateReconnecting,
		URL:          c.config.URL,
		State:        string(status.State),
		BytesWritten: status.BytesWritten,
		Error:        status.Error,
	}
}

// reconnectLoop retries the RTMP connection with exponential backoff.
func (c *RTMPCaller) reconnectLoop() {
	defer c.reconnecting.Store(false)

	for {
		select {
		case <-c.goCtx.Done():
			return
		default:
		}

		backoff := c.nextBackoff()
		slog.Info("RTMP reconnecting", "backoff", backoff, "url", rtmpMaskURL(c.config.URL))

		select {
		case <-c.goCtx.Done():
			return
		case <-time.After(backoff):
		}

		c.mu.Lock()
		c.closeLocked()
		// Reset ring buffer state for the new connection.
		c.ring.write_pos = 0
		c.ring.read_pos = 0
		c.ring.count = 0
		c.ring.eof = 0

		err := c.openLocked()
		c.mu.Unlock()

		if err != nil {
			c.lastError.Store(ptrTo(err.Error()))
			continue
		}

		c.pendingIDR.Store(true)
		c.state.Store(ptrTo(StateActive))
		c.lastError.Store(ptrTo(""))
		c.resetBackoff()

		slog.Info("RTMP reconnected", "url", rtmpMaskURL(c.config.URL))

		if c.onReconnect != nil {
			c.onReconnect()
		}
		return
	}
}

func (c *RTMPCaller) nextBackoff() time.Duration {
	c.mu.Lock()
	current := c.backoff
	next := current * 2
	if next > rtmpMaxBackoff {
		next = rtmpMaxBackoff
	}
	c.backoff = next
	c.mu.Unlock()
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(current) * jitter)
}

func (c *RTMPCaller) resetBackoff() {
	c.mu.Lock()
	c.resetBackoffLocked()
	c.mu.Unlock()
}

func (c *RTMPCaller) resetBackoffLocked() {
	c.backoff = rtmpInitialBackoff
}

// Compile-time check that RTMPCaller satisfies the Adapter interface.
var _ Adapter = (*RTMPCaller)(nil)
