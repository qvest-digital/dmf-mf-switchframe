//go:build !windows

package shmring

import (
	"math"
	"sync"
	"sync/atomic"
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
	defer func() { _ = w.Close() }()

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
	defer func() { _ = r.Close() }()

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
	defer func() { _ = w.Close() }()

	// Open reader before writing, so readPos starts at 0.
	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

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
	defer func() { _ = w.Close() }()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

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
	defer func() { _ = w.Close() }()

	// Write to set metadata.
	pcm := make([]float32, 10)
	w.Write(pcm, 0, 44100, 1)

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

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
	_ = w.Close()
}

// TestReaderRejectsOversizedCapacity is the regression test for the
// unvalidated-header finding: CapacityBytes comes from shared memory written
// by another process, and every unsafe read in the reader is bounded by it.
// A capacity that does not fit the fstat-derived segment size must be
// rejected, otherwise copyOut reads past the end of the mapping (SIGBUS).
func TestReaderRejectsOversizedCapacity(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}

	// Corrupt CapacityBytes to a huge power of two — far larger than the
	// segment — via the writer's mapped header, simulating a stale/corrupt or
	// version-skewed segment.
	hdr := (*header)(w.base)
	origCap := hdr.CapacityBytes
	hdr.CapacityBytes = 1 << 40

	_, err = NewReader(name)
	if err == nil {
		t.Error("NewReader with capacity larger than the segment should return an error")
	}

	// Restore so writer cleanup and a sanity re-open work.
	hdr.CapacityBytes = origCap
	r, err := NewReader(name)
	if err != nil {
		t.Fatalf("NewReader after restoring capacity: %v", err)
	}
	_ = r.Close()
	_ = w.Close()
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
	defer func() { _ = w.Close() }()

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
	defer func() { _ = r.Close() }()

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
	defer func() { _ = w.Close() }()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

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
	defer func() { _ = w.Close() }()

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
	_ = r2.Close()
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
	defer func() { _ = w.Close() }()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

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
	defer func() { _ = w.Close() }()

	pcm := make([]float32, 10)
	w.Write(pcm, 99, 16000, 1)

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

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

// TestReaderOverflowReturnsCleanDataWhenProducerIdle verifies the seqlock-style
// re-validation does NOT discard a legitimate overflow recovery: with the
// producer stopped, the post-copy WritePos re-read matches the entry value, so
// the full latest capacity is returned intact (the boundary "==" case where the
// reader is exactly capacityBytes behind an idle producer).
func TestReaderOverflowReturnsCleanDataWhenProducerIdle(t *testing.T) {
	name := testShmName(t)
	const capSamples = 1024
	w, err := NewWriter(name, capSamples)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	// Write 3x capacity in small chunks (producer then idle); each sample value
	// == its global index. Chunked writes (<= capacity) match real audio usage
	// so the physical ring ends holding exactly the last capSamples.
	const total = capSamples * 3
	const chunk = 128
	buf := make([]float32, chunk)
	for base := 0; base < total; base += chunk {
		for i := range buf {
			buf[i] = float32(base + i)
		}
		w.Write(buf, 0, 48000, 1)
	}

	// Producer is now idle. The reader is capSamples behind the latest data.
	got := r.Read()
	if len(got) != capSamples {
		t.Fatalf("overflow Read() = %d samples, want %d (latest capacity)", len(got), capSamples)
	}
	// Must be the newest capSamples, contiguous and untorn (idle producer).
	for i, v := range got {
		want := float32(total - capSamples + i)
		if v != want {
			t.Fatalf("overflow sample[%d] = %v, want %v (clean idle-overflow recovery)", i, v, want)
		}
	}

	// Caught up: next read returns nil.
	if got2 := r.Read(); got2 != nil {
		t.Errorf("post-overflow Read() = %d samples, want nil", len(got2))
	}
}

// TestReaderConcurrentNoGarbageToInference is the regression test for the
// torn-overflow finding. A producer continuously laps a tiny ring while a
// consumer reads concurrently. The seqlock-style re-validation guarantees the
// reader never emits NaN/Inf (the failure mode the finding flags as "potential
// NaN/garbage fed to inference") and that the consumer makes forward progress
// and fully drains once the producer stops.
//
// Note: this lossy SPSC ring can still return *stale* (previously-valid) values
// under a continuous concurrent producer — a fully non-lossy reader needs
// writer-side per-region sequencing. The invariant asserted here is the one the
// bounded fix actually guarantees and the one inference cares about: finite,
// in-range samples and liveness.
func TestReaderConcurrentNoGarbageToInference(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024) // tiny ring => constant lapping
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	const totalSamples = 1 << 20 // < 2^24, exactly representable in float32
	const chunk = 64

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]float32, chunk)
		for base := 0; base < totalSamples; base += chunk {
			for i := range buf {
				// All written values are finite and within [0,totalSamples).
				buf[i] = float32(base + i)
			}
			w.Write(buf, 0, 48000, 1)
		}
	}()

	var sawData bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			got := r.Read()
			for i, v := range got {
				sawData = true
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Errorf("Read returned NaN/Inf at index %d", i)
					return
				}
				// A torn combination of two valid in-range samples could land
				// outside [0,totalSamples); assert the inference-relevant bound.
				if v < 0 || v >= float32(totalSamples) {
					t.Errorf("Read returned out-of-range sample %v at index %d", v, i)
					return
				}
			}
			// Liveness/drain: stop once the producer is done and we're caught up.
			wp := atomic.LoadUint64(headerWritePos(r.base))
			if wp >= uint64(totalSamples)*4 && r.readPos >= wp {
				return
			}
		}
	}()

	wg.Wait()
	<-done
	if !sawData {
		t.Error("consumer never observed any data")
	}
}

