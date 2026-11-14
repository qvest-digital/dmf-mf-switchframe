package audio

import (
	"slices"
	"sync/atomic"

	"github.com/zsiec/switchframe/server/clock"
)

// arrivalHoldWindow is the span, in nanoseconds, over which a channel's
// arrivalHold takes the largest hold its pushes needed.
const arrivalHoldWindow = 1_000_000_000

// arrivalHoldHistory is how many windows in a row have to need at least a
// period less than an arrivalHold for it to come down: a minute.
const arrivalHoldHistory = 60

// arrivalHold places the audio of a source without a media clock, and the
// video that arrives with it, a whole number of program periods after
// their arrival.
//
// A tick mixes the period after its read point, ReadDelay-1 periods and
// the channel's delay behind the clock. The audio of a push placed at its
// arrival is read to its end that long after the push arrived. A source
// that delivers whole AAC frames, 21.3 ms of audio at 48 kHz, runs dry
// whenever its next push arrives later than that. Up to 60 fps the
// budget is one period, 16.7 ms at 60 fps.
//
// Each push records how far the last read had passed the arrival of the
// push before it: its need. The need is taken on arrivals, not on where
// the audio was placed, so a sender clock's drift, which moves the
// placement and not the arrivals, does not move it. A push that arrives
// more than two AAC frames after the one before follows a stall or a
// loss, which no hold covers, and is not counted.
//
// The hold is raised to a window's largest need when the source ran dry
// twice in the window, or once in it and once in the window before, after
// its audio was first placed. A single dry read is a start or a stall. A
// source that hands its AAC over in bunches with its video, as the replay
// player does, is continued ahead of its arrivals, never runs dry and is
// not held. The hold is whole periods, at most MaxSourceHold, and comes
// down after arrivalHoldHistory windows in a row needed a period less. A
// move places the source's audio again, a splice.
type arrivalHold struct {
	ticks atomic.Int64 // 90 kHz ticks the source is placed after its arrival
	end   int64        // TAI the current window ends at; 0 before the first push
	prev  int64        // TAI the last push arrived at; 0 before the first
	cur   int64        // largest need, in ns, of the current window
	maxes [arrivalHoldHistory]int64
	moved int64 // ticks the hold moved by since the last push was placed
	// dry is the times the source had run dry when the last window
	// closed, counted from when its audio was first placed.
	dry      int64
	counting bool // the source's audio was placed
	wasDry   bool // the source ran dry in the window before
	// off is the latest a push of the current window was placed after
	// its arrival and the hold, in ns; lastOff is off of the window
	// before. A move of the hold undoes it.
	off, lastOff int64
	offSeen      bool
}

// note records that a push of span ns of audio arrived at TAI now, the
// source's last read having ended at readEnd (0: none yet) and the source
// having run dry dry times; placed reports whether its audio was placed
// on a read yet.
func (h *arrivalHold) note(now, span, readEnd, dry int64, placed bool) {
	prev := h.prev
	h.prev = now
	period := clock.ProgramPeriod()
	if period <= 0 {
		return
	}
	if placed && !h.counting {
		h.counting, h.dry = true, dry
	}
	if h.end == 0 {
		h.end = now + arrivalHoldWindow
	}
	for closed := 0; now >= h.end; closed++ {
		if closed > arrivalHoldHistory {
			h.end = now
		}
		h.closeWindow(period, dry)
		h.end += arrivalHoldWindow
	}
	if prev == 0 || readEnd == 0 || now-prev > 2*span {
		return
	}
	h.cur = max(h.cur, readEnd-prev)
}

// ticksNs is t 90 kHz ticks in nanoseconds.
func ticksNs(t int64) int64 { return t * 1_000_000_000 / 90000 }

// placed records that a push arriving at TAI now was placed to end at end.
func (h *arrivalHold) placed(now, end int64) {
	if o := end - now - ticksNs(h.ticks.Load()); !h.offSeen || o > h.off {
		h.off, h.offSeen = o, true
	}
}

// closeWindow ends the current window, the source having run dry dry
// times so far.
func (h *arrivalHold) closeWindow(period, dry int64) {
	copy(h.maxes[1:], h.maxes[:len(h.maxes)-1])
	h.maxes[0], h.cur = h.cur, 0
	h.lastOff, h.off, h.offSeen = h.off, 0, false
	// One dry read can be a start or a stall; more in a window, or one in
	// two windows in a row, is the cadence.
	var ranDry bool
	if h.counting {
		n := dry - h.dry
		ranDry = n > 1 || n == 1 && h.wasDry
		h.dry, h.wasDry = dry, n > 0
	}
	most := clock.MaxSourceHold / period * period
	grains := func(needNs int64) int64 {
		if needNs <= 0 {
			return 0
		}
		p := ticksNs(period)
		return min((needNs+p-1)/p*period, most)
	}
	ticks := h.ticks.Load()
	set := func(g int64) {
		h.moved += g - ticks
		h.ticks.Store(g)
	}
	// The history holds the need that raised the hold for
	// arrivalHoldHistory windows, so it comes down no sooner.
	if g := grains(h.maxes[0]); ranDry && g > ticks {
		set(g)
	} else if g := grains(slices.Max(h.maxes[:])); g < ticks {
		set(g)
	}
}

// ArrivalHold returns how many 90 kHz ticks after its arrival a source
// whose audio has no media clock is placed, its video with it: 0 for a
// source that does not need it. It takes no lock.
func (m *Mixer) ArrivalHold(sourceKey string) int64 {
	if h, ok := m.holds.Load(sourceKey); ok {
		return h.(*atomic.Int64).Load()
	}
	return 0
}
