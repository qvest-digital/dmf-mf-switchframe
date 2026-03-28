package switcher

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// errPipelineClosed is returned by encode() when called after close().
var errPipelineClosed = errors.New("pipeline codecs: closed")

// allocWireBuffer allocates an owned wire format buffer. Each encoded frame
// needs its own buffer because BroadcastVideo fans out to viewers via buffered
// channels — async consumers (output muxer, SRT, WebTransport) may still
// reference WireData when the next encode cycle runs.
func allocWireBuffer(size int) []byte {
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

// DefaultBitrateForResolution returns the minimum encoding bitrate for
// broadcast-quality output at the given resolution.
func DefaultBitrateForResolution(width, height int) int {
	return defaultBitrateForResolution(width, height)
}

// pipelineCodecs manages a shared encoder for the video processing pipeline.
// The pipeline receives raw YUV420 frames (decoded per-source by sourceDecoder)
// and encodes them to H.264 or H.265 for program output.
type pipelineCodecs struct {
	mu             sync.Mutex
	encoder        transition.VideoEncoder
	encoderFactory transition.EncoderFactory
	encWidth       int
	encHeight      int
	groupID        uint32
	closed         bool

	// Pipeline codec — AVC (H.264) or HEVC (H.265).
	// Determines wire format conversion and parameter set extraction.
	pipelineCodec PipelineCodec

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
	// Protected by pc.mu (accessed from the async encodeLoop goroutine).
	lastOutputPTS int64

	// Callback invoked when the encoder produces a keyframe with new VPS/SPS/PPS.
	// For AVC, vps is always nil.
	onVideoInfoChange func(vps, sps, pps []byte, width, height int)

	// Reusable buffer for AnnexB->wire format conversion (grows to steady state).
	wireBuf []byte

	// Last-known VPS/SPS/PPS for deduplication -- only fire callback on change.
	lastVPS []byte
	lastSPS []byte
	lastPPS []byte

	// Pre-built encoder identification SEI NALU (user_data_unregistered, payload
	// type 5). Contains the Switchframe UUID and encoder metadata string. Built
	// lazily on the first keyframe and injected into every subsequent keyframe.
	// Invalidated on resolution change (format string changes).
	identSEI []byte
}

// SetEncoderFactory replaces the encoder factory and invalidates the current
// encoder. The next encode() call will create a new encoder using the new
// factory. The mutex serializes this with any in-flight encode().
func (pc *pipelineCodecs) SetEncoderFactory(f transition.EncoderFactory) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.encoder != nil {
		pc.encoder.Close()
		pc.encoder = nil
	}
	pc.encoderFactory = f
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
// YUV420 to H.264 or H.265 (based on pipelineCodec). Lazy-initializes the
// encoder on first call.
//
// The lock is held for the entire encode to prevent invalidateEncoder() from
// closing the encoder while Encode() is in progress (use-after-free).
// Called from the async encodeLoop goroutine (one per pipeline epoch).
// During pipeline swap, old and new encode goroutines may both call encode()
// concurrently — the mutex serializes them. Transient PTS reordering during
// the swap window is handled by the monotonic PTS enforcement below.
func (pc *pipelineCodecs) encode(pf *ProcessingFrame, forceIDR bool) (*media.VideoFrame, error) {
	if len(pf.Data) == 0 {
		return nil, nil
	}

	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.closed {
		return nil, errPipelineClosed
	}

	// Invalidate encoder and ident SEI on resolution change.
	if pc.encoder != nil && (pf.Width != pc.encWidth || pf.Height != pc.encHeight) {
		pc.encoder.Close()
		pc.encoder = nil
		pc.identSEI = nil // rebuild with new dimensions
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
			return nil, fmt.Errorf("pipeline: encoder init: %w", err)
		}
		pc.encoder = enc
		pc.encWidth = pf.Width
		pc.encHeight = pf.Height
		pc.createdBitrate = bitrate
	}

	// Encode under lock — prevents invalidateEncoder() from closing the
	// encoder while Encode() is using it.
	encoded, isKeyframe, err := pc.encoder.Encode(pf.Data, pf.PTS, forceIDR)
	if err != nil {
		return nil, fmt.Errorf("pipeline: encode: %w", err)
	}
	// Hardware encoders (e.g. VideoToolbox) may return nil data during warmup
	// (EAGAIN). Return nil frame to signal "no output yet" -- not an error.
	if len(encoded) == 0 {
		return nil, nil
	}

	// Convert Annex B → wire format (4-byte length-prefixed NALUs).
	// Wire format is identical for AVC and HEVC (both use length prefixes).
	pc.wireBuf = codec.AnnexBToWireInto(encoded, pc.wireBuf[:0])
	wireData := allocWireBuffer(len(pc.wireBuf))
	copy(wireData, pc.wireBuf)

	// Update group ID state.
	if pf.GroupID > pc.groupID {
		pc.groupID = pf.GroupID
	}
	if isKeyframe {
		pc.groupID++
	}
	groupID := pc.groupID

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
		}
		// Large forward jumps (> 3 frame durations) are intentionally
		// allowed through: the new source's PTS reseeds the timeline,
		// matching the audio mixer's forward-gap behavior and preventing
		// permanent A/V desync.
	}
	// Note: no 33-bit PTS masking here. The muxer handles PTS rebasing
	// and wrapping. Masking here would put video PTS in a different
	// domain than audio PTS (which comes from the mixer unmasked),
	// causing A/V desync in the muxed output.
	pc.lastOutputPTS = outPTS

	// Set codec string based on pipeline codec setting (not the source codec).
	codecStr := "h264"
	if pc.pipelineCodec == CodecHEVC {
		codecStr = "h265"
	}

	frame := &media.VideoFrame{
		PTS:        outPTS,
		DTS:        outPTS, // No B-frames: DTS always equals PTS
		IsKeyframe: isKeyframe,
		WireData:   wireData,
		Codec:      codecStr,
		GroupID:    groupID,
	}

	if isKeyframe {
		// Extract param sets BEFORE ident SEI injection so the SEI NALU
		// doesn't interfere with SPS/PPS/VPS detection.
		pc.extractParamSets(frame, wireData)
		pc.fireVideoInfoChangeIfNeeded(frame, pf.Width, pf.Height)

		// Inject Switchframe encoder identification SEI on keyframes.
		pc.injectIdentSEI(frame, pf, codecStr)
	}

	return frame, nil
}

