//go:build cgo && !noffmpeg

package srt

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

// pushTestData dials an SRT listener and sends a .ts test clip in 1316-byte chunks.
// It runs in a background goroutine and stops when ctx is cancelled or data is exhausted.
func pushTestData(t *testing.T, addr, streamID string, ctx context.Context) {
	t.Helper()
	cfg := srtgo.DefaultConfig()
	cfg.StreamID = streamID
	conn, err := srtgo.Dial(addr, cfg)
	if err != nil {
		t.Fatal("dial failed:", err)
	}

	go func() {
		defer conn.Close()
		data, err := os.ReadFile("../../test/clips/tears_of_steel.ts")
		if err != nil {
			return
		}
		// Send data in 1316-byte chunks (7 TS packets).
		for i := 0; i < len(data); i += 1316 {
			select {
			case <-ctx.Done():
				return
			default:
			}
			end := i + 1316
			if end > len(data) {
				end = len(data)
			}
			_, err := conn.Write(data[i:end])
			if err != nil {
				return
			}
			time.Sleep(time.Millisecond) // rough pacing to avoid overwhelming
		}
	}()
}

func TestIntegrationListenerPushE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var videoFrames atomic.Int32
	var audioFrames atomic.Int32
	var lastWidth, lastHeight atomic.Int32

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	l, err := NewListener(ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) {
			stats := NewConnStats(cfg.Mode, cfg.StreamID, cfg.LatencyMs)
			src := NewSource(cfg, conn, stats, log)
			src.OnRawVideo = func(key string, yuv []byte, width, height int, pts int64) {
				lastWidth.Store(int32(width))
				lastHeight.Store(int32(height))
				videoFrames.Add(1)
			}
			src.OnRawAudio = func(key string, pcm []float32, pts int64, sampleRate, channels int) {
				audioFrames.Add(1)
			}
			if err := src.Start(ctx); err != nil {
				t.Errorf("source start failed: %v", err)
			}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- l.Run(ctx)
	}()

	// Give the listener time to start.
	time.Sleep(100 * time.Millisecond)

	// Push a real .ts file via SRT.
	pushTestData(t, addr, "live/camera1", ctx)

	// Wait for at least 5 video frames to be decoded.
	deadline := time.After(14 * time.Second)
	for {
		if videoFrames.Load() >= 5 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: got %d video frames (need >=5), %d audio frames",
				videoFrames.Load(), audioFrames.Load())
		case <-time.After(100 * time.Millisecond):
			// poll
		}
	}

	// Verify video dimensions are valid (non-zero, even).
	w := lastWidth.Load()
	h := lastHeight.Load()
	if w <= 0 || h <= 0 {
		t.Errorf("invalid video dimensions: %dx%d", w, h)
	}
	if w%2 != 0 || h%2 != 0 {
		t.Errorf("video dimensions not even: %dx%d", w, h)
	}

	// Verify we got some audio frames too.
	if audioFrames.Load() == 0 {
		t.Error("expected at least some audio frames, got 0")
	}

	t.Logf("decoded %d video frames (%dx%d) and %d audio frames",
		videoFrames.Load(), w, h, audioFrames.Load())

	// Cleanup.
	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for listener Run to return")
	}
}

func TestIntegrationMultiSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var mu sync.Mutex
	seenKeys := make(map[string]bool)
	allSeen := make(chan struct{}, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	l, err := NewListener(ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) {
			// Close the connection; we only care about OnSource being called.
			conn.Close()
			mu.Lock()
			seenKeys[cfg.Key] = true
			count := len(seenKeys)
			mu.Unlock()
			if count >= 3 {
				select {
				case allSeen <- struct{}{}:
				default:
				}
			}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- l.Run(ctx)
	}()

	// Give the listener time to start.
	time.Sleep(100 * time.Millisecond)

	// Connect 3 clients with different streamIDs.
	streamIDs := []string{"live/cam1", "live/cam2", "live/cam3"}
	for _, sid := range streamIDs {
		cfg := srtgo.DefaultConfig()
		cfg.StreamID = sid
		conn, err := srtgo.Dial(addr, cfg)
		if err != nil {
			t.Fatalf("Dial %s failed: %v", sid, err)
		}
		defer conn.Close()
		// Small delay between connections to avoid thundering herd.
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for all 3 sources to be accepted.
	select {
	case <-allSeen:
	case <-time.After(10 * time.Second):
		mu.Lock()
		t.Fatalf("timeout: only saw %d sources: %v", len(seenKeys), seenKeys)
		mu.Unlock()
	}

	// Verify we got 3 different keys.
	mu.Lock()
	defer mu.Unlock()

	expectedKeys := map[string]bool{
		"srt:cam1": true,
		"srt:cam2": true,
		"srt:cam3": true,
	}
	for key := range expectedKeys {
		if !seenKeys[key] {
			t.Errorf("missing expected key %q in seen keys %v", key, seenKeys)
		}
	}
	if len(seenKeys) != 3 {
		t.Errorf("expected 3 unique keys, got %d: %v", len(seenKeys), seenKeys)
	}

	// Cleanup.
	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for listener Run to return")
	}
}

func TestIntegrationListenerReconnectRestoresConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	addr := findFreePort(t)
	store := tempStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Pre-save a config with custom label and position.
	err := store.Save(SourceConfig{
		Key:       "srt:mycam",
		Mode:      ModeListener,
		StreamID:  "live/mycam",
		Label:     "Studio A",
		Position:  3,
		LatencyMs: 200,
		DelayMs:   100,
	})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var gotConfig SourceConfig
	called := make(chan struct{}, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	l, err := NewListener(ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(cfg SourceConfig, conn *srtgo.Conn) {
			conn.Close()
			mu.Lock()
			gotConfig = cfg
			mu.Unlock()
			select {
			case called <- struct{}{}:
			default:
			}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- l.Run(ctx)
	}()

	// Give the listener time to start.
	time.Sleep(100 * time.Millisecond)

	// Connect with the matching streamID.
	cfg := srtgo.DefaultConfig()
	cfg.StreamID = "live/mycam"
	conn, err := srtgo.Dial(addr, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	// Wait for OnSource callback.
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for OnSource callback")
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify the restored config has the pre-saved label and position.
	if gotConfig.Key != "srt:mycam" {
		t.Errorf("expected key %q, got %q", "srt:mycam", gotConfig.Key)
	}
	if gotConfig.Label != "Studio A" {
		t.Errorf("expected restored label %q, got %q", "Studio A", gotConfig.Label)
	}
	if gotConfig.Position != 3 {
		t.Errorf("expected restored position %d, got %d", 3, gotConfig.Position)
	}
	if gotConfig.DelayMs != 100 {
		t.Errorf("expected restored delayMs %d, got %d", 100, gotConfig.DelayMs)
	}
	if gotConfig.LatencyMs != 200 {
		t.Errorf("expected restored latencyMs %d, got %d", 200, gotConfig.LatencyMs)
	}
	if gotConfig.Mode != ModeListener {
		t.Errorf("expected mode %q, got %q", ModeListener, gotConfig.Mode)
	}

	// Cleanup.
	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for listener Run to return")
	}
}
