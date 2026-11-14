package audio

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// A source placed at its arrival drifts against the program at its
// sender's clock error, and the mixer moves it back onto the read point
// each time it runs too far off. How far it may run off is a time the
// drift takes to cover, not a share of a program period: within the audio
// budget, a source read a period off its stamps at 240 fps is 4.2 ms off,
// and moving it back every 4.2 ms of drift splices its audio five times as
// often as at 50 fps for the same sender. 0.36 s of drift, an hour at
// +100 ppm, is moved back at most once per 15 ms of it at every program
// rate, and never runs the source dry.
func TestDriftingSourceIsMovedBackPerTimeNotPerPeriod(t *testing.T) {
	const (
		ppm   = 10000
		secs  = 36
		drift = int64(secs) * ppm // us of drift over the run: 360 ms
	)
	// Under the race detector, which finds nothing in this one goroutine,
	// 119.88 fps only, where a period is shorter than the tolerance.
	rates := programRates
	if raceDetector {
		rates = []clock.Rate{{Num: 120000, Den: 1001}}
	}
	for _, r := range rates {
		t.Run(rateName(r), func(t *testing.T) {
			setProgramRate(t, r)
			c := cutClock{r, 48000}
			start := clock.TAINow()
			now := start
			clock.SetTAISource(func() int64 { return now })
			t.Cleanup(func() { clock.SetTAISource(nil) })

			m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
			t.Cleanup(func() { _ = m.Close() })
			m.AddChannel("srt:cam")
			m.SetActive("srt:cam", true)

			pcm := make([]float32, 2*48000/10)
			for i := range pcm {
				pcm[i] = 0.25
			}
			d := clock.ReadDelay(r)
			first := clock.TimestampToIndex(r, start) + 1
			var frac int64
			underruns := 0
			ticks := uint64(secs * r.Num / r.Den)
			for i := uint64(0); i < ticks; i++ {
				n := first + i
				now = clock.IndexToTimestamp(r, n)
				// The sender delivers one period of its clock, which
				// runs ppm fast.
				per := int64(c.samples(n) - c.samples(n-1))
				frac += per * ppm
				k := per + frac/1_000_000
				frac %= 1_000_000
				m.IngestPCM("srt:cam", pcm[:2*k], clock.ArrivalPTS(int(k), 48000), 2)

				read := c.samples(n - d)
				out := m.ProduceSamples(read, int(c.samples(n-d+1)-read), clock.IndexPTS(rate48k, read))
				if i < uint64(r.Num/r.Den) {
					continue
				}
				for _, v := range out {
					if v == 0 {
						underruns++
						break
					}
				}
			}
			if underruns > 0 {
				t.Errorf("%d reads with silence from a source that never stopped", underruns)
			}
			moves := m.ChannelRealigns()["srt:cam"]
			if limit := drift/15_000 + 1; moves > limit {
				t.Errorf("%d moves for %d ms of drift, want at most %d, one per 15 ms",
					moves, drift/1000, limit)
			}
		})
	}
}
