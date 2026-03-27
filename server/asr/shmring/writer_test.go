//go:build !windows

package shmring

import (
	"fmt"
	"math"
	"os"
	"sync/atomic"
	"testing"
	"unsafe"
)

func testShmName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("/shmring_test_%d", os.Getpid())
}

func TestWriterCreateAndClose(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	if w.Capacity() != 1024 {
		t.Errorf("Capacity = %d, want 1024", w.Capacity())
	}

	// Header should have magic set.
	snap := w.Header()
	if snap.Magic != Magic {
		t.Errorf("Magic = 0x%08X, want 0x%08X", snap.Magic, Magic)
	}

	// WritePos and ReadPos should be zero initially.
	if snap.WritePos != 0 {
		t.Errorf("WritePos = %d, want 0", snap.WritePos)
	}
	if snap.ReadPos != 0 {
		t.Errorf("ReadPos = %d, want 0", snap.ReadPos)
	}
	if snap.WriteSeq != 0 {
		t.Errorf("WriteSeq = %d, want 0", snap.WriteSeq)
	}

	// Close should succeed and remove the shm segment.
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWriterCapacityMustBePowerOfTwo(t *testing.T) {
	name := testShmName(t)
	// 1000 is not a power of 2, should be rounded up to 1024.
	w, err := NewWriter(name, 1000)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	if w.Capacity() != 1024 {
		t.Errorf("Capacity = %d, want 1024 (rounded up from 1000)", w.Capacity())
	}
}

func TestWriterWriteSamples(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 4096)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// Write 100 samples.
	pcm := make([]float32, 100)
	for i := range pcm {
		pcm[i] = float32(i) * 0.01
	}

	w.Write(pcm, 12345, 48000, 2)

	snap := w.Header()

	// WritePos should be 100 * 4 = 400 bytes.
	expectedWritePos := uint64(100 * 4)
	if snap.WritePos != expectedWritePos {
		t.Errorf("WritePos = %d, want %d", snap.WritePos, expectedWritePos)
	}

	// SampleRate, Channels, BasePTS should be set.
	if snap.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", snap.SampleRate)
	}
	if snap.Channels != 2 {
		t.Errorf("Channels = %d, want 2", snap.Channels)
	}
	if snap.BasePTS != 12345 {
		t.Errorf("BasePTS = %d, want 12345", snap.BasePTS)
	}

	// WriteSeq should be 1 after one write.
	if snap.WriteSeq != 1 {
		t.Errorf("WriteSeq = %d, want 1", snap.WriteSeq)
	}

	// Verify data was actually written by reading from the mmap'd region.
	dataBase := unsafe.Add(w.base, HeaderSize)
	for i := 0; i < 100; i++ {
		ptr := (*float32)(unsafe.Add(dataBase, i*4))
		got := *ptr
		want := float32(i) * 0.01
		if math.Abs(float64(got-want)) > 1e-7 {
			t.Errorf("sample[%d] = %f, want %f", i, got, want)
		}
	}

	// Second write should advance WritePos further and bump WriteSeq.
	pcm2 := make([]float32, 50)
	for i := range pcm2 {
		pcm2[i] = float32(i) * 0.02
	}
	w.Write(pcm2, 99999, 48000, 2)

	snap2 := w.Header()
	expectedWritePos2 := uint64(150 * 4)
	if snap2.WritePos != expectedWritePos2 {
		t.Errorf("WritePos after second write = %d, want %d", snap2.WritePos, expectedWritePos2)
	}
	if snap2.WriteSeq != 2 {
		t.Errorf("WriteSeq after second write = %d, want 2", snap2.WriteSeq)
	}

	// BasePTS should remain from the first write (not overwritten).
	if snap2.BasePTS != 12345 {
		t.Errorf("BasePTS = %d, want 12345 (should not change after first write)", snap2.BasePTS)
	}
}

