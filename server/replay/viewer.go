package replay

import (
	"sync/atomic"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

// replayViewer implements distribution.Viewer and records video frames
// into a replayBuffer. Registered on each source relay alongside the
// switcher's sourceViewer. Audio frames are recorded for replay audio output.
type replayViewer struct {
	sourceKey string
	buffer    *replayBuffer

	videoSent   atomic.Int64
	audioSent   atomic.Int64
	captionSent atomic.Int64
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

// SendAudio records the audio frame into the replay buffer for audio playback.
func (v *replayViewer) SendAudio(frame *media.AudioFrame) {
	v.audioSent.Add(1)
	v.buffer.RecordAudioFrame(frame)
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
		AudioSent: v.audioSent.Load(),
	}
}

// Compile-time check that replayViewer satisfies the Viewer interface.
var _ distribution.Viewer = (*replayViewer)(nil)
