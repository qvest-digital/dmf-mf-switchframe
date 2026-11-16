package main

import (
	"fmt"
	"math"
	"sync/atomic"
	"testing"

	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/switcher"
)

// anchorMoveRun is what srtAnchorMove saw of the aired program audio.
type anchorMoveRun struct {
	breaks  int                 // breaks of the aired audio from 10 s on, counted from its samples
	counted int64               // breaks the mixer counted for the source from 10 s on
	maxAV   int64               // largest distance of the audio from its video's stamps, in 90 kHz ticks
	airAV   int64               // the same from its video as it airs, from half a second after the anchor last moved it
	settled clock.TrackingStats // the anchor's at 10 s
	stats   clock.TrackingStats // the anchor's at the end
}

const anchorMoveKey = "srt:move"

// srtAnchorMove runs an SRT source through its anchor and ingestSRTAudio
// for end ns at program rate r, which the caller set, and reads the program at every tick, as the
// sync loop does. Video comes at the program rate and AAC frames of 1024
// samples at srcRate, which the decoder hands over at 48 kHz; the left
// channel is a sine, the right one counts the 48 kHz samples. Each frame
// arrives as it has passed, a video frame a period later for its decode,
// late(video, at) later, at the time since the start it would have
// arrived, and a few ms of jitter. From 10 s on, every read is checked:
// its samples must continue the samples before, by the count on the right
// channel, at the rate a resampling under 0.5% leaves, with no silence;
// and the distance of its audio from the stamps the anchor gave the last
// video frame is recorded, and from that frame as it airs, on the first
// read point at or after its stamp. strict fails the test at the first
// break; otherwise the breaks are counted.
func srtAnchorMove(t *testing.T, r clock.Rate, srcRate int, end int64, late func(video bool, at int64) int64, strict bool) anchorMoveRun {
	t.Helper()
	var now atomic.Int64
	scriptTAI(t, &now)
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel(anchorMoveKey)
	m.SetLiveInput(anchorMoveKey)
	m.SetActive(anchorMoveKey, true)
	a := &App{mixer: m}
	var anchor clock.TrackingAnchor

	const ramp = 1 << 15 // 48 kHz samples the right channel counts before it wraps
	const warmup = 10_000_000_000
	sr := clock.Rate{Num: 48000, Den: 1}
	start := now.Load()
	src0 := clock.PTS(start)
	periodNs := clock.IndexToTimestamp(r, 1)
	frameNs := func(j int64) int64 { return j * 1024 * 1_000_000_000 / int64(srcRate) }
	samples := func(j int64) int64 { return j * 1024 * 48000 / int64(srcRate) } // 48 kHz samples before AAC frame j
	video := func(i int64) int64 {
		due := start + clock.IndexToTimestamp(r, uint64(i+1))
		return due + periodNs + late(true, due-start) + (i*7919%3)*1_000_000
	}
	audioAt := func(j int64) int64 {
		due := start + frameNs(j+1)
		return due + late(false, due-start) + (j*104729%3)*1_000_000
	}

	var res anchorMoveRun
	before := int64(-1) // the mixer's count at 10 s
	var vi, aj int64
	var vOff int64   // the anchor's offset for the last video frame, in ticks
	var vAir int64   // how much later than its source stamp the last video frame airs, in ticks
	var vMoved int64 // TAI the anchor last mapped a video frame with another offset than the one before
	var airing, counted bool
	var prevRaw float64
	var abs int64 // 48 kHz sample count of the last aired sample
	g := clock.TimestampToIndex(r, start) + 1
	for {
		tv, ta, tt := video(vi), audioAt(aj), clock.IndexToTimestamp(r, g)
		at := min(tv, ta, tt)
		if at-start > end {
			break
		}
		now.Store(at)
		switch at {
		case tv:
			pts := src0 + (vi*90000*r.Den)/r.Num
			mapped := anchor.VideoPTS(pts)
			if mapped-pts != vOff {
				vMoved = at
			}
			vOff = mapped - pts
			// The read points are the program's frame starts.
			vAir = onGrid(r, mapped) - pts
			vi++
		case ta:
			n := samples(aj+1) - samples(aj)
			pcm := make([]float32, 2*n)
			for k := range n {
				c := samples(aj) + k
				pcm[2*k] = float32(0.25 * math.Sin(2*math.Pi*997*float64(c)/48000))
				pcm[2*k+1] = float32(c%ramp) / ramp / 2
			}
			a.ingestSRTAudio(anchorMoveKey, &anchor, srtAudioJob{pcm: pcm, pts: src0 + aj*1024*90000/int64(srcRate), sampleRate: 48000, channels: 2})
			aj++
		default:
			read, n := tickRead(r, g)
			g++
			out := m.ProduceSamples(read, n, 0)
			if at-start < warmup {
				continue
			}
			if before < 0 {
				before, res.settled = m.ChannelBreaks()[anchorMoveKey], anchor.Stats()
			}
			for k := 0; k < n; k++ {
				silent := out[2*k] == 0 && out[2*k+1] == 0
				raw := float64(out[2*k+1]) * ramp * 2
				ok := !silent
				// The count wraps, and resampled, rings about the wrap.
				wrap := raw < 128 || raw > ramp-128 || prevRaw < 128 || prevRaw > ramp-128
				if ok && counted {
					d := raw - prevRaw
					if d < -ramp/2 {
						d += ramp
					}
					ok = math.Abs(d-1) <= 0.02 || wrap
					if wrap {
						abs++
					} else {
						abs += int64(math.Round(math.Remainder(raw-float64(abs%ramp), ramp)))
					}
				} else if !silent {
					// The sample count the placement of the last video
					// frame gives, wrapped to the one aired.
					est := int64(read) + int64(k) - int64(clock.PTSIndex(sr, src0)) - vOff*48000/90000
					abs = est + int64(math.Round(math.Remainder(raw-float64(est%ramp), ramp)))
				}
				if !ok && airing {
					res.breaks++
					if strict {
						t.Fatalf("read at %.3f s, sample %d of %d: the source's audio breaks (silent %v, count %.2f after %.2f)",
							float64(at-start)/1e9, k, n, silent, raw, prevRaw)
					}
				}
				airing, counted, prevRaw = !silent, !silent, raw
				if !silent && !wrap && k == 0 {
					aOff := clock.PTS(clock.IndexToTimestamp(sr, read)) - src0 - abs*90000/48000
					if d := max(aOff-vOff, vOff-aOff); d > res.maxAV {
						res.maxAV = d
					}
					if d := max(aOff-vAir, vAir-aOff); d > res.airAV && at-vMoved >= 500_000_000 {
						res.airAV = d
					}
				}
			}
		}
	}
	res.stats = anchor.Stats()
	res.counted = m.ChannelBreaks()[anchorMoveKey] - before
	return res
}

