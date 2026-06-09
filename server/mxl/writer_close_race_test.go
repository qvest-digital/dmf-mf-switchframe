package mxl

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingContinuousWriter blocks inside WriteSamples until released, and
// records whether the writer was closed (released) while a WriteSamples call
// was still in flight — the cgo equivalent would be a use-after-free on the
// released C flow-writer handle.
type blockingContinuousWriter struct {
	entered        chan struct{}
	enteredOnce    sync.Once
	release        chan struct{}
	closed         atomic.Bool
	usedAfterClose atomic.Bool
}

func (m *blockingContinuousWriter) WriteSamples(index uint64, channels [][]float32) error {
	m.enteredOnce.Do(func() { close(m.entered) })
	<-m.release
	if m.closed.Load() {
		m.usedAfterClose.Store(true)
	}
	return nil
}

func (m *blockingContinuousWriter) Close() error {
	m.closed.Store(true)
	return nil
}

// blockingDiscreteWriter is the discrete (data grain) analogue.
type blockingDiscreteWriter struct {
	entered        chan struct{}
	enteredOnce    sync.Once
	release        chan struct{}
	closed         atomic.Bool
	usedAfterClose atomic.Bool
}

func (m *blockingDiscreteWriter) WriteGrain(index uint64, data []byte) error {
	m.enteredOnce.Do(func() { close(m.entered) })
	<-m.release
	if m.closed.Load() {
		m.usedAfterClose.Store(true)
	}
	return nil
}

func (m *blockingDiscreteWriter) Close() error {
	m.closed.Store(true)
	return nil
}

// TestWriter_CloseWaitsForInFlightWriteAudio reproduces finding #73: WriteAudio
// snapshots the flow writer under w.mu but calls WriteSamples after unlocking,
// so Close() can release the flow writer (use-after-free in the cgo build)
// while WriteSamples is still executing. Close must not complete — and must
// not release the writer — until the in-flight call drains.
func TestWriter_CloseWaitsForInFlightWriteAudio(t *testing.T) {
	mock := &blockingContinuousWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	w := NewWriter(WriterConfig{SampleRate: 48000, Channels: 2})
	w.SetAudioWriter(mock, Rational{48000, 1})

	writeDone := make(chan struct{})
	go func() {
		w.WriteAudio([]float32{0.1, 0.2, 0.3, 0.4}, 0, 48000, 2)
		close(writeDone)
	}()
	<-mock.entered // WriteSamples is now in flight on the flow writer.

	closeDone := make(chan struct{})
	go func() {
		_ = w.Close()
		close(closeDone)
	}()

	// Close must block while WriteSamples is still in flight.
	select {
	case <-closeDone:
		t.Fatal("Close returned while WriteSamples was still in flight on the flow writer")
	case <-time.After(100 * time.Millisecond):
	}

	close(mock.release)
	<-writeDone

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the in-flight WriteSamples drained")
	}

	if mock.usedAfterClose.Load() {
		t.Fatal("flow writer was released (Close) while WriteSamples was still executing — use-after-free in cgo builds")
	}
	if !mock.closed.Load() {
		t.Fatal("Close should have released the audio flow writer")
	}

	// Post-close writes must be rejected without touching the released writer.
	w.WriteAudio([]float32{0.5, 0.6}, 1, 48000, 2)
	if mock.usedAfterClose.Load() {
		t.Fatal("WriteAudio after Close reached the released flow writer")
	}
}

// TestWriter_CloseWaitsForInFlightWriteDataGrain is the WriteDataGrain
// variant of finding #73: the atomic dataRef load is a TOCTOU — Close can
// release ref.writer while WriteGrain is executing on it.
func TestWriter_CloseWaitsForInFlightWriteDataGrain(t *testing.T) {
	mock := &blockingDiscreteWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	w := NewWriter(WriterConfig{})
	w.SetDataWriter(mock, Rational{30, 1})

	writeDone := make(chan struct{})
	go func() {
		_ = w.WriteDataGrain([]byte{0x01, 0x02, 0x03})
		close(writeDone)
	}()
	<-mock.entered // WriteGrain is now in flight on the flow writer.

	closeDone := make(chan struct{})
	go func() {
		_ = w.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close returned while WriteGrain was still in flight on the data flow writer")
	case <-time.After(100 * time.Millisecond):
	}

	close(mock.release)
	<-writeDone

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the in-flight WriteGrain drained")
	}

	if mock.usedAfterClose.Load() {
		t.Fatal("data flow writer was released (Close) while WriteGrain was still executing — use-after-free in cgo builds")
	}

	// Post-close writes must be rejected without touching the released writer.
	if err := w.WriteDataGrain([]byte{0x04}); err == nil {
		t.Fatal("WriteDataGrain after Close should return an error")
	}
	if mock.usedAfterClose.Load() {
		t.Fatal("WriteDataGrain after Close reached the released flow writer")
	}
}