func TestWriterWrapAround(t *testing.T) {
	name := testShmName(t)
	// Small capacity: 256 float32s = 1024 bytes.
	capacity := 256
	w, err := NewWriter(name, capacity)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	capacityBytes := uint64(capacity * 4)

	// Write exactly capacity samples to fill the buffer.
	pcm := make([]float32, capacity)
	for i := range pcm {
		pcm[i] = 1.0
	}
	w.Write(pcm, 0, 48000, 2)

	snap := w.Header()
	// WritePos should be monotonic (unwrapped): 256 * 4 = 1024.
	if snap.WritePos != capacityBytes {
		t.Errorf("WritePos after full write = %d, want %d (monotonic)", snap.WritePos, capacityBytes)
	}

	// Write 100 more samples to go past the wrap point.
	pcm2 := make([]float32, 100)
	for i := range pcm2 {
		pcm2[i] = float32(i) + 100.0
	}
	w.Write(pcm2, 1000, 48000, 2)

	snap2 := w.Header()
	// Monotonic: 1024 + 400 = 1424
	expectedPos := capacityBytes + uint64(100*4)
	if snap2.WritePos != expectedPos {
		t.Errorf("WritePos after wrap = %d, want %d (monotonic)", snap2.WritePos, expectedPos)
	}

	// Verify the wrapped data is correct by reading from the data region.
	dataBase := unsafe.Add(w.base, HeaderSize)
	for i := 0; i < 100; i++ {
		ptr := (*float32)(unsafe.Add(dataBase, i*4))
		got := *ptr
		want := float32(i) + 100.0
		if math.Abs(float64(got-want)) > 1e-7 {
			t.Errorf("wrapped sample[%d] = %f, want %f", i, got, want)
		}
	}
}

func TestWriterWrapAroundSplit(t *testing.T) {
	name := testShmName(t)
	// 256 float32s = 1024 bytes capacity.
	capacity := 256
	w, err := NewWriter(name, capacity)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// Write 200 samples (800 bytes), leaving 56 sample slots (224 bytes).
	pcm1 := make([]float32, 200)
	for i := range pcm1 {
		pcm1[i] = -1.0
	}
	w.Write(pcm1, 0, 48000, 2)

	// Now write 100 samples. 56 fit at the end, 44 wrap to the beginning.
	pcm2 := make([]float32, 100)
	for i := range pcm2 {
		pcm2[i] = float32(i) + 200.0
	}
	w.Write(pcm2, 1000, 48000, 2)

	snap := w.Header()
	// Total bytes written: (200 + 100) * 4 = 1200 (monotonic, unwrapped).
	expectedPos := uint64(1200)
	if snap.WritePos != expectedPos {
		t.Errorf("WritePos = %d, want %d (monotonic)", snap.WritePos, expectedPos)
	}

	// Verify the split write: first 56 samples at offset 800, then 44 at offset 0.
	dataBase := unsafe.Add(w.base, HeaderSize)

	// Check tail portion (offset 800..1023, 56 samples).
	for i := 0; i < 56; i++ {
		ptr := (*float32)(unsafe.Add(dataBase, 800+i*4))
		got := *ptr
		want := float32(i) + 200.0
		if math.Abs(float64(got-want)) > 1e-7 {
			t.Errorf("tail sample[%d] = %f, want %f", i, got, want)
		}
	}

	// Check head portion (offset 0..175, 44 samples).
	for i := 0; i < 44; i++ {
		ptr := (*float32)(unsafe.Add(dataBase, i*4))
		got := *ptr
		want := float32(i+56) + 200.0
		if math.Abs(float64(got-want)) > 1e-7 {
			t.Errorf("head sample[%d] = %f, want %f", i, got, want)
		}
	}
}

func TestWriterWriteSeqMonotonic(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	pcm := make([]float32, 10)
	for i := 0; i < 50; i++ {
		w.Write(pcm, 0, 48000, 2)
	}

	snap := w.Header()
	if snap.WriteSeq != 50 {
		t.Errorf("WriteSeq = %d, want 50", snap.WriteSeq)
	}
}

func TestWriterAtomicVisibility(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 4096)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// Simulate a reader checking the atomic fields via unsafe.Pointer.
	// This validates that the atomic operations on the mmap'd region work.
	writePos := headerWritePos(w.base)
	readPos := headerReadPos(w.base)
	writeSeq := headerWriteSeq(w.base)

	if atomic.LoadUint64(writePos) != 0 {
		t.Error("initial WritePos not 0")
	}
	if atomic.LoadUint64(readPos) != 0 {
		t.Error("initial ReadPos not 0")
	}
	if atomic.LoadUint64(writeSeq) != 0 {
		t.Error("initial WriteSeq not 0")
	}

	pcm := make([]float32, 64)
	w.Write(pcm, 0, 48000, 2)

	if got := atomic.LoadUint64(writePos); got != 256 {
		t.Errorf("WritePos via atomic = %d, want 256", got)
	}
	if got := atomic.LoadUint64(writeSeq); got != 1 {
		t.Errorf("WriteSeq via atomic = %d, want 1", got)
	}
}
