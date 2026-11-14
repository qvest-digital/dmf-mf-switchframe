package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// readyMaxLag is how long the program clock may go without a tick, and how
// far the program MXL flows may fall behind the clock, before the engine
// stops counting as producing its program. It is far above a grain period,
// so load that only delays a tick does not cost readiness, and far below
// how long a peer's restart takes, so an engine that stopped producing is
// seen before its peer is taken down.
const readyMaxLag = time.Second

// producing returns why the engine does not produce its program, nil once it
// does: its startup has settled, the program clock ticks, the program MXL
// flows are written at the clock and every destination it sends has
// started. What the program shows does not count: with no source on air it
// is black, which is a program too, and an engine whose inputs are not
// there yet stays reachable for an operator to set them up. It does not
// ask the peer, so an engine alone becomes ready too.
func (a *App) producing() error {
	if !a.startupDone.Load() {
		return errors.New("startup has not settled")
	}
	last := a.sw.LastTick()
	if last.IsZero() {
		return errors.New("program clock has not ticked")
	}
	if since := time.Since(last); since > readyMaxLag {
		return fmt.Errorf("program clock has not ticked for %s", since.Round(time.Millisecond))
	}
	if a.mxlOutput != nil {
		if err := a.mxlOutput.Writer().AtClock(readyMaxLag); err != nil {
			return fmt.Errorf("mxl output: %w", err)
		}
	}
	if a.outputMgr != nil {
		if ids := a.outputMgr.UnstartedDestinations(); len(ids) > 0 {
			return fmt.Errorf("destinations not started: %s", strings.Join(ids, ", "))
		}
	}
	return nil
}
