package main

import (
	"sync"

	"github.com/zsiec/switchframe/server/clock"
)

// playoutAnchors places each playout channel's continuous output PTS on
// the engine timeline. The channel's frame ticker runs on the engine's own
// clock, so the channel is placed once, at its first frame, and again only
// after a jump such as a restart.
var playoutAnchors sync.Map // source key -> *clock.Anchor

func playoutAnchor(key string) *clock.Anchor {
	a, _ := playoutAnchors.LoadOrStore(key, &clock.Anchor{})
	return a.(*clock.Anchor)
}
