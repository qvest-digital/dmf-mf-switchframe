package switcher

import (
	"testing"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// An SRT connection is read only once it is wired and its decoder is
// open, and what its sender sent until then waits in the socket: on a
// GPU engine whose card is at its encode-session cap, opening the preview
// encoder alone took 2.5 s, and opening the decoder about 0.9 s more. The
// connection's first frames are that backlog, decoded faster than real
// time, each later than its successor. Placed by the first of them, no
// frame of the connection is due for as long as its TrackingAnchor
// remembers that lateness, while the ring, which keeps at most a hold of
// frames ahead, drops every one of them: the program freezes, then goes
// black. The source's audio, in PES packets of seventeen AAC frames, goes
// through the same anchor and into a mixer channel.
//
// The anchor places a frame at most a hold (clock.MaxSourceHold) after
// its arrival, and a tick reads clock.ReaderDelayGrains behind the clock,
// once a period: the connection is on program within that sum of its
// first frame that arrived in real time, and every tick from then on has
// a frame due, no older than that sum. The anchor stops remembering the
// backlog after two TrackingWindows and steps its offset back once: that
// costs at most one ring drop and one audio realign, never a storm.
func TestFrameSyncShowsAConnectionThatStartsWithABacklog(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	clock.Epoch()
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })

	const (
		key      = "srt:cam"
		period   = int64(20_000_000)
		lateV    = int64(30_000_000) // transport and decode, in real time
		aacNs    = int64(1024) * 1_000_000_000 / 48000
		perPES   = 17
		backlog  = int64(3_400_000_000) // sent before the connection was read
		decodeNs = int64(4_000_000)     // a backlog frame's decode
		readLag  = clock.ReaderDelayGrains * 1800
		holdNs   = clock.MaxSourceHold * 100_000 / 9
		bound    = holdNs + readLag*100_000/9 + period
		n        = 960 // samples in a period
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
	firstRT, shownRT := int64(-1), int64(-1)
	var reordered0, realigns0 int64
	empty, stale := 0, 0
	for step := range int64(12_000) { // 12 s in 1 ms steps
		at := step * 1_000_000
		now = start + at
		for {
			natural := nextV - backlog + lateV
			due := max(natural, readyV)
			if due > at {
				break
			}
			pts := anchor.PTS(src(nextV))
			arrival[pts], realtime[pts] = due, due == natural
			if due == natural && firstRT < 0 {
				firstRT = due
			}
			fs.IngestRawVideo(key, &ProcessingFrame{PTS: pts})
			readyV = due + decodeNs
			nextV += period
		}
		// A PES packet is demuxed once its last AAC frame has arrived, in
		// order with the video around it.
		for nextA+perPES*aacNs <= nextV && nextA+perPES*aacNs-backlog+lateV <= at {
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
		pf := ss.popAt(clock.NowPTS() - readLag)
		ss.mu.Unlock()
		if shownRT < 0 {
			if pf != nil && realtime[pf.PTS] {
				shownRT = at
				reordered0, realigns0 = ss.reordered.Load(), m.ChannelRealigns()[key]
			}
			continue
		}
		if pf == nil {
			empty++
			continue
		}
		if at-arrival[pf.PTS] > bound {
			stale++
		}
	}
	if firstRT < 0 {
		t.Fatal("no frame arrived in real time: the backlog never drained")
	}
	if shownRT < 0 || shownRT-firstRT > bound {
		t.Fatalf("first real-time frame arrived at %d ms, first shown at %d ms (-1: never), want within %d ms; anchor moves %d",
			firstRT/1_000_000, shownRT/1_000_000, bound/1_000_000, anchor.Moves())
	}
	if empty > 0 || stale > 0 {
		t.Fatalf("after the first real-time frame was shown, %d ticks found no frame due and %d showed one older than %d ms",
			empty, stale, bound/1_000_000)
	}
	drops := ss.reordered.Load() - reordered0
	realigns := m.ChannelRealigns()[key] - realigns0
	if drops > 1 || realigns > 1 {
		t.Fatalf("after the first real-time frame was shown: %d ring drops and %d audio realigns, want at most one each",
			drops, realigns)
	}
	t.Logf("first real-time frame at %d ms, shown at %d ms; %d ring drops, %d realigns after; %d overflows in all",
		firstRT/1_000_000, shownRT/1_000_000, drops, realigns, ss.overflows.Load())
}
