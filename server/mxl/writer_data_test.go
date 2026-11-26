package mxl

import (
	"bytes"
	"context"
	"testing"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/scte104"
)

// A reader of the data flow, such as a playout engine waiting for
// SCTE-104, takes one data grain per picture at the picture's index and
// counts a missing one as lost. The data grain is therefore written with
// each video grain, at its index, holding what was queued for it or a
// frame of no packets.

// emptyANCFrame is an RFC 8331 frame of no packets.
var emptyANCFrame = []byte{0, 0, 0, 0, 0, 0}

func dataWriter(t *testing.T) (w *Writer, video, data *mockDiscreteWriter) {
	t.Helper()
	video, data = &mockDiscreteWriter{}, &mockDiscreteWriter{}
	w = NewWriter(WriterConfig{Width: 12, Height: 2})
	rate := Rational{videoRate.Num, videoRate.Den}
	w.SetVideoWriter(video, rate)
	w.SetDataWriter(data, rate)
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	t.Cleanup(func() { cancel(); _ = w.Close() })
	return w, video, data
}

func scte104Packet(t *testing.T, msg []byte) scte104.ANCPacket {
	t.Helper()
	p, err := scte104.MessagePacket(msg, 9)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A cue queued before a picture is in that picture's data grain, and the
// pictures before and after it carry empty frames at their own indices.
func TestWriter_DataGrainPerVideoGrain(t *testing.T) {
	fixClock(t, 7)
	w, video, data := dataWriter(t)
	base := clock.TimestampToIndex(videoRate, clock.TAINow())
	msg := []byte{0xFF, 0xFF, 0x00, 0x0D}

	for k := uint64(0); k < 3; k++ {
		if k == 1 {
			w.QueueANC(scte104Packet(t, msg))
		}
		w.WriteVideo(frameYUV(byte(16+k)), 12, 2, clock.IndexPTS(videoRate, base+k))
		waitGrains(t, data, int(k)+1)
	}

	vg, dg := video.getGrains(), data.getGrains()
	if len(dg) != 3 || len(vg) != 3 {
		t.Fatalf("%d video and %d data grains, want 3 each", len(vg), len(dg))
	}
	for k, g := range dg {
		if g.index != vg[k].index {
			t.Fatalf("data grain %d at index %d, video grain at %d", k, g.index, vg[k].index)
		}
		msgs, err := scte104.ParseDataGrain(g.data)
		if err != nil {
			t.Fatalf("data grain %d: %v", k, err)
		}
		if k == 1 {
			if len(msgs) != 1 || !bytes.Equal(msgs[0], msg) {
				t.Fatalf("data grain 1 carries %x, want the cued message %x", msgs, msg)
			}
			continue
		}
		if !bytes.Equal(g.data, emptyANCFrame) {
			t.Fatalf("data grain %d = %x, want an empty frame", k, g.data)
		}
	}
}

// A picture repeated while the producer stalls still gets its data grain:
// the reader would count the grain as missing otherwise.
func TestWriter_DataGrainForRepeatedPicture(t *testing.T) {
	w, video, data := dataWriter(t)
	w.WriteVideo(frameYUV(16), 12, 2, clock.TAINow())
	vg := waitGrains(t, video, 4)
	if len(vg) < 4 {
		t.Fatalf("%d video grains, want repeats", len(vg))
	}
	_ = w.Close()

	vg, dg := video.getGrains(), data.getGrains()
	if len(dg) != len(vg) {
		t.Fatalf("%d data grains for %d video grains", len(dg), len(vg))
	}
	for k := range vg {
		if dg[k].index != vg[k].index {
			t.Fatalf("data grain %d at index %d, video grain at %d", k, dg[k].index, vg[k].index)
		}
	}
}

// Packets that cannot be framed are dropped with their grain still
// written, so they do not hold up the cues after them.
func TestWriter_DataGrainDropsUnframeablePackets(t *testing.T) {
	fixClock(t, 7)
	w, _, data := dataWriter(t)
	base := clock.TimestampToIndex(videoRate, clock.TAINow())

	for range 256 { // ANC_Count holds 255
		w.QueueANC(scte104Packet(t, []byte{0x01}))
	}
	w.WriteVideo(frameYUV(16), 12, 2, clock.IndexPTS(videoRate, base))
	waitGrains(t, data, 1)
	w.QueueANC(scte104Packet(t, []byte{0x02}))
	w.WriteVideo(frameYUV(17), 12, 2, clock.IndexPTS(videoRate, base+1))

	dg := waitGrains(t, data, 2)
	if len(dg) != 2 {
		t.Fatalf("%d data grains, want 2", len(dg))
	}
	if !bytes.Equal(dg[0].data, emptyANCFrame) {
		t.Fatalf("data grain 0 = %x, want an empty frame", dg[0].data)
	}
	msgs, err := scte104.ParseDataGrain(dg[1].data)
	if err != nil || len(msgs) != 1 || !bytes.Equal(msgs[0], []byte{0x02}) {
		t.Fatalf("data grain 1 carries %x (%v), want the next cue", msgs, err)
	}
}

func TestSetDataWriter_AtomicStoreLoad(t *testing.T) {
	w := NewWriter(WriterConfig{})

	// Initially nil.
	ref := w.dataRef.Load()
	if ref != nil {
		t.Fatal("expected nil dataRef initially")
	}

	// Set a writer.
	mock1 := &mockDiscreteWriter{}
	w.SetDataWriter(mock1, Rational{25, 1})

	ref = w.dataRef.Load()
	if ref == nil {
		t.Fatal("expected dataRef to be set")
	}
	if ref.rate.Numerator != 25 || ref.rate.Denominator != 1 {
		t.Fatalf("expected rate 25/1, got %d/%d", ref.rate.Numerator, ref.rate.Denominator)
	}

	// Replace with a different writer.
	mock2 := &mockDiscreteWriter{}
	w.SetDataWriter(mock2, Rational{30000, 1001})

	ref = w.dataRef.Load()
	if ref == nil {
		t.Fatal("expected dataRef to be set after replacement")
	}
	if ref.rate.Numerator != 30000 || ref.rate.Denominator != 1001 {
		t.Fatalf("expected rate 30000/1001, got %d/%d", ref.rate.Numerator, ref.rate.Denominator)
	}
}

func TestClose_CleansUpDataWriter(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{})
	w.SetDataWriter(mock, Rational{30, 1})

	if err := w.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	mock.mu.Lock()
	closed := mock.closed
	mock.mu.Unlock()
	if !closed {
		t.Fatal("expected data writer to be closed")
	}

	// dataRef should be cleared.
	ref := w.dataRef.Load()
	if ref != nil {
		t.Fatal("expected dataRef to be nil after close")
	}
}

