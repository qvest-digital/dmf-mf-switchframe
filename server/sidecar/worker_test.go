package sidecar

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestWorkerSendRecv(t *testing.T) {
	// Create a Unix socket pair via a listener.
	dir := t.TempDir()
	sock := dir + "/test.sock"

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// Connect worker.
	w, err := NewWorker(sock)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Accept on host side.
	hostConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer func() { _ = hostConn.Close() }()

	// Worker sends a message to the host.
	sent := &Message{Type: "status", Data: "ok"}
	if err := w.Send(sent); err != nil {
		t.Fatalf("worker Send: %v", err)
	}

	// Host reads it.
	got, err := ReadMessage(hostConn)
	if err != nil {
		t.Fatalf("host ReadMessage: %v", err)
	}
	if got.Type != "status" {
		t.Errorf("host got Type = %q, want %q", got.Type, "status")
	}
	if got.Data != "ok" {
		t.Errorf("host got Data = %v, want %q", got.Data, "ok")
	}

	// Host sends a message to the worker.
	hostMsg := &Message{Cmd: "configure", Config: "test-config"}
	if err := WriteMessage(hostConn, hostMsg); err != nil {
		t.Fatalf("host WriteMessage: %v", err)
	}

	// Worker reads it.
	recv, err := w.Recv()
	if err != nil {
		t.Fatalf("worker Recv: %v", err)
	}
	if recv.Cmd != "configure" {
		t.Errorf("worker got Cmd = %q, want %q", recv.Cmd, "configure")
	}
	if recv.Config != "test-config" {
		t.Errorf("worker got Config = %v, want %q", recv.Config, "test-config")
	}
}

func TestWorkerRunShutdown(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/test.sock"

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	w, err := NewWorker(sock)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	defer func() { _ = w.Close() }()

	hostConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer func() { _ = hostConn.Close() }()

	// Start Run in a goroutine.
	runErr := make(chan error, 1)
	go func() {
		runErr <- w.Run(context.Background(), func(msg *Message) *Message {
			return nil
		})
	}()

	// Send shutdown from host.
	if err := WriteMessage(hostConn, &Message{Cmd: "shutdown"}); err != nil {
		t.Fatalf("host send shutdown: %v", err)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run returned error: %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}

func TestWorkerRunContext(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/test.sock"

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	w, err := NewWorker(sock)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	defer func() { _ = w.Close() }()

	hostConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer func() { _ = hostConn.Close() }()

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- w.Run(ctx, func(msg *Message) *Message {
			return nil
		})
	}()

	// Cancel the context.
	cancel()

	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to return after cancel")
	}
}

func TestWorkerRunHandler(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/test.sock"

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	w, err := NewWorker(sock)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	defer func() { _ = w.Close() }()

	hostConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer func() { _ = hostConn.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start Run with echo handler.
	runErr := make(chan error, 1)
	go func() {
		runErr <- w.Run(ctx, func(msg *Message) *Message {
			if msg.Cmd == "echo" {
				return &Message{Type: "echo", Data: msg.Data}
			}
			return nil
		})
	}()

	// Send an echo command from host.
	if err := WriteMessage(hostConn, &Message{Cmd: "echo", Data: "ping"}); err != nil {
		t.Fatalf("host send echo: %v", err)
	}

	// Read the reply.
	reply, err := ReadMessage(hostConn)
	if err != nil {
		t.Fatalf("host ReadMessage: %v", err)
	}
	if reply.Type != "echo" {
		t.Errorf("reply Type = %q, want %q", reply.Type, "echo")
	}
	if reply.Data != "ping" {
		t.Errorf("reply Data = %v, want %q", reply.Data, "ping")
	}

	// Clean shutdown.
	cancel()
	select {
	case <-runErr:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}

func TestWorkerReady(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/test.sock"

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	w, err := NewWorker(sock)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Capture stdout.
	oldStdout := os.Stdout
	r, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = wPipe

	w.Ready()

	// Restore stdout and read what was written.
	_ = wPipe.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()

	if buf.String() != "READY\n" {
		t.Errorf("Ready() printed %q, want %q", buf.String(), "READY\n")
	}
}

// TestWorkerRunShutdownUnwindsRecvGoroutine is a regression test for the recv
// goroutine leak: when Run returns on a shutdown command, the internal goroutine
// parked in a blocking w.Recv() must still unwind even though the caller never
// calls Close() (the documented footgun for embedders that keep the process
// alive after Run returns). Run sets a past read deadline on exit to force the
// parked read to fail, so the goroutine exits on its own.
func TestWorkerRunShutdownUnwindsRecvGoroutine(t *testing.T) {
	// Not t.TempDir(): it embeds this long test name, and the resulting unix
	// socket path exceeds the kernel's sun_path limit (104 bytes on macOS),
	// failing bind with EINVAL.
	dir, err := os.MkdirTemp("", "wrk-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := dir + "/test.sock"

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	w, err := NewWorker(sock)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	// Intentionally do NOT defer w.Close(): the whole point is that the recv
	// goroutine unwinds without the caller closing the conn.

	hostConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer func() { _ = hostConn.Close() }()

	baseline := runtime.NumGoroutine()

	runErr := make(chan error, 1)
	go func() {
		runErr <- w.Run(context.Background(), func(*Message) *Message { return nil })
	}()

	if err := WriteMessage(hostConn, &Message{Cmd: "shutdown"}); err != nil {
		t.Fatalf("host send shutdown: %v", err)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run returned error: %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}

	// The Run goroutine plus the internal recv goroutine must both have exited.
	// Poll instead of asserting once, since goroutine teardown is asynchronous.
	deadline := time.After(5 * time.Second)
	for {
		if runtime.NumGoroutine() <= baseline {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("recv goroutine did not unwind: NumGoroutine=%d, baseline=%d",
				runtime.NumGoroutine(), baseline)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestWorkerConn(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/test.sock"

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	w, err := NewWorker(sock)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	defer func() { _ = w.Close() }()

	if w.Conn() == nil {
		t.Error("Conn() returned nil, want non-nil")
	}
}
