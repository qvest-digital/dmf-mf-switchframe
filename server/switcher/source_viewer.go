package switcher

import (
	"sync/atomic"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

// frameHandler receives tagged frames from a sourceViewer. The Switcher
// implements this interface so that each source's frames arrive with the
// originating source key attached.
type frameHandler interface {
	handleVideoFrame(sourceKey string, frame *media.VideoFrame)
	handleAudioFrame(sourceKey string, frame *media.AudioFrame)
	handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame)
}

// sourceViewer implements distribution.Viewer and acts as a proxy that
// subscribes to a single source's Relay. Every frame received via
// SendVideo/SendAudio is forwarded to the central frameHandler with the
// source key attached, allowing the Switcher to know which source produced
// each frame.
type sourceViewer struct {
	sourceKey   string
	handler     frameHandler
	videoSent   atomic.Int64
	audioSent   atomic.Int64
	captionSent atomic.Int64
}

// Compile-time check that sourceViewer satisfies the Viewer interface.
var _ distribution.Viewer = (*sourceViewer)(nil)

// newSourceViewer creates a sourceViewer for the given source key that
// forwards all received frames to handler.
func newSourceViewer(sourceKey string, handler frameHandler) *sourceViewer {
	return &sourceViewer{
		sourceKey: sourceKey,
		handler:   handler,
	}
}

// ID returns a unique viewer identifier prefixed with "switchframe:".
func (sv *sourceViewer) ID() string {
	return "switchframe:" + sv.sourceKey
}

// SendVideo forwards a video frame to the handler tagged with the source key.
func (sv *sourceViewer) SendVideo(frame *media.VideoFrame) {
	sv.videoSent.Add(1)
	sv.handler.handleVideoFrame(sv.sourceKey, frame)
}

// SendAudio forwards an audio frame to the handler tagged with the source key.
func (sv *sourceViewer) SendAudio(frame *media.AudioFrame) {
	sv.audioSent.Add(1)
	sv.handler.handleAudioFrame(sv.sourceKey, frame)
}

// SendCaptions forwards a caption frame to the handler tagged with the source key.
func (sv *sourceViewer) SendCaptions(frame *ccx.CaptionFrame) {
	sv.captionSent.Add(1)
	sv.handler.handleCaptionFrame(sv.sourceKey, frame)
}

// Stats returns delivery metrics for this source viewer.
func (sv *sourceViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{
		ID:        sv.ID(),
		VideoSent: sv.videoSent.Load(),
		AudioSent: sv.audioSent.Load(),
	}
}
