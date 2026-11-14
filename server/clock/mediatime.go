package clock

import (
	"math/bits"
	"slices"
	"sync"
	"time"
)

// Media time is TAI, as libmxl counts it: nanoseconds since the SMPTE ST
// 2059 epoch, and an index i of a flow at rate R is the edit unit whose
// timestamp is nearest to i*R.den/R.num seconds. Every timestamp this
// server emits is derived from it, never from when something was read or
// processed, so a skipped grain costs exactly its own time and two
// essences, or two inputs, at the same TAI carry the same PTS.

// Rate is an edit rate, in edit units per second, as num/den.
type Rate struct {
	Num, Den int64
}

// TimestampToIndex returns the index of the edit unit at TAI timestamp ts,
// rounded to the nearest one as mxlTimestampToIndex does.
func TimestampToIndex(r Rate, ts int64) uint64 {
	if ts < 0 || r.Num <= 0 || r.Den <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(ts), uint64(r.Num))
	lo, carry := bits.Add64(lo, 500_000_000*uint64(r.Den), 0)
	hi += carry
	q, _ := bits.Div64(hi, lo, 1_000_000_000*uint64(r.Den))
	return q
}

// IndexToTimestamp returns the TAI timestamp of index i, rounded to the
// nearest nanosecond as mxlIndexToTimestamp does.
func IndexToTimestamp(r Rate, i uint64) int64 {
	if r.Num <= 0 || r.Den <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(i, uint64(r.Den)*1_000_000_000)
	lo, carry := bits.Add64(lo, uint64(r.Num)/2, 0)
	hi += carry
	q, _ := bits.Div64(hi, lo, uint64(r.Num))
	return int64(q)
}

// PTSBase is the PTS of the engine epoch. PTS 0 is left unused: several
// consumers take a zero PTS for "none yet".
const PTSBase = 90000

// ReaderDelayGrains is how far behind the clock every input is read, in
// program frame periods: a tick at TAI t takes the newest frame of each
// source whose media time is at or before t - ReaderDelayGrains frames.
// It is the budget for a producer committing a grain up to its period
// late, mirror transit to this node and the mirror's grain pacing. A
// grain that arrives later than that is shown one tick late, so the
// choice of frame does not flip between two grains from tick to tick.
const ReaderDelayGrains = 2

var (
	taiNowMu sync.RWMutex
	taiNow   = func() int64 { return time.Now().UnixNano() }

	epochOnce sync.Once
	epochNs   int64
)

// SetTAISource replaces the TAI clock. The mxl package installs libmxl's
// mxlGetTime; without it, the system clock stands in, which differs from
// TAI by a constant only. nil restores the system clock.
func SetTAISource(now func() int64) {
	if now == nil {
		now = func() int64 { return time.Now().UnixNano() }
	}
	taiNowMu.Lock()
	taiNow = now
	taiNowMu.Unlock()
}

// TAINow returns the current TAI time in nanoseconds.
func TAINow() int64 {
	taiNowMu.RLock()
	now := taiNow
	taiNowMu.RUnlock()
	return now()
}

// Epoch returns the TAI timestamp that PTSBase stands for: the first time
// anything in this process asked, so PTS starts near PTSBase and stays
// within 33 bits for a day.
func Epoch() int64 {
	epochOnce.Do(func() { epochNs = TAINow() })
	return epochNs
}

// PTS converts a TAI timestamp to a 90 kHz PTS on the engine timeline,
// rounded to the nearest tick so that a sample or grain index survives the
// round trip through PTS.
func PTS(ts int64) int64 {
	return roundDiv((ts-Epoch())*9, 100_000) + PTSBase
}

// TimestampOf converts a 90 kHz PTS on the engine timeline back to TAI.
func TimestampOf(pts int64) int64 {
	return Epoch() + roundDiv((pts-PTSBase)*100_000, 9)
}

// IndexPTS returns the PTS of index i of a flow at rate r.
func IndexPTS(r Rate, i uint64) int64 {
	return PTS(IndexToTimestamp(r, i))
}

// PTSIndex returns the index of a flow at rate r that pts stands for.
func PTSIndex(r Rate, pts int64) uint64 {
	return TimestampToIndex(r, TimestampOf(pts))
}

// ArrivalPTS returns the PTS of the first of n samples at the given rate
// that end now: how audio without a media clock is placed on the
// timeline, at its arrival.
func ArrivalPTS(n, sampleRate int) int64 {
	return PTS(TAINow() - int64(n)*1_000_000_000/int64(sampleRate))
}

// NowPTS returns the PTS of the current TAI time.
func NowPTS() int64 {
	return PTS(TAINow())
}

func roundDiv(a, b int64) int64 {
	if a >= 0 {
		return (a + b/2) / b
	}
	return -((-a + b/2) / b)
}

