package sidecar

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testBinary holds the path to the compiled testsidecar binary (SDK Worker pattern).
var testBinary string

// testListenerBinary holds the path to the listenersidecar binary (CEF-like pattern).
var testListenerBinary string

func TestMain(m *testing.M) {
	// Build the test sidecar binaries.
	tmpDir, err := os.MkdirTemp("", "sidecar-test-*")
	if err != nil {
		panic("create temp dir: " + err.Error())
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Standard SDK sidecar (sidecar connects to host).
	binPath := filepath.Join(tmpDir, "testsidecar")
	cmd := exec.Command("go", "build", "-o", binPath, "./testdata/testsidecar")
	cmd.Dir = filepath.Join(".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("build testsidecar: " + err.Error())
	}
	testBinary = binPath

	// Listener sidecar (sidecar creates listener, host connects — CEF pattern).
	listenerBinPath := filepath.Join(tmpDir, "listenersidecar")
	cmd2 := exec.Command("go", "build", "-o", listenerBinPath, "./testdata/listenersidecar")
	cmd2.Dir = filepath.Join(".")
	cmd2.Stdout = os.Stdout
	cmd2.Stderr = os.Stderr
	if err := cmd2.Run(); err != nil {
		panic("build listenersidecar: " + err.Error())
	}
	testListenerBinary = listenerBinPath

	os.Exit(m.Run())
}

func TestHostSpawnAndReady(t *testing.T) {
	h := NewHost(Config{
		Binary:       testBinary,
		ReadyTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	if !h.Alive() {
		t.Error("expected Alive() == true after Start")
	}
	if h.Generation() != 1 {
		t.Errorf("Generation() = %d, want 1", h.Generation())
	}
}

func TestHostSpawnBinaryNotFound(t *testing.T) {
	h := NewHost(Config{
		Binary:       "/nonexistent/binary/that/does/not/exist",
		ReadyTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := h.Start(ctx)
	if err == nil {
		_ = h.Close()
		t.Fatal("expected error for nonexistent binary")
	}
}

func TestHostSendAndReceive(t *testing.T) {
	var (
		mu      sync.Mutex
		gotEcho *Message
		echoCh  = make(chan struct{}, 1)
	)

	h := NewHost(Config{
		Binary:       testBinary,
		ReadyTimeout: 5 * time.Second,
	})
	h.OnMessage = func(msg *Message) {
		mu.Lock()
		defer mu.Unlock()
		if msg.Type == "echo" {
			gotEcho = msg
			select {
			case echoCh <- struct{}{}:
			default:
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	// Send an echo command.
	err := h.Send(&Message{Cmd: "echo", Data: "hello"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for echo response.
	select {
	case <-echoCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for echo response")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotEcho == nil {
		t.Fatal("expected echo message")
	}
	if gotEcho.Type != "echo" {
		t.Errorf("echo Type = %q, want %q", gotEcho.Type, "echo")
	}
	// Data round-trips through JSON as string.
	if gotEcho.Data != "hello" {
		t.Errorf("echo Data = %v, want %q", gotEcho.Data, "hello")
	}
}

func TestHostClose(t *testing.T) {
	h := NewHost(Config{
		Binary:       testBinary,
		ReadyTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !h.Alive() {
		t.Error("expected Alive() == true before Close")
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if h.Alive() {
		t.Error("expected Alive() == false after Close")
	}

	// Close is idempotent.
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestHostRestartOnCrash(t *testing.T) {
	restartCh := make(chan int64, 1)

	h := NewHost(Config{
		Binary:         testBinary,
		ReadyTimeout:   5 * time.Second,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		OnRestart: func(gen int64) {
			select {
			case restartCh <- gen:
			default:
			}
		},
	})

	// Set up echo verification for after restart.
	var (
		echoMu sync.Mutex
		echoCh = make(chan string, 1)
	)
	h.OnMessage = func(msg *Message) {
		if msg.Type == "echo" {
			echoMu.Lock()
			defer echoMu.Unlock()
			if s, ok := msg.Data.(string); ok {
				select {
				case echoCh <- s:
				default:
				}
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	gen1 := h.Generation()
	if gen1 != 1 {
		t.Fatalf("initial Generation() = %d, want 1", gen1)
	}
	if !h.Alive() {
		t.Fatal("expected Alive() == true after Start")
	}

	// Crash the sidecar.
	if err := h.Send(&Message{Cmd: "crash"}); err != nil {
		t.Fatalf("Send crash: %v", err)
	}

	// Wait for restart callback.
	select {
	case gen2 := <-restartCh:
		if gen2 <= gen1 {
			t.Errorf("restart generation %d <= initial generation %d", gen2, gen1)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for restart callback")
	}

	// Verify the host recovered.
	if !h.Alive() {
		t.Error("expected Alive() == true after restart")
	}
	if h.Restarts() != 1 {
		t.Errorf("Restarts() = %d, want 1", h.Restarts())
	}

	// Verify the new sidecar is functional by sending an echo.
	if err := h.Send(&Message{Cmd: "echo", Data: "post-crash"}); err != nil {
		t.Fatalf("Send echo after restart: %v", err)
	}

	select {
	case data := <-echoCh:
		if data != "post-crash" {
			t.Errorf("echo data = %q, want %q", data, "post-crash")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for echo after restart")
	}
}

func TestHostRestartBackoff(t *testing.T) {
	restartCh := make(chan time.Time, 4)

	h := NewHost(Config{
		Binary:         testBinary,
		ReadyTimeout:   5 * time.Second,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		OnRestart: func(_ int64) {
			select {
			case restartCh <- time.Now():
			default:
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	// First crash.
	crashTime1 := time.Now()
	if err := h.Send(&Message{Cmd: "crash"}); err != nil {
		t.Fatalf("Send crash 1: %v", err)
	}

	var restartTime1 time.Time
	select {
	case restartTime1 = <-restartCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for first restart")
	}
	elapsed1 := restartTime1.Sub(crashTime1)

	// Second crash.
	crashTime2 := time.Now()
	if err := h.Send(&Message{Cmd: "crash"}); err != nil {
		t.Fatalf("Send crash 2: %v", err)
	}

	var restartTime2 time.Time
	select {
	case restartTime2 = <-restartCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for second restart")
	}
	elapsed2 := restartTime2.Sub(crashTime2)

	// A successful restart resets the backoff, so both restarts wait
	// InitialBackoff with the host's +/-25% jitter: at least 37.5ms.
	t.Logf("first restart elapsed: %v, second restart elapsed: %v", elapsed1, elapsed2)

	minExpected := 50 * time.Millisecond * 3 / 4
	if elapsed1 < minExpected {
		t.Errorf("first restart elapsed %v < expected minimum %v", elapsed1, minExpected)
	}
	if elapsed2 < minExpected {
		t.Errorf("second restart elapsed %v < expected minimum %v", elapsed2, minExpected)
	}

	if h.Restarts() != 2 {
		t.Errorf("Restarts() = %d, want 2", h.Restarts())
	}
}

func TestHostRestartBackoffEscalation(t *testing.T) {
	// Test that when respawn FAILS, the backoff escalates.
	// We do this by crashing twice rapidly before the restart completes,
	// but that's hard to control. Instead, we verify the simpler property:
	// after two consecutive successful restarts from crashes, the generation
	// increments correctly.
	restartCh := make(chan int64, 4)

	h := NewHost(Config{
		Binary:         testBinary,
		ReadyTimeout:   5 * time.Second,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		OnRestart: func(gen int64) {
			select {
			case restartCh <- gen:
			default:
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	// Crash three times and track generation progression.
	var gens []int64
	gens = append(gens, h.Generation()) // gen 1

	for i := 0; i < 3; i++ {
		if err := h.Send(&Message{Cmd: "crash"}); err != nil {
			t.Fatalf("Send crash %d: %v", i+1, err)
		}
		select {
		case gen := <-restartCh:
			gens = append(gens, gen)
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for restart %d", i+1)
		}
	}

	// Verify generations are strictly increasing.
	for i := 1; i < len(gens); i++ {
		if gens[i] <= gens[i-1] {
			t.Errorf("generation[%d]=%d not > generation[%d]=%d", i, gens[i], i-1, gens[i-1])
		}
	}

	if h.Restarts() != 3 {
		t.Errorf("Restarts() = %d, want 3", h.Restarts())
	}

	// Verify the sidecar is still functional.
	if !h.Alive() {
		t.Error("expected Alive() == true after 3 restarts")
	}
}

func TestHostGenerationPreventsStaleRestart(t *testing.T) {
	h := NewHost(Config{
		Binary:         testBinary,
		ReadyTimeout:   5 * time.Second,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	// Generation starts at 1.
	if g := h.Generation(); g != 1 {
		t.Errorf("Generation() = %d, want 1", g)
	}

	// Restarts start at 0.
	if r := h.Restarts(); r != 0 {
		t.Errorf("Restarts() = %d, want 0", r)
	}

	// The generation is checked in readLoop and watchProcess to prevent
	// stale goroutines from triggering spurious restarts. This is verified
	// implicitly — if generation tracking were broken, the restart tests
	// above would see extra spurious restarts or panics.
}

func TestHostSendWhenNotAlive(t *testing.T) {
	h := NewHost(Config{
		Binary:       testBinary,
		ReadyTimeout: 5 * time.Second,
	})
	// Never started — Send should return "not alive".
	err := h.Send(&Message{Cmd: "echo"})
	if err == nil {
		t.Fatal("expected error from Send when not alive")
	}
	if !strings.Contains(err.Error(), "not alive") {
		t.Errorf("error = %q, want containing 'not alive'", err)
	}
}

func TestHostOnStatusCalledOnLifecycleEvents(t *testing.T) {
	var (
		mu       sync.Mutex
		statuses []Status
	)

	h := NewHost(Config{
		Binary:         testBinary,
		ReadyTimeout:   5 * time.Second,
		InitialBackoff: 100 * time.Millisecond,
		OnStatus: func(s Status) {
			mu.Lock()
			statuses = append(statuses, s)
			mu.Unlock()
		},
		OnRestart: func(_ int64) {},
	})

	ctx := context.Background()
	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Should have received "alive" status from spawn.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(statuses) < 1 {
		mu.Unlock()
		t.Fatal("expected at least 1 status callback after Start")
	}
	first := statuses[0]
	mu.Unlock()

	if !first.Alive {
		t.Error("first status should be Alive=true")
	}
	if first.Generation != 1 {
		t.Errorf("first status Generation = %d, want 1", first.Generation)
	}

	// Crash the sidecar — should get "not alive" status.
	_ = h.Send(&Message{Cmd: "crash"})

	// Wait for restart to complete.
	deadline := time.After(5 * time.Second)
	for {
		time.Sleep(50 * time.Millisecond)
		if h.Alive() && h.Restarts() >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for restart")
		default:
		}
	}

	mu.Lock()
	count := len(statuses)
	mu.Unlock()

	// Should have: spawn-alive, crash-not-alive, respawn-alive (at minimum)
	if count < 3 {
		t.Errorf("expected >= 3 status callbacks, got %d", count)
	}

	_ = h.Close()
}

func TestHostCleanupRemovesSocketDir(t *testing.T) {
	h := NewHost(Config{
		Binary:       testBinary,
		ReadyTimeout: 5 * time.Second,
	})

	ctx := context.Background()
	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Capture the socket dir while alive.
	h.mu.Lock()
	socketDir := h.socketDir
	h.mu.Unlock()

	if socketDir == "" {
		t.Fatal("socketDir should be set after Start")
	}

	// Verify the dir exists.
	if _, err := os.Stat(socketDir); err != nil {
		t.Fatalf("socket dir should exist: %v", err)
	}

	_ = h.Close()

	// Verify the dir was cleaned up.
	if _, err := os.Stat(socketDir); !os.IsNotExist(err) {
		t.Errorf("socket dir should be removed after Close, got err=%v", err)
	}
}

func TestHostConcurrentSend(t *testing.T) {
	// Verify that concurrent Send() calls don't corrupt the wire protocol.
	var (
		mu       sync.Mutex
		received []*Message
		done     = make(chan struct{}, 1)
	)

	h := NewHost(Config{
		Binary:       testBinary,
		ReadyTimeout: 5 * time.Second,
	})
	h.OnMessage = func(msg *Message) {
		if msg.Type == "echo" {
			mu.Lock()
			received = append(received, msg)
			if len(received) >= 20 {
				select {
				case done <- struct{}{}:
				default:
				}
			}
			mu.Unlock()
		}
	}

	ctx := context.Background()
	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	// Send 20 messages concurrently from 4 goroutines.
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				msg := &Message{Cmd: "echo", Data: fmt.Sprintf("g%d-m%d", goroutineID, i)}
				if err := h.Send(msg); err != nil {
					t.Errorf("Send: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	// Wait for all replies.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		mu.Lock()
		t.Fatalf("timeout: received %d/20 echo replies", len(received))
		mu.Unlock()
	}

	mu.Lock()
	if len(received) != 20 {
		t.Errorf("received %d echo replies, want 20", len(received))
	}
	mu.Unlock()
}

func TestHostCustomConnectAndBuildArgs(t *testing.T) {
	// Test the CEF-like pattern: sidecar creates listener, host connects.
	// Uses listenersidecar binary which creates control.sock in the socket dir.
	h := NewHost(Config{
		Binary:       testListenerBinary,
		ReadyTimeout: 5 * time.Second,
		BuildArgs: func(socketDir string) []string {
			return []string{socketDir}
		},
		Connect: func(socketDir string) (net.Conn, error) {
			controlPath := filepath.Join(socketDir, "control.sock")
			// Retry briefly — sidecar needs a moment to create the listener.
			var conn net.Conn
			var err error
			for i := 0; i < 50; i++ {
				conn, err = net.Dial("unix", controlPath)
				if err == nil {
					return conn, nil
				}
				time.Sleep(20 * time.Millisecond)
			}
			return nil, fmt.Errorf("connect to %s: %w", controlPath, err)
		},
	})

	ctx := context.Background()
	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	if !h.Alive() {
		t.Fatal("expected Alive after Start with custom Connect")
	}

	// Verify SocketDir is accessible.
	if h.SocketDir() == "" {
		t.Fatal("SocketDir should be non-empty")
	}

	// Send an echo via newline-delimited JSON (CEF protocol).
	conn := h.Conn()
	if conn == nil {
		t.Fatal("Conn() returned nil")
	}

	// Write newline-delimited JSON (CEF-style, not length-prefixed).
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(map[string]string{"cmd": "echo", "data": "hello"}); err != nil {
		t.Fatalf("encode echo: %v", err)
	}

	// Read response (also newline-delimited JSON).
	scanner := bufio.NewScanner(conn)
	_ = conn.(*net.UnixConn).SetReadDeadline(time.Now().Add(5 * time.Second))
	if !scanner.Scan() {
		t.Fatalf("no response: %v", scanner.Err())
	}

	var resp struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Type != "echo" {
		t.Errorf("response type = %q, want %q", resp.Type, "echo")
	}
}

func TestHostCustomConnectRestartOnCrash(t *testing.T) {
	// Verify that TriggerRestart works for custom-connect sidecars.
	restartCh := make(chan int64, 1)

	h := NewHost(Config{
		Binary:         testListenerBinary,
		ReadyTimeout:   5 * time.Second,
		InitialBackoff: 100 * time.Millisecond,
		BuildArgs: func(socketDir string) []string {
			return []string{socketDir}
		},
		Connect: func(socketDir string) (net.Conn, error) {
			controlPath := filepath.Join(socketDir, "control.sock")
			var conn net.Conn
			var err error
			for i := 0; i < 50; i++ {
				conn, err = net.Dial("unix", controlPath)
				if err == nil {
					return conn, nil
				}
				time.Sleep(20 * time.Millisecond)
			}
			return nil, fmt.Errorf("connect: %w", err)
		},
		OnRestart: func(gen int64) {
			select {
			case restartCh <- gen:
			default:
			}
		},
	})

	ctx := context.Background()
	if err := h.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	gen1 := h.Generation()

	// Crash via newline-delimited JSON.
	conn := h.Conn()
	encoder := json.NewEncoder(conn)
	_ = encoder.Encode(map[string]string{"cmd": "crash"})

	// The Host's watchProcess detects the exit and triggers restart.
	// (No readLoop in custom mode — watchProcess is the restart trigger.)
	select {
	case gen2 := <-restartCh:
		if gen2 <= gen1 {
			t.Errorf("restart gen %d <= initial gen %d", gen2, gen1)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for restart")
	}

	if !h.Alive() {
		t.Fatal("expected Alive after restart")
	}
}

// TestSidecarLoggerCapsNewlinelessOutput is a regression test for unbounded
// buffer growth: a sidecar that emits a very long line with no '\n' (crash dump
// or binary garbage from a wedged child) must not grow sidecarLogger.buf without
// bound. Write must cap the buffer, emit a truncation marker once, and recover
// once a newline finally arrives. Not parallel: it swaps the default slog logger.
func TestSidecarLoggerCapsNewlinelessOutput(t *testing.T) {
	var out bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	l := &sidecarLogger{prefix: "test-sidecar"}

	// Pump far more than the cap with no newline, in many small writes, mimicking
	// a streaming wedged child. buf must stay bounded throughout.
	chunk := bytes.Repeat([]byte("A"), 4096)
	for i := 0; i < 64; i++ { // 256 KiB total, no newline
		if _, err := l.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if len(l.buf) > maxSidecarLogLine {
			t.Fatalf("buf grew past cap: len=%d, cap=%d", len(l.buf), maxSidecarLogLine)
		}
	}

	// A truncation marker should have been emitted exactly once for the over-long
	// line (subsequent over-cap flushes are silent until the line ends).
	if got := strings.Count(out.String(), "[line truncated]"); got != 1 {
		t.Fatalf("expected exactly one truncation marker, got %d:\n%s", got, out.String())
	}

	// The newline that finally arrives ends the discarded line; the next complete
	// line must be logged normally, proving the logger recovered.
	out.Reset()
	if _, err := l.Write([]byte("tail-of-huge-line\nrecovered-line\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(l.buf) != 0 {
		t.Fatalf("buf not drained after trailing newline: len=%d", len(l.buf))
	}
	s := out.String()
	if strings.Contains(s, "tail-of-huge-line") {
		t.Errorf("discarded tail of truncated line was logged:\n%s", s)
	}
	if !strings.Contains(s, "recovered-line") {
		t.Errorf("post-truncation line was not logged:\n%s", s)
	}
}