// forEachSourceRate runs f at every program rate, set as the program's,
// for a 48 kHz stream and a 44.1 kHz one, which the decoder hands over at
// 48 kHz in frames of 1114 and 1115 samples, stamped by the stream's own
// 44.1 kHz frames. Under the race detector, which finds nothing in the one
// goroutine a case runs on, each of the timingRates it keeps runs one
// stream rate, taken in turn.
func forEachSourceRate(t *testing.T, f func(t *testing.T, r clock.Rate, srcRate int)) {
	for i, srcRate := range []int{48000, 44100} {
		t.Run(fmt.Sprint(srcRate), func(t *testing.T) {
			for j, r := range timingRates {
				if raceDetector && (i+j)%2 != 0 {
					continue
				}
				t.Run(fmt.Sprintf("%d_%dfps", r.Num, r.Den), func(t *testing.T) {
					clock.SetProgramRate(r)
					t.Cleanup(func() { clock.SetProgramRate(clock.Rate{Num: 50, Den: 1}) })
					f(t, r, srcRate)
				})
			}
		})
	}
}

// maxAiredAV is how far an SRT source's audio may air from its video, in
// 90 kHz ticks, once the source's anchor holds its video still: the video
// airs up to 3 ticks after its stamp (clock.TrackingGridMargin, and a tick
// more at a rate whose period is not a whole number of ticks), and the
// mixer leaves audio up to 2 samples off its stamps, 3.75 ticks, before it
// resamples it onto them.
const maxAiredAV = 7

