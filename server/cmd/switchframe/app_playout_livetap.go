package main

import (
	"sync"

	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

// playoutLiveTaps reads a live item's picture on an engine without a GPU
// into a buffer from the switcher's frame pool, so airing an input costs
// no allocation per frame. The buffer a channel emits as is goes into the
// pipeline, which returns it to its pool once done with it. A buffer the
// channel read but did not emit as is, blended into a transition or
// scaled, goes back when the channel emits its frame.
type playoutLiveTaps struct {
	sw *switcher.Switcher

	mu     sync.Mutex
	leases [playout.MaxChannels][]playoutLiveTapLease
}

type playoutLiveTapLease struct {
	buf  []byte
	pool *switcher.FramePool
}

// read returns sourceKey's latest picture for channelKey, or nil before
// one has arrived.
func (t *playoutLiveTaps) read(channelKey, sourceKey string) *playout.DecodedFrame {
	idx := playoutChannelIDFromKey(channelKey) - 1
	pool := t.sw.GetFramePool()
	var buf []byte
	if pool != nil && idx >= 0 && idx < playout.MaxChannels {
		buf = pool.Acquire()
	}
	f, ok := t.sw.ReadLiveTap(sourceKey, buf)
	if buf != nil {
		if ok && &f.Data[0] == &buf[0] {
			t.mu.Lock()
			t.leases[idx] = append(t.leases[idx], playoutLiveTapLease{buf: buf, pool: pool})
			t.mu.Unlock()
		} else {
			// No picture, or one larger than the pool's buffers.
			pool.Release(buf)
		}
	}
	if !ok {
		return nil
	}
	return &playout.DecodedFrame{
		YUV: f.Data, Width: f.Width, Height: f.Height, PTS: f.PTS, IsKeyframe: true,
	}
}

// settle hands channelKey's buffer that pf carries to the pipeline and
// returns the channel's other buffers to their pool. pf is the frame the
// channel emits, or nil when it stops.
func (t *playoutLiveTaps) settle(channelKey string, pf *switcher.ProcessingFrame) {
	idx := playoutChannelIDFromKey(channelKey) - 1
	if idx < 0 || idx >= playout.MaxChannels {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, l := range t.leases[idx] {
		if pf != nil && len(pf.Data) > 0 && &pf.Data[0] == &l.buf[0] {
			// Managed, since the frame-sync ring keeps the frame across
			// ticks; see ProcessingFrame.Ref.
			pf.SetPool(l.pool)
			pf.SetRefs(1)
			continue
		}
		l.pool.Release(l.buf)
	}
	clear(t.leases[idx])
	t.leases[idx] = t.leases[idx][:0]
}
