package audio

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// A source whose audio has no media clock, such as an SRT stream, is placed
// on the timeline at its arrival (clock.ArrivalPTS) and drifts against the
// program at its sender's clock error. Over a simulated hour at +100 ppm
// the mixer must never underrun or overrun that source's ring. The drift is not resampled: the
// mixer moves the source back onto the read point each time it runs more
// than one period off, and counts it. At +100 ppm and 40 ms periods that is
// 0.36 s / 40 ms = 9 moves an hour; this test pins that behaviour.
func TestDriftingSourceOverAnHour(t *testing.T) {
	const (
		fps       = 25
		period    = 48000 / fps // samples per program frame
		ppm       = 100
		hourTicks = 3600 * fps
	)
	start := clock.TAINow()
	now := start
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })

	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("srt:cam")
	m.SetActive("srt:cam", true)

	var frac int64
	pcm := make([]float32, 2*(period+1))
	for i := range pcm {
		pcm[i] = 0.25
	}
	underruns, maxDepth := 0, 0
	for tick := 0; tick < hourTicks; tick++ {
		now = start + int64(tick)*1_000_000_000/fps
		// The sender delivers one period of its clock, which runs ppm fast.
		frac += period * ppm
		n := int64(period) + frac/1_000_000
		frac %= 1_000_000
		m.IngestPCM("srt:cam", pcm[:2*n], clock.ArrivalPTS(int(n), 48000), 2)

		read := clock.TimestampToIndex(rate48k, now) - 2*period
		out := m.ProduceSamples(read, period, clock.IndexPTS(rate48k, read))
		if tick > 4 {
			for _, v := range out {
				if v == 0 {
					underruns++
					break
				}
			}
		}
		m.mu.Lock()
		if d := m.channels["srt:cam"].ringBuf.FrameCount(); d > maxDepth {
			maxDepth = d
		}
		m.mu.Unlock()
	}
	if underruns > 0 {
		t.Errorf("%d ticks with silence from a source that never stopped", underruns)
	}
	if limit := 10 * 1024; maxDepth >= limit {
		t.Errorf("ring reached %d samples, its capacity is %d: overrun", maxDepth, limit)
	}
	moves := m.ChannelRealigns()["srt:cam"]
	if moves < 8 || moves > 10 {
		t.Errorf("%d realigns in an hour at +%d ppm, want about 9 (one per %d-sample period of drift)",
			moves, ppm, period)
	}
	t.Logf("+%d ppm for an hour: %d realigns, max ring depth %d samples", ppm, moves, maxDepth)
}
