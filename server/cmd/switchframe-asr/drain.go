package main

import (
	"log/slog"
	"sync"
	"time"
)

// sampleSource is the subset of *shmring.Reader the drainer needs.
type sampleSource interface {
	Read() []float32
}

// shmDrainer empties a sample source on a dedicated goroutine into a bounded
// in-process buffer.
//
// The shared-memory ring holds only ~2s of audio and its writer never blocks
// (it overwrites the oldest data), so the ring must be drained continuously.
// The main loop cannot do that itself: the same select-case that reads the
// ring also runs synchronous Transcribe calls that can block for multiple
// seconds, which would let the writer lap the ring and silently discard
// mid-utterance audio. The drainer decouples the two: the ring is emptied on
// a fast tick regardless of how long the consumer is busy, and the consumer
// picks up everything accumulated since its last visit via Take.
//
// The pending buffer is bounded at maxPending samples; if the consumer stalls
// for longer than that, the oldest samples are dropped (matching the ring's
// lossy-but-bounded semantics, just with a much larger window).
type shmDrainer struct {
	src        sampleSource
	maxPending int

	mu      sync.Mutex
	pending []float32
	dropped uint64

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// newShmDrainer starts a drainer that polls src every interval.
func newShmDrainer(src sampleSource, interval time.Duration, maxPending int) *shmDrainer {
	d := &shmDrainer{
		src:        src,
		maxPending: maxPending,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	go d.run(interval)
	return d
}

func (d *shmDrainer) run(interval time.Duration) {
	defer close(d.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			d.drainOnce()
		}
	}
}

func (d *shmDrainer) drainOnce() {
	samples := d.src.Read()
	if len(samples) == 0 {
		return
	}

	d.mu.Lock()
	d.pending = append(d.pending, samples...)
	var dropped, totalDropped uint64
	if len(d.pending) > d.maxPending {
		drop := len(d.pending) - d.maxPending
		d.pending = d.pending[:copy(d.pending, d.pending[drop:])]
		d.dropped += uint64(drop)
		dropped, totalDropped = uint64(drop), d.dropped
	}
	d.mu.Unlock()

	if dropped > 0 {
		slog.Warn("audio drain buffer overflow, dropped oldest samples",
			"dropped", dropped, "total_dropped", totalDropped)
	}
}

// Take returns all samples accumulated since the last Take and clears the
// pending buffer. Returns nil when no new audio is available.
func (d *shmDrainer) Take() []float32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.pending
	d.pending = nil
	return p
}

// Stop terminates the drain goroutine and waits for it to exit. Idempotent.
// The underlying source must not be closed until Stop has returned.
func (d *shmDrainer) Stop() {
	d.stopOnce.Do(func() { close(d.stop) })
	<-d.done
}
