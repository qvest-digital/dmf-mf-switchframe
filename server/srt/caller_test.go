package srt

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

func TestCallerPullAndStop(t *testing.T) {
	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Start a real SRT listener to accept the caller's connection.
	srvCfg := srtgo.DefaultConfig()
	srvCfg.Latency = 120 * time.Millisecond
	ln, err := srtgo.Listen(addr, srvCfg)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	// Accept connections in background.
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Hold the connection open until test cleanup.
		<-acceptDone
		conn.Close()
	}()

	var mu sync.Mutex
	var gotConfig SourceConfig
	var gotConn *srtgo.Conn
	sourceCalled := make(chan struct{}, 1)
	sourceDone := make(chan struct{})

	caller := NewCaller(CallerConfig{
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) <-chan struct{} {
			mu.Lock()
			gotConfig = cfg
			gotConn = conn
			mu.Unlock()
			sourceCalled <- struct{}{}
			return sourceDone
		},
		Log: log,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := SourceConfig{
		Key:       "srt:remote1",
		Mode:      ModeCaller,
		Address:   addr,
		StreamID:  "live/remote1",
		LatencyMs: 120,
	}

	err = caller.Pull(ctx, config)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Wait for OnSource callback.
	select {
	case <-sourceCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for OnSource callback")
	}

	mu.Lock()
	if gotConn == nil {
		t.Error("expected non-nil connection in OnSource")
	}
	if gotConfig.Key != "srt:remote1" {
		t.Errorf("expected key %q, got %q", "srt:remote1", gotConfig.Key)
	}
	if gotConfig.Mode != ModeCaller {
		t.Errorf("expected mode %q, got %q", ModeCaller, gotConfig.Mode)
	}
	if gotConfig.Address != addr {
		t.Errorf("expected address %q, got %q", addr, gotConfig.Address)
	}
	mu.Unlock()

	// Verify active pulls.
	pulls := caller.ActivePulls()
	if len(pulls) != 1 {
		t.Fatalf("expected 1 active pull, got %d", len(pulls))
	}
	if pulls[0].Key != "srt:remote1" {
		t.Errorf("expected active pull key %q, got %q", "srt:remote1", pulls[0].Key)
	}

	// Stop the pull.
	caller.Stop("srt:remote1")

	// Verify no active pulls.
	pulls = caller.ActivePulls()
	if len(pulls) != 0 {
		t.Errorf("expected 0 active pulls after stop, got %d", len(pulls))
	}

	cancel()
}

func TestCallerActivePullsEmpty(t *testing.T) {
	store := tempStore(t)

	caller := NewCaller(CallerConfig{
		Store: store,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) <-chan struct{} {
			return make(chan struct{})
		},
	})

	pulls := caller.ActivePulls()
	if len(pulls) != 0 {
		t.Errorf("expected 0 active pulls initially, got %d", len(pulls))
	}
}

