package playout

import "sync"

// cuedStart holds the first picture of the item a cued channel starts on,
// with its sound, for the program tick that cuts or mixes to the channel:
// that tick puts them out stamped with its read point (Channel.startAt),
// and the run Start launches goes on from the picture after them.
type cuedStart struct {
	frame *DecodedFrame

	// mu is held by the tick while it puts the picture out, so a Start
	// that finds the cue taken waits until the picture and sound are out.
	mu      sync.Mutex
	taken   bool  // a tick put the picture out, stamped pts
	retired bool  // no tick may take it any more
	pts     int64 // the read point of the tick that took it
}

// retire makes the cue one no tick takes any more and reports whether a
// tick took it before.
func (cs *cuedStart) retire() (taken bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.retired = true
	return cs.taken
}

// cueOutputs are where a program tick puts a cue out (Manager.StartCuedAt).
type cueOutputs struct {
	// place is told the key of the channel and the read point its run is
	// stamped from, before the picture goes out.
	place func(key string, pts int64)
	video func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool)
	pcm   func(key string, pcm []float32, pts int64, channels int, start bool)
}

// startAt puts the cue of the channel out stamped readPTS, the read point
// of the program tick running, which calls it before it reads its sources:
// the item's first picture and its sound from the item's first sample, so
// the tick shows and plays them. It reports whether there was a cue to put
// out. It decodes nothing and does not wait: a cue a Start or a new cue is
// retiring meanwhile is left alone.
func (c *Channel) startAt(readPTS int64, out *cueOutputs) bool {
	if c.preroll == nil {
		return false
	}
	cs := c.preroll.cue.Load()
	if cs == nil || !cs.mu.TryLock() {
		return false
	}
	defer cs.mu.Unlock()
	if cs.taken || cs.retired {
		return false
	}
	cs.taken, cs.pts = true, readPTS

	key := c.Key()
	if out.place != nil {
		out.place(key, readPTS)
	}
	f := cs.frame
	if out.video != nil {
		out.video(key, f.YUV, f.Width, f.Height, readPTS, f.IsKeyframe)
	}
	// The channel has no run, so its PCM stream is the cue's to start. The
	// run's first tick, which plays the same picture, takes it over
	// (Channel.start), so the stream goes on from it unbroken.
	pcm := func([]float32, int64, int, bool) {}
	if out.pcm != nil {
		pcm = func(p []float32, pts int64, channels int, start bool) { out.pcm(key, p, pts, channels, start) }
	}
	c.pcmSet = false
	c.emitPCM(f, pcm, readPTS, 0)
	return true
}

// cueOn makes item the one the channel is cued on, nil when it is cued on
// none, and offers its first picture as the cue once it is prepared.
func (p *preroll) cueOn(item *PlaylistItem) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if item != nil {
		it := *item
		item = &it
	}
	p.cueItem = item
	p.offerCueLocked()
}

// offerCueLocked keeps cue the first picture of the cued item's
// preparation while it is ready, and retires a cue it no longer is.
// Caller holds p.mu.
func (p *preroll) offerCueLocked() {
	var first *DecodedFrame
	if it := p.cueItem; it != nil {
		if e := p.entries[it.ID]; e != nil && sameMedia(&e.item, it) && e.isDone() {
			first = primedFirst(e.src)
		}
	}
	old := p.cue.Load()
	if old != nil && old.frame == first {
		return
	}
	if old != nil {
		old.retire()
	}
	if first == nil {
		p.cue.Store(nil)
		return
	}
	p.cue.Store(&cuedStart{frame: first})
}

// primedFirst returns the picture a preparation decoded first and has not
// handed out yet, nil when there is none.
func primedFirst(src FrameIterator) *DecodedFrame {
	var pi *primedIterator
	switch it := src.(type) {
	case *primedIterator:
		pi = it
	case *primedLoopingIterator:
		pi = it.primedIterator
	}
	if pi == nil || !pi.primed || pi.err != nil || pi.first == nil || len(pi.first.YUV) == 0 {
		return nil
	}
	return pi.first
}

// sameFrame reports whether a and b show the same decoded picture.
func sameFrame(a, b *DecodedFrame) bool {
	return len(a.YUV) > 0 && len(b.YUV) > 0 && &a.YUV[0] == &b.YUV[0]
}
