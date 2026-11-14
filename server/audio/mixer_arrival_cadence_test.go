package audio

import (
	"fmt"
	"testing"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
)

// constStereoDecoder decodes every frame to 1024 stereo samples of 0.25.
type constStereoDecoder struct{}

func (constStereoDecoder) Decode([]byte) ([]float32, error) {
	out := make([]float32, 2048)
	for i := range out {
		out[i] = 0.25
	}
	return out, nil
}
func (constStereoDecoder) Channels() int { return 2 }
func (constStereoDecoder) Close() error  { return nil }

// programRates are the program frame rates the timing tests run at.
var programRates = []clock.Rate{
	{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1}, {Num: 30000, Den: 1001},
	{Num: 30, Den: 1}, {Num: 50, Den: 1}, {Num: 60000, Den: 1001}, {Num: 60, Den: 1},
	{Num: 100, Den: 1}, {Num: 120000, Den: 1001}, {Num: 120, Den: 1},
	{Num: 240000, Den: 1001}, {Num: 240, Den: 1},
}

// sourceRates are the sample rates the timing tests run sources and the
// mixer at.
var sourceRates = []int{44100, 48000, 96000}

// longSourceRates are sourceRates for a test that runs seconds of audio
// on one goroutine: under the race detector, which finds nothing there,
// 48 kHz only.
func longSourceRates() []int {
	if raceDetector {
		return []int{48000}
	}
	return sourceRates
}

func rateName(r clock.Rate) string {
	if r.Den == 1 {
		return fmt.Sprintf("%dfps", r.Num)
	}
	return fmt.Sprintf("%d_%dfps", r.Num, r.Den)
}

// setProgramRate runs the program at r for the rest of t. A program rate
// cannot be unset, and at 50 fps every bound the mixer takes from it is
// what it is without a running clock.
func setProgramRate(t *testing.T, r clock.Rate) {
	t.Helper()
	clock.SetProgramRate(r)
	t.Cleanup(func() { clock.SetProgramRate(clock.Rate{Num: 50, Den: 1}) })
}

// tickRead returns the 48 kHz samples the tick at program index n reads
// at rate r: the period clock.ReadDelay(r) periods behind it.
func tickRead(r clock.Rate, n uint64) (start uint64, samples int) {
	d := clock.ReadDelay(r)
	start = clock.TimestampToIndex(rate48k, clock.IndexToTimestamp(r, n-d))
	return start, int(clock.TimestampToIndex(rate48k, clock.IndexToTimestamp(r, n-d+1)) - start)
}

// pushAAC hands key one AAC frame of a source at sampleRate.
func pushAAC(m *Mixer, key string, sampleRate int) {
	m.IngestFrame(key, &media.AudioFrame{
		Data: []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, SampleRate: sampleRate, Channels: 2,
	})
}

// A producer that hands its AAC frames over with its video frames, as the
// replay player does, at 50 fps 48 kHz, 15 AAC frames over 16 pictures,
// one a frame period and none in every sixteenth; at 60 fps 25 over 32,
// none in about every fifth; at 100 fps and up in fewer than half.
// Placed by its arrival, each push moves the source's audio by the
// difference between an AAC frame and a period, back by a whole AAC frame
// at each empty period: a sawtooth about an AAC frame high, more than the
// period the mixer lets a source stray before it splices, a click and a
// short gap each time. Placed as a continuation of the audio before it
// even when it arrives well after that ends, a push is stamped before its
// arrival, and the read, clock.ReadDelay periods behind the clock, drops
// it. At every program rate, from a source at every sample rate the mixer
// resamples, on time and with one push in seven late, the cadence has to
// play through without a splice, a dry read or a silent sample.
func TestMixer_IngestFrame_ClipCadenceIsNeverSpliced(t *testing.T) {
	// The cadence repeats after a cycle of pictures, from 4 at 25 fps
	// from a 96 kHz source to 819200 at 240000/1001 from 44.1 kHz, so the
	// start phases are spread over the cycle: four from a 48 kHz source,
	// and one with the largest jitter from the others, which the mixer
	// resamples at a cost. The test runs on one goroutine, so the race
	// detector finds nothing in it: under it, where the audio package
	// already takes most of CI's time limit, one phase from 48 kHz at the
	// largest jitter only.
	for _, r := range programRates {
		for _, sr := range sourceRates {
			phases, jitters := 4, []int64{0, 3_000_000, 10_000_000}
			if raceDetector {
				phases, jitters = 1, jitters[2:]
			}
			if sr != 48000 {
				if raceDetector {
					continue
				}
				phases, jitters = 1, jitters[2:]
			}
			num, den := 1024*int(r.Num), sr*int(r.Den)
			cycle := num / gcd(num, den)
			phases = min(cycle, phases)
			for _, jitterNs := range jitters {
				for i := range phases {
					phase := i * cycle / phases
					name := fmt.Sprintf("%s/%dHz/jitter%dms/phase%d", rateName(r), sr, jitterNs/1_000_000, phase)
					t.Run(name, func(t *testing.T) {
						silent, realigns, underruns := playClipCadence(t, r, sr, phase, jitterNs)
						if realigns != 0 || underruns != 0 || silent != 0 {
							t.Fatalf("%d splices, %d dry reads and %d silent samples over the clip",
								realigns, underruns, silent)
						}
					})
				}
			}
		}
	}
}

