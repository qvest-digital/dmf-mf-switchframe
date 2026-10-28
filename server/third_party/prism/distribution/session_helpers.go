package distribution

import (
	"log/slog"
	"sync/atomic"

	"github.com/zsiec/prism/media"
)

// trySendVideo implements the damaged-group-aware video send logic shared
// by both single-stream viewerSession and multiplexed muxStreamView.
// It drops delta frames belonging to a GOP where an earlier frame was
// dropped, preventing the client from receiving un-decodable data.
func trySendVideo(
	frame *media.VideoFrame,
	videoCh chan *media.VideoFrame,
	damagedGroup *atomic.Uint32,
	videoSent *atomic.Int64,
	videoDropped *atomic.Int64,
) {
	if frame.IsKeyframe {
		damagedGroup.Store(0)
	} else if damagedGroup.Load() == frame.GroupID {
		videoDropped.Add(1)
		return
	}

	select {
	case videoCh <- frame:
		videoSent.Add(1)
		return
	default:
	}

	if frame.IsKeyframe {
		// The queued frames are older than this keyframe and the client
		// can skip to it, so it displaces them rather than being dropped:
		// a dropped keyframe would leave its group's deltas, sent once
		// there is room, with nothing to decode them against.
		displaced := drainVideo(videoCh)
		videoDropped.Add(int64(displaced))
		select {
		case videoCh <- frame:
			videoSent.Add(1)
			slog.Warn("trySendVideo: keyframe displaced queued frames (channel full)",
				"group", frame.GroupID,
				"displaced", displaced)
			return
		default:
		}
		slog.Warn("trySendVideo: keyframe dropped (channel full)",
			"group", frame.GroupID,
			"chanLen", len(videoCh),
			"chanCap", cap(videoCh))
	}
	videoDropped.Add(1)
	damagedGroup.Store(frame.GroupID)
}

// drainVideo empties videoCh without blocking and returns how many frames
// it took.
func drainVideo(videoCh chan *media.VideoFrame) int {
	n := 0
	for {
		select {
		case <-videoCh:
			n++
		default:
			return n
		}
	}
}

// fitsVideoStream reports whether frame may be written to the stream open
// for streamGroup. A frame of another group is a delta whose keyframe the
// client never received on this stream, and is undecodable there.
func fitsVideoStream(frame *media.VideoFrame, streamGroup uint32) bool {
	return frame.GroupID == streamGroup
}
