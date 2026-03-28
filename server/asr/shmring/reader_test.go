//go:build !windows

package shmring

import (
	"math"
	"testing"
	"unsafe"
)

func TestReaderOpenAndRead(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 4096)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// Write 1024 samples with known pattern.
	pcm := make([]float32, 1024)
	for i := range pcm {
		pcm[i] = float32(i) * 0.001
	}
	w.Write(pcm, 0, 48000, 2)

	// Open reader.
	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	// First read should return all 1024 samples.
	got := r.Read()
	if len(got) != 1024 {
		t.Fatalf("Read() returned %d samples, want 1024", len(got))
	}
	for i := 0; i < 1024; i++ {
		want := float32(i) * 0.001
		if math.Abs(float64(got[i]-want)) > 1e-7 {
			t.Errorf("sample[%d] = %f, want %f", i, got[i], want)
			break
		}
	}

	// Second read should return nil (caught up).
	got2 := r.Read()
	if got2 != nil {
		t.Errorf("second Read() returned %d samples, want nil (caught up)", len(got2))
	}
}

func TestReaderOverflow(t *testing.T) {
	name := testShmName(t)
	// Tiny buffer: 1024 float32 samples.
	w, err := NewWriter(name, 1024)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// Open reader before writing, so readPos starts at 0.
	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	// Write 2x capacity (2048 samples) without reading.
	// Write in two chunks so the writer wraps around.
	pcm1 := make([]float32, 1024)
	for i := range pcm1 {
		pcm1[i] = float32(i) // 0..1023
	}
	w.Write(pcm1, 0, 48000, 1)

	pcm2 := make([]float32, 1024)
	for i := range pcm2 {
		pcm2[i] = float32(i + 1024) // 1024..2047
	}
	w.Write(pcm2, 0, 48000, 1)

	// Reader should detect overflow and fast-forward.
	// Should return at most 1024 samples (the capacity) of the latest data.
	got := r.Read()
	if got == nil {
		t.Fatal("Read() returned nil, expected overflow recovery data")
	}
	if len(got) != 1024 {
		t.Fatalf("Read() returned %d samples, want 1024 (capacity)", len(got))
	}

	// The returned data should be the latest 1024 samples (1024..2047).
	for i := 0; i < 1024; i++ {
		want := float32(i + 1024)
		if math.Abs(float64(got[i]-want)) > 1e-5 {
			t.Errorf("overflow sample[%d] = %f, want %f", i, got[i], want)
			break
		}
	}

	// Subsequent read should return nil (caught up).
	got2 := r.Read()
	if got2 != nil {
		t.Errorf("post-overflow Read() returned %d samples, want nil", len(got2))
	}
}

func TestReaderMultipleReads(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 4096)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	// Write 100 samples.
	pcm1 := make([]float32, 100)
	for i := range pcm1 {
		pcm1[i] = float32(i) * 0.1
	}
	w.Write(pcm1, 0, 48000, 2)

	// Read should return exactly 100 samples.
	got1 := r.Read()
	if len(got1) != 100 {
		t.Fatalf("first Read() = %d samples, want 100", len(got1))
	}
	for i := 0; i < 100; i++ {
		want := float32(i) * 0.1
		if math.Abs(float64(got1[i]-want)) > 1e-6 {
			t.Errorf("read1 sample[%d] = %f, want %f", i, got1[i], want)
			break
		}
	}

	// Write 200 more samples.
	pcm2 := make([]float32, 200)
	for i := range pcm2 {
		pcm2[i] = float32(i+100) * 0.1
	}
	w.Write(pcm2, 1000, 48000, 2)

	// Read should return exactly 200 samples.
	got2 := r.Read()
	if len(got2) != 200 {
		t.Fatalf("second Read() = %d samples, want 200", len(got2))
	}
	for i := 0; i < 200; i++ {
		want := float32(i+100) * 0.1
		if math.Abs(float64(got2[i]-want)) > 1e-5 {
			t.Errorf("read2 sample[%d] = %f, want %f", i, got2[i], want)
			break
		}
	}
}

func TestReaderMetadata(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// Write to set metadata.
	pcm := make([]float32, 10)
	w.Write(pcm, 0, 44100, 1)

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	if r.SampleRate() != 44100 {
		t.Errorf("SampleRate() = %d, want 44100", r.SampleRate())
	}
	if r.Channels() != 1 {
		t.Errorf("Channels() = %d, want 1", r.Channels())
	}
}

