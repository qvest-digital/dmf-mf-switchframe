package mxl

import (
	"slices"

	"github.com/zsiec/switchframe/server/clock"
)

// Replaced by tests.
var (
	programPeriod = clock.ProgramPeriod
	nowPTS        = clock.NowPTS
)

// lateAudioWindow is the span, in 90 kHz ticks, over which an
// audioPlacement takes the largest lateness of a source's audio.
const lateAudioWindow = 90000

// lateAudioHistory is how many windows in a row have to need less than an
// audioPlacement's shift for it to come down: a minute.
const lateAudioHistory = 60

// lateAudioStep is the resolution of an audioPlacement's shift: 1 ms.
const lateAudioStep = 90

// lateAudioMargin is how much later than it needs an audioPlacement mixes
// a late source, and how much less it has to need for the shift to come
// down: 5 ms. A change of the shift splices the source's audio, and a
// producer's lag wanders by a few ms.
const lateAudioMargin = 5 * lateAudioStep

// audioPlacement places an MXL source's audio and video on the program's
// timeline when its audio arrives too late for the mixer.
//
// A tick at T(n) shows the newest video frame at or before T(n-2) and mixes
// the audio of [T(n-2), T(n-1)), so a source's video may arrive up to two
// periods after its media time and its audio only one: the last sample a
// tick mixes is a period younger than the frame it shows. A producer that
// commits its audio a few tens of ms behind TAI, as a GStreamer pipeline
// does, misses that, and what has not arrived by the read point is mixed
// as silence, every period.
//
// A source whose audio overruns its shift in two windows in a row is mixed
// later by the smaller of the two windows' largest overruns, less the
// delay the mixer already reads the source with, plus lateAudioMargin, in
// whole milliseconds. Its video is shown later by the nearest whole number
// of its grains (videoGrains), so the program's A/V stays within half a
// grain of the source's own. Each change of the shift splices the
// source's audio, so it comes down only after lateAudioHistory windows in
// a row needed more than
// lateAudioMargin less, as after a start-up whose first seconds arrive
// later than the rest. A window in which no grain arrived counts as
// needing none, and a source that needs none is mixed at its media time.
type audioPlacement struct {
	shift   int64                   // 90 kHz ticks added to the mixer's audio PTS
	end     int64                   // engine PTS the current window ends at; 0 before the first grain
	cur     int64                   // largest overrun of the deadline in the current window
	maxes   [lateAudioHistory]int64 // largest overrun of the last windows, newest first
	windows int                     // windows closed since the shift last moved
}

// pts returns the PTS the mixer places a grain at whose first sample is
// at pts, for a grain handed over now, the mixer reading the source delay
// ticks behind the read point. Without a running program clock grains
// keep their PTS.
func (p *audioPlacement) pts(pts, delay int64) int64 {
	period := programPeriod()
	if period <= 0 {
		return pts
	}
	now := nowPTS()
	if p.end == 0 {
		p.end = now + lateAudioWindow
	}
	for closed := 0; now >= p.end; closed++ {
		if closed > lateAudioHistory {
			p.end = now
		}
		p.closeWindow()
		p.end += lateAudioWindow
	}
	p.cur = max(p.cur, now-pts-(clock.ReaderDelayGrains-1)*period-delay)
	return pts + p.shift
}

// videoGrains is how many grains of a video at rate r a source's video is
// shown later than its media time to stay with audio mixed shift ticks
// late: the shift in whole grains, rounded to the nearest. A frame moved
// by whole grains lands on the PTS of a later grain of its own rate, the
// read point of a later tick when the rate is the program's.
func videoGrains(r clock.Rate, shift int64) uint64 {
	if shift <= 0 || r.Num <= 0 || r.Den <= 0 {
		return 0
	}
	return uint64((2*shift*r.Num + 90000*r.Den) / (2 * 90000 * r.Den))
}

func (p *audioPlacement) closeWindow() {
	copy(p.maxes[1:], p.maxes[:len(p.maxes)-1])
	p.maxes[0], p.cur = p.cur, 0
	p.windows++
	shift := func(late int64) int64 {
		if late <= 0 {
			return 0
		}
		return min((late+lateAudioMargin+lateAudioStep-1)/lateAudioStep*lateAudioStep, clock.MaxSourceHold)
	}
	if late := min(p.maxes[0], p.maxes[1]); late > p.shift {
		p.shift, p.windows = shift(late), 0
	} else if p.windows >= lateAudioHistory {
		if s := shift(slices.Max(p.maxes[:])); s < p.shift-lateAudioMargin {
			p.shift, p.windows = s, 0
		}
	}
}
