package main

import (
	"sync"

	"github.com/zsiec/switchframe/server/clock"
)

// sourceAnchors places each source whose clock is not locked to the
// engine's, an SRT stream, on the engine timeline by its own timestamps:
// one anchor per source key, shared by its video and its audio, so the
// pair keeps its own A/V relation and cadence however it arrives.
var sourceAnchors sync.Map // source key -> *clock.TrackingAnchor

func sourceAnchor(key string) *clock.TrackingAnchor {
	a, _ := sourceAnchors.LoadOrStore(key, &clock.TrackingAnchor{})
	return a.(*clock.TrackingAnchor)
}

// playoutAnchors places each playout channel's continuous output PTS on
// the engine timeline. The channel's frame ticker runs on the engine's own
// clock, so the channel is placed once, at its first frame, and again only
// after a jump such as a restart.
var playoutAnchors sync.Map // source key -> *clock.Anchor

func playoutAnchor(key string) *clock.Anchor {
	a, _ := playoutAnchors.LoadOrStore(key, &clock.Anchor{})
	return a.(*clock.Anchor)
}
