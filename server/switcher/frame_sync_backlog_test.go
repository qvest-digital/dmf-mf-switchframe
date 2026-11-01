package switcher

import (
	"testing"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// backlogRun drives an SRT connection whose sender sent backlog before
// the connection was first read through a TrackingAnchor, a frame
// synchronizer and a mixer channel, as the engine does, for 12 s in 1 ms
// steps with a tick every 20 ms. The backlog arrives once reading starts,
// a frame per decode, each later than its successor; the source's audio
// comes in PES packets of seventeen AAC frames, demuxed in order with the
// video around it.
type backlogRun struct {
	backlog int64 // ns sent before the connection was read
}

type backlogResult struct {
	firstRT, shownRT int64 // first real-time frame's arrival and first show, ns; -1: none
	firstShown       int64 // first frame's show, ns; -1: none
	// Ticks from the first frame shown on that found no frame due, and
	// the most consecutive ones.
	emptyAfterFirst, longestEmpty int
	// After the first real-time frame was shown: ticks without a frame
	// due, ticks showing one older than staleBound, ring drops, realigns.
	empty, stale     int
	drops, realigns  int64
	overflows, moves int64
}

const (
	backlogPeriod  = int64(20_000_000)
	backlogReadLag = clock.ReaderDelayGrains * 1800
	backlogHoldNs  = clock.MaxSourceHold * 100_000 / 9
	// staleBound is how old a shown frame may be: a hold, the read delay
	// and a period.
	staleBound = backlogHoldNs + backlogReadLag*100_000/9 + backlogPeriod
)

func (c backlogRun) run(t *testing.T) backlogResult {
	t.Helper()
	now := int64(1_798_000_000_000_000_000)
	clock.Epoch()
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })

	const (
		key      = "srt:cam"
		lateV    = int64(30_000_000) // transport and decode, in real time
		aacNs    = int64(1024) * 1_000_000_000 / 48000
		perPES   = 17
		decodeNs = int64(4_000_000) // a backlog frame's decode
		n        = 960              // samples in a period
	)
	rate48k := clock.Rate{Num: 48000, Den: 1}
	var anchor clock.TrackingAnchor
	fs := NewFrameSynchronizer()
	fs.AddSource(key)
	ss := fs.source(key)
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel(key)
	m.SetActive(key, true)
	pcm := make([]float32, 2*1024)
	for i := range pcm {
		pcm[i] = 0.25
	}
	src := func(ns int64) int64 { return 1_000_000 + ns*9/100_000 }

	start := now
	// arrival[pts] is when the frame placed at pts arrived, relative to
	// start; realtime[pts] whether it arrived as sent, not from the
	// backlog. Sender time 0 is backlog before start: what it sent until
	// then arrives once reading starts, a frame per decodeNs.
	arrival := map[int64]int64{}
	realtime := map[int64]bool{}
	nextV, nextA := int64(0), int64(0)
	readyV := int64(0) // when the decoder is free for the next frame
	r := backlogResult{firstRT: -1, shownRT: -1, firstShown: -1}
	var reordered0, realigns0 int64
	run := 0
	for step := range int64(12_000) {
		at := step * 1_000_000
		now = start + at
		for {
			natural := nextV - c.backlog + lateV
			due := max(natural, readyV)
			if due > at {
				break
			}
			pts := anchor.PTS(src(nextV))
			arrival[pts], realtime[pts] = due, due == natural
			if due == natural && r.firstRT < 0 {
				r.firstRT = due
			}
			fs.IngestRawVideo(key, &ProcessingFrame{PTS: pts})
			readyV = due + decodeNs
			nextV += backlogPeriod
		}
		for nextA+perPES*aacNs <= nextV && nextA+perPES*aacNs-c.backlog+lateV <= at {
			for k := range int64(perPES) {
				m.IngestPCM(key, pcm, anchor.PTS(src(nextA+k*aacNs)), 2)
			}
			nextA += perPES * aacNs
		}
		if step%20 != 0 {
			continue
		}
		read := clock.TimestampToIndex(rate48k, now) - 2*n
		m.ProduceSamples(read, n, clock.IndexPTS(rate48k, read))
		ss.mu.Lock()
		pf := ss.popAt(clock.NowPTS() - backlogReadLag)
		ss.mu.Unlock()
		if r.firstShown >= 0 {
			if pf == nil {
				r.emptyAfterFirst++
				run++
				r.longestEmpty = max(r.longestEmpty, run)
			} else {
				run = 0
			}
		} else if pf != nil {
			r.firstShown = at
		}
		if r.shownRT < 0 {
			if pf != nil && realtime[pf.PTS] {
				r.shownRT = at
				reordered0, realigns0 = ss.reordered.Load(), m.ChannelRealigns()[key]
			}
			continue
		}
		if pf == nil {
			r.empty++
			continue
		}
		if at-arrival[pf.PTS] > staleBound {
			r.stale++
		}
	}
	r.drops = ss.reordered.Load() - reordered0
	r.realigns = m.ChannelRealigns()[key] - realigns0
	r.overflows, r.moves = ss.overflows.Load(), anchor.Moves()
	return r
}

