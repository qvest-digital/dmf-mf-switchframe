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

// ReaderDelayGrains is the least delay every input is read with, in
// program frame periods: a tick at TAI t takes the newest frame of each
// source whose media time is at or before t - ReadDelay(rate) frames.
// It is the budget for a producer committing a grain up to its period
// late, mirror transit to this node and the mirror's grain pacing. A
// grain that arrives later than that is shown one tick late, so the
// choice of frame does not flip between two grains from tick to tick.
const ReaderDelayGrains = 2

// ReadDelayFloor is the least time behind the clock every input is read
// above 60 fps. Part of what the read delay covers does not shrink with
// the period: mirror transit and scheduling, the 10 ms batches an MXL
// audio producer commits, and the 21.3 ms AAC frames a source without a
// media clock delivers whole. A tick mixes the audio of the period after
// its read point, so a source's audio has one period less than the delay
// to arrive in: two periods leave 10 ms of it at 100 fps and 4.2 ms at
// 240, where an AAC source arriving on its own cadence runs dry until the
// delay is 4 periods at 100 and 120 fps and 7 at 240.
const ReadDelayFloor = 40 * time.Millisecond

// readDelayFloorFrom is the frame rate above which ReadDelayFloor applies.
// Up to it two periods are 33.3 ms or more, the delay the program has
// always been read with.
var readDelayFloorFrom = Rate{Num: 60, Den: 1}

// ReadDelay returns how many program frame periods behind the clock a
// tick at rate r reads: ReaderDelayGrains up to 60 fps, and above it as
// many as ReadDelayFloor takes, never fewer.
func ReadDelay(r Rate) uint64 {
	if r.Num <= 0 || r.Den <= 0 || r.Num*readDelayFloorFrom.Den <= readDelayFloorFrom.Num*r.Den {
		return ReaderDelayGrains
	}
	unit := int64(time.Second) * r.Den
	return max(ReaderDelayGrains, uint64((int64(ReadDelayFloor)*r.Num+unit-1)/unit))
}

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
// not locked to the engine's, such as an SRT stream, onto the engine
// timeline: every timestamp gets the same offset, so video and audio of
// the source keep their own relation, and the offset is the lateness
// (arrival less source timestamp) the source's latest timestamps arrive
// with. A source whose essences arrive in bursts, as an MPEG-TS stream's
// audio does in PES packets of several frames, is placed at its latest
// arrival, so it is on hand at every read.
//
// For the first TrackingSettle windows after a placement every timestamp
// is placed no earlier than it arrives (a video frame up to
// TrackingGridMargin earlier, as below), and the offset forgets a lateness
// after two windows, so the backlog a connection's socket and stream probe
// built before the first frame was decoded is left behind. Then the offset
// is the largest lateness of the settle windows after the first, and it is
// raised only when two windows in a row arrived more than TrackingSlack
// later than it, to the smaller of their largest latenesses, and lowered
// only when TrackingHistory windows in a row arrived earlier, to their
// largest. A frame up to TrackingSlack later than its placement is still
// read on time, its audio included. A single late burst, as a stalled
// sender or decode loop delivers, leaves the offset where it is: its
// frames are shown late and its audio that arrives after its read point is
// lost, once. A sender clock that drifts is followed, in steps of
// TrackingSlack when slow and of the drift over a window when fast,
// TrackingHistory windows behind. A jump of more than MaxAnchorSkew, as on
// a source restart, places the source again.
//
// From the settle on, the source is not moved by a step: the offset its
// timestamps get slews to a new offset at TrackingSlewRate, video and audio
// alike, so its audio is resampled onto the move rather than spliced and
// its video repeats or skips a frame at a time. Two stalls in windows in a
// row raise the offset as a lasting lateness does, and slewed, the raise
// costs no audio. A raise that lasts, a window after it still arriving
// more than TrackingSlack later than the slewed offset, three late windows
// in a row that two stalls cannot make, and a move of more than
// TrackingSlewMax, are taken at once, as a step, counted. The settle's own
// move from the offset the settle windows followed is a move like any
// other: it slews, or past TrackingSlewMax steps and counts.
//
// A tick shows a source's newest frame stamped at or before its read
// point, a program frame start, and plays the audio stamped from there on.
// A frame stamped between two frame starts therefore airs up to a period
// after its stamp while its audio airs on its stamps: the picture runs up
// to a period behind the sound, by an amount the offset sets and every
// move of it changes. So the offset timestamps are mapped with slews not
// to the offset but to an offset at or above it that places the source's
// video frames, passed to VideoPTS, TrackingGridMargin before a program
// frame start; each frame then airs on its stamp, and its audio with it.
// That target is the least such offset at or above the offset, and a
// target already set stays while it is less than TrackingGridMargin below
// the offset or less than TrackingDriftMargin above the next lower one, so
// the offset's own small moves do not move the source by a period back and
// forth. Where the frames land is taken from a window's frames when their
// leads on the frame starts agree within TrackingDriftMargin, as a source
// at the program rate or a whole fraction of it does; a window whose
// frames do not agree, as at another rate, where no offset places every
// frame just before a frame start, leaves it as it was. A source with no
// video, or none yet, is mapped at the offset. The raises and lowerings
// above are of the offset alone.
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
	applied   int64                  // offset timestamps are mapped with, slewing to target
	target    int64                  // the offset, or above it the video's alignment (alignUp)
	slewAt    int64                  // engine PTS up to which applied has slewed
	placedAt  int64                  // offset the source was last placed at
	cur       int64                  // largest lateness in the current window
	maxes     [TrackingHistory]int64 // largest lateness of the last windows, newest first
	windows   int                    // windows completed since the placement
	raisedAt  int                    // windows at the last raise
	windowEnd int64                  // engine PTS at which the current window ends
	moves     int64
	raises    int64
	steps     int64

	// The video's alignment on the program frame starts of rate grid
	// (zero: none): the target is IndexPTS(grid, kref) less
	// TrackingGridMargin less vref, the source PTS of a video frame.
	grid Rate
	vref int64
	kref uint64
	// The current window's video frames, measured on the frame starts of
	// rate vrate mapped with vt0, the target at its first: lead is how far
	// a frame is placed before the frame start at or after it. vfirst is
	// the first frame's, vlo and vhi the least and largest of the others'
	// less vfirst, wrapped to half a period either way, and the frame of
	// vlo is source PTS vloSrc placed at engine PTS vloAt.
	// ceils holds the least of the bounds MaxSourceHold put on the current
	// window's timestamps and on the last window's.
	ceils [2]int64

	vrate         Rate
	vt0           int64
	vn            int
	vfirst        int64
	vlo, vhi      int64
	vloSrc, vloAt int64
}

