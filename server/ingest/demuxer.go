package ingest

import (
	"context"
	"io"

	"github.com/zsiec/prism/media"
)

// Broadcaster receives demuxed frames from the streaming demuxer.
type Broadcaster interface {
	BroadcastVideo(frame *media.VideoFrame)
	BroadcastAudio(frame *media.AudioFrame)
}

// StreamDemuxer continuously reads MPEG-TS from an io.Reader and
// broadcasts parsed video/audio frames to a Broadcaster (typically
// a distribution.Relay).
type StreamDemuxer struct {
	key         string
	reader      io.Reader
	broadcaster Broadcaster
}

// NewStreamDemuxer creates a demuxer that reads MPEG-TS from reader and
// broadcasts frames via bc. The key is used for logging.
func NewStreamDemuxer(key string, reader io.Reader, bc Broadcaster) *StreamDemuxer {
	return &StreamDemuxer{
		key:         key,
		reader:      reader,
		broadcaster: bc,
	}
}

// Run reads MPEG-TS packets continuously until EOF or context cancellation.
// Returns nil on clean shutdown (EOF or context cancelled).
func (d *StreamDemuxer) Run(ctx context.Context) error {
	return nil // stub
}
