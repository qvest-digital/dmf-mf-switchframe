package switcher

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

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

	// Source-derived encoder parameters (updated via updateSourceStats).
	sourceBitrate int     // estimated bitrate from program source (bytes/sec * 8)
	sourceFPS     float32 // estimated FPS from program source

	// Callback invoked when the encoder produces a keyframe with new SPS/PPS.
	onVideoInfoChange func(sps, pps []byte, width, height int)

	// Last-known SPS/PPS for deduplication — only fire callback on change.
	lastSPS []byte
	lastPPS []byte
}
// decode converts a media.VideoFrame to a ProcessingFrame by decoding H.264
// to raw YUV420. Lazy-initializes the decoder on the first keyframe.
func (pc *pipelineCodecs) decode(frame *media.VideoFrame) (*ProcessingFrame, error) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.decoder == nil {
		if !frame.IsKeyframe {
			return nil, fmt.Errorf("pipeline: need keyframe to init decoder")
		}
		dec, err := pc.decoderFactory()
		if err != nil {
			return nil, fmt.Errorf("pipeline: decoder init: %w", err)
		}
		pc.decoder = dec
	}

	annexB := codec.AVC1ToAnnexB(frame.WireData)
	if frame.IsKeyframe && len(frame.SPS) > 0 {
		var buf []byte
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, frame.SPS...)
		buf = append(buf, 0x00, 0x00, 0x00, 0x01)
		buf = append(buf, frame.PPS...)
		buf = append(buf, annexB...)
		annexB = buf
	}

	yuv, w, h, err := pc.decoder.Decode(annexB)
	if err != nil {
		return nil, fmt.Errorf("pipeline: decode: %w", err)
	}

	yuvSize := w * h * 3 / 2
	if len(yuv) < yuvSize {
		return nil, fmt.Errorf("pipeline: decoder buffer too small: got %d, need %d", len(yuv), yuvSize)
	}
	yuvCopy := make([]byte, yuvSize)
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
func (pc *pipelineCodecs) encode(pf *ProcessingFrame, forceIDR bool) (*media.VideoFrame, error) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

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
			return nil, fmt.Errorf("pipeline: encoder init: %w", err)
		}
		pc.encoder = enc
		pc.encWidth = pf.Width
		pc.encHeight = pf.Height
	}

	encoded, isKeyframe, err := pc.encoder.Encode(pf.YUV, forceIDR)
	if err != nil {
		return nil, fmt.Errorf("pipeline: encode: %w", err)
	}

	if pf.GroupID > pc.groupID {
		pc.groupID = pf.GroupID
	}
	if isKeyframe {
		pc.groupID++
	}

	avc1 := codec.AnnexBToAVC1(encoded)
	frame := &media.VideoFrame{
		PTS:        pf.PTS,
		DTS:        pf.DTS,
		IsKeyframe: isKeyframe,
		WireData:   avc1,
		Codec:      pf.Codec,
		GroupID:    pc.groupID,
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
			if !bytes.Equal(frame.SPS, pc.lastSPS) || !bytes.Equal(frame.PPS, pc.lastPPS) {
				pc.lastSPS = append(pc.lastSPS[:0], frame.SPS...)
				pc.lastPPS = append(pc.lastPPS[:0], frame.PPS...)
				pc.onVideoInfoChange(frame.SPS, frame.PPS, pc.encWidth, pc.encHeight)
			}
		}
	}

	return frame, nil
}

// updateSourceStats propagates the program source's estimated bitrate and FPS
// to the encoder. These are used when the encoder is (re)created.
func (pc *pipelineCodecs) updateSourceStats(avgFrameSize float64, avgFPS float64) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if avgFPS > 0 {
		pc.sourceBitrate = int(avgFrameSize * avgFPS * 8)
		pc.sourceFPS = float32(avgFPS)
	}
}

// close releases decoder and encoder resources.
func (pc *pipelineCodecs) close() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.decoder != nil {
		pc.decoder.Close()
		pc.decoder = nil
	}
	if pc.encoder != nil {
		pc.encoder.Close()
		pc.encoder = nil
	}
}
