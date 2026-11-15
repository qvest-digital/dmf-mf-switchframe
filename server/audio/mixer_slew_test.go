package audio

import (
	"fmt"
	"math"
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// A source whose stamps move by a raise of 46 ms and back, as a
// TrackingAnchor slews them at TrackingSlewRate, is resampled onto them as
// they move: every read continues the audio before it, with no silence
// and no step larger than the sine's own, nothing is counted as a break,
// realign or underrun, and once the stamps stop, every sample airs on its
// stamp again, within the sample a resampling under 2 samples off leaves.
// Placed on its stamps after a move, as the source was, the raise aired
// 46 ms of silence and the lowering spliced 46 ms out. At 44.1 and 48 kHz.
func TestSlewedSourceFollowsItsMovingStampsWithoutABreak(t *testing.T) {
	for _, sampleRate := range []int{44100, 48000} {
		t.Run(fmt.Sprint(sampleRate), func(t *testing.T) {
			r := newSourceRig(t, sampleRate, true)
			const raise = 46 * 90 // ticks
			slew := int64(raise) * clock.TrackingSlewRate * 100_000 / 9
			offset := func(at int64) int64 {
				switch {
				case at < 4e9:
					return 0
				case at < 4e9+slew:
					return (at - 4e9) * 9 / 100_000 / clock.TrackingSlewRate
				case at < 30e9:
					return raise
				case at < 30e9+slew:
					return raise - (at-30e9)*9/100_000/clock.TrackingSlewRate
				}
				return 0
			}
			// A sample of the sine moves by at most 2*pi*1000/sr of its
			// 0.25 amplitude, 0.5% more resampled.
			most := 0.25 * 2 * math.Pi * 1000 / float64(sampleRate) * 1.006
			var exact int
			r.run(r.start+50e9, offset, func(idx uint64, out []float32) {
				if r.reads < 10 {
					r.prevL = out[len(out)-2]
					return
				}
				for i := 0; i < len(out)/2; i++ {
					if d := math.Abs(float64(out[2*i] - r.prevL)); d > most {
						t.Fatalf("read at %d ms: sample %d steps by %.4f, more than the sine's %.4f",
							(clock.IndexToTimestamp(clock.Rate{Num: r.sr, Den: 1}, idx)-r.start)/1_000_000, i, d, most)
					}
					r.prevL = out[2*i]
				}
				if at := clock.IndexToTimestamp(clock.Rate{Num: r.sr, Den: 1}, idx) - r.start; at > 45e9 {
					// On its stamps again: sample s airs at start + s.
					for i := 0; i < len(out)/2; i++ {
						want := float64((int64(idx) + int64(i) - int64(clock.TimestampToIndex(clock.Rate{Num: r.sr, Den: 1}, r.start))) % sourceRigRamp)
						// The kernel rings for its length about the count's wrap.
						nearWrap := want < slewTaps || want > sourceRigRamp-slewTaps
						if got := aired(out, i); !nearWrap && math.Abs(got-want) > 1+1e-3 {
							t.Fatalf("read at %d ms: frame %d airs source sample %.3f, want %.0f", at/1_000_000, i, got, want)
						}
					}
					exact++
				}
			})
			if exact == 0 {
				t.Fatal("no read after the stamps stopped")
			}
			if b, ra, u := r.m.ChannelBreaks()[r.key], r.m.ChannelRealigns()[r.key], r.m.ChannelUnderruns()[r.key]; b != 0 || ra != 0 || u != 0 {
				t.Fatalf("%d breaks, %d realigns, %d underruns, want none", b, ra, u)
			}
		})
	}
}

// A source whose stamps step, as a TrackingAnchor's step moves them, is
// placed on them again at once rather than resampled onto them over
// seconds while its video has stepped: here 46 ms, past half a period. The
// silence that airs is counted as one break.
func TestSlewedSourcePlacedAgainOnAStep(t *testing.T) {
	for _, sampleRate := range []int{44100, 48000} {
		t.Run(fmt.Sprint(sampleRate), func(t *testing.T) {
			r := newSourceRig(t, sampleRate, true)
			const jump = 46 * 90
			var silent int
			r.run(r.start+8e9, func(at int64) int64 {
				if at >= 4e9 {
					return jump
				}
				return 0
			}, func(_ uint64, out []float32) {
				if r.reads < 50 { // before its first audio
					return
				}
				for i := 0; i < len(out); i += 2 {
					if out[i] == 0 && out[i+1] == 0 {
						silent++
					}
				}
			})
			if want := int(jump * r.sr / 90000); silent < want-2 || silent > want+2 {
				t.Fatalf("%d silent frames, want the %d of the step", silent, want)
			}
			if b := r.m.ChannelBreaks()[r.key]; b != 1 {
				t.Fatalf("%d breaks, want 1", b)
			}
		})
	}
}

// The resampling kernel interpolates a sine at any fractional position
// within -60 dB of its value up to 15 kHz at 48 kHz: transparent where a
// 4-point kernel, -27 dB at 10 kHz, is not.
func TestSlewKernelErrorIsUnder60dBTo15kHz(t *testing.T) {
	for _, f := range []float64{1000, 8000, 12000, 15000} {
		const frames = 64
		w := 2 * math.Pi * f / 48000
		hist := make([]float32, 2*frames)
		for i := range frames {
			hist[2*i] = float32(math.Sin(w*float64(i) + 0.3))
			hist[2*i+1] = float32(math.Cos(w*float64(i) + 0.3))
		}
		var sum float64
		var count int
		out := make([]float32, 2)
		for k := range 1000 {
			pos := 20 + float64(k)/1000*20
			slewAt(out, hist, 2, pos)
			for c, want := range []float64{math.Sin(w*pos + 0.3), math.Cos(w*pos + 0.3)} {
				d := float64(out[c]) - want
				sum += d * d
				count++
			}
		}
		// Relative to the sine's RMS, 1/sqrt(2).
		if db := 10 * math.Log10(sum/float64(count)/0.5); db > -60 {
			t.Errorf("%.0f Hz: interpolation error %.1f dB, want -60 dB or less", f, db)
		}
	}
}

// A source's stamps that move by up to the slew a job may take are
// followed by resampling: each job airs at most n/slewDivisor frames
// shorter or longer than it is, and the moves add up to the stamps'.
func TestSlewedJobsChangeLengthByAtMostASampleIn200(t *testing.T) {
	t.Cleanup(clock.ResetProgramRate())
	clock.SetProgramRate(clock.Rate{Num: 24, Den: 1}) // a period of 3750 ticks: re-placed at 1000 samples
	for _, jump := range []int64{600, -600} {
		t.Run(fmt.Sprint(jump), func(t *testing.T) {
			clock.Epoch()
			m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
			t.Cleanup(func() { _ = m.Close() })
			m.AddChannel("src")
			ch := m.channels["src"]
			const n = 1024
			// A sample index of today's TAI, too large for a float64 to
			// hold a fraction of a sample finely.
			start := clock.IndexToTimestamp(clock.Rate{Num: 48000, Den: 1}, 86_000_000_000_000)
			var aired, moved int64
			for j := int64(0); j < 200; j++ {
				skew := int64(0)
				if j >= 20 {
					skew = jump
				}
				pts := clock.PTS(start + (j*n+skew)*1_000_000_000/48000)
				m.IngestPCMSlewed("src", make([]float32, 2*n), pts, 2)
				got := int64(ch.ringBuf.FrameCount())
				ch.ringBuf.Pop(ch.ringBuf.Len())
				if j == 0 {
					continue // the kernel's look-ahead is held back
				}
				if d := got - n; d > n/slewDivisor+1 || d < -(n/slewDivisor+1) {
					t.Fatalf("job %d aired %d frames for %d", j, got, n)
				}
				moved += got - n
				aired += got
			}
			if ch.slewPos != math.Trunc(ch.slewPos) {
				t.Fatalf("the jobs landed at position %v, between frames", ch.slewPos)
			}
			if moved != jump {
				t.Fatalf("the jobs moved %d frames, want the stamps' %d", moved, jump)
			}
		})
	}
}

// Stamps that jump by less than half a program period are resampled onto,
// with no break; by half a period, the audio is placed again on them, a
// break, so it never airs as far from its video while resampled onto it.
// At the shortest and longest program periods, later and earlier.
func TestSlewedSourcePlacedAgainFromHalfAPeriod(t *testing.T) {
	for _, r := range []clock.Rate{{Num: 240, Den: 1}, {Num: 24000, Den: 1001}} {
		t.Cleanup(clock.ResetProgramRate())
		clock.SetProgramRate(r)
		most := clock.ProgramPeriod() * 48000 / 90000 / 2
		for _, c := range []struct {
			jump   int64
			breaks int64
		}{{most - 1, 0}, {most, 1}, {-(most - 1), 0}, {-most, 1}} {
			t.Run(fmt.Sprintf("%d_%d/%d", r.Num, r.Den, c.jump), func(t *testing.T) {
				rig := newSourceRig(t, 48000, true)
				rig.run(rig.start+3e9, func(int64) int64 { return 0 }, func(uint64, []float32) {})
				rig.skew = c.jump
				rig.run(rig.start+30e9, func(int64) int64 { return 0 }, func(uint64, []float32) {})
				if b := rig.m.ChannelBreaks()[rig.key]; b != c.breaks {
					t.Fatalf("stamps %d samples off, half a period %d: %d breaks, want %d", c.jump, most, b, c.breaks)
				}
			})
		}
	}
}

// A 44.1 kHz stream decoded to 48 kHz arrives in jobs of 1114 and 1115
// samples stamped by its own frames in 90 kHz ticks, so a job's stamp is
// up to a sample either side of where the audio before it ends. That is
// the stream on its stamps, not a move: every job airs exactly as long as
// it is, with no resampling.
func TestSlewedStreamOnItsStampsKeepsItsJobLengths(t *testing.T) {
	t.Cleanup(clock.ResetProgramRate())
	clock.SetProgramRate(clock.Rate{Num: 50, Den: 1})
	clock.Epoch()
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("src")
	ch := m.channels["src"]
	src0 := clock.PTS(clock.IndexToTimestamp(clock.Rate{Num: 48000, Den: 1}, 86_000_000_000_000))
	samples := func(j int64) int64 { return j * 1024 * 48000 / 44100 }
	for j := int64(0); j < 2000; j++ {
		n := samples(j+1) - samples(j)
		m.IngestPCMSlewed("src", make([]float32, 2*n), src0+j*1024*90000/44100, 2)
		got := int64(ch.ringBuf.FrameCount())
		ch.ringBuf.Pop(ch.ringBuf.Len())
		if j > 0 && got != n {
			t.Fatalf("job %d of %d frames aired %d", j, n, got)
		}
	}
}