// Anchor maps a source's own 90 kHz timeline, such as an RTP media clock
// locked to PTP, onto the engine timeline: the first timestamp is
// placed at the time it arrives and every later one keeps its distance to
// it, so the source's video and audio keep their own relation (its lip
// sync) and its frame cadence. A jump of more than MaxAnchorSkew away from
// the clock, as on a source restart, places the source again.
type Anchor struct {
	mu     sync.Mutex
	offset int64
	set    bool
	moves  int64
}

// MaxAnchorSkew is how far an anchored source may run from the clock
// before it is placed again.
const MaxAnchorSkew = 2 * 90000

// PTS maps the source timestamp src to the engine timeline.
func (a *Anchor) PTS(src int64) int64 {
	now := NowPTS()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.set {
		a.offset, a.set = now-src, true
	} else if d := src + a.offset - now; d > MaxAnchorSkew || d < -MaxAnchorSkew {
		a.offset = now - src
		a.moves++
	}
	return src + a.offset
}

// Moves returns how often the source was placed again after a jump.
func (a *Anchor) Moves() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.moves
}

// TrackingAnchor maps the own 90 kHz timeline of a source whose clock is
// not locked to the engine's, such as an SRT stream or a playout channel,
// onto the engine timeline: every timestamp gets the same offset, so video
// and audio of the source keep their own relation, and the offset is the
// lateness (arrival less source timestamp) the source's latest timestamps
// arrive with. A source whose essences arrive in bursts, as an MPEG-TS
// stream's audio does in PES packets of several frames, is placed at its
// latest arrival, so it is on hand at every read.
//
// The offset moves the source's audio against the program read point, and
// the mixer splices a source's audio whenever it moved by more than a
// period, so the offset settles once and then moves only on a lasting
// change. For the first TrackingSettle windows after a placement every
// timestamp is placed no earlier than it arrives and the offset forgets a
// lateness after two windows, so the backlog a connection's socket and
// stream probe built before the first frame was decoded is left behind.
// Then the offset is the largest lateness of the settle windows after the
// first, and it is raised only when two windows in a row arrived more
// than TrackingSlack later than it, to the smaller of their largest
// latenesses, and lowered only when TrackingHistory windows in a row
// arrived earlier, to their largest. A frame up to TrackingSlack later
// than its placement is still read on time, its audio included. A
// single late burst, as a stalled sender or decode loop delivers, leaves
// the offset where it is: its frames are shown late and its audio that
// arrives after its read point is lost, once, rather than moving the rest
// of the source later and back. A sender clock that drifts is followed,
// in steps of TrackingSlack when slow and of the drift over a window when
// fast, TrackingHistory windows behind. A jump of more than MaxAnchorSkew, as on a source
// restart, places the source again.
//
// No timestamp is placed more than MaxSourceHold after its own arrival:
// the stores that hold a source's frames and audio until they are due
// keep no more than that, so a frame placed further ahead is dropped
// before it is due. A burst whose first frame is later than that ahead
// of the rest raises the offset only as far as each later frame allows.
// The bound applies to each essence on its own: a source whose video
// arrives more than MaxSourceHold later than its audio of the same time
// has its audio placed earlier than its video by the excess.
type TrackingAnchor struct {
	mu        sync.Mutex
	set       bool
	offset    int64
	placedAt  int64                  // offset the source was last placed at
	cur       int64                  // largest lateness in the current window
	maxes     [TrackingHistory]int64 // largest lateness of the last windows, newest first
	windows   int                    // windows completed since the placement
	windowEnd int64                  // engine PTS at which the current window ends
	moves     int64
	raises    int64
	settles   int64 // times the offset settled after a placement, or moved once settled
	told      int64 // settles SettledPTS has reported
	settledAt int64 // offset at the last settle or reported move
}

// TrackingWindow is the span over which a TrackingAnchor takes a
// source's largest lateness.
const TrackingWindow = 2 * 90000

// TrackingSettle is how many windows after a placement a TrackingAnchor
// follows every lateness at once, before its offset settles.
const TrackingSettle = 3

// TrackingSlack returns how much later than its offset a settled
// TrackingAnchor's source may arrive without raising it, in 90 kHz ticks,
// at the program rate the ticks read at. Until a program rate is set, the
// period is the shortest a hold is kept for.
func TrackingSlack() int64 {
	return trackingSlack(programPeriodOr(), ReaderDelayGrains)
}

