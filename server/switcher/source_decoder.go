package switcher

import (
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/transition"
)

// decoderInput wraps a video frame with its arrival timestamp for E2E
// latency measurement. The arrivalNano field records when the frame
// entered sourceViewer.SendVideo().
type decoderInput struct {
	frame       *media.VideoFrame
	arrivalNano int64
}

// sourceDecoder runs a per-source decode goroutine, converting H.264 frames
// to raw YUV420 ProcessingFrames. Each source gets its own decoder and
// goroutine, matching how FFmpeg decoders are single-threaded. The callback
// receives decoded frames for routing through the switcher pipeline.
type sourceDecoder struct {
	sourceKey string
	decoder   transition.VideoDecoder
	ch        chan decoderInput // capacity 2, newest-wins drop
	callback  func(string, *ProcessingFrame)
	done      chan struct{}

	// Reusable buffers for AVC1→AnnexB conversion (avoid alloc per frame).
	// Two buffers needed: PrependSPSPPSInto reads from annexBBuf while
	// writing to prependBuf — shared backing storage would corrupt data.
	annexBBuf  []byte
	prependBuf []byte

	// FramePool for YUV buffer allocation (nil-safe: falls back to make)
	pool *FramePool

	// Pipeline format for per-source resolution normalization.
	// Shared pointer from Switcher — reads are lock-free via atomic.Load().
	pipelineFormat *atomic.Pointer[PipelineFormat]

	// Reusable buffer for resolution scaling (lazy-allocated on first use).
	scaleBuf      []byte
	scaleWarnOnce sync.Once // log mismatch warning once per source

	// ST map registry for per-source lens correction / warping.
	// Looked up per-frame via SourceProcessor(sourceKey).
	stmapRegistry *stmap.Registry
	stmapBuf      []byte // reusable buffer for stmap warp output (lazy-allocated)

	// Reusable buffer for 420 8-bit → 422 10-bit upconversion (lazy-allocated).
	upconvertBuf []byte

	// gpuSourceActive — shared pointer to Switcher's atomic.Bool.
	// When true, skip CPU ST map correction (GPU source manager does it on GPU).
	gpuSourceActive *atomic.Bool

	// Frame stats (EMA of H.264 frame size/FPS for encoder params).
	// Written by Send() (relay goroutine), read by Stats() (decoder goroutine
	// via callback). Use atomic Uint64 + Float64bits/Float64frombits to avoid
	// data race (same pattern as audio/limiter.go, audio/compressor.go).
	avgFrameSizeBits atomic.Uint64
	avgFPSBits       atomic.Uint64
	lastPTS          int64
	frameCount       int
	lastGroupID      atomic.Uint32

	// Decode timing instrumentation (atomic, lock-free)
	lastDecodeNs atomic.Int64 // duration of last decoder.Decode() call
	maxDecodeNs  atomic.Int64 // max decode duration seen
	decodeDrops  atomic.Int64 // count of frames dropped by newest-wins policy
}

// newSourceDecoder creates a decoder for the given source key, starts its
// decode goroutine, and returns the decoder. Returns nil if the factory fails.
// pipelineFormat is the shared atomic pointer from Switcher for per-source
// resolution normalization (may be nil if no normalization is needed).
// gpuActive is a shared atomic.Bool from the Switcher — when true, CPU ST map
// correction is skipped (the GPU source manager handles it on GPU).
func newSourceDecoder(key string, factory transition.DecoderFactory, callback func(string, *ProcessingFrame), pool *FramePool, pipelineFormat *atomic.Pointer[PipelineFormat], stmapRegistry *stmap.Registry, gpuActive *atomic.Bool) *sourceDecoder {
	dec, err := factory()
	if err != nil {
		slog.Warn("source decoder creation failed", "source", key, "error", err)
		return nil
	}

	sd := &sourceDecoder{
		sourceKey:       key,
		decoder:         dec,
		ch:              make(chan decoderInput, 2),
		callback:        callback,
		done:            make(chan struct{}),
		pool:            pool,
		pipelineFormat:  pipelineFormat,
		stmapRegistry:   stmapRegistry,
		gpuSourceActive: gpuActive,
	}
	go sd.decodeLoop()
	return sd
}

