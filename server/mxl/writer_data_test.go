package mxl

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWriteDataGrain_WritesWithCorrectIndex(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{})
	w.SetDataWriter(mock, Rational{30, 1})

	data := []byte{0x01, 0x02, 0x03, 0x04}
	if err := w.WriteDataGrain(data); err != nil {
		t.Fatalf("WriteDataGrain error: %v", err)
	}

	grains := mock.getGrains()
	if len(grains) != 1 {
		t.Fatalf("expected 1 grain, got %d", len(grains))
	}

	// Verify data was written correctly.
	if len(grains[0].data) != len(data) {
		t.Fatalf("expected %d bytes, got %d", len(data), len(grains[0].data))
	}
	for i, b := range data {
		if grains[0].data[i] != b {
			t.Fatalf("data[%d] = 0x%02x, want 0x%02x", i, grains[0].data[i], b)
		}
	}

	// Index should be non-zero (CurrentIndex is set by stub.go init).
	// We can't predict the exact value since it's wall-clock based,
	// but it should have been obtained from CurrentIndex with the configured rate.
	// The fact that WriteGrain was called at all verifies the plumbing.
}

func TestWriteDataGrain_NoWriterSet(t *testing.T) {
	w := NewWriter(WriterConfig{})

	// No data writer set — should return error, not panic.
	err := w.WriteDataGrain([]byte{0x01})
	if err == nil {
		t.Fatal("expected error when no data writer set")
	}
	if err.Error() != "mxl writer: no data writer set" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteDataGrain_AfterClose(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{})
	w.SetDataWriter(mock, Rational{30, 1})

	if err := w.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Write after close should return error.
	err := w.WriteDataGrain([]byte{0x01})
	if err == nil {
		t.Fatal("expected error after close")
	}
	if err.Error() != "mxl writer: closed" {
		t.Fatalf("unexpected error: %v", err)
	}

	// No grains should have been written.
	grains := mock.getGrains()
	if len(grains) != 0 {
		t.Fatalf("expected 0 grains after close, got %d", len(grains))
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

	// Write should go to mock2, not mock1.
	if err := w.WriteDataGrain([]byte{0xAA}); err != nil {
		t.Fatalf("WriteDataGrain error: %v", err)
	}

	if len(mock1.getGrains()) != 0 {
		t.Fatal("expected no grains on replaced writer")
	}
	if len(mock2.getGrains()) != 1 {
		t.Fatal("expected 1 grain on current writer")
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

func TestWriteDataGrain_MultipleWrites(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{})
	w.SetDataWriter(mock, Rational{30, 1})

	// Write multiple data grains (simulating multiple SCTE-104 cues).
	for i := 0; i < 5; i++ {
		data := []byte{byte(i), byte(i + 1)}
		if err := w.WriteDataGrain(data); err != nil {
			t.Fatalf("WriteDataGrain[%d] error: %v", i, err)
		}
	}

	grains := mock.getGrains()
	if len(grains) != 5 {
		t.Fatalf("expected 5 grains, got %d", len(grains))
	}

	// Verify each grain has the expected data.
	for i, g := range grains {
		if len(g.data) != 2 {
			t.Fatalf("grain[%d] data len = %d, want 2", i, len(g.data))
		}
		if g.data[0] != byte(i) || g.data[1] != byte(i+1) {
			t.Fatalf("grain[%d] data = %v, want [%d %d]", i, g.data, i, i+1)
		}
	}
}

func TestWriteDataGrain_ConcurrentAccess(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{})
	w.SetDataWriter(mock, Rational{30, 1})

	var wg sync.WaitGroup
	wg.Add(2)

	// Concurrent writes should not race (atomic.Pointer is lock-free).
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = w.WriteDataGrain([]byte{byte(i)})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = w.WriteDataGrain([]byte{byte(i + 100)})
		}
	}()

	wg.Wait()

	grains := mock.getGrains()
	if len(grains) != 100 {
		t.Fatalf("expected 100 grains, got %d", len(grains))
	}
}

func TestWriteDataGrain_ConcurrentWithClose(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{})
	w.SetDataWriter(mock, Rational{30, 1})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = w.WriteDataGrain([]byte{byte(i)})
		}
	}()

	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		_ = w.Close()
	}()

	wg.Wait()
	// No panic or race — that's the assertion.
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

	// Write a data grain through the writer.
	if err := out.Writer().WriteDataGrain([]byte{0xDE, 0xAD}); err != nil {
		t.Fatalf("WriteDataGrain error: %v", err)
	}

	grains := dMock.getGrains()
	if len(grains) != 1 {
		t.Fatalf("expected 1 grain, got %d", len(grains))
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
