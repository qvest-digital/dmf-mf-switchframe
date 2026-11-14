package mxl

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

// stoppingDiscreteWriter writes like mockDiscreteWriter until fail is set,
// then refuses every grain, as a flow whose writer broke.
type stoppingDiscreteWriter struct {
	mockDiscreteWriter
	fail atomic.Bool
}

func (f *stoppingDiscreteWriter) WriteGrain(index uint64, data []byte) error {
	if f.fail.Load() {
		return errors.New("write refused")
	}
	return f.mockDiscreteWriter.WriteGrain(index, data)
}

// refusingContinuousWriter refuses every batch.
type refusingContinuousWriter struct{ mockContinuousWriter }

func (*refusingContinuousWriter) WriteSamples(uint64, [][]float32) error {
	return errors.New("write refused")
}

// A writer is at the clock while its video grains keep up with the clock,
// also through a stall of the frames it is fed, which it fills with
// repeats: what a merge of the program flow sees is a head that moves. It
// is not once its grains stop landing, which a reader of the flow sees as
// a head that stands still. At every frame rate the engine takes, the
// slowest's grains 42 ms apart.
func TestWriter_AtClockFollowsTheVideoWritten(t *testing.T) {
	const maxLag = 300 * time.Millisecond
	for _, rate := range []Rational{{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1}, {50, 1}, {60000, 1001}, {60, 1}, {120, 1}, {240, 1}} {
		t.Run(fmt.Sprintf("%d/%d", rate.Numerator, rate.Denominator), func(t *testing.T) {
			t.Parallel()
			mock := &stoppingDiscreteWriter{}
			w := NewWriter(WriterConfig{Width: 12, Height: 2})
			w.SetVideoWriter(mock, rate)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w.Start(ctx)

			if err := w.AtClock(maxLag); err == nil {
				t.Fatal("at the clock before a grain was written")
			}
			w.WriteVideo(frameYUV(16), 12, 2, framePTS(rate, CurrentIndex(rate)))
			deadline := time.Now().Add(2 * time.Second)
			for w.AtClock(maxLag) != nil {
				if time.Now().After(deadline) {
					t.Fatalf("not at the clock after a grain was written: %v", w.AtClock(maxLag))
				}
				time.Sleep(10 * time.Millisecond)
			}

			// No frame for three times maxLag: the writer repeats the last one.
			for end := time.Now().Add(3 * maxLag); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
				if err := w.AtClock(maxLag); err != nil {
					t.Fatalf("a stall of the frames fed counts as a stalled writer: %v", err)
				}
			}

			mock.fail.Store(true)
			time.Sleep(3 * maxLag)
			if err := w.AtClock(maxLag); err == nil {
				t.Fatal("at the clock with no grain written for three times maxLag")
			}
		})
	}
}

// Audio counts as written up to the end of the last batch written, and
// only once the flow took it, at every sample rate a flow carries.
func TestWriter_AtClockFollowsTheAudioWritten(t *testing.T) {
	const maxLag = 100 * time.Millisecond
	for _, sr := range []int64{32000, 44100, 48000, 96000} {
		t.Run(fmt.Sprint(sr), func(t *testing.T) {
			var now atomic.Uint64
			saved := CurrentIndex
			CurrentIndex = func(Rational) uint64 { return now.Load() }
			t.Cleanup(func() { CurrentIndex = saved })
			rate := clock.Rate{Num: sr, Den: 1}
			lag := uint64(sr / 10) // maxLag in samples
			const batch = 1024
			start := clock.TimestampToIndex(rate, clock.TAINow())
			now.Store(start + batch)
			pcm := make([]float32, batch*2)

			w := NewWriter(WriterConfig{SampleRate: int(sr), Channels: 2})
			w.SetAudioWriter(&mockContinuousWriter{}, Rational{sr, 1})
			if err := w.AtClock(maxLag); err == nil {
				t.Fatal("at the clock before audio was written")
			}
			w.WriteAudio(pcm, clock.IndexPTS(rate, start), int(sr), 2)
			if err := w.AtClock(maxLag); err != nil {
				t.Fatalf("not at the clock with the audio written up to it: %v", err)
			}
			now.Store(start + batch + lag)
			if err := w.AtClock(maxLag); err != nil {
				t.Fatalf("maxLag behind: %v", err)
			}
			now.Store(start + batch + lag + uint64(sr/1000))
			if err := w.AtClock(maxLag); err == nil {
				t.Fatal("at the clock 1 ms past maxLag behind")
			}

			failing := NewWriter(WriterConfig{SampleRate: int(sr), Channels: 2})
			failing.SetAudioWriter(&refusingContinuousWriter{}, Rational{sr, 1})
			now.Store(start + batch)
			failing.WriteAudio(pcm, clock.IndexPTS(rate, start), int(sr), 2)
			if err := failing.AtClock(maxLag); err == nil {
				t.Fatal("at the clock with audio the flow refused")
			}
		})
	}
}
