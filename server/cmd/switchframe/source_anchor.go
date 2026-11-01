package main

import (
	"sync"

	"github.com/zsiec/switchframe/server/clock"
)

// sourceAnchors places each source whose clock is not locked to the
// engine's (SRT, playout) on the engine timeline by its own timestamps:
// one anchor per source key, shared by its video and its audio, so the
// pair keeps its own A/V relation and cadence however it arrives.
var sourceAnchors sync.Map // source key -> *clock.TrackingAnchor

func sourceAnchor(key string) *clock.TrackingAnchor {
	a, _ := sourceAnchors.LoadOrStore(key, &clock.TrackingAnchor{})
	return a.(*clock.TrackingAnchor)
}
