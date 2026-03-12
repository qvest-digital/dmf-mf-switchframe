package switcher

import (
	"log/slog"
	"math"
	"sync/atomic"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// sourceDecoder runs a per-source decode goroutine, converting H.264 frames
// to raw YUV420 ProcessingFrames. Each source gets its own decoder and
// goroutine, matching how FFmpeg decoders are single-threaded. The callback
// receives decoded frames for routing through the switcher pipeline.
type sourceDecoder struct {
	sourceKey string
	decoder   transition.VideoDecoder
	ch        chan *media.VideoFrame // capacity 2, newest-wins drop
	callback  func(string, *ProcessingFrame)
	done      chan struct{}

	// Reusable buffers for AVC1→AnnexB conversion (avoid alloc per frame).
	// Two buffers needed: PrependSPSPPSInto reads from annexBBuf while
	// writing to prependBuf — shared backing storage would corrupt data.
	annexBBuf  []byte
	prependBuf []byte

	// FramePool for YUV buffer allocation (nil-safe: falls back to make)
	pool *FramePool

	// Frame stats (EMA of H.264 frame size/FPS for encoder params).
	// Written by Send() (relay goroutine), read by Stats() (decoder goroutine
	// via callback). Use atomic Uint64 + Float64bits/Float64frombits to avoid
	// data race (same pattern as audio/limiter.go, audio/compressor.go).
	avgFrameSizeBits atomic.Uint64
	avgFPSBits       atomic.Uint64
	lastPTS          int64
	frameCount       int
	lastGroupID      atomic.Uint32
}

// newSourceDecoder creates a decoder for the given source key, starts its
// decode goroutine, and returns the decoder. Returns nil if the factory fails.
func newSourceDecoder(key string, factory transition.DecoderFactory, callback func(string, *ProcessingFrame), pool *FramePool) *sourceDecoder {
	dec, err := factory()
	if err != nil {
		slog.Warn("source decoder creation failed", "source", key, "error", err)
		return nil
	}

	sd := &sourceDecoder{
		sourceKey: key,
		decoder:   dec,
		ch:        make(chan *media.VideoFrame, 2),
		callback:  callback,
		done:      make(chan struct{}),
		pool:      pool,
	}
	go sd.decodeLoop()
	return sd
}

// Send enqueues an H.264 frame for decoding. Uses newest-wins drop policy:
// if the channel is full, the oldest frame is dropped.
func (sd *sourceDecoder) Send(frame *media.VideoFrame) {
	sd.updateStats(frame)

	select {
	case sd.ch <- frame:
	default:
		// Channel full — drop oldest, enqueue new (newest-wins).
		select {
		case <-sd.ch:
		default:
		}
		select {
		case sd.ch <- frame:
		default:
		}
	}
}

// Close stops the decode goroutine and releases the decoder.
func (sd *sourceDecoder) Close() {
	close(sd.ch)
	<-sd.done
	sd.decoder.Close()
}

// Stats returns the rolling average frame size and FPS.
// Safe for concurrent access from a different goroutine than Send().
func (sd *sourceDecoder) Stats() (avgFrameSize, avgFPS float64) {
	return math.Float64frombits(sd.avgFrameSizeBits.Load()),
		math.Float64frombits(sd.avgFPSBits.Load())
}

// decodeLoop reads H.264 frames from the channel, converts AVC1→AnnexB,
// decodes to YUV420, and invokes the callback with a ProcessingFrame.
func (sd *sourceDecoder) decodeLoop() {
	defer close(sd.done)

	for frame := range sd.ch {
		// Convert AVC1 wire format to Annex B for decoder (buffer reuse)
		sd.annexBBuf = codec.AVC1ToAnnexBInto(frame.WireData, sd.annexBBuf[:0])
		if frame.IsKeyframe && len(frame.SPS) > 0 && len(frame.PPS) > 0 {
			sd.prependBuf = codec.PrependSPSPPSInto(frame.SPS, frame.PPS, sd.annexBBuf, sd.prependBuf[:0])
			sd.annexBBuf, sd.prependBuf = sd.prependBuf, sd.annexBBuf
		}

		yuv, w, h, err := sd.decoder.Decode(sd.annexBBuf)
		if err != nil {
			slog.Debug("source decoder: decode failed",
				"source", sd.sourceKey, "error", err)
			continue
		}

		// Deep-copy YUV (decoder reuses internal buffer)
		yuvSize := w * h * 3 / 2
		if len(yuv) < yuvSize {
			continue
		}
		var buf []byte
		var framePool *FramePool
		if sd.pool != nil {
			buf = sd.pool.Acquire()
			if len(buf) < yuvSize {
				// Pool buffer too small for this frame (e.g., 4K source with 1080p pool).
				// Return the undersized buffer and fall back to heap allocation.
				// Set framePool to nil so ReleaseYUV won't put the oversized
				// buffer back into the pool.
				sd.pool.Release(buf)
				buf = make([]byte, yuvSize)
				framePool = nil
			} else {
				framePool = sd.pool
			}
		} else {
			buf = make([]byte, yuvSize)
		}
		copy(buf, yuv[:yuvSize])

		pf := &ProcessingFrame{
			YUV:    buf[:yuvSize],
			Width:  w,
			Height: h,
			PTS:    frame.PTS,
			DTS:    frame.DTS,
			// IsKeyframe intentionally NOT propagated from source H.264 stream.
			// The program re-encoder operates on raw YUV — source GOP structure
			// is irrelevant. IDR placement is controlled by: (1) encoder gop_size,
			// (2) RequestKeyframe() on cuts/output-start, (3) transition engine
			// first-frame flag. Propagating source keyframes caused ~5x excessive
			// IDRs (every source GOP boundary forced a program IDR).
			IsKeyframe: false,
			GroupID:     frame.GroupID,
			Codec:      frame.Codec,
			pool:       framePool,
		}
		pf.SetRefs(1) // frame_sync ownership — shared across value copies

		sd.lastGroupID.Store(frame.GroupID)
		sd.callback(sd.sourceKey, pf)
	}
}

// updateStats maintains rolling EMA of frame size and FPS.
// Called from Send() which is single-writer per source viewer goroutine.
// Stats are stored atomically so Stats() can be called from another goroutine.
func (sd *sourceDecoder) updateStats(frame *media.VideoFrame) {
	sd.frameCount++
	frameSize := float64(len(frame.WireData))

	const alpha = 0.1 // EMA smoothing factor
	if sd.frameCount == 1 {
		sd.avgFrameSizeBits.Store(math.Float64bits(frameSize))
	} else {
		prev := math.Float64frombits(sd.avgFrameSizeBits.Load())
		sd.avgFrameSizeBits.Store(math.Float64bits(alpha*frameSize + (1-alpha)*prev))
	}

	// FPS from PTS delta (requires at least 2 frames)
	if sd.lastPTS > 0 && frame.PTS > sd.lastPTS {
		ptsDelta := frame.PTS - sd.lastPTS
		// PTS is in 90kHz units
		instantFPS := 90000.0 / float64(ptsDelta)
		if sd.frameCount == 2 {
			sd.avgFPSBits.Store(math.Float64bits(instantFPS))
		} else {
			prev := math.Float64frombits(sd.avgFPSBits.Load())
			sd.avgFPSBits.Store(math.Float64bits(alpha*instantFPS + (1-alpha)*prev))
		}
	}
	sd.lastPTS = frame.PTS
}
