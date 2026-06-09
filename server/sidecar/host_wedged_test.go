package sidecar

import (
	"net"
	"testing"
	"time"
)

// A net.Pipe with no reader blocks Write forever unless a write deadline is
// set — the same situation as a wedged sidecar (process alive but not
// reading) whose socket send buffer is full.

// TestHostCloseDoesNotBlockOnWedgedSidecar is a regression test for the
// shutdown-message write in Close(): it used to be an unbounded blocking
// write performed while holding h.mu, so a wedged sidecar made Close hang
// forever and deadlocked every other Host method behind the mutex.
func TestHostCloseDoesNotBlockOnWedgedSidecar(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	h := NewHost(Config{ShutdownTimeout: 200 * time.Millisecond})
	h.mu.Lock()
	h.conn = server
	h.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- h.Close() }()

	select {
	case <-done:
		// Close returned; the deadline bounded the wedged write.
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on shutdown write to a wedged sidecar")
	}

	// Conn, SocketDir, etc. must not deadlock behind h.mu afterwards.
	if c := h.Conn(); c != nil {
		t.Errorf("Conn() = %v after Close, want nil", c)
	}
}

// TestHostCloseAfterInflightSendTimeout is a regression test for Close
// racing an in-flight Send on a wedged sidecar: Close set the write deadline
// before acquiring writeMu, but Send's epilogue clears the deadline after its
// own write times out. Close's shutdown write then blocked forever on the
// wedged conn while holding h.mu.
func TestHostCloseAfterInflightSendTimeout(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	h := NewHost(Config{WriteTimeout: 200 * time.Millisecond, ShutdownTimeout: 200 * time.Millisecond})
	h.mu.Lock()
	h.conn = server
	h.mu.Unlock()
	h.alive.Store(true)

	sendDone := make(chan struct{})
	go func() {
		_ = h.Send(&Message{Cmd: "ping"})
		close(sendDone)
	}()
	// Let Send get blocked inside WriteMessage while holding writeMu.
	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan struct{})
	go func() {
		_ = h.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		// Close re-armed the deadline after acquiring writeMu, so the
		// shutdown write was bounded even though Send cleared the deadline.
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung after in-flight Send cleared its deadline")
	}
	<-sendDone
}

// TestHostSendTimesOutOnWedgedSidecar verifies that Send bounds its socket
// write: one blocked write on a wedged sidecar previously held writeMu
// forever, permanently blocking all subsequent senders with no recovery
// until the process exited.
func TestHostSendTimesOutOnWedgedSidecar(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	h := NewHost(Config{WriteTimeout: 200 * time.Millisecond})
	h.mu.Lock()
	h.conn = server
	h.mu.Unlock()
	h.alive.Store(true)

	errCh := make(chan error, 1)
	go func() { errCh <- h.Send(&Message{Cmd: "ping"}) }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected a timeout error from Send on a wedged sidecar")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send blocked forever on a wedged sidecar")
	}

	// A timed-out control write means the sidecar is wedged; recovery must be
	// triggered rather than leaving the host claiming a healthy sidecar.
	if h.Alive() {
		t.Error("expected Alive() == false after a Send write timeout")
	}
	select {
	case req := <-h.restartCh:
		if req.reason == "" {
			t.Error("restart signal has empty reason")
		}
	default:
		t.Error("expected a restart signal after a Send write timeout")
	}
}

// TestDrainStaleRestartSignalKeepsNewGenerationCrash is a regression test
// for the post-respawn drain in restartLoop: it used to unconditionally
// swallow one buffered signal. If the freshly spawned sidecar crashed
// immediately (crash-looping binary), the new generation's only restart
// signal was consumed by the drain and the host stayed dead forever.
func TestDrainStaleRestartSignalKeepsNewGenerationCrash(t *testing.T) {
	t.Parallel()

	h := NewHost(Config{})
	h.spawnGen.Store(2) // respawn just produced generation 2

	// The new generation crashed right after spawn: its watchProcess
	// enqueued a gen-2 signal before the drain ran.
	h.triggerRestartForGen(2, "process exited")
	h.drainStaleRestartSignal()

	select {
	case req := <-h.restartCh:
		if req.gen != 2 {
			t.Errorf("restart signal gen = %d, want 2", req.gen)
		}
	default:
		t.Fatal("legitimate crash signal from the new generation was swallowed by the drain")
	}
}

// TestDrainRecoversDroppedNewGenerationSignal is a regression test for the
// dropped-signal variant of the drain bug: a stale old-generation signal
// occupies the cap-1 restartCh when the freshly spawned generation crashes,
// so triggerRestartForGen drops the new-generation signal (buffer full, but
// alive is set false). The drain then discarded the stale signal without
// compensating, leaving the host dead forever with no restart pending.
func TestDrainRecoversDroppedNewGenerationSignal(t *testing.T) {
	t.Parallel()

	h := NewHost(Config{})
	h.spawnGen.Store(2) // respawn just succeeded
	h.alive.Store(true)

	// Stale signal from old gen 1 (e.g. old readLoop woken by conn close)
	// fills the cap-1 buffer.
	h.restartCh <- restartRequest{gen: 1, reason: "stale readLoop"}

	// New generation crashes: the signal is dropped (buffer full), but
	// alive is set to false.
	h.triggerRestartForGen(2, "new gen crashed")

	h.drainStaleRestartSignal()

	select {
	case <-h.restartCh:
		// Drain compensated for the dropped current-generation signal.
	default:
		t.Fatal("new-generation crash signal lost: host would stay dead forever")
	}
}

// TestDrainStaleRestartSignalRemovesOldGenerationSignal verifies the drain
// still removes signals left over from the generation that was just
// replaced, so a stale readLoop/watchProcess notification does not cause a
// spurious extra restart cycle.
func TestDrainStaleRestartSignalRemovesOldGenerationSignal(t *testing.T) {
	t.Parallel()

	h := NewHost(Config{})
	h.spawnGen.Store(2)
	h.alive.Store(true) // spawnLocked set alive on the successful respawn

	// Stale signal from the old generation (1), enqueued during the restart
	// cycle before spawnLocked bumped the generation.
	h.restartCh <- restartRequest{gen: 1, reason: "read loop ended"}
	h.drainStaleRestartSignal()

	select {
	case req := <-h.restartCh:
		t.Fatalf("stale old-generation signal was not drained: %+v", req)
	default:
	}
}
