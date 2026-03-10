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
	handleRawVideoFrame(sourceKey string, pf *ProcessingFrame)
	handleAudioFrame(sourceKey string, frame *media.AudioFrame)
	handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame)
}

// sourceViewer implements distribution.Viewer and acts as a proxy that
// subscribes to a single source's Relay. Every frame received via
// SendVideo/SendAudio is forwarded to the central frameHandler with the
// source key attached, allowing the Switcher to know which source produced
// each frame. If a delayBuffer is set, frames are routed through it
// for configurable per-source lip-sync delay. If frameSync is set,
// frames are routed through the FrameSynchronizer instead (bypassing
// the delay buffer) for freerun frame alignment.
//
// delayBuffer and frameSync are atomic.Pointer fields so that the Switcher
// can safely toggle them from SetFrameSync (which holds the write lock but
// runs concurrently with frame-delivery goroutines that only hold the read
// lock or no lock at all).
type sourceViewer struct {
	sourceKey   string
	id          string
	handler     frameHandler
	delayBuffer atomic.Pointer[DelayBuffer]
	frameSync   atomic.Pointer[FrameSynchronizer]
	srcDecoder  atomic.Pointer[sourceDecoder]
	videoSent atomic.Int64
	_pad1     [56]byte //lint:ignore U1000 cache line padding between atomic counters
	audioSent atomic.Int64
	_pad2     [56]byte //lint:ignore U1000 cache line padding between atomic counters
	captionSent atomic.Int64
}

// Compile-time check that sourceViewer satisfies the Viewer interface.
var _ distribution.Viewer = (*sourceViewer)(nil)

// newSourceViewer creates a sourceViewer for the given source key that
// forwards all received frames to handler.
func newSourceViewer(sourceKey string, handler frameHandler) *sourceViewer {
	return &sourceViewer{
		sourceKey: sourceKey,
		id:        "switchframe:" + sourceKey,
		handler:   handler,
	}
}

// ID returns a unique viewer identifier prefixed with "switchframe:".
func (sv *sourceViewer) ID() string {
	return sv.id
}

// SendVideo forwards a video frame to the handler tagged with the source key.
// When a sourceDecoder is set (always-decode mode), the H.264 frame is sent
// to the decoder goroutine; decoded YUV frames flow through frameSync/delay
// buffer/handler via the sourceDecoder callback. Otherwise, the legacy H.264
// path is used: frame sync → delay buffer → handler.
func (sv *sourceViewer) SendVideo(frame *media.VideoFrame) {
	sv.videoSent.Add(1)
	// Always-decode path: H.264 → sourceDecoder → callback → raw video pipeline
	if dec := sv.srcDecoder.Load(); dec != nil {
		dec.Send(frame)
		return
	}
	// Legacy H.264 path
	if fs := sv.frameSync.Load(); fs != nil {
		fs.IngestVideo(sv.sourceKey, frame)
		return
	}
	if db := sv.delayBuffer.Load(); db != nil {
		db.handleVideoFrame(sv.sourceKey, frame)
		return
	}
	sv.handler.handleVideoFrame(sv.sourceKey, frame)
}

// SendAudio forwards an audio frame to the handler tagged with the source key.
// Audio always bypasses the FrameSynchronizer — audio is a continuous sample
// stream (48 kHz / ~47 AAC frames/sec) that must not be quantized to the
// video tick clock (30 fps). Routing audio through the frame sync batches
// delivery to tick boundaries, causing bursty timing that downstream audio
// schedulers (browser AudioContext) cannot absorb cleanly.
// If a delay buffer is configured, frames route through it for lip-sync delay.
func (sv *sourceViewer) SendAudio(frame *media.AudioFrame) {
	sv.audioSent.Add(1)
	if db := sv.delayBuffer.Load(); db != nil {
		db.handleAudioFrame(sv.sourceKey, frame)
		return
	}
	sv.handler.handleAudioFrame(sv.sourceKey, frame)
}

// SendCaptions forwards a caption frame to the handler tagged with the source key.
// If a delay buffer is configured, the frame is routed through it.
// Note: captions are not frame-synced — they pass through directly.
func (sv *sourceViewer) SendCaptions(frame *ccx.CaptionFrame) {
	sv.captionSent.Add(1)
	if db := sv.delayBuffer.Load(); db != nil {
		db.handleCaptionFrame(sv.sourceKey, frame)
		return
	}
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
