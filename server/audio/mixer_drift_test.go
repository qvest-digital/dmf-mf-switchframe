package audio

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// A source whose audio has no media clock, such as an SRT stream, is placed
// on the timeline at its arrival (clock.ArrivalPTS) and drifts against the
// program at its sender's clock error. The mixer must never underrun or
// overrun that source's ring. The drift is not resampled: the mixer moves
// the source back onto the read point each time it runs more than one
// period off, and counts it. The test runs an hour of drift at +100 ppm,
// 0.36 s, as 6 minutes at +1000 ppm: 0.36 s / 40 ms periods = about 9
// moves, as many as the hour makes, in a tenth of the ticks.
func TestDriftingSourceOverAnHour(t *testing.T) {
	const (
		fps    = 25
		period = 48000 / fps // samples per program frame
		ppm    = 1000
		ticks  = 360 * fps
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
	// Room for the most samples one sender period delivers.
	pcm := make([]float32, 2*(period+period*ppm/1_000_000+1))
	for i := range pcm {
		pcm[i] = 0.25
	}
	underruns, maxDepth := 0, 0
	for tick := 0; tick < ticks; tick++ {
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
	if limit := channelRingFrames; maxDepth >= limit {
		t.Errorf("ring reached %d samples, its capacity is %d: overrun", maxDepth, limit)
	}
	moves := m.ChannelRealigns()["srt:cam"]
	if moves < 8 || moves > 10 {
		t.Errorf("%d realigns in 6 minutes at +%d ppm, want about 9 (one per %d-sample period of drift)",
			moves, ppm, period)
	}
	t.Logf("+%d ppm for 6 minutes: %d realigns, max ring depth %d samples", ppm, moves, maxDepth)
}

// The same hour of +100 ppm drift, 0.36 s, for a source placed by its own
// PTS through a clock.TrackingAnchor, as an SRT stream is, its audio
// arriving in 180 ms bursts as an MPEG-TS muxer's PES packets deliver it:
// the ring never underruns or overruns, and the drift is moved back at most
// once per period of it. It runs as 36 s at +10000 ppm, a hundredth of the
// ticks, as the anchor's two-second windows then still step by half a
// period at most.
func TestDriftingAnchoredSourceOverAnHour(t *testing.T) {
	const (
		fps      = 25
		period   = 48000 / fps // samples per program frame
		ppm      = 10000
		ticks    = 36 * fps
		perBurst = 9 // 20 ms batches in a 180 ms burst
		batch    = 960
	)
	start := clock.TAINow()
	now := start
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })

	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("srt:cam")
	m.SetActive("srt:cam", true)

	var anchor clock.TrackingAnchor
	pcm := make([]float32, 2*batch)
	for i := range pcm {
		pcm[i] = 0.25
	}
	// The sender's clock runs ppm fast: its batches of 20 ms of its own
	// time arrive every 20 ms less ppm, nine at a time.
	senderBatch := int64(20_000_000) * 1_000_000 / (1_000_000 + ppm)
	var srcPTS int64 = 1_000_000
	var sent int64
	underruns, maxDepth := 0, 0
	for tick := 0; tick < ticks; tick++ {
		now = start + int64(tick)*1_000_000_000/fps
		for (sent+perBurst)*senderBatch <= now-start {
			for k := 0; k < perBurst; k++ {
				m.IngestPCM("srt:cam", pcm, anchor.PTS(srcPTS), 2)
				srcPTS += 1800
			}
			sent += perBurst
		}

		read := clock.TimestampToIndex(rate48k, now) - 2*period
		out := m.ProduceSamples(read, period, clock.IndexPTS(rate48k, read))
		if tick > 25 {
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
	if limit := channelRingFrames; maxDepth >= limit {
		t.Errorf("ring reached %d samples, its capacity is %d: overrun", maxDepth, limit)
	}
	moves := m.ChannelRealigns()["srt:cam"]
	// Each move places the ring exactly, so the drift takes no more moves
	// than placing every batch at its arrival does.
	if moves < 1 || moves > 10 {
		t.Errorf("%d realigns in 36 s at +%d ppm, want 1 to 10 (at most one per %d-sample period of drift)",
			moves, ppm, period)
	}
	t.Logf("+%d ppm for 36 s in bursts: %d realigns, max ring depth %d samples", ppm, moves, maxDepth)
}