// playClipCadence feeds two and a half seconds of the clip player's audio
// cadence at program rate r, from a source at sampleRate, starting
// phase pictures into it, through IngestFrame on a simulated clock,
// reading each tick's period clock.ReadDelay(r) periods behind the tick
// as the sync loop does. It returns the silent samples read after the
// first half second, and the channel's splices and dry reads after it.
func playClipCadence(t *testing.T, r clock.Rate, sampleRate, phase int, jitterNs int64) (silent int, realigns, underruns int64) {
	setProgramRate(t, r)
	first := clock.TimestampToIndex(r, clock.TAINow())
	base := clock.IndexToTimestamp(r, first)
	var now int64
	clock.SetTAISource(func() int64 { return base + now })
	t.Cleanup(func() { clock.SetTAISource(nil) })

	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
		DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
	defer func() { _ = m.Close() }()
	m.AddChannel("clip:1")
	m.SetActive("clip:1", true)

	warm := int(clock.TimestampToIndex(r, int64(time.Second/2)))
	sentBy := func(f int) int { return int(int64(f+phase+1) * int64(sampleRate) * r.Den / (1024 * r.Num)) }
	sent := sentBy(-1)
	for f := 0; f < int(clock.TimestampToIndex(r, int64(5*time.Second/2))); f++ {
		tick := clock.IndexToTimestamp(r, first+uint64(f)) - base
		now = tick
		if f%7 == 3 {
			now += jitterNs
		}
		for ; sent < sentBy(f); sent++ {
			pushAAC(m, "clip:1", sampleRate)
		}
		now = tick
		rs, rn := tickRead(r, first+uint64(f))
		out := m.ProduceSamples(rs, rn, 0)
		if f < warm {
			if f == warm-1 {
				m.mu.RLock()
				realigns, underruns = -m.channels["clip:1"].realigns, -m.channels["clip:1"].underruns
				m.mu.RUnlock()
			}
			continue
		}
		for i := 0; i < len(out); i += 2 {
			if out[i] == 0 {
				silent++
			}
		}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return silent, realigns + m.channels["clip:1"].realigns, underruns + m.channels["clip:1"].underruns
}

// A read that finds less audio than it needs plays silence for the rest;
// the source's underrun count says how often, where the realign count
// does not move.
func TestMixer_CountsDryReads(t *testing.T) {
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	pcm := make([]float32, 2*960)
	start := clock.TimestampToIndex(rate48k, clock.TAINow())
	m.IngestPCM("cam1", pcm, clock.IndexPTS(rate48k, start), 2)
	m.ProduceSamples(start, 960, 0)
	m.ProduceSamples(start+960, 960, 0)
	m.ProduceSamples(start+1920, 960, 0)
	m.IngestPCM("cam1", pcm, clock.IndexPTS(rate48k, start+2880), 2)
	m.ProduceSamples(start+2880, 960, 0)
	m.ProduceSamples(start+3840, 960, 0)

	if got := m.ChannelUnderruns()["cam1"]; got != 2 {
		t.Fatalf("underruns = %d over two stretches without audio, want 2", got)
	}
	if got := m.ChannelShortReads()["cam1"]; got != 0 {
		t.Fatalf("short reads = %d over reads that found nothing, want 0: a source that stopped counts underruns only", got)
	}
}

// Audio that starts after the read point plays silence before it: the
// read is short whether the ring starts within the period or a period
// or more later.
func TestMixer_CountsReadsBeforeTheRingsAudio(t *testing.T) {
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	pcm := make([]float32, 2*3*960)
	start := clock.TimestampToIndex(rate48k, clock.TAINow())
	m.IngestPCM("cam1", pcm, clock.IndexPTS(rate48k, start+1440), 2)
	m.ProduceSamples(start, 960, 0) // all of it before the ring's audio
	if got := m.ChannelShortReads()["cam1"]; got != 1 {
		t.Fatalf("short reads = %d after a period before the ring's audio, want 1", got)
	}
	m.ProduceSamples(start+960, 960, 0) // half of it before the ring's audio
	if got := m.ChannelShortReads()["cam1"]; got != 2 {
		t.Fatalf("short reads = %d after a read starting before the ring's audio, want 2", got)
	}
	m.ProduceSamples(start+1920, 960, 0)
	if got := m.ChannelShortReads()["cam1"]; got != 2 {
		t.Fatalf("short reads = %d after a full read, want 2", got)
	}
	if got := m.ChannelUnderruns()["cam1"]; got != 0 {
		t.Fatalf("underruns = %d, want 0", got)
	}
}

// A source whose audio reaches each read only in part is short on every
// read: one underrun that never ends, and a short read per period.
func TestMixer_CountsEveryShortRead(t *testing.T) {
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	half := make([]float32, 2*480)
	for i := range half {
		half[i] = 0.25
	}
	start := clock.TimestampToIndex(rate48k, clock.TAINow())
	const periods = 50
	for p := uint64(0); p < periods; p++ {
		// Only the first half of each period has arrived when it is read.
		m.IngestPCM("cam1", half, clock.IndexPTS(rate48k, start+p*960), 2)
		m.ProduceSamples(start+p*960, 960, 0)
		m.IngestPCM("cam1", half, clock.IndexPTS(rate48k, start+p*960+480), 2)
	}
	if got := m.ChannelUnderruns()["cam1"]; got != 1 {
		t.Fatalf("underruns = %d, want 1", got)
	}
	if got := m.ChannelShortReads()["cam1"]; got != periods {
		t.Fatalf("short reads = %d over %d short periods, want %d", got, periods, periods)
	}
}

// A source that reconnects starts afresh: a read that finds its new
// connection's audio short counts as a dry read of its own, not as the
// continuation of one before the reconnect.
func TestMixer_ResetChannelInputCountsTheNextDryRead(t *testing.T) {
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	pcm := make([]float32, 2*960)
	start := clock.TimestampToIndex(rate48k, clock.TAINow())
	m.IngestPCM("cam1", pcm, clock.IndexPTS(rate48k, start), 2)
	m.ProduceSamples(start, 960, 0)
	m.ProduceSamples(start+960, 960, 0) // dry
	m.ResetChannelInput("cam1")
	// The new connection's first audio is short of a read.
	m.IngestPCM("cam1", pcm[:2*480], clock.IndexPTS(rate48k, start+1920), 2)
	m.ProduceSamples(start+1920, 960, 0) // dry again

	if got := m.ChannelUnderruns()["cam1"]; got != 2 {
		t.Fatalf("underruns = %d, want 2: the dry read after the reconnect was not counted", got)
	}
}

// ringEnd returns where key's audio ends and how many sample frames its
// ring holds.
func ringEnd(m *Mixer, key string) (end int64, frames int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch := m.channels[key]
	return ch.ringEnd, ch.ringBuf.FrameCount()
}

// arrivalBound is the most a source's audio is placed behind its arrival
// at program rate r once the audio before it has been read: 5.3 ms.
func arrivalBound(clock.Rate) int64 {
	return int64(16*time.Millisecond/3) + 11_112
}

// A push that arrives late while the audio before it is still unread
// continues that audio: no read has passed its place, so the lateness is
// the producer's scheduling, not a gap. Placed at its arrival instead, it
// moved all the buffered audio later with it, and a few such pushes moved
// the audio of a source that hands its AAC over with its video a whole
// period off the read point, which splices it.
// Once everything before it has been read, a push later than arrivalBound
// is placed at its arrival as before, and one within it continues the
// audio. Each holds at every program rate, from a source at every sample
// rate.
func TestMixer_IngestFrame_LatePushContinuesUnreadAudio(t *testing.T) {
	for _, r := range programRates {
		for _, sr := range sourceRates {
			bound := arrivalBound(r)
			for _, tc := range []struct {
				name    string
				drained bool
				late    int64
			}{
				{"unread/10ms", false, 10_000_000},
				{"drained/10ms", true, 10_000_000},
				{"drained/past-bound", true, bound + 300_000},
				{"drained/within-bound", true, bound - 300_000},
			} {
				t.Run(fmt.Sprintf("%s/%dHz/%s", rateName(r), sr, tc.name), func(t *testing.T) {
					setProgramRate(t, r)
					base := clock.IndexToTimestamp(rate48k, clock.TimestampToIndex(rate48k, clock.TAINow()))
					var now int64
					clock.SetTAISource(func() int64 { return base + now })
					t.Cleanup(func() { clock.SetTAISource(nil) })
					m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
						DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
					defer func() { _ = m.Close() }()
					m.AddChannel("replay")
					m.SetActive("replay", true)
					frame := int64(1024) * 1_000_000_000 / int64(sr)

					now = frame
					pushAAC(m, "replay", sr) // stamped at its arrival
					end, n := ringEnd(m, "replay")
					if tc.drained {
						start := clock.TimestampToIndex(rate48k, end) - uint64(n)
						m.ProduceSamples(start, n, 0)
					}
					now = end - base + frame + tc.late
					pushAAC(m, "replay", sr)
					got, total := ringEnd(m, "replay")
					want := end + int64(total-n)*1_000_000_000/48000 // continued
					if tc.drained {
						want = base + now // at its arrival
						if tc.late < bound {
							want = end + int64(total)*1_000_000_000/48000
						}
					}
					if d := got - want; d < -20_834 || d > 20_834 {
						t.Fatalf("a push %.2f ms late ends %.1f samples off %d (bound %.2f ms)",
							float64(tc.late)/1e6, float64(d)/20_833.3, want, float64(bound)/1e6)
					}
				})
			}
		}
	}
}

// A push that arrives up to two AAC frames of its source off the audio
// before it, late while that audio is due and unread or early, continues
// it; one further off is placed at its arrival. An AAC frame is 1024
// samples at the source's rate, which the mixer resamples to its own, so
// the slack is the same time at the mixer rate: 46.4 ms from a 44.1 kHz
// source, 42.7 ms from 48 kHz and 21.3 ms from 96 kHz.
func TestMixer_IngestFrame_ArrivalSlackIsTwoAACFramesOfTheSource(t *testing.T) {
	for _, r := range programRates {
		for _, sr := range sourceRates {
			for _, tc := range []struct {
				name   string
				frames float64 // how far off, in AAC frames; negative: early
				placed bool    // placed at its arrival
			}{
				{"late1.9", 1.9, false}, {"late2.1", 2.1, true},
				{"early1.9", -1.9, false}, {"early2.1", -2.1, true},
			} {
				t.Run(fmt.Sprintf("%s/%dHz/%s", rateName(r), sr, tc.name), func(t *testing.T) {
					setProgramRate(t, r)
					base := clock.IndexToTimestamp(rate48k, clock.TimestampToIndex(rate48k, clock.TAINow()))
					var now int64
					clock.SetTAISource(func() int64 { return base + now })
					t.Cleanup(func() { clock.SetTAISource(nil) })
					m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
						DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
					defer func() { _ = m.Close() }()
					m.AddChannel("relay")
					m.SetActive("relay", true)
					frame := int64(1024) * 1_000_000_000 / int64(sr)

					now = frame
					pushAAC(m, "relay", sr)
					end, n := ringEnd(m, "relay")
					now = end - base + frame + int64(tc.frames*float64(frame))
					pushAAC(m, "relay", sr)
					got, total := ringEnd(m, "relay")
					want := end + int64(total-n)*1_000_000_000/48000
					if tc.placed {
						want = base + now
					}
					if d := got - want; d < -20_834 || d > 20_834 {
						t.Fatalf("a push %.1f AAC frames off ends %.1f samples off %d", tc.frames, float64(d)/20_833.3, want)
					}
				})
			}
		}
	}
}

// arrivalRun feeds an arrival-stamped source 1024-sample AAC frames at
// sampleRate in virtual time for secs, its clock ppm slow, reading it at
// program rate r clock.ReadDelay(r) periods behind the clock. Frames due
// in [lostFrom, lostTo) are lost. placed is called after each push with
// how far before its arrival the newest audio ends.
func arrivalRun(t *testing.T, r clock.Rate, sampleRate int, active bool, delayMs int, ppm, secs, lostFrom, lostTo int64, placed func(at, before int64)) {
	t.Helper()
	setProgramRate(t, r)
	first := clock.TimestampToIndex(r, clock.TAINow())
	base := clock.IndexToTimestamp(r, first)
	var now int64
	clock.SetTAISource(func() int64 { return base + now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
		DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
	defer func() { _ = m.Close() }()
	m.AddChannel("relay:1")
	m.SetActive("relay:1", active)
	if delayMs > 0 {
		if err := m.SetAudioDelay("relay:1", delayMs); err != nil {
			t.Fatal(err)
		}
	}
	frameAt := func(k int64) int64 {
		return (k + 1) * 1024 * 1_000_000_000 / int64(sampleRate) * 1_000_000 / (1_000_000 - ppm)
	}
	var k int64
	for n := first + 1; ; n++ {
		tick := clock.IndexToTimestamp(r, n) - base
		if tick >= secs*1_000_000_000 {
			break
		}
		for ; frameAt(k) <= tick; k++ {
			if now = frameAt(k); now >= lostFrom && now < lostTo {
				continue
			}
			pushAAC(m, "relay:1", sampleRate)
			end, _ := ringEnd(m, "relay:1")
			placed(now, base+now-end)
		}
		now = tick
		read, count := tickRead(r, n)
		m.ProduceSamples(read, count, 0)
	}
}

// A channel delay holds that much audio unread, so it does not show that
// a push continues audio no read has passed. After a batch of a delayed
// source is lost, the next push is placed as it is for the same source
// without a delay; continued, the delay would absorb the gap and the
// source's audio would play that much ahead of its video. At 50 fps and
// from 100 fps up, where the read delay is no longer than two AAC frames,
// the reads have passed the audio before the loss, and both are placed at
// their arrival.
func TestMixer_IngestFrame_DelayedSourceRealignsAfterLostAudio(t *testing.T) {
	for _, r := range programRates {
		for _, sr := range longSourceRates() {
			for _, lost := range []int64{20, 30} {
				t.Run(fmt.Sprintf("%s/%dHz/lost=%dms", rateName(r), sr, lost), func(t *testing.T) {
					const from = 600_000_000
					to := from + lost*1_000_000
					after := func(delayMs int) (before int64) {
						checked := false
						arrivalRun(t, r, sr, true, delayMs, 0, 1, from, to, func(at, b int64) {
							if at >= to && !checked {
								before, checked = b, true
							}
						})
						if !checked {
							t.Fatal("no push after the loss")
						}
						return before
					}
					delayed, undelayed := after(100), after(0)
					if d := delayed - undelayed; d < -20_834 || d > 20_834 {
						t.Fatalf("first push after the loss ends %.2f ms before its arrival, %.2f ms without a delay",
							float64(delayed)/1e6, float64(undelayed)/1e6)
					}
					readDelay := int64(clock.ReadDelay(r)) * clock.IndexToTimestamp(r, 1)
					if aac := int64(2*1024) * 1_000_000_000 / int64(sr); readDelay <= aac && delayed > arrivalBound(r) {
						t.Fatalf("first push after the loss ends %.2f ms before its arrival, want at most %.2f ms",
							float64(delayed)/1e6, float64(arrivalBound(r))/1e6)
					}
				})
			}
		}
	}
}

// An arrival-stamped source off program is never read, so its audio is
// always unread, and that does not make a late push jitter either. A
// source whose clock runs slow arrives later and later against the audio
// before it, and off program stays within arrivalBound of its arrival,
// and of the arrival-stamped video it plays with, as it does on program.
func TestMixer_IngestFrame_SlowSourceOffProgramStaysOnItsArrival(t *testing.T) {
	for _, r := range programRates {
		for _, sr := range longSourceRates() {
			t.Run(fmt.Sprintf("%s/%dHz", rateName(r), sr), func(t *testing.T) {
				worst := int64(0)
				arrivalRun(t, r, sr, false, 0, 2000, 4, 0, 0, func(_, before int64) { worst = max(worst, before) })
				if limit := arrivalBound(r); worst > limit {
					t.Fatalf("audio placed up to %.2f ms before its arrival, want at most %.2f ms",
						float64(worst)/1e6, float64(limit)/1e6)
				}
			})
		}
	}
}