func TestClose_CleansUpAllWriters(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	aMock := &mockContinuousWriter{}
	dMock := &mockDiscreteWriter{}

	w := NewWriter(WriterConfig{Width: 12, Height: 2, SampleRate: 48000, Channels: 2})
	w.SetVideoWriter(vMock, Rational{30, 1})
	w.SetAudioWriter(aMock, Rational{48000, 1})
	w.SetDataWriter(dMock, Rational{30, 1})

	if err := w.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	if !vMock.closed {
		t.Fatal("expected video writer to be closed")
	}
	aMock.mu.Lock()
	aClosed := aMock.closed
	aMock.mu.Unlock()
	if !aClosed {
		t.Fatal("expected audio writer to be closed")
	}
	dMock.mu.Lock()
	dClosed := dMock.closed
	dMock.mu.Unlock()
	if !dClosed {
		t.Fatal("expected data writer to be closed")
	}
}

func TestOutput_StartLifecycleWithDataWriter(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	aMock := &mockContinuousWriter{}
	dMock := &mockDiscreteWriter{}

	out := NewOutput(OutputConfig{
		FlowName:  "program",
		Width:     12,
		Height:    2,
		VideoRate: Rational{30, 1},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out.StartLifecycle(ctx, vMock, aMock, dMock)

	// Verify the data writer was set on the underlying Writer.
	ref := out.Writer().dataRef.Load()
	if ref == nil {
		t.Fatal("expected dataRef to be set")
	}
	if ref.rate.Numerator != 30 || ref.rate.Denominator != 1 {
		t.Fatalf("expected data rate 30/1, got %d/%d", ref.rate.Numerator, ref.rate.Denominator)
	}

	// A program picture brings its data grain along.
	out.Writer().WriteVideo(frameYUV(16), 12, 2, 0)
	if grains := waitGrains(t, dMock, 1); len(grains) == 0 {
		t.Fatal("no data grain written with the program video")
	}
}

func TestOutput_StartLifecycleWithoutDataWriter(t *testing.T) {
	vMock := &mockDiscreteWriter{}

	out := NewOutput(OutputConfig{
		FlowName: "program",
		Width:    12,
		Height:   2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No data writer — backward compatible call.
	out.StartLifecycle(ctx, vMock, nil)

	// dataRef should be nil.
	ref := out.Writer().dataRef.Load()
	if ref != nil {
		t.Fatal("expected nil dataRef when no data writer provided")
	}
}

func TestOutput_StartLifecycleNilDataWriter(t *testing.T) {
	vMock := &mockDiscreteWriter{}

	out := NewOutput(OutputConfig{
		FlowName: "program",
		Width:    12,
		Height:   2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Explicit nil data writer — should be treated as absent.
	out.StartLifecycle(ctx, vMock, nil, nil)

	ref := out.Writer().dataRef.Load()
	if ref != nil {
		t.Fatal("expected nil dataRef when nil data writer provided")
	}
}
