package mxl

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// Program audio is written at the sample indices of its own PTS, the tick's
// output time, exactly as program video is written at the tick's grain:
// the two program flows carry no offset between them, whatever the clock
// reads when a batch arrives.

var audioRate = clock.Rate{Num: 48000, Den: 1}

func audioWriter(t *testing.T) (*Writer, *mockContinuousWriter) {
	t.Helper()
	mock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{SampleRate: 48000, Channels: 2})
	w.SetAudioWriter(mock, Rational{48000, 1})
	return w, mock
}

// libmxl's OpenSamples(index, count) writes [index-count, index): a batch
// whose first sample is s goes to index s+count.
func TestWriter_AudioIndexIsTheBatchPTS(t *testing.T) {
	const batch = 1024
	saved := CurrentIndex
	CurrentIndex = func(Rational) uint64 { return 1 } // the clock is not consulted
	t.Cleanup(func() { CurrentIndex = saved })

	w, mock := audioWriter(t)
	start := clock.TimestampToIndex(audioRate, clock.TAINow())
	pcm := make([]float32, batch*2)
	for i := uint64(0); i < 50; i++ { // a backlog delivered at once
		w.WriteAudio(pcm, clock.IndexPTS(audioRate, start+i*batch), 48000, 2)
	}
	got := mock.getSamples()
	if len(got) != 50 {
		t.Fatalf("%d batches written, want 50", len(got))
	}
	for i, s := range got {
		if want := start + uint64(i+1)*batch; s.index != want {
			t.Fatalf("batch %d written at %d, want %d (%d samples off)",
				i, s.index, want, int64(s.index)-int64(want))
		}
	}
}

// A batch overlapping what was written is dropped rather than shifted
// later, which would move all following audio off its time.
func TestWriter_AudioOverlapIsDroppedNotShifted(t *testing.T) {
	const batch = 1024
	w, mock := audioWriter(t)
	start := clock.TimestampToIndex(audioRate, clock.TAINow())
	pcm := make([]float32, batch*2)
	w.WriteAudio(pcm, clock.IndexPTS(audioRate, start), 48000, 2)
	w.WriteAudio(pcm, clock.IndexPTS(audioRate, start+batch/2), 48000, 2) // overlaps
	w.WriteAudio(pcm, clock.IndexPTS(audioRate, start+batch), 48000, 2)
	got := mock.getSamples()
	if len(got) != 2 {
		t.Fatalf("%d batches written, want 2 (the overlapping one dropped)", len(got))
	}
	if got[1].index != start+2*batch {
		t.Fatalf("third batch at %d, want %d", got[1].index, start+2*batch)
	}
	if w.AudioOverlaps() != 1 {
		t.Fatalf("overlaps %d, want 1", w.AudioOverlaps())
	}
}
