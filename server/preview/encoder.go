// Package preview provides per-source preview encoding for browser multiview.
//
// Each source gets its own Encoder goroutine that scales incoming raw YUV420
// frames to a lower resolution and encodes them with a lightweight x264 preset.
// The encoded H.264 stream is broadcast to a per-source MoQ relay so browsers
// can subscribe to individual preview feeds.
//
// The Encoder uses a newest-wins drop policy: if the encode goroutine falls
// behind, older frames are silently discarded so the preview always shows the
// most recent frame. This is the correct behavior for a monitoring preview --
// latency matters more than every-frame delivery.
package preview

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/prism/moq"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// Relay is the subset of distribution.Relay used by the preview encoder.
type Relay interface {
	BroadcastVideo(frame *media.VideoFrame)
	BroadcastAudio(frame *media.AudioFrame)
	SetVideoInfo(info distribution.VideoInfo)
}

// extradataProvider is an optional interface for encoders that store SPS/PPS
// in extradata (via AV_CODEC_FLAG_GLOBAL_HEADER) rather than inline in the
// encoded bitstream. The preview encoder uses this flag for downstream muxer
// compatibility, so we extract SPS/PPS from extradata after encoder creation.
type extradataProvider interface {
	Extradata() []byte
}

// Config configures a preview encoder instance.
type Config struct {
	SourceKey string // Source identifier (e.g. "cam1", "srt:feed1")
	Width     int    // Preview output width (e.g. 854)
	Height    int    // Preview output height (e.g. 480)
	Bitrate   int    // Target bitrate in bps (e.g. 500_000)
	FPSNum    int    // Frame rate numerator (e.g. 30)
	FPSDen    int    // Frame rate denominator (e.g. 1)
	Relay     Relay  // MoQ relay for broadcast
}

// encodeJob carries a YUV420 frame to the encode goroutine.
type encodeJob struct {
	yuv []byte
	w   int
	h   int
	pts int64
}

// Encoder scales and encodes preview frames for a single source.
// Each source gets its own Encoder instance -- they run completely
// independently with no shared state.
type Encoder struct {
	cfg      Config
	ch       chan encodeJob
	done     chan struct{}
	stopOnce sync.Once
}

// NewEncoder creates and starts a preview encoder goroutine.
// The goroutine runs until Stop() is called.
func NewEncoder(cfg Config) (*Encoder, error) {
	e := &Encoder{
		cfg:  cfg,
		ch:   make(chan encodeJob, 2),
		done: make(chan struct{}),
	}
	go e.loop()
	return e, nil
}

// Send submits a raw YUV420 frame for preview encoding.
// Non-blocking with newest-wins drop policy: if the channel is full,
// the oldest frame is drained and replaced with the new one.
// The YUV data is deep-copied so the caller can reuse or release the buffer.
func (e *Encoder) Send(yuv []byte, w, h int, pts int64) {
	// Deep copy -- caller may reuse the buffer.
	cp := make([]byte, len(yuv))
	copy(cp, yuv)

	job := encodeJob{yuv: cp, w: w, h: h, pts: pts}

	// Try non-blocking send.
	select {
	case e.ch <- job:
		return
	default:
	}

	// Channel full -- drain one (newest-wins) and send.
	select {
	case <-e.ch:
	default:
	}
	select {
	case e.ch <- job:
	default:
		// Channel closed or race -- drop silently.
	}
}

// Stop signals the encode goroutine to exit and waits for it to finish.
// Safe to call multiple times.
func (e *Encoder) Stop() {
	e.stopOnce.Do(func() {
		close(e.ch)
	})
	<-e.done
}

// extractSPSPPSFromExtradata parses SPS and PPS NALUs from encoder extradata.
// The preview encoder uses AV_CODEC_FLAG_GLOBAL_HEADER, which stores SPS/PPS
// in extradata rather than inline in the encoded bitstream. The extradata is
// in Annex B format (start-code prefixed NALUs).
func extractSPSPPSFromExtradata(extradata []byte) (sps, pps []byte) {
	if len(extradata) == 0 {
		return nil, nil
	}
	// Convert Annex B extradata to AVC1, then extract NALUs.
	avc1 := codec.AnnexBToAVC1(extradata)
	for _, nalu := range codec.ExtractNALUs(avc1) {
		if len(nalu) == 0 {
			continue
		}
		switch nalu[0] & 0x1F {
		case 7: // SPS
			sps = make([]byte, len(nalu))
			copy(sps, nalu)
		case 8: // PPS
			pps = make([]byte, len(nalu))
			copy(pps, nalu)
		}
	}
	return sps, pps
}