// injectIdentSEI builds (if needed) and injects the encoder identification
// SEI NALU into keyframe wire data. The SEI contains a user_data_unregistered
// message with the Switchframe UUID and encoder metadata, allowing downstream
// tools to identify Switchframe-encoded streams.
// Caller must hold pc.mu.
func (pc *pipelineCodecs) injectIdentSEI(frame *media.VideoFrame, pf *ProcessingFrame, codecStr string) {
	// Build the ident SEI lazily on the first keyframe.
	if pc.identSEI == nil {
		fpsNum := 30000
		fpsDen := 1001
		if pc.formatRef != nil {
			if f := pc.formatRef.Load(); f != nil {
				fpsNum = f.FPSNum
				fpsDen = f.FPSDen
			}
		}
		bitDepth := 8
		chroma := "420"
		if pc.pipelineCodec == CodecHEVC {
			// Conservative defaults; the actual profile is encoder-dependent.
			// For HEVC we may be encoding 10-bit 4:2:2 but the encoder name
			// and profile from the factory are not available here, so we use
			// the pipeline codec as the encoder name.
		}
		pc.identSEI = codec.BuildEncoderIdentSEI(codecStr, codec.EncoderIdentInfo{
			EncoderName: codecStr, // best available without factory introspection
			Profile:     codecStr,
			Width:       pf.Width,
			Height:      pf.Height,
			FPSNum:      fpsNum,
			FPSDen:      fpsDen,
			BitDepth:    bitDepth,
			Chroma:      chroma,
		})
	}

	if len(pc.identSEI) == 0 {
		return
	}

	// Convert wire → Annex B, insert SEI before VCL, convert back to wire.
	annexB := codec.WireToAnnexBInto(frame.WireData, nil)
	withIdent := caption.InsertSEIBeforeVCLInto(pc.identSEI, annexB, nil, codecStr)
	newWire := codec.AnnexBToWireInto(withIdent, nil)

	// Allocate an owned buffer for the expanded wire data.
	owned := allocWireBuffer(len(newWire))
	copy(owned, newWire)
	frame.WireData = owned
}

// extractParamSets extracts codec parameter sets from wire-format keyframe data
// and populates the VideoFrame fields. For AVC: SPS (type 7) + PPS (type 8).
// For HEVC: VPS (type 32) + SPS (type 33) + PPS (type 34).
// Caller must hold pc.mu.
func (pc *pipelineCodecs) extractParamSets(frame *media.VideoFrame, wireData []byte) {
	if pc.pipelineCodec == CodecHEVC {
		// HEVC: extract from Annex B (need to convert back from wire for the
		// param set scanner which operates on Annex B). We already have the
		// Annex B data in the encoder output, but we discarded it. Instead,
		// extract NALUs from wire format and check HEVC NAL types.
		for _, nalu := range codec.ExtractNALUs(wireData) {
			if len(nalu) < 2 {
				continue
			}
			nalType := codec.HEVCNALUType(nalu[0])
			switch nalType {
			case codec.HEVCNALVPS:
				frame.VPS = append([]byte(nil), nalu...)
			case codec.HEVCNALSPS:
				frame.SPS = append([]byte(nil), nalu...)
			case codec.HEVCNALPPS:
				frame.PPS = append([]byte(nil), nalu...)
			}
		}
	} else {
		// AVC: extract SPS (type 7) and PPS (type 8).
		for _, nalu := range codec.ExtractNALUs(wireData) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1F {
			case 7:
				frame.SPS = append([]byte(nil), nalu...)
			case 8:
				frame.PPS = append([]byte(nil), nalu...)
			}
		}
	}
}

// fireVideoInfoChangeIfNeeded checks if the parameter sets have changed and
// fires the onVideoInfoChange callback if so. Releases and re-acquires pc.mu
// around the callback to avoid holding the lock during potentially slow processing.
// Caller must hold pc.mu.
func (pc *pipelineCodecs) fireVideoInfoChangeIfNeeded(frame *media.VideoFrame, width, height int) {
	if pc.onVideoInfoChange == nil {
		return
	}
	// For AVC: require SPS + PPS. For HEVC: require VPS + SPS + PPS.
	if frame.SPS == nil || frame.PPS == nil {
		return
	}
	if pc.pipelineCodec == CodecHEVC && frame.VPS == nil {
		return
	}

	changed := !bytes.Equal(frame.SPS, pc.lastSPS) ||
		!bytes.Equal(frame.PPS, pc.lastPPS) ||
		!bytes.Equal(frame.VPS, pc.lastVPS)

	if !changed {
		return
	}

	pc.lastVPS = append(pc.lastVPS[:0], frame.VPS...)
	pc.lastSPS = append(pc.lastSPS[:0], frame.SPS...)
	pc.lastPPS = append(pc.lastPPS[:0], frame.PPS...)

	// Release lock before callback to avoid holding it during
	// potentially slow onVideoInfoChange processing.
	pc.mu.Unlock()
	pc.onVideoInfoChange(frame.VPS, frame.SPS, frame.PPS, width, height)
	pc.mu.Lock()
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
