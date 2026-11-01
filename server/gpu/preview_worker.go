package gpu

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// previewWorker runs a source's preview encoder on a goroutine of its own,
// which opens the encoder before it encodes. Opening one can take seconds:
// on a card at its encode-session cap, NVENC fails twice before libx264
// takes over. A registration that waited for it would hold up the
// source's ingest for as long, and an SRT connection would build a backlog
// in its socket meanwhile. Items queued while the encoder opens wait,
// newest first, as at any other time. stop closes the encoder before it
// returns, after the open when it comes during one, so a card's session
// is free again for an encoder opened next.
type previewWorker[T any] struct {
	mu      sync.Mutex
	ch      chan T // capacity 1, newest wins
	stopped bool
	done    chan struct{}
	opened  atomic.Bool
	release func(T)
}

// previewEncodeFunc encodes item, the first one with an IDR frame
// (forceIDR), hands on what it produced and releases item. It reports
// whether item was encoded, so the next one needs no IDR.
type previewEncodeFunc[T any] func(item T, forceIDR bool) bool

// startPreviewWorker starts the goroutine that opens the encoder with
// open and encodes every item queued, and returns without waiting for
// the open. open returns the encode function and what closes the
// encoder. release frees an item that is not encoded: one replaced by a
// newer one, or queued once the worker is stopped or its encoder failed
// to open.
func startPreviewWorker[T any](source string, open func() (previewEncodeFunc[T], func(), error), release func(T)) *previewWorker[T] {
	w := &previewWorker[T]{ch: make(chan T, 1), done: make(chan struct{}), release: release}
	go w.run(source, open)
	return w
}

func (w *previewWorker[T]) run(source string, open func() (previewEncodeFunc[T], func(), error)) {
	defer close(w.done)
	encode, closeEnc, err := open()
	if err != nil {
		slog.Warn("gpu: source manager: preview encoder failed", "source", source, "error", err)
		w.mu.Lock()
		w.stopped = true
		w.mu.Unlock()
		for _, item := range w.drain() {
			w.release(item)
		}
		return
	}
	defer closeEnc()
	w.opened.Store(true)
	forceIDR := true
	for item := range w.ch {
		if encode(item, forceIDR) {
			forceIDR = false
		}
	}
}

// drain takes the items still queued for a worker whose encoder never
// opened.
func (w *previewWorker[T]) drain() []T {
	w.mu.Lock()
	defer w.mu.Unlock()
	var items []T
	for {
		select {
		case item, ok := <-w.ch:
			if !ok {
				return items
			}
			items = append(items, item)
		default:
			return items
		}
	}
}

// queue hands item to the encoder, replacing an item still waiting, and
// releases item instead once the worker is stopped or its encoder failed
// to open.
func (w *previewWorker[T]) queue(item T) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		w.release(item)
		return
	}
	var dropped T
	replaced := false
	select {
	case dropped = <-w.ch:
		replaced = true
	default:
	}
	w.ch <- item
	w.mu.Unlock()
	if replaced {
		w.release(dropped)
	}
}

// stop stops the worker and waits for it to exit with its encoder
// closed; an item still waiting is encoded first. Safe to call more than
// once.
func (w *previewWorker[T]) stop() {
	w.mu.Lock()
	if !w.stopped {
		w.stopped = true
		close(w.ch)
	}
	w.mu.Unlock()
	<-w.done
}

// accepting reports whether queue still takes items: the worker is not
// stopped and its encoder is open or still opening.
func (w *previewWorker[T]) accepting() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.stopped
}

// active reports whether the encoder is open and the worker not stopped.
func (w *previewWorker[T]) active() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.opened.Load() && !w.stopped
}