// loop is the encode goroutine. It reads from the channel, scales, encodes,
// and broadcasts to the relay. All encoder/buffer state is goroutine-local.
func (e *Encoder) loop() {
	defer close(e.done)

	var (
		encoder   transition.VideoEncoder
		groupID   atomic.Uint32
		infoSent  bool
		scaledYUV []byte // persistent scale buffer (reused across frames)
		encYUV    []byte // persistent encoder input buffer (reused)
		lastSrcW  int
		lastSrcH  int
		sps       []byte // cached SPS from extradata
		pps       []byte // cached PPS from extradata
	)

	targetW := e.cfg.Width
	targetH := e.cfg.Height
	targetSize := targetW * targetH * 3 / 2

	for job := range e.ch {
		w, h, pts := job.w, job.h, job.pts

		// Source resolution changed -- recreate encoder.
		if encoder != nil && (w != lastSrcW || h != lastSrcH) {
			encoder.Close()
			encoder = nil
			infoSent = false
			sps = nil
			pps = nil
		}
		lastSrcW = w
		lastSrcH = h

		// Lazy encoder creation.
		if encoder == nil {
			enc, err := codec.NewPreviewEncoder(targetW, targetH, e.cfg.Bitrate, e.cfg.FPSNum, e.cfg.FPSDen)
			if err != nil {
				slog.Error("preview: encoder init failed",
					"key", e.cfg.SourceKey, "error", err)
				continue
			}
			encoder = enc

			// Extract SPS/PPS from extradata. The preview encoder uses
			// AV_CODEC_FLAG_GLOBAL_HEADER which stores parameter sets in
			// extradata rather than inline in the Annex B output.
			if ep, ok := enc.(extradataProvider); ok {
				sps, pps = extractSPSPPSFromExtradata(ep.Extradata())
			}
		}

		// Scale to target resolution if needed.
		var frameYUV []byte
		if w == targetW && h == targetH {
			// No scaling needed -- use directly.
			frameYUV = job.yuv
		} else {
			// Grow scaledYUV buffer if needed (reused across frames).
			if cap(scaledYUV) < targetSize {
				scaledYUV = make([]byte, targetSize)
			}
			scaledYUV = scaledYUV[:targetSize]
			transition.ScaleYUV420(job.yuv, w, h, scaledYUV, targetW, targetH)
			frameYUV = scaledYUV
		}

		// Copy into encoder buffer (encoder may hold a reference).
		if cap(encYUV) < targetSize {
			encYUV = make([]byte, targetSize)
		}
		encYUV = encYUV[:targetSize]
		copy(encYUV, frameYUV)

		// Encode.
		encoded, isKeyframe, err := encoder.Encode(encYUV, pts, false)
		if err != nil || len(encoded) == 0 {
			continue
		}

		// On keyframes, prepend SPS/PPS from extradata to the Annex B
		// output. The preview encoder uses AV_CODEC_FLAG_GLOBAL_HEADER
		// which strips SPS/PPS from the bitstream.
		if isKeyframe && sps != nil && pps != nil {
			encoded = codec.PrependSPSPPS(sps, pps, encoded)
		}

		// Convert Annex B -> AVC1 for MoQ wire format.
		avc1 := codec.AnnexBToAVC1(encoded)
		if len(avc1) == 0 {
			continue
		}

		if isKeyframe {
			groupID.Add(1)
		}

		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
			GroupID:     groupID.Load(),
		}

		// Extract SPS/PPS from AVC1 data for the frame metadata.
		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7: // SPS
					frame.SPS = nalu
				case 8: // PPS
					frame.PPS = nalu
				}
			}

			// Set VideoInfo on first keyframe so browsers can configure decoder.
			if !infoSent && frame.SPS != nil && frame.PPS != nil {
				avcC := moq.BuildAVCDecoderConfig(frame.SPS, frame.PPS)
				if avcC != nil {
					e.cfg.Relay.SetVideoInfo(distribution.VideoInfo{
						Codec:         codec.ParseSPSCodecString(frame.SPS),
						Width:         targetW,
						Height:        targetH,
						DecoderConfig: avcC,
					})
					slog.Info("preview: VideoInfo set",
						"key", e.cfg.SourceKey,
						"width", targetW,
						"height", targetH,
					)
					infoSent = true
				}
			}
		}

		e.cfg.Relay.BroadcastVideo(frame)
	}

	// Channel closed -- clean up encoder.
	if encoder != nil {
		encoder.Close()
	}
}
