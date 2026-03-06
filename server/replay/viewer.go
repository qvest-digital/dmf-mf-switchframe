package replay

import (
	"sync/atomic"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

// replayViewer implements distribution.Viewer and records video frames
// into a replayBuffer. Registered on each source relay alongside the
// switcher's sourceViewer. Audio and captions are silently ignored
// (replay audio is muted in v1).
type replayViewer struct {
	sourceKey string
	buffer    *replayBuffer

	videoSent    atomic.Int64
	audioDropped atomic.Int64
	captionSent  atomic.Int64
}

// newReplayViewer creates a viewer that feeds frames into the given buffer.
func newReplayViewer(sourceKey string, buffer *replayBuffer) *replayViewer {
	return &replayViewer{
		sourceKey: sourceKey,
		buffer:    buffer,
	}
}

// ID returns the viewer identifier. Format: "replay:{sourceKey}".
func (v *replayViewer) ID() string {
	return "replay:" + v.sourceKey
}

// SendVideo records the frame into the replay buffer. This is a pure
// memory operation (deep copy) and never blocks the relay broadcast.
func (v *replayViewer) SendVideo(frame *media.VideoFrame) {
	v.videoSent.Add(1)
	v.buffer.RecordFrame(frame)
}

// SendAudio is a no-op. Replay audio is muted at <1x speed.
func (v *replayViewer) SendAudio(_ *media.AudioFrame) {
	v.audioDropped.Add(1)
}

// SendCaptions is a no-op.
func (v *replayViewer) SendCaptions(_ *ccx.CaptionFrame) {
	v.captionSent.Add(1)
}

// Stats returns delivery metrics for this viewer.
func (v *replayViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{
		ID:        v.ID(),
		VideoSent: v.videoSent.Load(),
		// AudioSent reports dropped audio frames (replay discards all audio).
		AudioSent: v.audioDropped.Load(),
	}
}

// Compile-time check that replayViewer satisfies the Viewer interface.
var _ distribution.Viewer = (*replayViewer)(nil)