func TestCallerPersistence(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "srt_sources.json")
	store1, err := NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	addr := findFreePort(t)

	// Start a real SRT listener.
	srvCfg := srtgo.DefaultConfig()
	srvCfg.Latency = 120 * time.Millisecond
	ln, err := srtgo.Listen(addr, srvCfg)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	// Accept connections in background (accept multiple for restore).
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold connections open briefly then close.
			go func() {
				time.Sleep(10 * time.Second)
				conn.Close()
			}()
		}
	}()

	sourceCalled1 := make(chan struct{}, 1)
	sourceDone1 := make(chan struct{})

	caller1 := NewCaller(CallerConfig{
		Store:          store1,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) <-chan struct{} {
			sourceCalled1 <- struct{}{}
			return sourceDone1
		},
		Log: log,
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	config := SourceConfig{
		Key:       "srt:remote1",
		Mode:      ModeCaller,
		Address:   addr,
		StreamID:  "live/remote1",
		LatencyMs: 120,
		Label:     "Remote Camera",
		Position:  3,
	}

	err = caller1.Pull(ctx1, config)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Wait for connection.
	select {
	case <-sourceCalled1:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for first caller OnSource")
	}

	// Verify persisted to store.
	saved, ok := store1.Get("srt:remote1")
	if !ok {
		t.Fatal("expected config saved to store")
	}
	if saved.Address != addr {
		t.Errorf("expected saved address %q, got %q", addr, saved.Address)
	}
	if saved.Label != "Remote Camera" {
		t.Errorf("expected saved label %q, got %q", "Remote Camera", saved.Label)
	}

	// Stop the first caller.
	cancel1()

	// Create a new store from the same file to simulate restart.
	store2, err := NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}

	sourceCalled2 := make(chan struct{}, 1)
	sourceDone2 := make(chan struct{})
	defer close(sourceDone2)

	caller2 := NewCaller(CallerConfig{
		Store:          store2,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) <-chan struct{} {
			sourceCalled2 <- struct{}{}
			return sourceDone2
		},
		Log: log,
	})

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// Restore from store — should re-establish the pull.
	caller2.RestoreFromStore(ctx2)

	// Verify OnSource called for restored pull.
	select {
	case <-sourceCalled2:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for restored caller OnSource")
	}

	pulls := caller2.ActivePulls()
	if len(pulls) != 1 {
		t.Fatalf("expected 1 active pull after restore, got %d", len(pulls))
	}
	if pulls[0].Key != "srt:remote1" {
		t.Errorf("expected restored pull key %q, got %q", "srt:remote1", pulls[0].Key)
	}
	if pulls[0].Label != "Remote Camera" {
		t.Errorf("expected restored label %q, got %q", "Remote Camera", pulls[0].Label)
	}

	cancel2()
}

func TestCallerStopDeletesFromStore(t *testing.T) {
	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Start a real SRT listener.
	srvCfg := srtgo.DefaultConfig()
	srvCfg.Latency = 120 * time.Millisecond
	ln, err := srtgo.Listen(addr, srvCfg)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				time.Sleep(10 * time.Second)
				conn.Close()
			}()
		}
	}()

	sourceCalled := make(chan struct{}, 1)
	sourceDone := make(chan struct{})

	caller := NewCaller(CallerConfig{
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) <-chan struct{} {
			sourceCalled <- struct{}{}
			return sourceDone
		},
		Log: log,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := SourceConfig{
		Key:       "srt:remote1",
		Mode:      ModeCaller,
		Address:   addr,
		StreamID:  "live/remote1",
		LatencyMs: 120,
	}

	err = caller.Pull(ctx, config)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	select {
	case <-sourceCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for OnSource callback")
	}

	// Verify saved to store.
	_, ok := store.Get("srt:remote1")
	if !ok {
		t.Fatal("expected config in store after Pull")
	}

	// Stop the pull.
	caller.Stop("srt:remote1")

	// Verify removed from store.
	_, ok = store.Get("srt:remote1")
	if ok {
		t.Error("expected config removed from store after Stop")
	}

	cancel()
}

