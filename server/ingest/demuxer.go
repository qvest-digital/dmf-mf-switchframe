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

	for videoCh != nil || audioCh != nil {
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
func (d *StreamDemuxer) convertAndBroadcastVideo(frame *media.VideoFrame) {
	if len(frame.NALUs) == 0 {
		return
	}

	// Flatten Annex B NALUs into a single byte stream, then convert to AVC1.
	// Each NALU in frame.NALUs is [0x00 0x00 0x00 0x01 | data].
	var size int
	for _, n := range frame.NALUs {
		size += len(n)
	}
	annexB := make([]byte, 0, size)
	for _, n := range frame.NALUs {
		annexB = append(annexB, n...)
	}
	frame.WireData = codec.AnnexBToAVC1(annexB)

	d.broadcaster.BroadcastVideo(frame)
}