func TestReaderInvalidMagic(t *testing.T) {
	name := testShmName(t)

	// Create a valid segment using Writer, then corrupt the magic.
	w, err := NewWriter(name, 1024)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	// Write some data so the segment is fully initialized.
	pcm := make([]float32, 10)
	w.Write(pcm, 0, 48000, 2)

	// Corrupt the magic in the header via the writer's base pointer.
	*(*uint32)(w.base) = 0xDEADBEEF

	// Attempt to open with NewReader — should fail due to invalid magic.
	_, err = NewReader(name)
	if err == nil {
		t.Fatal("NewReader with invalid magic should return error")
	}

	// Restore magic so writer cleanup works properly.
	*(*uint32)(w.base) = Magic
	w.Close()
}

func TestReaderWrapAroundRead(t *testing.T) {
	name := testShmName(t)
	// 256 float32 samples = 1024 bytes capacity.
	capacity := 256
	w, err := NewWriter(name, capacity)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// Write 200 samples to move writePos to 800.
	pcm1 := make([]float32, 200)
	for i := range pcm1 {
		pcm1[i] = -1.0
	}
	w.Write(pcm1, 0, 48000, 2)

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	// Read the initial 200 samples to advance readPos.
	got1 := r.Read()
	if len(got1) != 200 {
		t.Fatalf("initial Read() = %d, want 200", len(got1))
	}

	// Now write 100 samples that wrap around the buffer.
	// 56 at tail (offset 800..1023), 44 at head (offset 0..175).
	pcm2 := make([]float32, 100)
	for i := range pcm2 {
		pcm2[i] = float32(i) + 500.0
	}
	w.Write(pcm2, 1000, 48000, 2)

	// Read should return exactly 100 samples with correct values,
	// even though the data wraps around the physical buffer.
	got2 := r.Read()
	if len(got2) != 100 {
		t.Fatalf("wrap Read() = %d, want 100", len(got2))
	}
	for i := 0; i < 100; i++ {
		want := float32(i) + 500.0
		if math.Abs(float64(got2[i]-want)) > 1e-5 {
			t.Errorf("wrap sample[%d] = %f, want %f", i, got2[i], want)
			break
		}
	}
}

func TestReaderSizeFromShm(t *testing.T) {
	// Verify the reader correctly derives capacityBytes from the shm segment size.
	name := testShmName(t)
	w, err := NewWriter(name, 2048)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	// The reader's capacityBytes should match the writer's.
	if r.capacityBytes != w.capacityBytes {
		t.Errorf("reader capacityBytes = %d, writer capacityBytes = %d", r.capacityBytes, w.capacityBytes)
	}
}

func TestReaderCloseDoesNotUnlink(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}

	// Close the reader.
	if err := r.Close(); err != nil {
		t.Fatalf("Reader Close: %v", err)
	}

	// Writer should still be functional (shm not unlinked).
	pcm := make([]float32, 10)
	w.Write(pcm, 0, 48000, 2)
	snap := w.Header()
	if snap.WritePos != 40 {
		t.Errorf("WritePos after reader close = %d, want 40", snap.WritePos)
	}

	// Should be able to open a new reader (shm still exists).
	r2, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader after reader close: %v", err)
	}
	r2.Close()
}

// shmStat returns the size of a POSIX shared memory object by opening and fstat-ing.
// This is used by the reader to determine the total mapped size.
func TestReaderWritePosUpdatesVisible(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 4096)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	// Initially no data.
	if got := r.Read(); got != nil {
		t.Errorf("initial Read() = %d samples, want nil", len(got))
	}

	// Write from writer, read from reader — verifies cross-process atomics.
	pcm := make([]float32, 50)
	for i := range pcm {
		pcm[i] = 42.0
	}
	w.Write(pcm, 0, 48000, 2)

	got := r.Read()
	if len(got) != 50 {
		t.Fatalf("Read() = %d samples, want 50", len(got))
	}
	for i, v := range got {
		if v != 42.0 {
			t.Errorf("sample[%d] = %f, want 42.0", i, v)
			break
		}
	}
}

// Verify reader header field direct access via unsafe.Pointer (exported for internal use).
func TestReaderInternalBase(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	pcm := make([]float32, 10)
	w.Write(pcm, 99, 16000, 1)

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	// Verify the base pointer is valid and magic is accessible.
	h := (*header)(r.base)
	if h.Magic != Magic {
		t.Errorf("Magic via base = 0x%08X, want 0x%08X", h.Magic, Magic)
	}
	if h.SampleRate != 16000 {
		t.Errorf("SampleRate via base = %d, want 16000", h.SampleRate)
	}

	_ = unsafe.Pointer(r.base) // verify base is exported within package
}
