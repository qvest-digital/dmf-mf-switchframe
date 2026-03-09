package switcher

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// allocAVC1Buffer allocates an owned AVC1 buffer. Each encoded frame needs
// its own buffer because BroadcastVideo fans out to viewers via buffered
// channels — async consumers (output muxer, SRT, WebTransport) may still
// reference WireData when the next encode cycle runs.
func allocAVC1Buffer(size int) []byte {
	return make([]byte, size)
}

// defaultBitrateForResolution returns the minimum encoding bitrate for
// broadcast-quality output at the given resolution. This serves as a quality
// floor — the encoder will never use less than this, even if the source
// stream has a lower bitrate. Re-encoding always needs headroom above the
// source bitrate to compensate for generation loss (decode→encode).
//
// These values target visually clean output on the "fast" x264 preset and
// are comparable to typical broadcast/streaming bitrates:
//   - 720p:  6 Mbps  (YouTube recommends 5 Mbps, broadcast uses 6-8)
//   - 1080p: 10 Mbps (YouTube recommends 8 Mbps, broadcast uses 10-15)
func defaultBitrateForResolution(width, height int) int {
	pixels := width * height
	switch {
	case pixels >= 3840*2160: // 4K
		return 20_000_000
	case pixels >= 1920*1080: // 1080p
		return 10_000_000
	case pixels >= 1280*720: // 720p
		return 6_000_000
	default: // 480p and below
		return 2_000_000
	}
}

// pipelineCodecs manages a shared encoder for the video processing pipeline.
// The pipeline receives raw YUV420 frames (decoded per-source by sourceDecoder)
// and encodes them to H.264 for program output.
type pipelineCodecs struct {
	mu             sync.Mutex
	encoder        transition.VideoEncoder
	encoderFactory transition.EncoderFactory
	encWidth       int
	encHeight      int
	groupID        uint32
	closed         bool

	// Pipeline format reference — provides FPS for encoder creation.
	// Points to the Switcher's atomic PipelineFormat pointer for lock-free reads.
	formatRef *atomic.Pointer[PipelineFormat]

	// Source-derived encoder parameters (updated via updateSourceStats).
	sourceBitrate  int     // estimated bitrate from program source (bytes/sec * 8)
	sourceFPS      float32 // estimated FPS from program source
	createdBitrate int     // bitrate used when encoder was created (for change detection)

	// Output timestamp normalization. The pipeline encoder has max_b_frames=0,
	// so DTS must always equal PTS. Additionally, sources with B-frames can
	// produce scrambled PTS (the sourceDecoder uses input frame PTS, but the
	// FFmpeg decoder reorders internally). We enforce monotonic output PTS.
	//
	// Only accessed from the videoProcessingLoop goroutine (single-writer).
	lastOutputPTS int64

	// Callback invoked when the encoder produces a keyframe with new SPS/PPS.
	onVideoInfoChange func(sps, pps []byte, width, height int)

	// Reusable buffer for AnnexB->AVC1 conversion (grows to steady state).
	avc1Buf []byte

	// Last-known SPS/PPS for deduplication -- only fire callback on change.
	lastSPS []byte
	lastPPS []byte
}