// programPeriodOr is ProgramPeriod, or the shortest period a hold is kept
// for until a program rate is set.
func programPeriodOr() int64 {
	if p := ProgramPeriod(); p > 0 {
		return p
	}
	return minFramePeriod
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// trackingSlack is the slack for a program frame period of period ticks
// read delay periods behind the clock. A tick reads video delay periods
// behind the clock, but audio from there for one period, so an audio
// frame that starts just before that period ends is on hand only if it
// arrives within delay-1 periods of its placement, however long the frame
// is. Video gets half its delay periods, which also cover a producer's
// commit and transit. The slack is the lesser of the two, less
// TrackingDriftMargin. Within it a raise moves
// only the source's video, since the mixer realigns audio only past a
// period, and leaves the two apart until the offset comes down; a lasting
// lateness past it that is not raised for runs the audio dry read after
// read.
func trackingSlack(period, delay int64) int64 {
	return max(min((delay-1)*period, delay*period/2)-TrackingDriftMargin, 0)
}

// TrackingDriftMargin is how far a lasting lateness may grow past
// TrackingSlack before the two windows that raise for it end, in 90 kHz
// ticks: a sender clock 250 ppm slow drifts 1 ms over two windows. It is
// a time, not a share of the period, since drift is.
const TrackingDriftMargin = 90

// TrackingHistory is how many windows in a row a settled TrackingAnchor's
// source must arrive earlier than its offset for the offset to come down:
// a minute, so a lateness that comes and goes within one does not move the
// source.
const TrackingHistory = 30

// PTS maps the source timestamp src to the engine timeline.
func (a *TrackingAnchor) PTS(src int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.place(src)
}

// SettledPTS is PTS that also reports whether the offset settled, or
// moved half a program period or more from where it was last reported,
// since the last SettledPTS call: true for the first timestamp it maps on
// the new offset. A settle, raise or lowering can move the source by less
// than a period, which the mixer never realigns, so the source's audio is
// placed on its timestamps again from that one on, as its video is. A
// smaller move is left to add up: placing the audio again splices it, and
// the lowerings a sender clock's drift brings are fractions of a
// millisecond.
func (a *TrackingAnchor) SettledPTS(src int64) (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pts := a.place(src)
	settled := a.told != a.settles
	a.told = a.settles
	return pts, settled
}

// place maps src to the engine timeline. Caller holds a.mu.
func (a *TrackingAnchor) place(src int64) int64 {
	now := NowPTS()
	late := now - src
	was, placed, settled := a.offset, false, false
	switch {
	case !a.set || late-a.offset > MaxAnchorSkew || a.offset-late > MaxAnchorSkew:
		if a.set {
			a.moves++
		}
		a.set = true
		a.offset, a.cur, a.windows = late, late, 0
		a.windowEnd = now + TrackingWindow
		placed = true
	case now >= a.windowEnd:
		copy(a.maxes[1:], a.maxes[:])
		a.maxes[0], a.cur = a.cur, late
		a.windows++
		a.windowEnd = now + TrackingWindow
		switch {
		case a.windows < TrackingSettle:
			a.offset = max(a.maxes[0], late)
		case a.windows == TrackingSettle:
			a.offset = slices.Max(a.maxes[:TrackingSettle-1])
			a.settles++
			settled = true
		case min(a.maxes[0], a.maxes[1]) > a.offset+TrackingSlack():
			a.offset = min(a.maxes[0], a.maxes[1])
		case a.windows >= TrackingSettle+TrackingHistory && slices.Max(a.maxes[:]) < a.offset:
			a.offset = slices.Max(a.maxes[:])
		}
	default:
		a.cur = max(a.cur, late)
		if a.windows < TrackingSettle {
			a.offset = max(a.offset, late)
		}
	}
	ceil := late + MaxSourceHold
	a.offset, a.cur = min(a.offset, ceil), min(a.cur, ceil)
	for i := range a.maxes {
		a.maxes[i] = min(a.maxes[i], ceil)
	}
	if placed {
		a.placedAt = a.offset
	} else if a.offset > was {
		a.raises++
	}
	if settled {
		a.settledAt = a.offset
	} else if !placed && a.windows > TrackingSettle && 2*abs(a.offset-a.settledAt) >= programPeriodOr() {
		a.settles++
		a.settledAt = a.offset
	}
	return src + a.offset
}

// Moves returns how often the source was placed again after a jump.
func (a *TrackingAnchor) Moves() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.moves
}

// TrackingStats reports how a TrackingAnchor has placed its source.
type TrackingStats struct {
	// Offset is how far the offset moved since the source was last
	// placed, in 90 kHz ticks: up when its timestamps arrived later, down
	// when a window forgot a lateness.
	Offset int64
	// Raises counts the timestamps that raised the offset: each moved the
	// source's later timestamps that much later on the engine timeline.
	Raises int64
	Moves  int64 // placements again after a jump
}

// Stats returns the anchor's placement statistics.
func (a *TrackingAnchor) Stats() TrackingStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return TrackingStats{Offset: a.offset - a.placedAt, Raises: a.raises, Moves: a.moves}
}
