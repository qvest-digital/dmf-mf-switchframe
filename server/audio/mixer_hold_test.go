package audio

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// An SRT source's video and AAC share one clock.TrackingAnchor, placed at
// the latest arrival of either. A video frame that arrives 400 ms late
// raises the offset at once, and the AAC that follows, in PES packets of
// eight frames, is placed that much further ahead of its arrival and
// waits in the channel's ring until the read point reaches it. The ring
// must hold a hold as long as the video stores keep frames for
// (clock.MaxSourceHold), at every program rate and mixer rate; a full
// ring drops its oldest samples, which are the next ones to be read.
func TestMixerRingHoldsAnAnchoredSourceThroughALatenessSpike(t *testing.T) {
	forEachCutClock(t, ringHoldsALatenessSpike)
}

func ringHoldsALatenessSpike(t *testing.T, c cutClock) {
	const (
		lateV  = int64(30_000_000)
		spike  = int64(400_000_000)
		perPES = 8
	)
	aacNs := int64(1024) * 1_000_000_000 / int64(c.sr)
	start := clock.TAINow()
	now := start
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	m := NewMixer(MixerConfig{SampleRate: c.sr, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("srt:cam")
	m.SetActive("srt:cam", true)

	var anchor clock.TrackingAnchor
	pcm := make([]float32, 2*1024)
	for i := range pcm {
		pcm[i] = 0.25
	}
	src := func(ns int64) int64 { return 1_000_000 + ns*9/100_000 }
	depth := func() int {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.channels["srt:cam"].ringBuf.FrameCount()
	}
	d := clock.ReadDelay(c.r)
	n := clock.TimestampToIndex(c.r, start) + 1
	nextV, nextA := int64(0), int64(0)
	maxDepth, silent := 0, 0
	for step := int64(0); step < 8_000; step++ { // 8 s in 1 ms steps
		now = start + step*1_000_000
		for {
			late := lateV
			if nextV == 5_000_000_000 {
				late += spike
			}
			if nextV+late > now-start {
				break
			}
			anchor.PTS(src(nextV))
			nextV += 20_000_000
		}
		for nextA+perPES*aacNs+lateV <= now-start {
			for k := int64(0); k < perPES; k++ {
				m.IngestPCM("srt:cam", pcm, anchor.PTS(src(nextA+k*aacNs)), 2)
			}
			maxDepth = max(maxDepth, depth())
			nextA += perPES * aacNs
		}
		for ; clock.IndexToTimestamp(c.r, n) <= now; n++ {
			read := c.samples(n - d)
			out := m.ProduceSamples(read, int(c.samples(n-d+1)-read), clock.IndexPTS(clock.Rate{Num: int64(c.sr), Den: 1}, read))
			if step < 2000 {
				continue
			}
			for _, v := range out {
				if v == 0 {
					silent++
					break
				}
			}
		}
	}
	limit := m.channelRingFrames()
	t.Logf("max ring depth %d samples of %d, %d silent reads", maxDepth, limit, silent)
	if maxDepth >= limit {
		t.Fatalf("ring reached %d samples, its capacity is %d: the spike overran it", maxDepth, limit)
	}
}