// TrackingGridMargin is how far before a program frame start a settled
// TrackingAnchor places its source's video, in 90 kHz ticks. At a rate
// whose period is not a whole number of ticks, such as 60000/1001 (1501.5),
// a source's frame stamps and the frame starts each step by 1501 and 1502
// ticks, not in turn with each other, so a frame placed a tick before one
// frame start can be a tick past the next.
const TrackingGridMargin = 2

// TrackingWindow is the span over which a TrackingAnchor takes a
// source's largest lateness.
const TrackingWindow = 2 * 90000

// TrackingSettle is how many windows after a placement a TrackingAnchor
// follows every lateness at once, before its offset settles.
const TrackingSettle = 3

// TrackingSlack returns how much later than its offset a settled
// TrackingAnchor's source may arrive without raising it, in 90 kHz ticks,
// at the program rate and read delay the ticks read at. Until a program
// rate is set, the period is the shortest a hold is kept for.
func TrackingSlack() int64 {
	return trackingSlack(programPeriodOr(), int64(ProgramReadDelay()))
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
// TrackingDriftMargin. A lasting lateness past it that is not raised for
// runs the audio dry read after read.
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

// TrackingSlewRate is how much engine time a settled TrackingAnchor takes
// to move the offset it maps with by one tick: 250 ticks, 0.4%, under the
// 0.5% its source's audio is resampled at (audio.Mixer.IngestPCMSlewed),
// so the audio keeps up with its video. A 46 ms raise takes 11.5 s. A step
// moves the audio by placing it again on its stamps, as it moves the video.
const TrackingSlewRate = 250

// TrackingSlewMax is the largest move of a settled TrackingAnchor's offset
// that slews, in 90 kHz ticks (100 ms, 25 s at TrackingSlewRate); a larger
// one is a step.
const TrackingSlewMax = 9000

// PTS maps the source timestamp src to the engine timeline.
func (a *TrackingAnchor) PTS(src int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.place(src)
}

// VideoPTS maps the source timestamp src of a video frame to the engine
// timeline, as PTS does, and measures where the frame lands on the
// program's frame starts, which the offset slews to place it just before.
func (a *TrackingAnchor) VideoPTS(src int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	pts := a.place(src)
	a.noteVideo(src)
	return pts
}

// noteVideo measures the lead on the program frame starts of the video
// frame src mapped with the target. An aligned frame whose lead fell under
// TrackingGridMargin, by no more than TrackingDriftMargin, as a sender
// clock that runs fast moves its frames towards the frame starts, is
// aligned again at once: left to the end of the window, its frames would
// pass a frame start and air a period late for the rest of it. Caller
// holds a.mu.
func (a *TrackingAnchor) noteVideo(src int64) {
	r, ok := ProgramRate()
	if !ok {
		return
	}
	if r != a.vrate {
		a.vrate, a.vn = r, 0
	}
	period := roundDiv(90000*r.Den, r.Num)
	wrap := func(d int64) int64 {
		if d >= period/2 {
			return d - period
		} else if d < -period/2 {
			return d + period
		}
		return d
	}
	leadAt := func(at int64) int64 { return IndexPTS(r, startAtOrAfter(r, at)) - at }
	if at := src + a.target; a.grid == r {
		if d := wrap(leadAt(at) - TrackingGridMargin); d < 0 && d >= -TrackingDriftMargin {
			a.vref, a.kref = src, PTSIndex(r, at+TrackingGridMargin+d)
		}
	}
	if a.vn == 0 {
		a.vt0 = a.target
	}
	at := src + a.vt0
	lead := leadAt(at)
	if a.vn == 0 {
		a.vfirst, a.vlo, a.vhi, a.vloSrc, a.vloAt = lead, 0, 0, src, at
		a.vn = 1
		return
	}
	d := wrap(lead - a.vfirst)
	if d < a.vlo {
		a.vlo, a.vloSrc, a.vloAt = d, src, at
	}
	a.vhi = max(a.vhi, d)
	a.vn++
}

// realign takes the video's alignment from the window that just ended:
// the frame of the least lead is placed TrackingGridMargin before its
// frame start, and every other frame up to TrackingDriftMargin more, when
// the window's frames agree that far. Of the offsets that do so, the one
// within half a period of the target is taken: the window's frames were
// measured with the target it began with, and one that moved a period
// since must not be moved back by the measurement, only by alignUp.
// Caller holds a.mu.
func (a *TrackingAnchor) realign() {
	r, ok := ProgramRate()
	if ok && a.vn >= 2 && a.vrate == r && a.vhi-a.vlo <= TrackingDriftMargin {
		a.grid, a.vref = r, a.vloSrc
		a.kref = PTSIndex(r, a.vloAt+a.vfirst+a.vlo)
		half := roundDiv(90000*r.Den, 2*r.Num)
		at := func(k uint64) int64 { return IndexPTS(r, k) - TrackingGridMargin - a.vref }
		for at(a.kref) < a.target-half {
			a.kref++
		}
		for a.kref > 0 && at(a.kref) > a.target+half {
			a.kref--
		}
	}
	a.vn = 0
}

// alignUp returns the target for offset lo: lo without an alignment, and
// with one, an offset that places the video on it. It moves up to the
// least such offset at or above lo once the target is more than
// TrackingGridMargin below lo, and down to it only once the next lower
// one is TrackingDriftMargin or more above lo and above the largest
// lateness of this window and the last: a raise would take the offset up
// to that lateness, and the target straight back up with it, as a sender
// clock that runs slow arrives later until it does. At a rate whose period is
// not a whole number of ticks, the offsets that place the video step by
// 1501 and 1502 ticks, say, and which of them a measurement finds can
// change by a tick; and a sender clock that runs fast moves both the
// alignment and a lowering offset down by about the same drift a window.
// Without that much allowed either way, the source would move a period
// up and back whenever they did. The target is thus within
// [lo-TrackingGridMargin, lo+period+TrackingDriftMargin), and above it by
// as far as the latest lateness is over lo while a raise is pending.
// Caller holds a.mu.
func (a *TrackingAnchor) alignUp(lo int64) int64 {
	if r, ok := ProgramRate(); !ok || r != a.grid {
		a.grid = Rate{}
		return lo
	}
	at := func(k uint64) int64 { return IndexPTS(a.grid, k) - TrackingGridMargin - a.vref }
	if at(a.kref) < lo-TrackingGridMargin {
		a.kref = startAtOrAfter(a.grid, lo+a.vref)
	} else if a.kref > 0 && at(a.kref-1) >= a.latest(lo)+TrackingDriftMargin {
		a.kref = startAtOrAfter(a.grid, lo+TrackingGridMargin+a.vref)
	}
	return at(a.kref)
}

// latest returns the largest of lo and the latenesses of the current window
// and, once one has ended, the last. Caller holds a.mu.
func (a *TrackingAnchor) latest(lo int64) int64 {
	l := max(lo, a.cur)
	if a.windows > 0 {
		l = max(l, a.maxes[0])
	}
	return l
}

// startAtOrAfter returns the index of the first frame start of rate r at
// or after the engine PTS pts.
func startAtOrAfter(r Rate, pts int64) uint64 {
	i := PTSIndex(r, pts)
	for IndexPTS(r, i) < pts {
		i++
	}
	return i
}

// place maps src to the engine timeline. Caller holds a.mu.
func (a *TrackingAnchor) place(src int64) int64 {
	now := NowPTS()
	late := now - src
	was, placed, step := a.offset, false, false
	switch {
	case !a.set || late-a.offset > MaxAnchorSkew || a.offset-late > MaxAnchorSkew:
		if a.set {
			a.moves++
		}
		a.set = true
		a.offset, a.cur, a.windows = late, late, 0
		a.windowEnd = now + TrackingWindow
		a.grid, a.vn = Rate{}, 0
		a.ceils = [2]int64{late + MaxSourceHold, late + MaxSourceHold}
		placed = true
	case now >= a.windowEnd:
		a.realign()
		a.ceils[1], a.ceils[0] = a.ceils[0], late+MaxSourceHold
		copy(a.maxes[1:], a.maxes[:])
		a.maxes[0], a.cur = a.cur, late
		a.windows++
		a.windowEnd = now + TrackingWindow
		later := min(a.maxes[0], a.maxes[1])
		switch {
		case a.windows < TrackingSettle:
			a.offset = max(a.maxes[0], late)
		case a.windows == TrackingSettle:
			a.offset = slices.Max(a.maxes[:TrackingSettle-1])
		case later > a.offset+TrackingSlack():
			a.offset, a.raisedAt = later, a.windows
		case a.applied < a.offset && a.windows >= a.raisedAt+1 && later > a.applied+TrackingSlack():
			step = true
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
	a.ceils[0] = min(a.ceils[0], ceil)
	a.offset, a.cur = min(a.offset, ceil), min(a.cur, ceil)
	for i := range a.maxes {
		a.maxes[i] = min(a.maxes[i], ceil)
	}
	if placed {
		a.placedAt = a.offset
	} else if a.offset > was {
		a.raises++
	}
	// An alignment past what this window or the last let any timestamp be
	// mapped with is dropped rather than clamped: clamped each time, the
	// target would move with every essence the bound comes from, and the
	// source slew without end.
	if a.target = a.alignUp(a.offset); a.target > min(a.ceils[0], a.ceils[1]) {
		a.target = a.offset
	}
	a.slew(now, step)
	a.applied = min(a.applied, ceil)
	return src + a.applied
}

// slew moves the offset timestamps are mapped with towards the target:
// at once before the settle, and after it at TrackingSlewRate, or at once
// as a step when step is set or the move is more than TrackingSlewMax.
// Caller holds a.mu.
func (a *TrackingAnchor) slew(now int64, step bool) {
	d := a.target - a.applied
	switch {
	case d == 0 || a.windows < TrackingSettle:
		a.applied, a.slewAt = a.target, now
	case step || abs(d) > TrackingSlewMax:
		a.applied, a.slewAt = a.target, now
		a.steps++
	default:
		if n := (now - a.slewAt) / TrackingSlewRate; n > 0 {
			a.slewAt += n * TrackingSlewRate
			a.applied += max(-n, min(n, d))
		}
	}
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
	// when a window forgot a lateness. Once settled, the source's
	// timestamps get there at TrackingSlewRate.
	Offset int64
	// Raises counts the timestamps that raised the offset: each moved the
	// source's later timestamps that much later on the engine timeline.
	Raises int64
	Moves  int64 // placements again after a jump
	// Steps counts the moves of a settled offset taken at once rather than
	// slewed: each moved the source's video and audio by a step.
	Steps int64
}

// Stats returns the anchor's placement statistics.
func (a *TrackingAnchor) Stats() TrackingStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return TrackingStats{Offset: a.offset - a.placedAt, Raises: a.raises, Moves: a.moves, Steps: a.steps}
}