// invalidateEncoder forces encoder recreation on next encode call.
func (pc *pipelineCodecs) invalidateEncoder() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.encoder != nil {
		pc.encoder.Close()
		pc.encoder = nil
	}
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

	// Invalidate encoder on resolution change.
	if pc.encoder != nil && (pf.Width != pc.encWidth || pf.Height != pc.encHeight) {
		pc.encoder.Close()
		pc.encoder = nil
	}

	// Invalidate encoder when the effective bitrate would change significantly
	// (>20%) from what was used at creation. The effective bitrate is
	// max(sourceBitrate, resolutionDefault), so only invalidate when the
	// source bitrate exceeds the current createdBitrate by >20% (pulling
	// it above the resolution floor). Low source bitrates are clamped to the
	// floor and won't trigger invalidation.
	if pc.encoder != nil && pc.sourceBitrate > 0 && pc.createdBitrate > 0 {
		resDefault := defaultBitrateForResolution(pc.encWidth, pc.encHeight)
		effectiveBitrate := resDefault
		if pc.sourceBitrate > effectiveBitrate {
			effectiveBitrate = pc.sourceBitrate
		}
		ratio := float64(effectiveBitrate) / float64(pc.createdBitrate)
		if ratio > 1.2 || ratio < 0.8 {
			pc.encoder.Close()
			pc.encoder = nil
		}
	}

	if pc.encoder == nil {
		// Use the higher of resolution-based default and source bitrate.
		// The resolution default is a quality floor — re-encoding always
		// needs at least this many bits to look clean at the given resolution.
		// Source bitrate can exceed the floor (e.g., high-quality 1080p at
		// 12 Mbps) but should never pull the encoder below it (e.g., a
		// low-bitrate 720p source at 1.6 Mbps would look terrible re-encoded
		// at 1.6 Mbps due to generation loss).
		bitrate := defaultBitrateForResolution(pf.Width, pf.Height)
		if pc.sourceBitrate > bitrate {
			bitrate = pc.sourceBitrate
		}
		// Read pipeline format for FPS
		fpsNum := 30000
		fpsDen := 1001
		if pc.formatRef != nil {
			if f := pc.formatRef.Load(); f != nil {
				fpsNum = f.FPSNum
				fpsDen = f.FPSDen
			}
		}
		enc, err := pc.encoderFactory(pf.Width, pf.Height, bitrate, fpsNum, fpsDen)
		if err != nil {
			pc.mu.Unlock()
			return nil, fmt.Errorf("pipeline: encoder init: %w", err)
		}
		pc.encoder = enc
		pc.encWidth = pf.Width
		pc.encHeight = pf.Height
		pc.createdBitrate = bitrate
	}
	encoder := pc.encoder
	pc.mu.Unlock()

	// Phase 2: Encode OUTSIDE lock
	encoded, isKeyframe, err := encoder.Encode(pf.YUV, pf.PTS, forceIDR)
	if err != nil {
		return nil, fmt.Errorf("pipeline: encode: %w", err)
	}
	// Hardware encoders (e.g. VideoToolbox) may return nil data during warmup
	// (EAGAIN). Return nil frame to signal "no output yet" -- not an error.
	if len(encoded) == 0 {
		return nil, nil
	}

	pc.avc1Buf = codec.AnnexBToAVC1Into(encoded, pc.avc1Buf[:0])
	avc1 := allocAVC1Buffer(len(pc.avc1Buf))
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

	// Normalize output timestamps. The pipeline encoder has max_b_frames=0
	// (no B-frames), so DTS must always equal PTS. Sources have independent
	// PTS timelines, so switching sources (cuts and transitions) can produce
	// both backwards jumps and large forward jumps. Enforce monotonic PTS
	// with bounded forward advancement to prevent decoder stalls.
	outPTS := pf.PTS
	if pc.lastOutputPTS > 0 {
		fpsNum, fpsDen := 30000, 1001
		if pc.formatRef != nil {
			if f := pc.formatRef.Load(); f != nil {
				fpsNum = f.FPSNum
				fpsDen = f.FPSDen
			}
		}
		frameDur := int64(90000) * int64(fpsDen) / int64(fpsNum)
		if outPTS <= pc.lastOutputPTS {
			// PTS went backwards (source switch or B-frame reorder) —
			// advance by one frame duration.
			outPTS = pc.lastOutputPTS + frameDur
		} else if outPTS > pc.lastOutputPTS+frameDur*3 {
			// PTS jumped too far forward (source switch to a source with
			// a much larger PTS origin). Cap to one frame duration to
			// prevent downstream MPEG-TS decoders from stalling on the gap.
			outPTS = pc.lastOutputPTS + frameDur
		}
	}
	pc.lastOutputPTS = outPTS

	frame := &media.VideoFrame{
		PTS:        outPTS,
		DTS:        outPTS, // No B-frames: DTS always equals PTS
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
				// Copy SPS — ExtractNALUs returns sub-slices of the AVC1
				// buffer. SPS/PPS are stored separately on the frame and
				// may outlive the WireData reference.
				frame.SPS = append([]byte(nil), nalu...)
			case 8:
				frame.PPS = append([]byte(nil), nalu...)
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

// close releases encoder resources.
func (pc *pipelineCodecs) close() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.closed = true
	if pc.encoder != nil {
		pc.encoder.Close()
		pc.encoder = nil
	}
}
