package clock

import "sync/atomic"

// MaxSourceHold bounds the hold a store keeps a source's frames for
// (SourceHold), in 90 kHz ticks (500 ms). A TrackingAnchor places no
// timestamp further ahead of its arrival than this, which a mixer
// channel's 640 ms ring holds as well.
const MaxSourceHold = 45000

// MaxSourceDelay is the longest delay a source is read with, in 90 kHz
// ticks (500 ms).
const MaxSourceDelay = 45000

// minFramePeriod is the shortest frame period a hold is kept for (1/120 s),
// or the program's own when that is shorter (holdPeriod): a store keeps no
// more than (MaxSourceHold + the read lag + the source's delay) /
// holdPeriod frames waiting whatever a source's timestamps do, and a
// source placed further ahead is not kept whole.
const minFramePeriod = 90000 / 120

// readLag is how far behind the current time a frame can still be unread,
// in 90 kHz ticks: a tick reads ReadDelay program frame periods behind its
// grain, and the next tick comes a period later. 0 until a realtime
// ReferenceClock starts.
var readLag atomic.Int64

// programPeriod is the program frame period in 90 kHz ticks, rounded up.
// 0 until a realtime ReferenceClock starts.
var programPeriod atomic.Int64

// programDelay is the program's ReadDelay; 0 until a realtime
// ReferenceClock starts.
var programDelay atomic.Uint64

// holdPeriod is the frame period a SourceHold is kept for, in 90 kHz
// ticks: minFramePeriod, or the program frame period rounded down when
// that is shorter, so a source at the program rate is kept whole for
// MaxSourceHold at any rate.
var holdPeriod atomic.Int64

// SetProgramRate records the program frame rate the ticks read at. A
// realtime ReferenceClock sets it as it starts.
func SetProgramRate(r Rate) {
	d := ReadDelay(r)
	n := (int64(d) + 1) * 90000 * r.Den
	readLag.Store((n + r.Num - 1) / r.Num)
	programPeriod.Store((90000*r.Den + r.Num - 1) / r.Num)
	programDelay.Store(d)
	holdPeriod.Store(max(1, min(minFramePeriod, 90000*r.Den/r.Num)))
}

// ProgramPeriod returns the program frame period the ticks read at, in
// 90 kHz ticks, rounded up; 0 until a realtime ReferenceClock starts.
func ProgramPeriod() int64 {
	return programPeriod.Load()
}

// ProgramReadDelay returns how many program frame periods behind the
// clock the ticks read (ReadDelay); ReaderDelayGrains until a realtime
// ReferenceClock starts.
func ProgramReadDelay() uint64 {
	if d := programDelay.Load(); d != 0 {
		return d
	}
	return ReaderDelayGrains
}

// SourceHold counts a source's frames that wait in a store for the read
// point, for a store that keeps a source's frames from their arrival to
// the tick that shows them. A tick reads a source delay further behind
// than the program's read lag, and a source placed by a TrackingAnchor
// waits the anchor's offset beyond its own lateness: an MPEG-TS stream's
// video waits about one PES packet of its AAC, and the tail of a
// connection's backlog burst waits a hold, packed as closely as it was
// decoded. Every frame placed after the oldest read point a tick can
// still use is counted, not the wait in frame periods: frames packed
// closer than a period still wait once frames arrive a period apart again.
// It is not safe for concurrent use.
type SourceHold struct {
	placed []int64 // PTS of the last frames, a ring
	next   int     // index in placed of the next frame
}

// Frames records a frame placed at pts as the store takes it, and returns
// how many of the source's last frames, this one included, a tick reading
// the source delay behind the program has not reached yet: placed after
// the current engine time less the program's read lag and delay, never
// more than (MaxSourceHold + the read lag + delay) / holdPeriod: a frame
// placed MaxSourceHold after its arrival waits that long.
func (h *SourceHold) Frames(pts, delay int64) int {
	period := holdPeriod.Load()
	if period == 0 {
		period = minFramePeriod
	}
	lag := readLag.Load()
	if n := int((MaxSourceHold + MaxSourceDelay + lag) / period); len(h.placed) < n {
		// Slots not yet written hold 0, which no read point is before.
		h.placed = append(h.placed, make([]int64, n-len(h.placed))...)
	}
	h.placed[h.next] = pts
	h.next = (h.next + 1) % len(h.placed)
	read := NowPTS() - lag - delay
	n := 0
	for _, p := range h.placed {
		if p > read {
			n++
		}
	}
	return min(n, int((MaxSourceHold+lag+delay)/period))
}
