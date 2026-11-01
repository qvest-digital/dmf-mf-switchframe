package gpu

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// previewLog records what a previewWorker did with each item and with its
// encoder.
type previewLog struct {
	mu       sync.Mutex
	encoded  []int
	idr      []bool
	released []int
	closed   bool
	events   []string // "opened", "closed", in order
}

func (l *previewLog) note(e string) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *previewLog) release(i int) {
	l.mu.Lock()
	l.released = append(l.released, i)
	l.mu.Unlock()
}

// opener returns an open function that waits for gate, then fails with
// err or returns an encoder that records into l.
func (l *previewLog) opener(gate <-chan struct{}, err error) func() (previewEncodeFunc[int], func(), error) {
	return func() (previewEncodeFunc[int], func(), error) {
		<-gate
		l.note("opened")
		if err != nil {
			return nil, nil, err
		}
		encode := func(i int, forceIDR bool) bool {
			l.mu.Lock()
			l.encoded = append(l.encoded, i)
			l.idr = append(l.idr, forceIDR)
			l.mu.Unlock()
			return true
		}
		closeEnc := func() {
			l.mu.Lock()
			l.closed = true
			l.mu.Unlock()
			l.note("closed")
		}
		return encode, closeEnc, nil
	}
}

func (l *previewLog) snapshot() (encoded []int, idr []bool, released []int, closed bool, events []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int(nil), l.encoded...), append([]bool(nil), l.idr...),
		append([]int(nil), l.released...), l.closed, append([]string(nil), l.events...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// start starts a worker and fails the test when the start waits for the
// encoder to open.
func start(t *testing.T, l *previewLog, gate <-chan struct{}, err error) *previewWorker[int] {
	t.Helper()
	started := make(chan *previewWorker[int], 1)
	go func() { started <- startPreviewWorker("srt:cam", l.opener(gate, err), l.release) }()
	select {
	case w := <-started:
		return w
	case <-time.After(time.Second):
		t.Fatal("startPreviewWorker waited for the encoder to open")
		return nil
	}
}

// Registering a source with a preview must not wait for its encoder to
// open: on a card at its session cap the open takes seconds, and an SRT
// connection's socket fills meanwhile. The frames that arrive during the
// open replace each other; the newest is encoded once the encoder is
// open, as an IDR frame, and the ones after it are not.
func TestPreviewWorkerDoesNotWaitForItsEncoderToOpen(t *testing.T) {
	l := &previewLog{}
	gate := make(chan struct{})
	w := start(t, l, gate, nil)
	for i := 1; i <= 3; i++ {
		w.queue(i)
	}
	if w.active() {
		t.Fatal("active before the encoder opened")
	}
	close(gate)
	waitFor(t, "frame 3 encoded", func() bool { e, _, _, _, _ := l.snapshot(); return len(e) == 1 })
	if !w.active() {
		t.Fatal("not active with the encoder open")
	}
	w.queue(4)
	waitFor(t, "frame 4 encoded", func() bool { e, _, _, _, _ := l.snapshot(); return len(e) == 2 })
	w.stop()

	encoded, idr, released, closed, _ := l.snapshot()
	if len(encoded) != 2 || encoded[0] != 3 || encoded[1] != 4 || !idr[0] || idr[1] {
		t.Fatalf("encoded %v with IDR %v, want [3 4] with [true false]", encoded, idr)
	}
	if len(released) != 2 || released[0] != 1 || released[1] != 2 {
		t.Fatalf("released %v, want the replaced [1 2]", released)
	}
	if !closed || w.active() {
		t.Fatalf("encoder closed %v, active %v after stop", closed, w.active())
	}
}

// A stop during the open, as a reconnect's registration stops the old
// connection's preview, returns only once the encoder that open returns
// is closed again: a card has a limited number of encode sessions, and
// the new connection's encoder opens right after. Nothing queued leaks.
func TestPreviewWorkerStoppedDuringItsOpenClosesTheEncoderFirst(t *testing.T) {
	l := &previewLog{}
	gate := make(chan struct{})
	w := start(t, l, gate, nil)
	w.queue(1)
	stopped := make(chan struct{})
	go func() {
		w.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while the encoder was still opening")
	case <-time.After(50 * time.Millisecond):
	}
	w.queue(2) // after the stop: released, never encoded
	close(gate)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop never returned")
	}
	encoded, _, released, closed, events := l.snapshot()
	if !closed || len(events) != 2 || events[0] != "opened" || events[1] != "closed" {
		t.Fatalf("encoder events %v, closed %v; want opened then closed before stop returned", events, closed)
	}
	if len(encoded)+len(released) != 2 || len(released) == 0 || released[len(released)-1] != 2 {
		t.Fatalf("encoded %v, released %v: want 1 encoded or released and 2 released, each once", encoded, released)
	}
	w.stop() // a second stop returns at once
}

// An encoder that fails to open leaves the source without a preview:
// what was queued and what is queued later is released, and stop
// returns.
func TestPreviewWorkerWhoseEncoderFailsReleasesItsFrames(t *testing.T) {
	l := &previewLog{}
	gate := make(chan struct{})
	w := start(t, l, gate, errors.New("session cap"))
	w.queue(1)
	close(gate)
	waitFor(t, "frame 1 released", func() bool { _, _, r, _, _ := l.snapshot(); return len(r) == 1 })
	w.queue(2)
	w.stop()
	encoded, _, released, closed, _ := l.snapshot()
	if len(encoded) != 0 || closed || len(released) != 2 || released[0] != 1 || released[1] != 2 {
		t.Fatalf("encoded %v, closed %v, released %v; want nothing encoded or closed and [1 2] released",
			encoded, closed, released)
	}
	if w.active() {
		t.Fatal("active without an encoder")
	}
}
