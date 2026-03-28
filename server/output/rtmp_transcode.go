package output

import (
	"fmt"
	"sync"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// rtmpTranscoder decodes HEVC frames and re-encodes them as H.264 for RTMP
// output. Standard RTMP only supports H.264; Enhanced RTMP for HEVC is not
// widely supported by ingest endpoints (YouTube, Twitch, Facebook).
//
// The transcoder is created lazily when the first HEVC frame arrives at the
// RTMP adapter. It is destroyed when the RTMP adapter is closed.
type rtmpTranscoder struct {
	mu      sync.Mutex
	decoder transition.VideoDecoder // HEVC -> YUV420P 8-bit
	encoder transition.VideoEncoder // YUV420P -> H.264
	closed  bool

	// Cached H.264 parameter sets from the most recent keyframe.
	lastSPS []byte
	lastPPS []byte
}

// newRTMPTranscoder creates a new transcoder with pre-created decoder and encoder.
func newRTMPTranscoder(decoder transition.VideoDecoder, encoder transition.VideoEncoder) *rtmpTranscoder {
	return &rtmpTranscoder{
		decoder: decoder,
		encoder: encoder,
	}
}

// Transcode decodes an HEVC frame to YUV420P and re-encodes it as H.264.
// The returned VideoFrame has Codec="h264" and AVC1-format WireData.
func (t *rtmpTranscoder) Transcode(frame *media.VideoFrame) (*media.VideoFrame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, fmt.Errorf("rtmp transcoder: closed")
	}

	// 1. Convert HEVC wire data (HVC1 length-prefixed) to Annex B for the decoder.
	annexB := codec.WireToAnnexB(frame.WireData)

	// Prepend VPS/SPS/PPS on keyframes so the decoder can initialize.
	if frame.IsKeyframe && len(frame.VPS) > 0 && len(frame.SPS) > 0 && len(frame.PPS) > 0 {
		annexB = codec.PrependVPSSPSPPS(frame.VPS, frame.SPS, frame.PPS, annexB)
	}

	// 2. Decode HEVC -> YUV420P.
	yuv, _, _, err := t.decoder.Decode(annexB)
	if err != nil {
		return nil, fmt.Errorf("rtmp transcoder: decode HEVC: %w", err)
	}
	if yuv == nil {
		// Decoder is buffering (B-frame reorder delay). Skip this frame.
		return nil, fmt.Errorf("rtmp transcoder: decoder buffering")
	}

	// 3. Encode YUV420P -> H.264 (Annex B).
	h264AnnexB, isKeyframe, err := t.encoder.Encode(yuv, frame.PTS, frame.IsKeyframe)
	if err != nil {
		return nil, fmt.Errorf("rtmp transcoder: encode H.264: %w", err)
	}

	// 4. Extract SPS/PPS from H.264 keyframes.
	var sps, pps []byte
	if isKeyframe {
		sps, pps = extractH264SPSPPS(h264AnnexB)
		if sps != nil {
			t.lastSPS = append(t.lastSPS[:0], sps...)
		}
		if pps != nil {
			t.lastPPS = append(t.lastPPS[:0], pps...)
		}
	}

	// Use cached SPS/PPS if this keyframe didn't have them inline.
	if sps == nil && len(t.lastSPS) > 0 {
		sps = t.lastSPS
	}
	if pps == nil && len(t.lastPPS) > 0 {
		pps = t.lastPPS
	}

	// 5. Convert Annex B -> AVC1 wire format (length-prefixed NALUs).
	wire := codec.AnnexBToAVC1(h264AnnexB)

	return &media.VideoFrame{
		PTS:        frame.PTS,
		DTS:        frame.DTS,
		IsKeyframe: isKeyframe,
		Codec:      "h264",
		SPS:        sps,
		PPS:        pps,
		WireData:   wire,
	}, nil
}

// Close releases the decoder and encoder resources.
func (t *rtmpTranscoder) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}
	t.closed = true

	if t.decoder != nil {
		t.decoder.Close()
	}
	if t.encoder != nil {
		t.encoder.Close()
	}
}

// extractH264SPSPPS scans Annex B data for SPS (NALU type 7) and PPS (type 8).
// Returns the raw NALU bytes (without the start code prefix) for each, or nil
// if not found.
func extractH264SPSPPS(annexB []byte) (sps, pps []byte) {
	nalus := splitAnnexBNALUs(annexB)
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		naluType := nalu[0] & 0x1F
		switch naluType {
		case 7: // SPS
			sps = make([]byte, len(nalu))
			copy(sps, nalu)
		case 8: // PPS
			pps = make([]byte, len(nalu))
			copy(pps, nalu)
		}
	}
	return
}

// splitAnnexBNALUs splits Annex B byte stream into individual NALUs.
// Each NALU is returned without its start code prefix (00 00 00 01 or 00 00 01).
func splitAnnexBNALUs(data []byte) [][]byte {
	var nalus [][]byte
	start := -1

	for i := 0; i < len(data); i++ {
		// Look for 00 00 01 or 00 00 00 01 start codes.
		if i+2 < len(data) && data[i] == 0 && data[i+1] == 0 {
			isStart3 := data[i+2] == 1
			isStart4 := i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1

			if isStart3 || isStart4 {
				// End the previous NALU.
				if start >= 0 {
					nalus = append(nalus, data[start:i])
				}

				if isStart4 {
					start = i + 4
					i += 3
				} else {
					start = i + 3
					i += 2
				}
			}
		}
	}

	// Append the last NALU.
	if start >= 0 && start < len(data) {
		nalus = append(nalus, data[start:])
	}

	return nalus
}
