package clock

// MaxSourceHold bounds the hold a store sizes itself for (SourceHold), in
// 90 kHz ticks (500 ms). A source placed further ahead of its arrival is
// not kept whole: a tick that wants one of its oldest frames finds it gone.
const MaxSourceHold = 45000

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
	last      int64 // PTS of the previous frame
	cur, prev hold  // the current and the previous window
	windowEnd int64 // engine PTS at which the current window ends
}

type hold struct {
	ticks    int64 // largest hold
	interval int64 // smallest positive PTS distance of two frames
}

// Frames records a frame placed at pts as the store takes it, and returns
// how many frames the source's hold adds to the store's depth: the hold,
// bounded by MaxSourceHold, in the source's frame periods, rounded up. The
// frame period is the smallest PTS distance of two frames in the windows:
// a frame placed under a raised offset is further from its predecessor.
func (h *SourceHold) Frames(pts int64) int {
	now := NowPTS()
	ticks := min(max(pts-now, 0), MaxSourceHold)
	var d int64
	if h.last != 0 && pts > h.last {
		d = pts - h.last
	}
	h.last = pts
	if now >= h.windowEnd {
		h.prev, h.cur = h.cur, hold{ticks: ticks, interval: d}
		h.windowEnd = now + TrackingWindow
	} else {
		h.cur.ticks = max(h.cur.ticks, ticks)
		if d > 0 && (h.cur.interval == 0 || d < h.cur.interval) {
			h.cur.interval = d
		}
	}
	interval := h.cur.interval
	if p := h.prev.interval; p > 0 && (interval == 0 || p < interval) {
		interval = p
	}
	if interval == 0 {
		return 0
	}
	return int((max(h.cur.ticks, h.prev.ticks) + interval - 1) / interval)
}
