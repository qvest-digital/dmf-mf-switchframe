package audio

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// The program audio of a tick is each source's audio at the tick's read
// point, the same media time the video of that tick shows, so the program
// carries no A/V offset whatever the mixer has buffered.

var rate48k = clock.Rate{Num: 48000, Den: 1}

func mediaMixer(t *testing.T) *Mixer {
	t.Helper()
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("mxl:a")
	m.SetActive("mxl:a", true)
	return m
}

// rampMod is the period of ramp's values. Every value is a multiple of
// 1/(2*rampMod) below 0.5, exact in float32 and under the master limiter.
const rampMod = 4096

// ramp returns n stereo samples whose value is the sample index modulo
// rampMod, scaled by 1/(2*rampMod).
func ramp(start uint64, n int) []float32 {
	pcm := make([]float32, 2*n)
	for i := 0; i < n; i++ {
		v := float32((start+uint64(i))%rampMod) / (2 * rampMod)
		pcm[2*i] = v
		pcm[2*i+1] = v
	}
	return pcm
}

// firstLeft returns the sample index modulo rampMod of ramp audio.
func firstLeft(pcm []float32) uint64 { return uint64(pcm[0]*2*rampMod + 0.5) }

// timeline is a stretch of the 48 kHz media timeline starting near now. A
// test feeds a mixer at its sample indexes and reads the mixer back at
// them, so every read finds the audio fed for it.
type timeline struct {
	base uint64 // sample index of offset 0
	read uint64 // next read point
}

func newTimeline() *timeline {
	now := clock.TimestampToIndex(rate48k, clock.TAINow())
	return &timeline{base: now, read: now}
}

// pts returns the PTS of the sample offset samples after the base.
func (tl *timeline) pts(offset int) int64 {
	return clock.IndexPTS(rate48k, tl.base+uint64(offset))
}

// push pushes interleaved pcm into key's ring as the samples from offset
// on, as an ingest would.
func (tl *timeline) push(m *Mixer, key string, pcm []float32, offset int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.channels[key]
	ch.ringBuf.Push(pcm)
	m.stampRing(ch, tl.pts(offset), len(pcm)/m.numChannels)
}

// produce returns m's next n samples from the read point on, stamped with
// the read point's PTS, and advances the read point past them.
func (tl *timeline) produce(m *Mixer, n int) []float32 {
	start := tl.read
	tl.read += uint64(n)
	return m.ProduceSamples(start, n, clock.IndexPTS(rate48k, start))
}

// Audio buffered ahead of the read point (the source delivers up to the
// clock, the program reads two frames behind) comes out at the read
// point's samples, not at whatever is oldest in the buffer.
func TestProduceSamplesAtReadPoint(t *testing.T) {
	now := clock.TimestampToIndex(rate48k, clock.TAINow())
	m := mediaMixer(t)
	const period = 1920 // 40 ms at 25 fps
	start := now - 10*period
	for i := uint64(0); i < 10; i++ {
		s := start + i*period
		m.IngestPCM("mxl:a", ramp(s, period), clock.IndexPTS(rate48k, s), 2)
	}
	read := start + 7*period // three periods buffered beyond the read point
	out := m.ProduceSamples(read, period, 0)
	if got := firstLeft(out); got != read%rampMod {
		t.Fatalf("first sample out is %d, want the read point's %d (offset %d samples)",
			got, read%rampMod, int64(got)-int64(read%rampMod))
	}
	out = m.ProduceSamples(read+period, period, 0)
	if got := firstLeft(out); got != (read+period)%rampMod {
		t.Fatalf("next period starts at %d, want %d", got, (read+period)%rampMod)
	}
}

// Audio that is not due yet is not played early: the tick gets silence
// and the buffered audio waits for its media time.
func TestProduceSamplesWaitsForFutureAudio(t *testing.T) {
	now := clock.TimestampToIndex(rate48k, clock.TAINow())
	m := mediaMixer(t)
	const period = 1920
	m.IngestPCM("mxl:a", ramp(now, period), clock.IndexPTS(rate48k, now), 2)
	out := m.ProduceSamples(now-2*period, period, 0)
	for i, v := range out {
		if v != 0 {
			t.Fatalf("sample %d is %v: audio of a later media time played early", i, v)
		}
	}
	out = m.ProduceSamples(now, period, 0)
	if got := firstLeft(out); got != now%rampMod {
		t.Fatalf("audio due at %d came out as %d", now%rampMod, got)
	}
}
