package audio

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// A source's audio delay moves the point its ring is read at, the way a
// video delay moves the frame-sync read point: a tick reading media time
// T plays the source's audio of T minus the delay. The audio keeps
// coming, delayed, rather than arriving late for its own media time.
func TestAudioDelayReadsTheRingThatMuchEarlier(t *testing.T) {
	m := mediaMixer(t)
	const delayMs = 100
	const delay = delayMs * 48 // samples
	if err := m.SetAudioDelay("mxl:a", delayMs); err != nil {
		t.Fatal(err)
	}
	if got := m.AudioDelayMs("mxl:a"); got != delayMs {
		t.Fatalf("AudioDelayMs = %d, want %d", got, delayMs)
	}

	// The source delivers up to the clock; the program reads two periods
	// behind it, as the sync loop does.
	const period = 1600
	now := clock.TimestampToIndex(rate48k, clock.TAINow())
	first := now - delay - 4*period
	next := first
	for read := now - 2*period; read < now+20*period; read += period {
		for next < read+2*period {
			m.IngestPCM("mxl:a", ramp(next, period), clock.IndexPTS(rate48k, next), 2)
			next += period
		}
		out := m.ProduceSamples(read, period, 0)
		want := read - delay
		for i := 0; i < period; i++ {
			if got := uint64(out[2*i]*2*rampMod + 0.5); got != (want+uint64(i))%rampMod {
				t.Fatalf("read %d sample %d is media sample %d (mod %d), want %d: the audio is not delayed by %d ms",
					read-first, i, got, rampMod, (want+uint64(i))%rampMod, delayMs)
			}
		}
	}
}

// A source delay is kept for the source's key, not for the channel: a
// source whose channel is removed and added again, as when it is removed
// and registered again, is still delayed as its video is.
func TestSourceDelaySurvivesChannelReAdd(t *testing.T) {
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	m.AddChannel("srt:cam")
	m.SetSourceDelay("srt:cam", 200)
	m.RemoveChannel("srt:cam")
	m.AddChannel("srt:cam")
	m.mu.Lock()
	got := m.channels["srt:cam"].sourceDelayMs
	m.mu.Unlock()
	if got != 200 {
		t.Fatalf("channel source delay after re-adding %d ms, want the source's 200", got)
	}
}
