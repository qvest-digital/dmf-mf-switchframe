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

const avc1BufCap = 65536 // 64KB default buffer capacity for AVC1 pool

// avc1Pool recycles AVC1 output buffers to avoid 50-150KB/frame allocations
// on every pipeline encode. Seeded with 64KB; getAVC1Buffer transparently
// allocates larger buffers for higher bitrate frames.
var avc1Pool = sync.Pool{
	New: func() any {
		return make([]byte, 0, avc1BufCap)
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
	sourceBitrate int     // estimated bitrate from program source (bytes/sec * 8)
	sourceFPS     float32 // estimated FPS from program source

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

	if pc.encoder != nil && (pf.Width != pc.encWidth || pf.Height != pc.encHeight) {
		pc.encoder.Close()
		pc.encoder = nil
	}

	if pc.encoder == nil {
		bitrate := transition.DefaultBitrate
		if pc.sourceBitrate > 0 {
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
