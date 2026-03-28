//go:build correctness

package audio

import (
	"sync"
	"testing"
)

// TestRingBufferContinuity writes sequential numbered samples, reads them back,
// and verifies no gaps or repeats.
func TestRingBufferContinuity(t *testing.T) {
	const capacity = 4096
	rb := NewPCMRingBuffer(capacity)

	// Write sequential samples in varying chunk sizes
	chunkSizes := []int{100, 200, 50, 300, 150, 1024, 512}
	var nextWrite float32

	for _, cs := range chunkSizes {
		samples := make([]float32, cs)
		for i := range samples {
			samples[i] = nextWrite
			nextWrite++
		}
		rb.Push(samples)
	}

	totalWritten := int(nextWrite)
	if rb.Len() != totalWritten {
		t.Fatalf("expected %d samples in buffer, got %d", totalWritten, rb.Len())
	}

	// Read back in different chunk sizes and verify continuity
	var nextExpected float32
	readSizes := []int{250, 100, 500, 186, 300}
	for _, rs := range readSizes {
		if rb.Len() < rs {
			rs = rb.Len()
		}
		if rs <= 0 {
			break
		}
		out := rb.Pop(rs)
		if len(out) != rs {
			t.Fatalf("Pop(%d) returned %d samples", rs, len(out))
		}
		for i, v := range out {
			if v != nextExpected {
				t.Fatalf("at offset %d: expected %.0f, got %.0f", i, nextExpected, v)
			}
			nextExpected++
		}
	}
}

// TestRingBufferOverflow verifies that when the buffer overflows, the newest
// samples are preserved and oldest are discarded.
func TestRingBufferOverflow(t *testing.T) {
	const capacity = 2048 // minimum capacity
	rb := NewPCMRingBuffer(capacity)

	// Write more than capacity
	samples := make([]float32, capacity+500)
	for i := range samples {
		samples[i] = float32(i)
	}
	rb.Push(samples)

	if rb.Len() != capacity {
		t.Fatalf("expected buffer full at %d, got %d", capacity, rb.Len())
	}

	// The oldest 500 samples should be discarded
	out := rb.Pop(capacity)
	expectedStart := float32(500) // first 500 were dropped
	for i, v := range out {
		expected := expectedStart + float32(i)
		if v != expected {
			t.Fatalf("at index %d: expected %.0f, got %.0f", i, expected, v)
		}
	}
}

// TestRingBufferConcurrentWriteSequentialRead uses a mutex to simulate the
// real usage pattern: multiple goroutines push data (under lock), one reader
// pulls it. Verifies that all samples appear in order.
func TestRingBufferConcurrentWriteSequentialRead(t *testing.T) {
	const capacity = 32768
	rb := NewPCMRingBuffer(capacity)

	var mu sync.Mutex
	var nextID float32

	// Simulate concurrent writers
	const numWriters = 4
	const samplesPerWriter = 2000
	var wg sync.WaitGroup

	// Each writer pushes sequential IDs (assigned under lock)
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < samplesPerWriter; i++ {
				chunk := make([]float32, 10)
				mu.Lock()
				for j := range chunk {
					chunk[j] = nextID
					nextID++
				}
				rb.Push(chunk)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	totalWritten := numWriters * samplesPerWriter * 10
	if totalWritten > capacity {
		// Some samples were dropped due to overflow
		if rb.Len() != capacity {
			t.Fatalf("expected buffer full at %d, got %d", capacity, rb.Len())
		}
	} else {
		if rb.Len() != totalWritten {
			t.Fatalf("expected %d samples, got %d", totalWritten, rb.Len())
		}
	}

	// Read all samples and verify monotonically increasing (no gaps within
	// the retained window)
	var prev float32 = -1
	remaining := rb.Len()
	for remaining > 0 {
		readSize := 512
		if readSize > remaining {
			readSize = remaining
		}
		out := rb.Pop(readSize)
		for i, v := range out {
			if v <= prev {
				t.Fatalf("non-monotonic at index %d: prev=%.0f, got=%.0f", i, prev, v)
			}
			prev = v
		}
		remaining -= readSize
	}
}

// TestRingBufferUnderflow verifies that reading from an empty buffer returns
// silence (zeros).
func TestRingBufferUnderflow(t *testing.T) {
	rb := NewPCMRingBuffer(4096)

	// Push some data
	rb.Push([]float32{1.0, 2.0, 3.0})
	// Drain it
	rb.Pop(3)

	// Now buffer is empty, Pop should return silence
	silence := rb.Pop(100)
	if len(silence) != 100 {
		t.Fatalf("expected 100 samples, got %d", len(silence))
	}
	for i, v := range silence {
		if v != 0 {
			t.Fatalf("expected silence at index %d, got %f", i, v)
		}
	}
}

// TestRingBufferWrapAround verifies correct operation when rd/wr pointers
// wrap around the circular buffer boundary.
func TestRingBufferWrapAround(t *testing.T) {
	const capacity = 2048
	rb := NewPCMRingBuffer(capacity)

	// Fill nearly to capacity, then drain, then fill again to force wrap
	fill := make([]float32, capacity-100)
	for i := range fill {
		fill[i] = float32(i)
	}
	rb.Push(fill)
	rb.Pop(capacity - 100)

	// Now wr and rd are near the end of the buffer. Push more to wrap.
	wrap := make([]float32, 500)
	for i := range wrap {
		wrap[i] = float32(10000 + i)
	}
	rb.Push(wrap)

	if rb.Len() != 500 {
		t.Fatalf("expected 500 samples, got %d", rb.Len())
	}

	out := rb.Pop(500)
	for i, v := range out {
		expected := float32(10000 + i)
		if v != expected {
			t.Fatalf("at index %d: expected %.0f, got %.0f", i, expected, v)
		}
	}
}