// Send enqueues an H.264 frame for decoding. Uses newest-wins drop policy:
// if the channel is full, the oldest frame is dropped. arrivalNano is the
// UnixNano timestamp when the frame entered sourceViewer.SendVideo(),
// propagated through the pipeline for E2E latency measurement.
func (sd *sourceDecoder) Send(frame *media.VideoFrame, arrivalNano int64) {
	sd.updateStats(frame)

	input := decoderInput{frame: frame, arrivalNano: arrivalNano}
	select {
	case sd.ch <- input:
	default:
		// Channel full — drop oldest, enqueue new (newest-wins).
		sd.decodeDrops.Add(1)
		select {
		case <-sd.ch:
		default:
		}
		select {
		case sd.ch <- input:
		default:
		}
	}
}

// PerfStats returns decode timing and drop statistics.
// Safe for concurrent access from any goroutine.
func (sd *sourceDecoder) PerfStats() (lastDecodeNs, maxDecodeNs, drops int64) {
	return sd.lastDecodeNs.Load(), sd.maxDecodeNs.Load(), sd.decodeDrops.Load()
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

	for input := range sd.ch {
		frame := input.frame
		decodeStartNano := time.Now().UnixNano()

		// Convert wire format to Annex B for decoder (buffer reuse).
		// Works identically for AVC and HEVC (same 4-byte length prefix → start code).
		sd.annexBBuf = codec.WireToAnnexBInto(frame.WireData, sd.annexBBuf[:0])
		if frame.IsKeyframe && len(frame.SPS) > 0 && len(frame.PPS) > 0 {
			if len(frame.VPS) > 0 {
				// HEVC: prepend VPS+SPS+PPS for decoder initialization.
				sd.prependBuf = codec.PrependVPSSPSPPSInto(frame.VPS, frame.SPS, frame.PPS, sd.annexBBuf, sd.prependBuf[:0])
			} else {
				// H.264: prepend SPS+PPS only.
				sd.prependBuf = codec.PrependSPSPPSInto(frame.SPS, frame.PPS, sd.annexBBuf, sd.prependBuf[:0])
			}
			sd.annexBBuf, sd.prependBuf = sd.prependBuf, sd.annexBBuf
		}

		// DecodeInto interface: if the decoder supports it, we can decode
		// directly into a pre-acquired pool buffer, eliminating one alloc+copy.
		type decodeIntoer interface {
			DecodeInto(data []byte, dst []byte) ([]byte, int, int, error)
		}

		var yuv []byte
		var w, h int
		var err error
		var poolBuf []byte
		var usedPoolBuf bool

		t0 := time.Now().UnixNano()
		if di, ok := sd.decoder.(decodeIntoer); ok && sd.pool != nil {
			poolBuf = sd.pool.Acquire()
			yuv, w, h, err = di.DecodeInto(sd.annexBBuf, poolBuf)
			if err == nil && len(yuv) > 0 && len(poolBuf) > 0 && &yuv[0] == &poolBuf[0] {
				usedPoolBuf = true
			}
		} else {
			yuv, w, h, err = sd.decoder.Decode(sd.annexBBuf)
		}
		decodeEndNano := time.Now().UnixNano()
		dur := decodeEndNano - t0
		sd.lastDecodeNs.Store(dur)
		atomicutil.UpdateMax(&sd.maxDecodeNs, dur)

		if err != nil {
			if poolBuf != nil && !usedPoolBuf {
				sd.pool.Release(poolBuf)
			}
			slog.Debug("source decoder: decode failed",
				"source", sd.sourceKey, "error", err)
			continue
		}

		// Determine the pipeline color format for this frame.
		var pipeFmt *PipelineFormat
		pipeColorFmt := ColorYUV420_8bit // default
		if sd.pipelineFormat != nil {
			pipeFmt = sd.pipelineFormat.Load()
			if pipeFmt != nil {
				pipeColorFmt = pipeFmt.Color.ColorFormat
			}
		}

		// Detect decoded output format by buffer size.
		// A native 10-bit decoder produces w*h*4 bytes (YUV422P10LE),
		// while the standard decoder produces w*h*3/2 (YUV420P 8-bit).
		expected420Size := ColorYUV420_8bit.FrameSize(w, h)
		expected422_10Size := ColorYUV422_10bit.FrameSize(w, h)
		decodedIs10bit422 := len(yuv) == expected422_10Size
		decodedIs420_8bit := len(yuv) >= expected420Size && !decodedIs10bit422

		// Validate output size.
		if !decodedIs10bit422 && !decodedIs420_8bit {
			if poolBuf != nil {
				sd.pool.Release(poolBuf)
			}
			continue
		}

		// Determine the frame's native color format.
		decodedFormat := ColorYUV420_8bit
		if decodedIs10bit422 {
			decodedFormat = ColorYUV422_10bit
		}

		// Effective frame size based on decoded format.
		yuvSize := decodedFormat.FrameSize(w, h)

		// Scale to pipeline format if resolution differs.
		needsScale := false
		needsScale10 := false
		if pipeFmt != nil && pipeFmt.Width > 0 && pipeFmt.Height > 0 && (w != pipeFmt.Width || h != pipeFmt.Height) {
			if decodedIs420_8bit {
				needsScale = true
			} else if decodedIs10bit422 {
				needsScale10 = true
			}
		}

		var buf []byte
		var framePool *FramePool
		outputFormat := decodedFormat

		if usedPoolBuf && !needsScale && !needsScale10 {
			// Common fast path: decoder wrote directly into pool buffer,
			// no scaling needed. Skip the copy entirely.
			buf = poolBuf[:yuvSize]
			framePool = sd.pool
		} else if usedPoolBuf && needsScale {
			// Decoder wrote into pool buffer but we need to scale.
			sd.scaleWarnOnce.Do(func() {
				slog.Info("source resolution differs from pipeline format; scaling with bilinear",
					"source", sd.sourceKey, "source_w", w, "source_h", h,
					"pipeline_w", pipeFmt.Width, "pipeline_h", pipeFmt.Height)
			})
			dstSize := ColorYUV420_8bit.FrameSize(pipeFmt.Width, pipeFmt.Height)
			if len(sd.scaleBuf) < dstSize {
				sd.scaleBuf = make([]byte, dstSize)
			}
			transition.ScaleYUV420(yuv[:yuvSize], w, h, sd.scaleBuf[:dstSize], pipeFmt.Width, pipeFmt.Height)
			// Return the pool buffer (wrong resolution data) and allocate for scaled output.
			sd.pool.Release(poolBuf)
			buf = make([]byte, dstSize)
			copy(buf, sd.scaleBuf[:dstSize])
			framePool = nil
			w = pipeFmt.Width
			h = pipeFmt.Height
			yuvSize = dstSize
		} else {
			// Original path: decoder allocated its own buffer.
			if poolBuf != nil {
				sd.pool.Release(poolBuf) // unused pre-acquired buffer
			}

			if needsScale {
				sd.scaleWarnOnce.Do(func() {
					slog.Info("source resolution differs from pipeline format; scaling with bilinear",
						"source", sd.sourceKey, "source_w", w, "source_h", h,
						"pipeline_w", pipeFmt.Width, "pipeline_h", pipeFmt.Height)
				})
				dstSize := ColorYUV420_8bit.FrameSize(pipeFmt.Width, pipeFmt.Height)
				if len(sd.scaleBuf) < dstSize {
					sd.scaleBuf = make([]byte, dstSize)
				}
				transition.ScaleYUV420(yuv[:yuvSize], w, h, sd.scaleBuf[:dstSize], pipeFmt.Width, pipeFmt.Height)
				yuv = sd.scaleBuf
				w = pipeFmt.Width
				h = pipeFmt.Height
				yuvSize = dstSize
			}

			if needsScale10 {
				sd.scaleWarnOnce.Do(func() {
					slog.Info("source resolution differs from pipeline format; scaling 10-bit with bilinear",
						"source", sd.sourceKey, "source_w", w, "source_h", h,
						"pipeline_w", pipeFmt.Width, "pipeline_h", pipeFmt.Height)
				})
				dstSize := ColorYUV422_10bit.FrameSize(pipeFmt.Width, pipeFmt.Height)
				if len(sd.scaleBuf) < dstSize {
					sd.scaleBuf = make([]byte, dstSize)
				}
				if err := transition.ScaleYUV422P10(yuv[:yuvSize], w, h, sd.scaleBuf[:dstSize], pipeFmt.Width, pipeFmt.Height); err == nil {
					yuv = sd.scaleBuf
					w = pipeFmt.Width
					h = pipeFmt.Height
					yuvSize = dstSize
				} else {
					slog.Warn("10-bit scaling failed, passing through at source resolution",
						"source", sd.sourceKey, "error", err)
				}
			}

			// Deep-copy YUV (decoder reuses internal buffer)
			if sd.pool != nil {
				buf = sd.pool.Acquire()
				if len(buf) < yuvSize {
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
		}

		// Format conversion: match decoded format to pipeline format.
		if pipeColorFmt == ColorYUV422_10bit && outputFormat == ColorYUV420_8bit {
			// 8-bit 420 decoded → 10-bit 422 pipeline: upconvert.
			dstSize := ColorYUV422_10bit.FrameSize(w, h)
			if len(sd.upconvertBuf) < dstSize {
				sd.upconvertBuf = make([]byte, dstSize)
			}
			if err := Upconvert420to422_10(buf[:yuvSize], sd.upconvertBuf[:dstSize], w, h); err == nil {
				// Allocate a new buffer for the upconverted data.
				newBuf := make([]byte, dstSize)
				copy(newBuf, sd.upconvertBuf[:dstSize])
				// Release the original buffer.
				if framePool != nil {
					framePool.Release(buf)
				}
				buf = newBuf
				yuvSize = dstSize
				outputFormat = ColorYUV422_10bit
				framePool = nil
			}
		}
		// When decodedIs10bit422 && pipeColorFmt == ColorYUV422_10bit:
		// No conversion needed — frame is already in pipeline format.

		// Apply per-source ST map correction (post-decode, pre-fan-out).
		// All consumers (preview, replay, pipeline) see corrected frames.
		// Skip when GPU source manager is active — it handles ST map on GPU.
		if (sd.gpuSourceActive == nil || !sd.gpuSourceActive.Load()) && sd.stmapRegistry != nil {
			if proc := sd.stmapRegistry.SourceProcessor(sd.sourceKey); proc != nil && proc.Active() {
				if outputFormat == ColorYUV422_10bit {
					// 10-bit 422 ST map warp.
					dstSize := ColorYUV422_10bit.FrameSize(w, h)
					if len(sd.stmapBuf) < dstSize {
						sd.stmapBuf = make([]byte, dstSize)
					}
					if err := proc.Warp10(sd.stmapBuf[:dstSize], buf[:yuvSize], w, h); err == nil {
						copy(buf[:yuvSize], sd.stmapBuf[:dstSize])
					}
				} else {
					// 8-bit 420 ST map warp.
					dstSize := ColorYUV420_8bit.FrameSize(w, h)
					if len(sd.stmapBuf) < dstSize {
						sd.stmapBuf = make([]byte, dstSize)
					}
					proc.ProcessYUV(sd.stmapBuf[:dstSize], buf[:yuvSize], w, h)
					copy(buf[:yuvSize], sd.stmapBuf[:dstSize])
				}
			}
		}

		pf := &ProcessingFrame{
			Data:   buf[:yuvSize],
			Format: outputFormat,
			Width:  w,
			Height: h,
			PTS:    frame.PTS,
			DTS:    frame.DTS,
			// IsKeyframe intentionally NOT propagated from source H.264 stream.
			// The program re-encoder operates on raw YUV — source GOP structure
			// is irrelevant. IDR placement is controlled by: (1) encoder gop_size,
			// (2) RequestKeyframe() on cuts/output-start, (3) transition engine
			// first-frame flag. Propagating source keyframes caused excessive
			// IDRs (every source GOP boundary forced a program IDR).
			IsKeyframe:      false,
			GroupID:         frame.GroupID,
			Codec:           frame.Codec,
			ArrivalNano:     input.arrivalNano,
			DecodeStartNano: decodeStartNano,
			DecodeEndNano:   decodeEndNano,
			pool:            framePool,
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
