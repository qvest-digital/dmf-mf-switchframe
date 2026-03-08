package switcher

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// errDecoderBuffering is returned when the decoder needs more input frames
// before it can produce output (H.264 B-frame reordering). This is normal
// startup behavior, not an error — the frame is buffered internally.
var errDecoderBuffering = errors.New("pipeline: decoder buffering")

// avc1Pool recycles AVC1 output buffers to avoid 50-150KB/frame allocations
// on every pipeline encode. Seeded with 64KB; getAVC1Buffer transparently
// allocates larger buffers for higher bitrate frames.
var avc1Pool = sync.Pool{
	New: func() any {
		return make([]byte, 0, 65536)
	},
}

func getAVC1Buffer(size int) []byte {
	buf, ok := avc1Pool.Get().([]byte)
	if !ok || cap(buf) < size {
		return make([]byte, size)
	}
	return buf[:size]
}

func putAVC1Buffer(buf []byte) {
	if buf != nil {
		avc1Pool.Put(buf[:0]) //nolint:staticcheck // slice value is intentional
	}
}

// pipelineCodecs manages a shared decoder/encoder pair for the video processing
// pipeline. Instead of each processor (compositor, key bridge) owning its own
// codec pair, the pipeline coordinator uses a single decode/encode cycle.
type pipelineCodecs struct {
	mu             sync.Mutex
	decoder        transition.VideoDecoder
	encoder        transition.VideoEncoder
	decoderFactory transition.DecoderFactory
	encoderFactory transition.EncoderFactory
	encWidth       int
	encHeight      int
	groupID        uint32
	closed         bool

	// replayDecoder is a pre-warmed decoder kept in the pool for GOP replay.
	// On transition, replayGOP flushes and reuses this instead of creating
	// a fresh decoder (which costs ~500-700ms for VideoToolbox cold start).
	// After replay, the old pipeline decoder is recycled into this slot.
	replayDecoder transition.VideoDecoder
	prewarmWg     sync.WaitGroup

	// Source-derived encoder parameters (updated via updateSourceStats).
	sourceBitrate int     // estimated bitrate from program source (bytes/sec * 8)
	sourceFPS     float32 // estimated FPS from program source

	// forceNextIDR causes the next encode call to produce a keyframe.
	// Set by replayGOP after building decoder references so the first
	// live frame through the pipeline produces a browser sync point.
	forceNextIDR bool

	// Callback invoked when the encoder produces a keyframe with new SPS/PPS.
	onVideoInfoChange func(sps, pps []byte, width, height int)

	// Reusable buffer for AnnexB→AVC1 conversion (grows to steady state).
	avc1Buf []byte

	// Last-known SPS/PPS for deduplication — only fire callback on change.
	lastSPS []byte
	lastPPS []byte

	// Instrumentation for replay GOP performance tracking.
	replayGOPCount    atomic.Int64
	replayGOPLastNano atomic.Int64
	replayGOPMaxNano  atomic.Int64
	replayGOPPoolHits atomic.Int64 // reused from pool (vs factory creation)
}

