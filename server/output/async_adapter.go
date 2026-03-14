package output

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const tsPacketBufCap = 65536 // 64KB default buffer capacity for TS packet pool

var tsPacketPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, tsPacketBufCap)
		return &b
	},
}

// AsyncAdapter wraps an Adapter with a buffered channel for non-blocking writes.
// When the buffer is full, writes are dropped and the drop counter is incremented.
// This prevents a slow adapter (e.g., disk I/O or network) from blocking other
// adapters in the fan-out loop.
type AsyncAdapter struct {
	inner       Adapter
	buffer      chan *[]byte
	dropped     atomic.Int64
	lastDropLog atomic.Int64
	stopCh      chan struct{}
	doneCh      chan struct{}
	stopOnce    sync.Once
}

// NewAsyncAdapter creates an AsyncAdapter wrapping the given inner adapter
// with a buffered channel of the specified size.
func NewAsyncAdapter(inner Adapter, bufSize int) *AsyncAdapter {
	return &AsyncAdapter{
		inner:  inner,
		buffer: make(chan *[]byte, bufSize),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// ID delegates to the inner adapter.
func (a *AsyncAdapter) ID() string {
	return a.inner.ID()
}

// Start delegates to the inner adapter and then starts the drain goroutine.
func (a *AsyncAdapter) Start(ctx context.Context) error {
	if err := a.inner.Start(ctx); err != nil {
		return err
	}
	go a.drain()
	return nil
}

// startDrain starts only the drain goroutine without calling inner.Start().
// Used by Manager when the inner adapter has already been started.
func (a *AsyncAdapter) startDrain() {
	go a.drain()
}

// Write copies the data and sends it to the buffer channel. If the channel
// is full, the packet is dropped and the drop counter is incremented.
// Write never blocks.
func (a *AsyncAdapter) Write(tsData []byte) (int, error) {
	bp, ok := tsPacketPool.Get().(*[]byte)
	if !ok {
		b := make([]byte, 0, tsPacketBufCap)
		bp = &b
	}
	*bp = append((*bp)[:0], tsData...)

	select {
	case a.buffer <- bp:
		// Sent successfully.
	default:
		// Buffer full, drop the packet.
		tsPacketPool.Put(bp)
		a.dropped.Add(1)
		now := time.Now().UnixNano()
		last := a.lastDropLog.Load()
		if now-last > int64(time.Second) {
			if a.lastDropLog.CompareAndSwap(last, now) {
				slog.Warn("async adapter dropped packet",
					"adapter", a.inner.ID(),
					"dropped", a.dropped.Load())
			}
		}
	}

	return len(tsData), nil
}

// Dropped returns the total number of packets dropped due to a full buffer.
func (a *AsyncAdapter) Dropped() int64 {
	return a.dropped.Load()
}

// Stop signals the drain goroutine to exit and waits for it to finish
// draining any remaining buffered data. Safe to call multiple times.
// Resets the dropped counter so metrics are not inflated across start/stop cycles.
func (a *AsyncAdapter) Stop() {
	a.stopOnce.Do(func() {
		close(a.stopCh)
	})
	<-a.doneCh
	a.dropped.Store(0)
}

// Close delegates to the inner adapter.
func (a *AsyncAdapter) Close() error {
	return a.inner.Close()
}

// Status delegates to the inner adapter.
func (a *AsyncAdapter) Status() AdapterStatus {
	return a.inner.Status()
}

// drain reads from the buffer channel and writes to the inner adapter.
// It exits when stopCh is closed and the buffer is drained.
func (a *AsyncAdapter) drain() {
	defer close(a.doneCh)

	for {
		select {
		case bp := <-a.buffer:
			if bp == nil {
				// Channel closed or nil data; skip.
				continue
			}
			if _, err := a.inner.Write(*bp); err != nil {
				slog.Error("async adapter inner write error",
					"adapter", a.inner.ID(),
					"err", err)
			}
			tsPacketPool.Put(bp)
		case <-a.stopCh:
			// Drain remaining items in the buffer before exiting.
			for {
				select {
				case bp := <-a.buffer:
					if bp == nil {
						continue
					}
					if _, err := a.inner.Write(*bp); err != nil {
						slog.Error("async adapter inner write error during drain",
							"adapter", a.inner.ID(),
							"err", err)
					}
					tsPacketPool.Put(bp)
				default:
					return
				}
			}
		}
	}
}

// Compile-time check that AsyncAdapter satisfies the Adapter interface.
var _ Adapter = (*AsyncAdapter)(nil)
