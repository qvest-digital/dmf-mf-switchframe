package output

import (
	"log/slog"
	"sync/atomic"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

const (
	// videoChSize is the buffered channel capacity for video frames.
	// At 30 fps this provides ~3 seconds of buffer.
	videoChSize = 100
	// audioChSize is the buffered channel capacity for audio frames.
	// At ~43 fps (AAC) this provides ~2 seconds of buffer.
	audioChSize = 100
	// captionChSize is the buffered channel capacity for caption frames.
	captionChSize = 32
)

// OutputViewer implements distribution.Viewer and sits on the program relay.
// It receives video, audio, and caption frames via buffered channels and
// feeds them to the TSMuxer in a separate goroutine, avoiding blocking the
// relay's broadcast calls.
type OutputViewer struct {
	videoCh   chan *media.VideoFrame
	audioCh   chan *media.AudioFrame
	captionCh chan *ccx.CaptionFrame

	videoSent   atomic.Int64
	audioSent   atomic.Int64
	captionSent atomic.Int64

	videoDropped   atomic.Int64
	audioDropped   atomic.Int64
	captionDropped atomic.Int64

	muxer    *TSMuxer
	onVideo  func(*media.VideoFrame) // optional callback for confidence monitor
	stopCh   chan struct{}
	done     chan struct{}
}

// NewOutputViewer creates an OutputViewer that feeds frames to the given
// TSMuxer. The optional onVideo callback is invoked for each video frame
// after muxing (used by the confidence monitor). It must be set at
// construction time; it is read without synchronization in Run().
func NewOutputViewer(muxer *TSMuxer, onVideo func(*media.VideoFrame)) *OutputViewer {
	return &OutputViewer{
		videoCh:   make(chan *media.VideoFrame, videoChSize),
		audioCh:   make(chan *media.AudioFrame, audioChSize),
		captionCh: make(chan *ccx.CaptionFrame, captionChSize),
		muxer:     muxer,
		onVideo:   onVideo,
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// ID returns the viewer identifier.
func (v *OutputViewer) ID() string {
	return "switchframe:output"
}

// SendVideo enqueues a video frame. Non-blocking: drops the frame if the
// channel is full to avoid stalling the relay broadcast.
func (v *OutputViewer) SendVideo(frame *media.VideoFrame) {
	v.videoSent.Add(1)
	select {
	case v.videoCh <- frame:
	default:
		v.videoDropped.Add(1)
		slog.Warn("output viewer dropped video frame", "pts", frame.PTS)
	}
}

// SendAudio enqueues an audio frame. Non-blocking: drops the frame if the
// channel is full.
func (v *OutputViewer) SendAudio(frame *media.AudioFrame) {
	v.audioSent.Add(1)
	select {
	case v.audioCh <- frame:
	default:
		v.audioDropped.Add(1)
		slog.Warn("output viewer dropped audio frame", "pts", frame.PTS)
	}
}

// SendCaptions enqueues a caption frame. Non-blocking: drops the frame if
// the channel is full.
func (v *OutputViewer) SendCaptions(frame *ccx.CaptionFrame) {
	v.captionSent.Add(1)
	select {
	case v.captionCh <- frame:
	default:
		v.captionDropped.Add(1)
		slog.Warn("output viewer dropped caption frame", "pts", frame.PTS)
	}
}

// Stats returns delivery metrics for this viewer.
func (v *OutputViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{
		ID:             v.ID(),
		VideoSent:      v.videoSent.Load(),
		AudioSent:      v.audioSent.Load(),
		CaptionSent:    v.captionSent.Load(),
		VideoDropped:   v.videoDropped.Load(),
		AudioDropped:   v.audioDropped.Load(),
		CaptionDropped: v.captionDropped.Load(),
	}
}

// Run starts the drain goroutine that reads frames from channels and feeds
// them to the TSMuxer. It blocks until Stop() is called.
func (v *OutputViewer) Run() {
	defer close(v.done)

	for {
		select {
		case <-v.stopCh:
			// Drain remaining frames before exiting.
			v.drain()
			return

		case frame := <-v.videoCh:
			if err := v.muxer.WriteVideo(frame); err != nil {
				slog.Error("output viewer: mux video error", "err", err)
			}
			if v.onVideo != nil {
				v.onVideo(frame)
			}

		case frame := <-v.audioCh:
			if err := v.muxer.WriteAudio(frame); err != nil {
				slog.Error("output viewer: mux audio error", "err", err)
			}

		// Captions are received but not muxed in Phase 5 (TS muxer
		// does not have a caption PID). We still drain the channel to
		// avoid backpressure.
		case <-v.captionCh:
		}
	}
}

// Stop signals the drain goroutine to exit and waits for it to finish.
func (v *OutputViewer) Stop() {
	close(v.stopCh)
	<-v.done
}

// drain flushes any remaining video and audio frames from the channels.
func (v *OutputViewer) drain() {
	for {
		select {
		case frame := <-v.videoCh:
			if err := v.muxer.WriteVideo(frame); err != nil {
				slog.Error("output viewer: mux video error (drain)", "err", err)
			}
		case frame := <-v.audioCh:
			if err := v.muxer.WriteAudio(frame); err != nil {
				slog.Error("output viewer: mux audio error (drain)", "err", err)
			}
		case <-v.captionCh:
		default:
			return
		}
	}
}

// DebugSnapshot returns viewer metrics for debug snapshots.
func (v *OutputViewer) DebugSnapshot() map[string]any {
	return map[string]any{
		"video_sent":      v.videoSent.Load(),
		"audio_sent":      v.audioSent.Load(),
		"caption_sent":    v.captionSent.Load(),
		"video_dropped":   v.videoDropped.Load(),
		"audio_dropped":   v.audioDropped.Load(),
		"caption_dropped": v.captionDropped.Load(),
	}
}

// Compile-time check that OutputViewer satisfies the Viewer interface.
var _ distribution.Viewer = (*OutputViewer)(nil)