// decode converts a media.VideoFrame to a ProcessingFrame by decoding H.264
// to raw YUV420. Lazy-initializes the decoder on the first keyframe.
//
// If precomputedAnnexB is non-nil, it is used directly instead of converting
// from AVC1. This avoids duplicate conversion when the caller (handleVideoFrame)
// has already computed AnnexB for the GOP cache and/or transition engine.
//
// The lock is only held for lazy-init and to capture the decoder reference.
// The actual decode (30-100ms) runs outside the lock. This is safe because
// the pipeline is single-threaded (one videoProcessingLoop goroutine).
func (pc *pipelineCodecs) decode(frame *media.VideoFrame, precomputedAnnexB []byte) (*ProcessingFrame, error) {
	// Phase 1: Lock to get decoder reference + lazy-init
	pc.mu.Lock()
	needPrewarm := false
	if pc.decoder == nil {
		if !frame.IsKeyframe {
			pc.mu.Unlock()
			return nil, fmt.Errorf("pipeline: need keyframe to init decoder")
		}
		dec, err := pc.decoderFactory()
		if err != nil {
			pc.mu.Unlock()
			return nil, fmt.Errorf("pipeline: decoder init: %w", err)
		}
		pc.decoder = dec
		needPrewarm = pc.replayDecoder == nil && pc.decoderFactory != nil
	}
	decoder := pc.decoder
	factory := pc.decoderFactory
	pc.mu.Unlock()

	if needPrewarm && factory != nil {
		pc.prewarmWg.Add(1)
		go func() {
			defer pc.prewarmWg.Done()
			rd, err := factory()
			if err != nil {
				return
			}
			pc.mu.Lock()
			if pc.closed || pc.replayDecoder != nil {
				pc.mu.Unlock()
				rd.Close()
				return
			}
			pc.replayDecoder = rd
			pc.mu.Unlock()
		}()
	}

	// Phase 2: NALU conversion + decode OUTSIDE lock
	annexB := precomputedAnnexB
	if len(annexB) == 0 {
		annexB = codec.AVC1ToAnnexB(frame.WireData)
		if frame.IsKeyframe {
			annexB = codec.PrependSPSPPS(frame.SPS, frame.PPS, annexB)
		}
	}

	yuv, w, h, err := decoder.Decode(annexB)
	if err != nil {
		if strings.Contains(err.Error(), "buffering") {
			return nil, errDecoderBuffering
		}
		return nil, fmt.Errorf("pipeline: decode: %w", err)
	}

	yuvSize := w * h * 3 / 2
	if len(yuv) < yuvSize {
		return nil, fmt.Errorf("pipeline: decoder buffer too small: got %d, need %d", len(yuv), yuvSize)
	}
	yuvCopy := getYUVBuffer(yuvSize)
	copy(yuvCopy, yuv[:yuvSize])

	return &ProcessingFrame{
		YUV:        yuvCopy,
		Width:      w,
		Height:     h,
		PTS:        frame.PTS,
		DTS:        frame.DTS,
		IsKeyframe: frame.IsKeyframe,
		GroupID:    frame.GroupID,
		Codec:      frame.Codec,
	}, nil
}

// encode converts a ProcessingFrame back to a media.VideoFrame by encoding
// YUV420 to H.264. Lazy-initializes the encoder on first call.
//
// The lock is only held for config checks and state updates, not for the
// actual encode (30-100ms). This is safe because the pipeline is
// single-threaded (one videoProcessingLoop goroutine).
func (pc *pipelineCodecs) encode(pf *ProcessingFrame, forceIDR bool) (*media.VideoFrame, error) {
	// Phase 1: Lock for config + init
	pc.mu.Lock()
	// Check forceNextIDR flag (set by replayGOP after building decoder
	// references). This ensures the first live frame after a transition
	// produces a keyframe for browser sync.
	if pc.forceNextIDR {
		forceIDR = true
		pc.forceNextIDR = false
	}

	if pc.encoder != nil && (pf.Width != pc.encWidth || pf.Height != pc.encHeight) {
		pc.encoder.Close()
		pc.encoder = nil
	}

	if pc.encoder == nil {
		bitrate := transition.DefaultBitrate
		fps := float32(transition.DefaultFPS)
		if pc.sourceBitrate > 0 {
			bitrate = pc.sourceBitrate
		}
		if pc.sourceFPS > 0 {
			fps = pc.sourceFPS
		}
		enc, err := pc.encoderFactory(pf.Width, pf.Height, bitrate, fps)
		if err != nil {
			pc.mu.Unlock()
			return nil, fmt.Errorf("pipeline: encoder init: %w", err)
		}
		pc.encoder = enc
		pc.encWidth = pf.Width
		pc.encHeight = pf.Height
	}
	encoder := pc.encoder
	pc.mu.Unlock()

	// Phase 2: Encode OUTSIDE lock
	encoded, isKeyframe, err := encoder.Encode(pf.YUV, pf.PTS, forceIDR)
	if err != nil {
		return nil, fmt.Errorf("pipeline: encode: %w", err)
	}
	// Hardware encoders (e.g. VideoToolbox) may return nil data during warmup
	// (EAGAIN). Return nil frame to signal "no output yet" — not an error.
	if len(encoded) == 0 {
		return nil, nil
	}

	pc.avc1Buf = codec.AnnexBToAVC1Into(encoded, pc.avc1Buf[:0])
	avc1 := getAVC1Buffer(len(pc.avc1Buf))
	copy(avc1, pc.avc1Buf)

	// Phase 3: Lock for state update
	pc.mu.Lock()
	if pf.GroupID > pc.groupID {
		pc.groupID = pf.GroupID
	}
	if isKeyframe {
		pc.groupID++
	}
	groupID := pc.groupID
	pc.mu.Unlock()

	frame := &media.VideoFrame{
		PTS:        pf.PTS,
		DTS:        pf.DTS,
		IsKeyframe: isKeyframe,
		WireData:   avc1,
		Codec:      pf.Codec,
		GroupID:    groupID,
	}

	if isKeyframe {
		for _, nalu := range codec.ExtractNALUs(avc1) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1F {
			case 7:
				frame.SPS = nalu
			case 8:
				frame.PPS = nalu
			}
		}
		if frame.SPS != nil && frame.PPS != nil && pc.onVideoInfoChange != nil {
			pc.mu.Lock()
			if !bytes.Equal(frame.SPS, pc.lastSPS) || !bytes.Equal(frame.PPS, pc.lastPPS) {
				pc.lastSPS = append(pc.lastSPS[:0], frame.SPS...)
				pc.lastPPS = append(pc.lastPPS[:0], frame.PPS...)
				pc.mu.Unlock()
				pc.onVideoInfoChange(frame.SPS, frame.PPS, pf.Width, pf.Height)
			} else {
				pc.mu.Unlock()
			}
		}
	}

	return frame, nil
}

