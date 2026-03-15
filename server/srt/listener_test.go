package srt

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

func findFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.LocalAddr().String()
	l.Close()
	return addr
}

func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "srt_sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestListenerAcceptsConnection(t *testing.T) {
	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var mu sync.Mutex
	var gotConfig SourceConfig
	var gotConn *srtgo.Conn
	called := make(chan struct{}, 1)

	l, err := NewListener(ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) {
			mu.Lock()
			gotConfig = cfg
			gotConn = conn
			mu.Unlock()
			called <- struct{}{}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- l.Run(ctx)
	}()

	// Give the listener time to start
	time.Sleep(50 * time.Millisecond)

	// Dial with a stream ID
	cfg := srtgo.DefaultConfig()
	cfg.StreamID = "live/camera1"
	conn, err := srtgo.Dial(addr, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	// Wait for OnSource callback
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for OnSource callback")
	}

	mu.Lock()
	defer mu.Unlock()

	if gotConn == nil {
		t.Fatal("expected non-nil connection in OnSource")
	}
	if gotConfig.Key != "srt:camera1" {
		t.Errorf("expected key %q, got %q", "srt:camera1", gotConfig.Key)
	}
	if gotConfig.Mode != ModeListener {
		t.Errorf("expected mode %q, got %q", ModeListener, gotConfig.Mode)
	}
	if gotConfig.StreamID != "live/camera1" {
		t.Errorf("expected streamID %q, got %q", "live/camera1", gotConfig.StreamID)
	}

	// Cleanup
	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}

func TestListenerExtractsStreamKey(t *testing.T) {
	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var mu sync.Mutex
	var gotConfig SourceConfig
	called := make(chan struct{}, 1)

	l, err := NewListener(ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) {
			mu.Lock()
			gotConfig = cfg
			mu.Unlock()
			called <- struct{}{}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- l.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	cfg := srtgo.DefaultConfig()
	cfg.StreamID = "live/mycamera"
	conn, err := srtgo.Dial(addr, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for OnSource callback")
	}

	mu.Lock()
	defer mu.Unlock()

	if gotConfig.Key != "srt:mycamera" {
		t.Errorf("expected key %q, got %q", "srt:mycamera", gotConfig.Key)
	}
	if gotConfig.Label != "mycamera" {
		t.Errorf("expected label %q, got %q", "mycamera", gotConfig.Label)
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}

func TestListenerRestoresConfig(t *testing.T) {
	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Pre-save a config with custom label and position
	err := store.Save(SourceConfig{
		Key:       "srt:mycam",
		Mode:      ModeListener,
		StreamID:  "live/mycam",
		Label:     "Custom Camera",
		Position:  5,
		LatencyMs: 200,
		DelayMs:   50,
	})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var gotConfig SourceConfig
	called := make(chan struct{}, 1)

	l, err := NewListener(ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) {
			mu.Lock()
			gotConfig = cfg
			mu.Unlock()
			called <- struct{}{}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- l.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	cfg := srtgo.DefaultConfig()
	cfg.StreamID = "live/mycam"
	conn, err := srtgo.Dial(addr, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for OnSource callback")
	}

	mu.Lock()
	defer mu.Unlock()

	if gotConfig.Key != "srt:mycam" {
		t.Errorf("expected key %q, got %q", "srt:mycam", gotConfig.Key)
	}
	if gotConfig.Label != "Custom Camera" {
		t.Errorf("expected restored label %q, got %q", "Custom Camera", gotConfig.Label)
	}
	if gotConfig.Position != 5 {
		t.Errorf("expected restored position %d, got %d", 5, gotConfig.Position)
	}
	if gotConfig.DelayMs != 50 {
		t.Errorf("expected restored delayMs %d, got %d", 50, gotConfig.DelayMs)
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}

func TestListenerShutdown(t *testing.T) {
	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	l, err := NewListener(ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) {
			// no-op
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan error, 1)
	go func() {
		runDone <- l.Run(ctx)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel the context
	cancel()

	// Run should return cleanly
	select {
	case err := <-runDone:
		if err != nil {
			t.Errorf("expected nil error on shutdown, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to return after cancel")
	}
}