// A tick shows an SRT source's newest frame stamped at or before its read
// point and plays its audio from there on its stamps, so a frame stamped
// between two read points airs up to a period after its audio. Which phase
// of the period a source's stamps fall in follows from its lateness, so
// its pictures aired behind its sound by anything from nothing to a period,
// a new amount with every connection. Mapped so its video lands just
// before a read point, the source airs its audio with its video, whatever
// its lateness, at every program rate.
func TestSRTAudioAirsWithItsVideoAtAnyLateness(t *testing.T) {
	phases := int64(8)
	if raceDetector {
		phases = 2
	}
	forEachTimingRate(t, func(t *testing.T, r clock.Rate) {
		clock.SetProgramRate(r)
		t.Cleanup(func() { clock.SetProgramRate(clock.Rate{Num: 50, Den: 1}) })
		period := clock.IndexToTimestamp(r, 1)
		for q := range phases {
			late := 3_000_000 + q*period/phases
			res := srtAnchorMove(t, r, 48000, 13_000_000_000, func(bool, int64) int64 { return late }, true)
			if res.airAV > maxAiredAV {
				t.Fatalf("lateness %.2f ms: audio aired up to %d ticks from its video, more than %d",
					float64(late)/1e6, res.airAV, maxAiredAV)
			}
		}
	})
}

// The live shape of an anchor move that aired 50 ms of silence: an SRT
// source whose audio arrives on time, frame by frame, and whose video path
// stalls twice, 2.5 s apart in windows in a row, each stall's frames handed
// over at its end, TrackingSlack and 27 ms late. The anchor takes the two
// for a lasting lateness and raises its offset, and a minute on time
// lowers it again. Through both, every program read airs the source's
// audio on from the read before, with no silence and no step, and the
// audio airs within a program period of its video's stamps, and with its
// video once the anchor holds it still. Placed again on its
// moved stamps, as it was, the source's audio aired the raise as silence
// and the lowering as a splice, while the source's audio counters stayed
// flat. At every program rate, for 48 and 44.1 kHz streams.
func TestSRTAudioAirsThroughAnAnchorMove(t *testing.T) {
	forEachSourceRate(t, func(t *testing.T, r clock.Rate, srcRate int) {
		// Each stall starts as a frame is due, so its first frame is the
		// stall's length late, at the program rate's slack.
		stallNs := (clock.TrackingSlack() + 27*90) * 100_000 / 9
		var stalls []int64
		for _, from := range []int64{10_500_000_000, 13_000_000_000} {
			stalls = append(stalls, clock.IndexToTimestamp(r, clock.TimestampToIndex(r, from))+stallNs)
		}
		late := func(video bool, at int64) int64 {
			for _, e := range stalls {
				if video && at <= e && at >= e-stallNs {
					return e - at
				}
			}
			return 0
		}
		res := srtAnchorMove(t, r, srcRate, 90_000_000_000, late, true)
		// Back within the jitter the settled offset took its maximum of.
		if s := res.stats; s.Raises <= res.settled.Raises || s.Offset < res.settled.Offset-270 || s.Offset > res.settled.Offset+270 {
			t.Fatalf("anchor %+v, settled %+v: want the stalls raised for and the offset lowered back a minute on", s, res.settled)
		}
		if period := clock.ProgramPeriod(); res.maxAV > period {
			t.Fatalf("audio aired up to %d ticks from its video's stamps, more than the %d-tick period", res.maxAV, period)
		}
		if res.airAV > maxAiredAV {
			t.Fatalf("audio aired up to %d ticks from its video once the anchor held it still, more than %d", res.airAV, maxAiredAV)
		}
	})
}

