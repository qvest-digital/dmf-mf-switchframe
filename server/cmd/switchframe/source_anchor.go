package main

import (
	"sync"

	"github.com/zsiec/switchframe/server/clock"
)

// playoutAnchors places each playout channel's continuous output PTS on
// the engine timeline. The channel's frame ticker runs on the engine's own
// clock, so each run of the channel is placed once, at its first frame,
// and again only after a jump.
var playoutAnchors sync.Map // source key -> *gridAnchor

// playoutRate is the program rate playout anchors place on, set by
// initPlayout; nil places on 50 fps.
var playoutRate func() clock.Rate

func playoutAnchor(key string) *gridAnchor {
	a, _ := playoutAnchors.LoadOrStore(key, &gridAnchor{rate: playoutRate})
	return a.(*gridAnchor)
}

// gridAnchor is a clock.Anchor that places a source on the program's
// frame grid: its first timestamp maps to the first frame start of the
// program rate at or after the clock, not to the clock itself. A tick
// shows the newest frame stamped at or before its read point, which lies
// on that grid, and plays the audio stamped from its read point on, so a
// source placed between two grid points had its pictures shown up to a
// period after their stamps and its audio played on them: up to a period
// ahead of its pictures. A source whose frames step by the program
// period, as a playout channel's do, stays on the grid.
type gridAnchor struct {
	rate   func() clock.Rate
	mu     sync.Mutex
	offset int64
	set    bool
}

// PTS maps the source timestamp src to the engine timeline.
func (a *gridAnchor) PTS(src int64) int64 {
	now := clock.NowPTS()
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := src + a.offset - now; !a.set || d > clock.MaxAnchorSkew || d < -clock.MaxAnchorSkew {
		a.offset, a.set = onGrid(a.programRate(), now)-src, true
	}
	return src + a.offset
}

func (a *gridAnchor) programRate() clock.Rate {
	if a.rate != nil {
		if r := a.rate(); r.Num > 0 && r.Den > 0 {
			return r
		}
	}
	return clock.Rate{Num: 50, Den: 1}
}

// onGrid returns the PTS of the first frame start of rate r at or after
// pts.
func onGrid(r clock.Rate, pts int64) int64 {
	i := clock.PTSIndex(r, pts)
	for clock.IndexPTS(r, i) < pts {
		i++
	}
	return clock.IndexPTS(r, i)
}
