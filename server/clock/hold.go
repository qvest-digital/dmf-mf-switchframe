package clock

import "slices"

// MaxSourceHold bounds the hold a store keeps a source's frames for
// (SourceHold), in 90 kHz ticks (500 ms). A TrackingAnchor places no
// timestamp further ahead of its arrival than this, which a mixer
// channel's 640 ms ring holds as well.
const MaxSourceHold = 45000

// minFramePeriod is the shortest frame period a hold is kept for (1/120 s):
// a store keeps no more than MaxSourceHold/minFramePeriod frames waiting
// whatever a source's timestamps do, and a source placed further ahead is
// not kept whole.
const minFramePeriod = 90000 / 120

// maxHoldFrames is the most frames SourceHold counts as waiting.
const maxHoldFrames = MaxSourceHold / minFramePeriod

// SourceHold counts a source's frames that wait in a store before the read
// point reaches them, for a store that keeps a source's frames from their
// arrival to the tick that shows them. A frame placed at PTS p and stored
// at engine time t waits p - t beyond the reader delay. A source placed by
// a TrackingAnchor at the latest arrival of its video and audio waits the
// offset's excess over its own lateness: an MPEG-TS stream's video waits
// about one PES packet of its AAC. Every frame that arrives in that wait
// is kept beside the waiting one. The frames are counted, not the wait in
// frame periods: the anchor places the tail of a backlog burst at arrival
// plus MaxSourceHold, closer together than a period, and those frames
// still wait once frames arrive a period apart again. It is not safe for
// concurrent use.
type SourceHold struct {
	last   int64                // PTS of the previous frame
	placed [maxHoldFrames]int64 // PTS of the last frames, a ring
	next   int                  // index in placed of the next frame
	steps  [15]int64            // PTS distances of the last frames, newest first
	nSteps int                  // steps recorded, up to len(steps)
}

// Frames records a frame placed at pts as the store takes it, and returns
// how many of the source's last frames, this one included, are placed
// after the current engine time: the frames the store keeps beyond its
// own depth, never more than MaxSourceHold/minFramePeriod.
func (h *SourceHold) Frames(pts int64) int {
	if h.last != 0 && pts > h.last {
		copy(h.steps[1:], h.steps[:len(h.steps)-1])
		h.steps[0] = pts - h.last
		h.nSteps = min(h.nSteps+1, len(h.steps))
	}
	h.last = pts
	h.placed[h.next] = pts
	h.next = (h.next + 1) % len(h.placed)
	now := NowPTS()
	n := 0
	for _, p := range h.placed {
		if p > now {
			n++
		}
	}
	return n
}

// Period returns the source's frame period in 90 kHz ticks, 0 before its
// second frame: the lower quartile of the distances of its last
// len(steps) frames, at least minFramePeriod. A store counts the frames a
// source's delay holds back in it. Not the smallest distance: an anchor
// that lowers its offset places one frame closer to its predecessor than
// a frame period, and one such frame must not multiply the frames a delay
// takes. The lower quartile rather than the median keeps a store deep
// enough while jitter spreads the distances.
func (h *SourceHold) Period() int64 {
	if h.nSteps == 0 {
		return 0
	}
	s := h.steps // a copy, sorted in place
	sorted := s[:h.nSteps]
	slices.Sort(sorted)
	return max(sorted[h.nSteps/4], minFramePeriod)
}
