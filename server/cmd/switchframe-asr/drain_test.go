package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/asr/shmring"
)

// TestDrainerKeepsRingDrainedDuringSlowConsumer is the regression test for the
// ring-starvation finding: the main loop used to call reader.Read() from the
// same select-case that runs multi-second synchronous Transcribe calls, so the
// small (~2s) shm ring was lapped by the never-blocking writer and
// mid-utterance audio was silently discarded.
//
// The drainer empties the ring on its own goroutine, so even when the consumer
// is blocked for far longer than the ring can hold, no samples are lost. The
// test writes many times the ring capacity while the consumer never calls
// Take(), then verifies every sample arrives exactly once, in order.
func TestDrainerKeepsRingDrainedDuringSlowConsumer(t *testing.T) {
	name := fmt.Sprintf("/asr-drain-%d", os.Getpid())
	const ringCap = 8192 // samples — tiny vs the amount written below

	w, err := shmring.NewWriter(name, ringCap)
	if err != nil {
		if strings.Contains(err.Error(), "shared memory not available") {
			t.Skip("shared memory not available (no cgo)")
		}
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	r, err := shmring.NewReader(name)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	d := newShmDrainer(r, 5*time.Millisecond, 1<<22)
	defer d.Stop()

	// Producer: writes 32x the ring capacity in small paced chunks while the
	// consumer is "stuck in Transcribe" (never calls Take until the end).
	const chunk = 256
	const total = ringCap * 32
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		buf := make([]float32, chunk)
		for base := 0; base < total; base += chunk {
			for i := range buf {
				buf[i] = float32(base + i)
			}
			w.Write(buf, 0, 48000, 1)
			time.Sleep(2 * time.Millisecond)
		}
	}()

	<-producerDone
	// Let the drainer pick up the tail of the stream.
	time.Sleep(100 * time.Millisecond)

	got := d.Take()
	if len(got) != total {
		t.Fatalf("drained %d samples, want %d (audio lost while consumer was blocked)", len(got), total)
	}
	for i, v := range got {
		if v != float32(i) {
			t.Fatalf("sample[%d] = %v, want %v (out of order or torn)", i, v, float32(i))
		}
	}

	// Take clears the pending buffer.
	if again := d.Take(); len(again) != 0 {
		t.Errorf("second Take() = %d samples, want 0", len(again))
	}
}

// fakeSource feeds canned chunks to the drainer.
type fakeSource struct {
	mu     sync.Mutex
	chunks [][]float32
}

func (f *fakeSource) push(c []float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunks = append(f.chunks, c)
}

func (f *fakeSource) Read() []float32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.chunks) == 0 {
		return nil
	}
	c := f.chunks[0]
	f.chunks = f.chunks[1:]
	return c
}

// TestDrainerBoundsPendingBuffer verifies the drainer's memory bound: when the
// consumer never takes, the pending buffer keeps only the newest maxPending
// samples (oldest are dropped, mirroring the lossy-but-bounded ring semantics).
func TestDrainerBoundsPendingBuffer(t *testing.T) {
	src := &fakeSource{}
	const maxPending = 1000
	const total = 4000

	for base := 0; base < total; base += 100 {
		c := make([]float32, 100)
		for i := range c {
			c[i] = float32(base + i)
		}
		src.push(c)
	}

	d := newShmDrainer(src, time.Millisecond, maxPending)
	defer d.Stop()

	// Wait until the source is fully consumed.
	deadline := time.After(5 * time.Second)
	for {
		src.mu.Lock()
		remaining := len(src.chunks)
		src.mu.Unlock()
		if remaining == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("drainer never consumed the source")
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(20 * time.Millisecond)

	got := d.Take()
	if len(got) != maxPending {
		t.Fatalf("Take() = %d samples, want %d (bounded)", len(got), maxPending)
	}
	// Must hold the NEWEST samples.
	for i, v := range got {
		want := float32(total - maxPending + i)
		if v != want {
			t.Fatalf("sample[%d] = %v, want %v (newest samples kept)", i, v, want)
		}
	}
}

// TestDrainerStopJoins verifies Stop terminates the drain goroutine and is
// idempotent.
func TestDrainerStopJoins(t *testing.T) {
	src := &fakeSource{}
	d := newShmDrainer(src, time.Millisecond, 100)
	d.Stop()
	d.Stop() // must not panic
	if got := d.Take(); len(got) != 0 {
		t.Errorf("Take() after Stop = %d samples, want 0", len(got))
	}
}