// An SRT connection is read only once it is wired and its decoder is
// open, and what its sender sent until then waits in the socket: on a
// GPU engine whose card is at its encode-session cap, opening the preview
// encoder alone took 2.5 s, and opening the decoder about 0.9 s more. The
// connection's first frames are that backlog, decoded faster than real
// time, each later than its successor. Placed by the first of them, no
// frame of the connection is due for as long as its TrackingAnchor
// remembers that lateness, while the ring, which keeps at most a hold of
// frames ahead, drops every one of them: the program freezes, then goes
// black. The source's audio goes through the same anchor and into a mixer
// channel.
//
// The anchor places a frame at most a hold (clock.MaxSourceHold) after
// its arrival, and a tick reads clock.ReaderDelayGrains behind the clock,
// once a period: the connection is on program within that sum of its
// first frame that arrived in real time, and every tick from then on has
// a frame due, no older than that sum. The anchor stops remembering the
// backlog after two TrackingWindows and steps its offset back once: that
// costs at most one ring drop and one audio realign, never a storm.
func TestFrameSyncShowsAConnectionThatStartsWithABacklog(t *testing.T) {
	r := backlogRun{backlog: 3_400_000_000}.run(t)
	if r.firstRT < 0 {
		t.Fatal("no frame arrived in real time: the backlog never drained")
	}
	if r.shownRT < 0 || r.shownRT-r.firstRT > staleBound {
		t.Fatalf("first real-time frame arrived at %d ms, first shown at %d ms (-1: never), want within %d ms; anchor moves %d",
			r.firstRT/1_000_000, r.shownRT/1_000_000, staleBound/1_000_000, r.moves)
	}
	if r.empty > 0 || r.stale > 0 {
		t.Fatalf("after the first real-time frame was shown, %d ticks found no frame due and %d showed one older than %d ms",
			r.empty, r.stale, staleBound/1_000_000)
	}
	if r.drops > 1 || r.realigns > 1 {
		t.Fatalf("after the first real-time frame was shown: %d ring drops and %d audio realigns, want at most one each",
			r.drops, r.realigns)
	}
	t.Logf("first real-time frame at %d ms, shown at %d ms; %d ring drops, %d realigns after; %d overflows in all",
		r.firstRT/1_000_000, r.shownRT/1_000_000, r.drops, r.realigns, r.overflows)
}

// A backlog of about a second, as a reconnect whose first read waits for
// the previous connection's teardown builds, is longer than a hold: the
// anchor places its tail at arrival plus a hold, its frames closer
// together than a period. They are still waiting when the source's frames
// arrive a period apart again. The ring keeps every one of them: a ring
// that sized the hold in frame periods dropped its oldest frames, the next
// ones due, and the program froze for about 250 ms half a second in. What
// remains is the step the anchor takes once for the first AAC PES packet,
// which arrives later than the video of its time: a few ticks, never a
// freeze the switcher logs as a recovered source (over 100 ms).
func TestFrameSyncKeepsABacklogTailPlacedAhead(t *testing.T) {
	for _, backlogMs := range []int64{600, 800, 1_000, 1_200} {
		r := backlogRun{backlog: backlogMs * 1_000_000}.run(t)
		if r.firstShown < 0 {
			t.Fatalf("backlog %d ms: the connection was never shown", backlogMs)
		}
		if r.overflows > 0 || int64(r.longestEmpty)*backlogPeriod > 100_000_000 {
			t.Errorf("backlog %d ms: %d frames dropped unread, %d ticks without a frame due after the first was shown, %d in a row; want none dropped and no run over 100 ms",
				backlogMs, r.overflows, r.emptyAfterFirst, r.longestEmpty)
		}
	}
}