// TestReaderConcurrentReadsAreUntorn is the regression test for the in-flight
// torn-write finding: the post-copy WritePos recheck alone cannot see a
// producer that is mid-copy (already overwriting the reader's region) but has
// not yet published the new WritePos, so the reader could return a batch that
// mixes old-lap and new-lap samples.
//
// The writer publishes the values 0,1,2,... in order, so every batch a correct
// reader returns must be an exact contiguous logical span of the written
// stream: within a batch, each sample is exactly the previous sample + 1, and
// batches never go backwards. A torn read shows up as a discontinuity inside a
// single batch.
func TestReaderConcurrentReadsAreUntorn(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024) // tiny ring => constant lapping
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	const totalSamples = 1 << 21 // < 2^24, exactly representable in float32
	const chunk = 96

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]float32, chunk)
		for base := 0; base < totalSamples; base += chunk {
			for i := range buf {
				buf[i] = float32(base + i)
			}
			w.Write(buf, 0, 48000, 1)
		}
	}()

	var lastSeen float32 = -1
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			got := r.Read()
			for i, v := range got {
				if i == 0 {
					if v <= lastSeen {
						t.Errorf("batch went backwards: starts at %v, already saw %v", v, lastSeen)
						return
					}
				} else if v != got[i-1]+1 {
					t.Errorf("torn batch: sample[%d] = %v, want %v (contiguous with previous)", i, v, got[i-1]+1)
					return
				}
				lastSeen = v
			}
			// Liveness/drain: stop once the producer is done and we're caught up.
			wp := atomic.LoadUint64(headerWritePos(r.base))
			if wp >= uint64(totalSamples)*4 && r.readPos >= wp {
				return
			}
		}
	}()

	wg.Wait()
	<-done
	if lastSeen < 0 {
		t.Error("consumer never observed any data")
	}
}

// TestWriterMetadataPublishedAtomically verifies metadata (SampleRate/Channels/
// BasePTS) is set-once and readable atomically through the exported header
// accessors, matching the values the producer published on first write.
func TestWriterMetadataPublishedAtomically(t *testing.T) {
	name := testShmName(t)
	w, err := NewWriter(name, 1024)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Before any write, metadata is zero (atomic loads of a freshly zeroed mmap).
	if got := atomic.LoadUint32(headerSampleRate(w.base)); got != 0 {
		t.Errorf("pre-write SampleRate = %d, want 0", got)
	}

	pcm := make([]float32, 16)
	w.Write(pcm, 0x0123456789ABCDEF, 48000, 2)

	if got := atomic.LoadUint32(headerSampleRate(w.base)); got != 48000 {
		t.Errorf("atomic SampleRate = %d, want 48000", got)
	}
	if got := atomic.LoadUint32(headerChannels(w.base)); got != 2 {
		t.Errorf("atomic Channels = %d, want 2", got)
	}
	if got := atomic.LoadInt64(headerBasePTS(w.base)); got != 0x0123456789ABCDEF {
		t.Errorf("atomic BasePTS = 0x%X, want 0x0123456789ABCDEF", got)
	}

	// Reader observes the same metadata atomically.
	r, err := NewReader(name)
	if err != nil {
		skipIfNoShm(t, err)
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	if got := r.SampleRate(); got != 48000 {
		t.Errorf("Reader.SampleRate() = %d, want 48000", got)
	}
	if got := r.Channels(); got != 2 {
		t.Errorf("Reader.Channels() = %d, want 2", got)
	}

	// Second write must not overwrite set-once metadata.
	w.Write(pcm, 999, 16000, 1)
	if got := atomic.LoadUint32(headerSampleRate(w.base)); got != 48000 {
		t.Errorf("SampleRate after second write = %d, want 48000 (set-once)", got)
	}
	if got := atomic.LoadInt64(headerBasePTS(w.base)); got != 0x0123456789ABCDEF {
		t.Errorf("BasePTS after second write changed; want set-once 0x0123456789ABCDEF")
	}
}