// replayGOP feeds all cached GOP frames through a decoder to build its
// reference frame chain, then swaps the warmed-up decoder into the pipeline
// and sets forceNextIDR so the first live frame produces a keyframe for
// browser sync.
//
// Pool semantics: replayGOP takes a pre-warmed decoder from the pool (or
// creates one via factory on first call), flushes it (~1ms), decodes the
// GOP through it, then swaps it into the pipeline. The OLD pipeline decoder
// is recycled into the pool slot (not closed), so the next transition reuses
// it. This eliminates both creation (~500-700ms VideoToolbox cold start) and
// destruction overhead on every transition.
//
// The decoder is warmed up OUTSIDE the lock, eliminating the O(N × decode_time)
// lock hold that previously blocked videoProcessingLoop for 400-600ms.
func (pc *pipelineCodecs) replayGOP(frames []*media.VideoFrame) {
	start := time.Now()
	if len(frames) == 0 {
		return
	}

	// Phase 1: Take replay decoder from pool (or capture factory).
	pc.mu.Lock()
	if pc.decoder == nil {
		pc.mu.Unlock()
		return
	}
	replayDec := pc.replayDecoder
	pc.replayDecoder = nil
	factory := pc.decoderFactory
	pc.mu.Unlock()

	poolHit := replayDec != nil

	if replayDec == nil && factory == nil {
		// No pool decoder and no factory — fall back to in-place replay.
		// This path is only hit in tests that set decoder directly.
		pc.replayGOPInPlace(frames)
		return
	}

	// Phase 2: Get a decoder (from pool or factory), outside the lock.
	if replayDec == nil {
		// First-time only: create via factory (cold start).
		var err error
		replayDec, err = factory()
		if err != nil {
			return
		}
	} else {
		// Flush the pooled decoder to clear stale reference frames (~1ms).
		type flusher interface{ Flush() }
		if f, ok := replayDec.(flusher); ok {
			f.Flush()
		}
	}

	// Phase 3: Decode all GOP frames outside the lock (N × 8ms).
	decoded := false
	for _, frame := range frames {
		annexB := codec.AVC1ToAnnexB(frame.WireData)
		if frame.IsKeyframe {
			annexB = codec.PrependSPSPPS(frame.SPS, frame.PPS, annexB)
		}
		if _, _, _, err := replayDec.Decode(annexB); err == nil {
			decoded = true
		}
	}

	if !decoded {
		// No frames decoded — return decoder to pool instead of wasting it.
		pc.mu.Lock()
		if pc.replayDecoder == nil {
			pc.replayDecoder = replayDec
		} else {
			pc.mu.Unlock()
			replayDec.Close()
			goto instrument
		}
		pc.mu.Unlock()
		goto instrument
	}

	// Phase 4: Swap — warmed-up decoder becomes pipeline decoder,
	// old pipeline decoder is recycled into pool for next transition.
	{
		pc.mu.Lock()
		oldDec := pc.decoder
		pc.decoder = replayDec
		pc.forceNextIDR = true
		// Recycle the old pipeline decoder into the pool slot.
		// It's safe because the IDR gate prevents concurrent decode() calls.
		if pc.replayDecoder != nil {
			// Shouldn't happen (we nil'd it above), but be safe.
			pc.replayDecoder.Close()
		}
		pc.replayDecoder = oldDec
		pc.mu.Unlock()
	}

instrument:
	dur := time.Since(start)
	durNano := dur.Nanoseconds()
	pc.replayGOPCount.Add(1)
	pc.replayGOPLastNano.Store(durNano)
	updateAtomicMax(&pc.replayGOPMaxNano, durNano)
	if poolHit {
		pc.replayGOPPoolHits.Add(1)
	}
	slog.Info("replayGOP", "frames", len(frames), "ms", dur.Milliseconds(), "pool_hit", poolHit)
}