func TestCallerReconnectsOnDisconnect(t *testing.T) {
	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Start a real SRT listener.
	srvCfg := srtgo.DefaultConfig()
	srvCfg.Latency = 120 * time.Millisecond
	ln, err := srtgo.Listen(addr, srvCfg)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	// Accept connections — close the first one to trigger reconnect.
	acceptCount := 0
	var acceptMu sync.Mutex
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			acceptMu.Lock()
			acceptCount++
			n := acceptCount
			acceptMu.Unlock()
			if n == 1 {
				// Close first connection quickly to trigger reconnect.
				conn.Close()
			} else {
				// Keep subsequent connections open.
				go func() {
					time.Sleep(30 * time.Second)
					conn.Close()
				}()
			}
		}
	}()

	var mu sync.Mutex
	callCount := 0
	secondCall := make(chan struct{}, 1)

	caller := NewCaller(CallerConfig{
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) <-chan struct{} {
			done := make(chan struct{})
			mu.Lock()
			callCount++
			n := callCount
			mu.Unlock()
			if n == 1 {
				// First call — source done immediately (simulating disconnect).
				close(done)
			} else {
				// Second call — keep alive.
				secondCall <- struct{}{}
			}
			return done
		},
		Log: log,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := SourceConfig{
		Key:       "srt:remote1",
		Mode:      ModeCaller,
		Address:   addr,
		StreamID:  "live/remote1",
		LatencyMs: 120,
	}

	err = caller.Pull(ctx, config)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Wait for the second OnSource call (proves reconnection happened).
	select {
	case <-secondCall:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for reconnection OnSource callback")
	}

	mu.Lock()
	if callCount < 2 {
		t.Errorf("expected OnSource called at least 2 times, got %d", callCount)
	}
	mu.Unlock()

	cancel()
}

func TestCallerValidation(t *testing.T) {
	store := tempStore(t)
	caller := NewCaller(CallerConfig{
		Store: store,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) <-chan struct{} {
			return make(chan struct{})
		},
	})

	ctx := context.Background()

	// Missing key.
	err := caller.Pull(ctx, SourceConfig{
		Mode:     ModeCaller,
		Address:  "127.0.0.1:5000",
		StreamID: "live/test",
	})
	if err == nil {
		t.Error("expected error for missing key")
	}

	// Missing address.
	err = caller.Pull(ctx, SourceConfig{
		Key:      "srt:test",
		Mode:     ModeCaller,
		StreamID: "live/test",
	})
	if err == nil {
		t.Error("expected error for missing address")
	}

	// Wrong mode — should be forced to caller.
	// Pull sets mode to ModeCaller before validation, so listener mode config should work.
	// (But address is still required.)
}

func TestCallerReplacesExistingPull(t *testing.T) {
	addr1 := findFreePort(t)
	addr2 := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Start two SRT listeners.
	for _, a := range []string{addr1, addr2} {
		srvCfg := srtgo.DefaultConfig()
		srvCfg.Latency = 120 * time.Millisecond
		ln, err := srtgo.Listen(a, srvCfg)
		if err != nil {
			t.Fatalf("Listen %s: %v", a, err)
		}
		defer ln.Close()
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					time.Sleep(30 * time.Second)
					conn.Close()
				}()
			}
		}()
	}

	sourceCalled := make(chan string, 2)

	caller := NewCaller(CallerConfig{
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) <-chan struct{} {
			sourceCalled <- cfg.Address
			return make(chan struct{}) // never done
		},
		Log: log,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// First pull.
	err := caller.Pull(ctx, SourceConfig{
		Key:       "srt:remote1",
		Mode:      ModeCaller,
		Address:   addr1,
		StreamID:  "live/remote1",
		LatencyMs: 120,
	})
	if err != nil {
		t.Fatalf("Pull 1: %v", err)
	}

	select {
	case a := <-sourceCalled:
		if a != addr1 {
			t.Errorf("first pull: expected addr %q, got %q", addr1, a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for first pull OnSource")
	}

	// Second pull with same key but different address — should replace.
	err = caller.Pull(ctx, SourceConfig{
		Key:       "srt:remote1",
		Mode:      ModeCaller,
		Address:   addr2,
		StreamID:  "live/remote1",
		LatencyMs: 120,
	})
	if err != nil {
		t.Fatalf("Pull 2: %v", err)
	}

	select {
	case a := <-sourceCalled:
		if a != addr2 {
			t.Errorf("second pull: expected addr %q, got %q", addr2, a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for second pull OnSource")
	}

	// Only one active pull.
	pulls := caller.ActivePulls()
	if len(pulls) != 1 {
		t.Fatalf("expected 1 active pull after replace, got %d", len(pulls))
	}

	cancel()
}