// A lateness that lasts, of the whole source and past the audio budget and
// the up to a period more its anchor maps it with to air its video on a
// read point, is raised for, and two windows on the raise is taken as a
// step: until the anchor raises, the audio arrives after its reads and
// airs in pieces, and the step places it again on the moved stamps, a gap.
// None of it goes uncounted: every break the aired samples show, the
// source's breaks count once, and the step is counted on the anchor. At
// every program rate, for 48 and 44.1 kHz streams.
func TestSRTAudioCountsEveryBreakOfALastingLateness(t *testing.T) {
	forEachSourceRate(t, func(t *testing.T, r clock.Rate, srcRate int) {
		lasting := clock.TrackingSlack() + clock.ProgramPeriod() + 30*90
		late := func(_ bool, at int64) int64 {
			if at >= 12_000_000_000 {
				return lasting * 100_000 / 9
			}
			return 0
		}
		res := srtAnchorMove(t, r, srcRate, 30_000_000_000, late, false)
		if res.stats.Steps != 1 {
			t.Fatalf("anchor %+v: want the lasting raise stepped once", res.stats)
		}
		if res.breaks == 0 {
			t.Fatal("no break aired: the case does not exercise the counter")
		}
		if res.counted != int64(res.breaks) {
			t.Fatalf("the source counted %d breaks, the aired samples show %d", res.counted, res.breaks)
		}
	})
}

// ptsGPU is a GPU source manager that records the PTS of every frame the
// switcher ingests.
type ptsGPU struct{ pts chan int64 }

func (g *ptsGPU) IngestYUV(_ string, _ []byte, _, _ int, pts int64) { g.pts <- pts }
func (g *ptsGPU) RemoveSource(string)                               {}

// An SRT source's video goes through its connection's pipeline ingest,
// which maps each frame through the anchor with srtVideoPTS, as the NVDEC
// path does: once the anchor settled, every frame the switcher ingests is
// stamped just before a program frame start, so it airs on its stamp, with
// its audio. Mapped by the lateness alone, a frame lands anywhere in the
// period before one. At every program rate.
func TestSRTVideoIngestLandsJustBeforeAFrameStart(t *testing.T) {
	forEachTimingRate(t, func(t *testing.T, r clock.Rate) {
		var now atomic.Int64
		scriptTAI(t, &now)
		const key = "srt:ingest"
		sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
		// After the switcher's own clock, which sets its rate as it starts.
		clock.SetProgramRate(r)
		t.Cleanup(func() { clock.SetProgramRate(clock.Rate{Num: 50, Den: 1}) })
		gpu := &ptsGPU{pts: make(chan int64, 1)}
		sw.SetGPUSourceManager(gpu)
		m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
		t.Cleanup(func() { _ = m.Close() })
		sw.SetMixer(m)
		a := &App{sw: sw, mixer: m, srtSources: map[string]*srtSourceState{}}
		video := make(chan srtVideoJob)
		a.startSRTPipelineIngest(key, a.bindSRTSource(key), video, nil)
		t.Cleanup(func() { close(video) })

		start := now.Load()
		src0 := clock.PTS(start)
		// Late by a third of a period and 5 ms, so a frame mapped by its
		// lateness alone lands well inside a period.
		late := 5_000_000 + clock.IndexToTimestamp(r, 1)/3
		var checked int
		for i := int64(0); ; i++ {
			at := clock.IndexToTimestamp(r, uint64(i))
			if at > 10_000_000_000 {
				break
			}
			now.Store(start + at + late)
			video <- srtVideoJob{yuv: make([]byte, 16*16*3/2), w: 16, h: 16, pts: src0 + i*90000*r.Den/r.Num}
			pts := <-gpu.pts
			if at < 8_000_000_000 {
				continue
			}
			if lead := onGrid(r, pts) - pts; lead < clock.TrackingGridMargin || lead > clock.TrackingGridMargin+2 {
				t.Fatalf("frame %d ingested %d ticks before the program frame start at or after it, want %d to %d",
					i, lead, clock.TrackingGridMargin, clock.TrackingGridMargin+2)
			}
			checked++
		}
		if got, _ := clock.ProgramRate(); checked == 0 || got != r {
			t.Fatalf("%d frames checked at program rate %v, want some at %v", checked, got, r)
		}
	})
}
