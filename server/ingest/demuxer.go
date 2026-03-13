package ingest

import (
	"context"
	"io"
	"log/slog"

	"github.com/zsiec/prism/demux"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

// Broadcaster receives demuxed frames from the streaming demuxer.
type Broadcaster interface {
	BroadcastVideo(frame *media.VideoFrame)
	BroadcastAudio(frame *media.AudioFrame)
}

// StreamDemuxer continuously reads MPEG-TS from an io.Reader and
// broadcasts parsed video/audio frames to a Broadcaster (typically
// a distribution.Relay). Uses Prism's demux.Demuxer for correct
// MPEG-TS parsing (ADTS, H.264/H.265, SPS/PPS, captions).
type StreamDemuxer struct {
	key         string
	reader      io.Reader
	broadcaster Broadcaster
	log         *slog.Logger
	annexBBuf   []byte // reusable buffer for Annex B flattening (avoids alloc per frame)
}

// NewStreamDemuxer creates a demuxer that reads MPEG-TS from reader and
// broadcasts frames via bc. The key is used for logging.
func NewStreamDemuxer(key string, reader io.Reader, bc Broadcaster) *StreamDemuxer {
	return &StreamDemuxer{
		key:         key,
		reader:      reader,
		broadcaster: bc,
		log:         slog.With("component", "ingest", "key", key),
	}
}

// Run reads MPEG-TS packets continuously until EOF or context cancellation.
// Returns nil on clean shutdown (EOF or context cancelled).
func (d *StreamDemuxer) Run(ctx context.Context) error {
	dmx := demux.NewDemuxer(d.reader, d.log)

	// Run the demuxer in a background goroutine. It closes its output
	// channels on return, which terminates our select loop below.
	errCh := make(chan error, 1)
	go func() {
		errCh <- dmx.Run(ctx)
	}()

	videoCh := dmx.Video()
	audioCh := dmx.Audio()
	captionCh := dmx.Captions()

	for videoCh != nil || audioCh != nil || captionCh != nil {
		select {
		case frame, ok := <-videoCh:
			if !ok {
				videoCh = nil
				continue
			}
			d.convertAndBroadcastVideo(frame)
		case frame, ok := <-audioCh:
			if !ok {
				audioCh = nil
				continue
			}
			d.broadcaster.BroadcastAudio(frame)
		case _, ok := <-captionCh:
			if !ok {
				captionCh = nil
				continue
			}
			// Drain captions to prevent blocking the demuxer goroutine.
			// Caption data flows via SEI NALUs in the video stream instead.
		case <-ctx.Done():
			return nil
		}
	}

	// Demuxer channels closed -- wait for Run() to return.
	if err := <-errCh; err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// convertAndBroadcastVideo converts Prism's Annex B NALUs to AVC1 WireData
// format expected by Switchframe's sourceViewer/sourceDecoder pipeline.
// SPS/PPS NALUs are stripped for both H.264 and H.265 since they're already
// in the separate frame fields — including them would cause duplication when
// sourceDecoder prepends them on keyframes. VPS is kept inline for H.265
// because sourceDecoder doesn't re-inject it.
func (d *StreamDemuxer) convertAndBroadcastVideo(frame *media.VideoFrame) {
	if len(frame.NALUs) == 0 {
		return
	}

	// Flatten Annex B NALUs into a single byte stream, skipping parameter sets.
	// Each NALU in frame.NALUs is [0x00 0x00 0x00 0x01 | data].
	d.annexBBuf = d.annexBBuf[:0]
	for _, n := range frame.NALUs {
		if len(n) > 4 && isParameterSetNALU(n[4], frame.Codec) {
			continue
		}
		d.annexBBuf = append(d.annexBBuf, n...)
	}

	if len(d.annexBBuf) == 0 {
		return // no slice NALUs after stripping parameter sets
	}

	frame.WireData = codec.AnnexBToAVC1(d.annexBBuf)
	d.broadcaster.BroadcastVideo(frame)
}

// isParameterSetNALU returns true if the first byte of a NALU (after the
// start code) indicates a parameter set that should be stripped from WireData.
// Only strips what sourceDecoder re-injects (SPS + PPS). VPS is NOT stripped
// for H.265 because sourceDecoder.PrependSPSPPSInto doesn't re-inject it —
// stripping VPS would lose it entirely, and HEVC decoders require VPS before
// SPS to initialize.
//
// H.264: NAL type = byte & 0x1F → SPS=7, PPS=8
// H.265: NAL type = (byte >> 1) & 0x3F → SPS=33, PPS=34 (VPS=32 kept inline)
func isParameterSetNALU(firstByte byte, codec string) bool {
	if codec == "h265" {
		nalType := (firstByte >> 1) & 0x3F
		return nalType == 33 || nalType == 34 // SPS, PPS only — VPS stays inline
	}
	nalType := firstByte & 0x1F
	return nalType == 7 || nalType == 8 // SPS, PPS
}
