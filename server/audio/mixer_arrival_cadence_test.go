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

// The clip player hands its AAC frames over with its video frames: at 50
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