// replayGOPInPlace is the fallback path when no decoder factory is available.
// Holds the lock for the entire decode sequence (used only in tests).
func (pc *pipelineCodecs) replayGOPInPlace(frames []*media.VideoFrame) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.decoder == nil || len(frames) == 0 {
		return
	}

	decoded := false
	for _, frame := range frames {
		annexB := codec.AVC1ToAnnexB(frame.WireData)
		if frame.IsKeyframe {
			annexB = codec.PrependSPSPPS(frame.SPS, frame.PPS, annexB)
		}
		if _, _, _, err := pc.decoder.Decode(annexB); err == nil {
			decoded = true
		}
	}

	if decoded {
		pc.forceNextIDR = true
	}
}

// feedDeltaFrames decodes additional frames through the pipeline decoder to
// close the timing window between replayGOP's GOP snapshot and the routing
// switch. During replayGOP (~8-30ms), 1-2 P-frames may arrive that the
// transition engine drops (it's already idle). Without feeding them to the
// pipeline decoder, the first P-frame after routing switches references
// frames the decoder hasn't seen, causing macroblock corruption.
//
// Must be called after replayGOP and before the routing switch. The output
// is discarded — we only need the decoder to build its reference chain.
func (pc *pipelineCodecs) feedDeltaFrames(frames []*media.VideoFrame) {
	if len(frames) == 0 {
		return
	}

	pc.mu.Lock()
	decoder := pc.decoder
	pc.mu.Unlock()

	if decoder == nil {
		return
	}

	for _, frame := range frames {
		annexB := codec.AVC1ToAnnexB(frame.WireData)
		if frame.IsKeyframe {
			annexB = codec.PrependSPSPPS(frame.SPS, frame.PPS, annexB)
		}
		_, _, _, _ = decoder.Decode(annexB) // output discarded — only building reference chain
	}
}

// updateSourceStats propagates the program source's estimated bitrate and FPS
// to the encoder. These are used when the encoder is (re)created.
// Uses TryLock to avoid blocking the source delivery goroutine on the rare
// occasion when pc.mu is held (lazy-init or state update). Stats are
// approximate and will be picked up on the next available frame.
func (pc *pipelineCodecs) updateSourceStats(avgFrameSize float64, avgFPS float64) {
	if !pc.mu.TryLock() {
		return
	}
	defer pc.mu.Unlock()
	if avgFPS > 0 {
		raw := int(avgFrameSize * avgFPS * 8)
		// Clamp to sane bounds: 500 Kbps floor, 50 Mbps ceiling.
		// The ceiling accommodates 4K sources (~25-40 Mbps typical).
		// Resolution-aware clamping happens at encoder creation time
		// via the factory, but we prevent garbage values here.
		const (
			minBitrate = 500_000    // 500 Kbps
			maxBitrate = 50_000_000 // 50 Mbps
		)
		if raw < minBitrate {
			raw = minBitrate
		}
		if raw > maxBitrate {
			raw = maxBitrate
		}
		pc.sourceBitrate = raw
		pc.sourceFPS = float32(avgFPS)
	}
}

// dimensions returns the current encoder width and height.
// Returns (0, 0) if no frame has been encoded yet or if pc is nil.
func (pc *pipelineCodecs) dimensions() (int, int) {
	if pc == nil {
		return 0, 0
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.encWidth, pc.encHeight
}

// close releases decoder, encoder, and pool resources.
// Waits for any in-flight prewarm goroutine before cleanup.
func (pc *pipelineCodecs) close() {
	pc.prewarmWg.Wait()
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.closed = true
	if pc.decoder != nil {
		pc.decoder.Close()
		pc.decoder = nil
	}
	if pc.encoder != nil {
		pc.encoder.Close()
		pc.encoder = nil
	}
	if pc.replayDecoder != nil {
		pc.replayDecoder.Close()
		pc.replayDecoder = nil
	}
}

// replayStats returns instrumentation data for the replay decoder pool.
func (pc *pipelineCodecs) replayStats() map[string]any {
	return map[string]any{
		"replay_gop_count":     pc.replayGOPCount.Load(),
		"replay_gop_last_ms":   float64(pc.replayGOPLastNano.Load()) / 1e6,
		"replay_gop_max_ms":    float64(pc.replayGOPMaxNano.Load()) / 1e6,
		"replay_gop_pool_hits": pc.replayGOPPoolHits.Load(),
	}
}
