package mxl

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

var videoShape = FlowConfig{
	Format:     DataFormatVideo,
	GrainRate:  Rational{50, 1},
	SliceSizes: [4]uint32{5120},
	GrainCount: 100,
}

// invalidDiscreteReader is a handle on a flow whose writer recreated it:
// every read reports the flow invalid, as libmxl does from then on.
type invalidDiscreteReader struct {
	closed bool
}

func (r *invalidDiscreteReader) ReadGrain(_, _ uint64) ([]byte, GrainInfo, error) {
	return nil, GrainInfo{}, fmt.Errorf("%w: read grain", ErrFlowInvalid)
}
func (r *invalidDiscreteReader) ConfigInfo() FlowConfig     { return videoShape }
func (r *invalidDiscreteReader) HeadIndex() (uint64, error) { return 1, nil }
func (r *invalidDiscreteReader) Close() error               { r.closed = true; return nil }

// A source whose writer restarts keeps delivering grains from the new flow.
// Without the wrapper the reader loop counts the invalid reads as fatal and
// stops for good (TestReader_HandlesFlowError).
func TestReopeningReader_ReaderLoopSurvivesRecreatedFlow(t *testing.T) {
	stale := &invalidDiscreteReader{}
	fresh := newMockDiscreteReader([]mockGrain{
		{data: []byte{7}, info: GrainInfo{Index: 40, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{8}, info: GrainInfo{Index: 41, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
	}, videoShape)
	opens := 0
	flow := NewReopeningReader("f", stale, func(id string) (DiscreteReader, error) {
		opens++
		return fresh, nil
	}, nil)

	reader := NewVideoReader(ReaderConfig{BufSize: 4, TimeoutMs: 1, Width: 6, Height: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reader.StartVideo(ctx, flow)

	for _, want := range []byte{7, 8} {
		select {
		case g, ok := <-reader.Video():
			if !ok {
				t.Fatal("reader loop stopped instead of reading the recreated flow")
			}
			if g.V210[0] != want {
				t.Fatalf("grain = %d, want %d", g.V210[0], want)
			}
		case <-ctx.Done():
			t.Fatal("no grain from the recreated flow")
		}
	}
	cancel()
	reader.Wait()

	if !stale.closed {
		t.Error("handle on the replaced flow was not closed")
	}
	if opens != 1 {
		t.Errorf("opened %d times, want 1", opens)
	}
}

// A flow recreated with another geometry is not read: the reader loop was set
// up for the old line stride and would misread every grain.
func TestReopeningReader_RefusesChangedShape(t *testing.T) {
	other := videoShape
	other.SliceSizes = [4]uint32{2560}
	replacement := newMockDiscreteReader(nil, other)
	closed := false
	flow := NewReopeningReader("f", &invalidDiscreteReader{}, func(string) (DiscreteReader, error) {
		return &closeTracking{DiscreteReader: replacement, closed: &closed}, nil
	}, nil)

	for range 2 {
		if _, _, err := flow.ReadGrain(0, 0); !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout while no matching flow exists", err)
		}
	}
	if !closed {
		t.Error("mismatched flow handle was left open")
	}
	if got := flow.ConfigInfo(); got != videoShape {
		t.Errorf("ConfigInfo = %+v, want the original %+v", got, videoShape)
	}
}

// A ring of a different depth is the same flow to a reader.
func TestReopeningReader_AcceptsOtherRingDepth(t *testing.T) {
	deeper := videoShape
	deeper.GrainCount = 300
	flow := NewReopeningReader("f", &invalidDiscreteReader{}, func(string) (DiscreteReader, error) {
		return newMockDiscreteReader(nil, deeper), nil
	}, nil)

	readUntilReopened(t, flow)
}

// readUntilReopened reads past the invalid handle and expects the next read
// to report ErrTooLate, which is what moves a reader loop to the new head.
func readUntilReopened(t *testing.T, flow DiscreteReader) {
	t.Helper()
	if _, _, err := flow.ReadGrain(0, 0); !errors.Is(err, ErrTimeout) {
		t.Fatalf("read of the replaced flow: err = %v, want ErrTimeout", err)
	}
	if _, _, err := flow.ReadGrain(0, 0); !errors.Is(err, ErrTooLate) {
		t.Fatalf("first read of the reopened flow: err = %v, want ErrTooLate to resync", err)
	}
}

// A writer that is down for longer than one attempt: the flow reopens on a
// later read, and that read still resyncs instead of reading the old index
// out of the new ring.
func TestReopeningReader_DelayedReopenStillResyncs(t *testing.T) {
	fresh := newMockDiscreteReader([]mockGrain{
		{data: []byte{9}, info: GrainInfo{Index: 7, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
	}, videoShape)
	present := false
	flow := NewReopeningReader("f", &invalidDiscreteReader{}, func(string) (DiscreteReader, error) {
		if !present {
			return nil, errors.New("flow not found")
		}
		return fresh, nil
	}, nil).(*reopeningReader)

	if _, _, err := flow.ReadGrain(0, 0); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if _, _, err := flow.ReadGrain(0, 0); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout while the writer is down", err)
	}
	present = true
	flow.r.nextRetry = time.Time{}
	if _, _, err := flow.ReadGrain(0, 0); !errors.Is(err, ErrTooLate) {
		t.Fatalf("err = %v, want ErrTooLate on the first read of the reopened flow", err)
	}
	data, _, err := flow.ReadGrain(7, 0)
	if err != nil || data[0] != 9 {
		t.Fatalf("read after resync = %v, %v; want grain 9", data, err)
	}
}

// While the flow is gone, open is tried once per interval rather than on
// every read of a loop polling with a short timeout.
func TestReopeningReader_RetriesOncePerInterval(t *testing.T) {
	opens := 0
	flow := NewReopeningReader("f", &invalidDiscreteReader{}, func(string) (DiscreteReader, error) {
		opens++
		return nil, errors.New("flow not found")
	}, nil)

	for range 5 {
		if _, _, err := flow.ReadGrain(0, 0); !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
	}
	if opens != 1 {
		t.Errorf("opened %d times within one interval, want 1", opens)
	}
	if _, err := flow.HeadIndex(); err == nil {
		t.Error("HeadIndex succeeded with no flow open")
	}
}

type invalidContinuousReader struct{}

func (invalidContinuousReader) ReadSamples(uint64, int, uint64) ([][]float32, error) {
	return nil, fmt.Errorf("%w: read samples", ErrFlowInvalid)
}
func (invalidContinuousReader) ConfigInfo() FlowConfig {
	return FlowConfig{Format: DataFormatAudio, GrainRate: Rational{48000, 1}, ChannelCount: 2}
}
func (invalidContinuousReader) HeadIndex() (uint64, error) { return 0, nil }
func (invalidContinuousReader) Close() error               { return nil }

func TestReopeningAudioReader_ReadsRecreatedFlow(t *testing.T) {
	cfg := invalidContinuousReader{}.ConfigInfo()
	fresh := newMockContinuousReader([]mockSamples{{pcm: [][]float32{{0.5}, {0.25}}}}, cfg)
	flow := NewReopeningAudioReader("a", invalidContinuousReader{}, func(string) (ContinuousReader, error) {
		return fresh, nil
	}, nil)

	if _, err := flow.ReadSamples(0, 1, 0); !errors.Is(err, ErrTimeout) {
		t.Fatalf("read of the replaced flow: err = %v, want ErrTimeout", err)
	}
	if _, err := flow.ReadSamples(0, 1, 0); !errors.Is(err, ErrTooLate) {
		t.Fatalf("err = %v, want ErrTooLate to resync on the reopened flow", err)
	}
	pcm, err := flow.ReadSamples(0, 1, 0)
	if err != nil {
		t.Fatalf("read from reopened flow: %v", err)
	}
	if pcm[0][0] != 0.5 {
		t.Errorf("sample = %v, want 0.5", pcm[0][0])
	}
}

type closeTracking struct {
	DiscreteReader
	closed *bool
}

func (c *closeTracking) Close() error { *c.closed = true; return nil }
