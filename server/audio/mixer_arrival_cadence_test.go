package audio

import (
	"fmt"
	"testing"

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

// A producer that hands its AAC frames over with its video frames, as the
// replay player does, at 50
// fps, 480 AAC frames over 512 pictures, one a frame period and none in
// every sixteenth; at 60 fps 400, none in about every fifth. Placed
// by its arrival, each push moves the source's audio by the difference
// between 1024 samples and a period, back by a whole AAC frame at each
// empty period: a sawtooth about an AAC frame high, more than the period
// the mixer lets a source stray before it splices, a click and a short
// gap each time. Placed as a continuation of the audio before it even when
// it arrives well after that ends, a push is stamped before its arrival,
// and the read, two frame periods behind the clock, drops it. Every start
// phase of the cadence, at both rates, on time and with one push in seven
// late, has to play through without a splice, a dry read or a silent
// sample.
func TestMixer_IngestFrame_ClipCadenceIsNeverSpliced(t *testing.T) {
	// The cadence repeats every 16 pictures at 50 fps and every 32 at
	// 60 fps: those are all the start phases there are. The test runs on
	// one goroutine, so the race detector finds nothing in it: under it,
	// where the audio package already takes most of CI's time limit, one
	// phase each; the build without it runs them all.
	step := 1
	if raceDetector {
		step = 32
	}
	for _, rate := range []struct {
		fps       int64
		aacFrames int // over 512 pictures, as long as they are
		cycle     int
	}{{50, 480, 16}, {60, 400, 32}} {
		for _, jitterNs := range []int64{0, 3_000_000, 10_000_000} {
			for phase := 0; phase < rate.cycle; phase += step {
				name := fmt.Sprintf("%dfps/jitter%dms/phase%d", rate.fps, jitterNs/1_000_000, phase)
				t.Run(name, func(t *testing.T) {
					silent, realigns, underruns := playClipCadence(t, rate.fps, rate.aacFrames, phase, jitterNs)
					if realigns != 0 || underruns != 0 || silent != 0 {
						t.Fatalf("%d splices, %d dry reads and %d silent samples over the clip",
							realigns, underruns, silent)
					}
				})
			}
		}
	}
}

// playClipCadence feeds 512 pictures' worth of the clip player's audio
// cadence, starting phase pictures into it, through IngestFrame on a
// simulated clock, reading each tick's period two frame periods behind
// the tick as the sync loop does. It returns the silent samples read after
// the first second, and the channel's splices and dry reads after it.
func playClipCadence(t *testing.T, fps int64, aacFrames, phase int, jitterNs int64) (silent int, realigns, underruns int64) {
	base := clock.TAINow()
	var now int64
	clock.SetTAISource(func() int64 { return base + now })
	t.Cleanup(func() { clock.SetTAISource(nil) })

	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
		DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
	defer func() { _ = m.Close() }()
	m.AddChannel("clip:1")
	m.SetActive("clip:1", true)

	const pictures = 512
	period := 1_000_000_000 / fps
	samples := int(48000 / fps)
	sent := (phase + 1) * aacFrames / pictures
	for f := 0; f < pictures; f++ {
		tick := int64(f) * period
		now = tick
		if f%7 == 3 {
			now += jitterNs
		}
		for ; sent < (f+phase+1)*aacFrames/pictures; sent++ {
			m.IngestFrame("clip:1", &media.AudioFrame{
				Data: []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, SampleRate: 48000, Channels: 2,
			})
		}
		now = tick
		read := clock.TimestampToIndex(rate48k, clock.TAINow()) - uint64(2*samples)
		out := m.ProduceSamples(read, samples, 0)
		if int64(f) < fps {
			if int64(f) == fps-1 {
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

// A push that arrives late while the audio before it is still unread
// continues that audio: no read has passed its place, so the lateness is
// the producer's scheduling, not a gap. Placed at its arrival instead, it
// moved all the buffered audio later with it, and a few such pushes moved
// the audio of a source that hands its AAC over with its video a whole
// period off the read point, which splices it.
// Once everything before it has been read, a push that late is placed at
// its arrival as before.
func TestMixer_IngestFrame_LatePushContinuesUnreadAudio(t *testing.T) {
	const spf = 1024
	const late = 480 // 10 ms
	for _, drained := range []bool{false, true} {
		t.Run(fmt.Sprintf("drained=%v", drained), func(t *testing.T) {
			base := clock.TimestampToIndex(rate48k, clock.TAINow())
			var at uint64 // samples since base
			clock.SetTAISource(func() int64 { return clock.IndexToTimestamp(rate48k, base+at) })
			t.Cleanup(func() { clock.SetTAISource(nil) })
			m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
				DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
			defer func() { _ = m.Close() }()
			m.AddChannel("replay")
			m.SetActive("replay", true)
			push := func() {
				m.IngestFrame("replay", &media.AudioFrame{
					Data: []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, SampleRate: 48000, Channels: 2,
				})
			}

			at = spf
			push() // stamped at its arrival: samples [0, spf)
			if drained {
				m.ProduceSamples(base, spf, 0)
			}
			at = 2*spf + late
			push()

			m.mu.RLock()
			end := m.channels["replay"].ringEnd
			m.mu.RUnlock()
			want := clock.IndexToTimestamp(rate48k, base+2*spf)
			if drained {
				want = clock.IndexToTimestamp(rate48k, base+at)
			}
			if d := end - want; d < -20_833 || d > 20_833 {
				t.Fatalf("second push ends %.1f samples off %d", float64(d)/20_833.3, want)
			}
		})
	}
}

// arrivalRun feeds an arrival-stamped source 1024-sample AAC frames in
// virtual time for secs, its clock ppm slow, reading it at 60 fps two
// periods behind the clock. Frames due in [lostFrom, lostTo) are lost.
// placed is called after each push with how far before its arrival the
// newest audio ends.
func arrivalRun(t *testing.T, active bool, delayMs int, ppm, secs, lostFrom, lostTo int64, placed func(at, before int64)) {
	t.Helper()
	base := clock.IndexToTimestamp(rate48k, clock.TimestampToIndex(rate48k, clock.TAINow()))
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
	const period = 1_000_000_000 / 60
	frameAt := func(k int64) int64 { return (k + 1) * 1024 * 1_000_000_000 / 48000 * 1_000_000 / (1_000_000 - ppm) }
	var k int64
	for tick := int64(period); tick < secs*1_000_000_000; tick += period {
		for ; frameAt(k) <= tick; k++ {
			if now = frameAt(k); now >= lostFrom && now < lostTo {
				continue
			}
			m.IngestFrame("relay:1", &media.AudioFrame{
				Data: []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, SampleRate: 48000, Channels: 2,
			})
			m.mu.RLock()
			end := m.channels["relay:1"].ringEnd
			m.mu.RUnlock()
			placed(now, base+now-end)
		}
		now = tick
		m.ProduceSamples(clock.TimestampToIndex(rate48k, clock.TAINow())-2*800, 800, 0)
	}
}

// A channel delay holds that much audio unread, so it does not show that
// a push continues audio no read has passed. After a batch of a delayed
// source is lost, the next push is placed at its arrival, as for a
// source without a delay; continued, the delay would absorb the gap and
// the source's audio would play that much ahead of its video.
func TestMixer_IngestFrame_DelayedSourceRealignsAfterLostAudio(t *testing.T) {
	for _, lost := range []int64{20, 30} {
		t.Run(fmt.Sprintf("lost=%dms", lost), func(t *testing.T) {
			const from = 2_500_000_000
			checked := false
			arrivalRun(t, true, 100, 0, 3, from, from+lost*1_000_000, func(at, before int64) {
				if at >= from+lost*1_000_000 && !checked {
					checked = true
					if before > 256*1_000_000_000/48000 {
						t.Fatalf("first push after the loss ends %.1f ms before its arrival", float64(before)/1e6)
					}
				}
			})
			if !checked {
				t.Fatal("no push after the loss")
			}
		})
	}
}

// An arrival-stamped source off program is never read, so its audio is
// always unread, and that does not make a late push jitter either. A
// source whose clock runs slow arrives later and later against the audio
// before it, and off program stays within arrivalLate of its arrival, and
// of the arrival-stamped video it plays with, as it does on program.
func TestMixer_IngestFrame_SlowSourceOffProgramStaysOnItsArrival(t *testing.T) {
	worst := int64(0)
	arrivalRun(t, false, 0, 2000, 30, 0, 0, func(_, before int64) { worst = max(worst, before) })
	if limit := int64(arrivalLate) * 1_000_000_000 / 48000; worst > limit {
		t.Fatalf("audio placed up to %.1f ms before its arrival, want at most %.1f ms",
			float64(worst)/1e6, float64(limit)/1e6)
	}
}
