package clock

import "slices"

// MaxSourceHold bounds the hold a store sizes itself for (SourceHold), in
// 90 kHz ticks (500 ms). A source placed further ahead of its arrival is
// not kept whole: a tick that wants one of its oldest frames finds it gone.
const MaxSourceHold = 45000

// minFramePeriod is the shortest frame period SourceHold counts a hold in
// (1/120 s), so a hold never adds more than MaxSourceHold/minFramePeriod
// frames whatever a source's timestamps do.
const minFramePeriod = 90000 / 120

// SourceHold measures how long a source's frames wait in a store before
// the read point reaches them, for a store that keeps a source's frames
// from their arrival to the tick that shows them. A frame placed at PTS p
// and stored at engine time t waits p - t beyond the reader delay. A source
// placed by a TrackingAnchor at the latest arrival of its video and audio
// waits the offset's excess over its own lateness: an MPEG-TS stream's
// video waits about one PES packet of its AAC. Every frame that arrives in
// that wait is kept beside the waiting one. Like the anchor's offset, the
// hold is the largest over the current and the previous TrackingWindow, so
// the store keeps the frames placed under a raised offset until the offset
// has come down. It is not safe for concurrent use.
type SourceHold struct {
	last      int64     // PTS of the previous frame
	cur, prev int64     // largest hold in the current and the previous window
	windowEnd int64     // engine PTS at which the current window ends
	steps     [15]int64 // PTS distances of the last frames, newest first
	nSteps    int       // steps recorded, up to len(steps)
}

// Frames records a frame placed at pts as the store takes it, and returns
// how many frames the source's hold adds to the store's depth: the hold,
// bounded by MaxSourceHold, in the source's frame periods (Period),
// rounded up. A store keeps at most MaxSourceHold/Period of them beyond
// its own depth, and never more than MaxSourceHold/minFramePeriod.
func (h *SourceHold) Frames(pts int64) int {
	now := NowPTS()
	ticks := min(max(pts-now, 0), MaxSourceHold)
	if h.last != 0 && pts > h.last {
		copy(h.steps[1:], h.steps[:len(h.steps)-1])
		h.steps[0] = pts - h.last
		h.nSteps = min(h.nSteps+1, len(h.steps))
	}
	h.last = pts
	if now >= h.windowEnd {
		h.prev, h.cur = h.cur, ticks
		h.windowEnd = now + TrackingWindow
	} else {
		h.cur = max(h.cur, ticks)
	}
	period := h.Period()
	if period == 0 {
		return 0
	}
	return int((max(h.cur, h.prev) + period - 1) / period)
}

// Period returns the source's frame period in 90 kHz ticks, 0 before its
// second frame: the lower quartile of the distances of its last
// len(steps) frames, at least minFramePeriod. Not the smallest distance:
// an anchor that lowers its offset places one frame closer to its
// predecessor than a frame period, and one such frame must not multiply
// the frames a hold takes. The lower quartile rather than the median
// keeps a store deep enough while jitter spreads the distances.
func (h *SourceHold) Period() int64 {
	if h.nSteps == 0 {
		return 0
	}
	s := h.steps // a copy, sorted in place
	sorted := s[:h.nSteps]
	slices.Sort(sorted)
	return max(sorted[h.nSteps/4], minFramePeriod)
}
