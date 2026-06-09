package main

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/switcher"
)

// nopConn is a stand-in for the SRT connection (srt.NewSource accepts any
// io.ReadCloser).
type nopConn struct{}

func (nopConn) Read(p []byte) (int, error) { return 0, io.EOF }
func (nopConn) Close() error               { return nil }

// TestFailSRTSourceStart_RunsOnStoppedCleanup covers finding #46: when
// src.Start() fails (e.g. FFmpeg decoder creation error), no decode goroutine
// is launched, so the decode-goroutine teardown that normally invokes
// OnStopped never runs and src.Stop() does not call it either. Everything
// wireSRTSource set up BEFORE Start — fan-out goroutines parked on job
// channels, the preview encoder, GPU source-manager and replay registrations,
// the srtSources map entry, the listener slot — is reclaimed only by
// OnStopped. The Start-error paths must therefore invoke OnStopped
// explicitly (exactly once), or all of it leaks on every failed connection
// attempt.
func TestFailSRTSourceStart_RunsOnStoppedCleanup(t *testing.T) {
	// Mirror the wireSRTSource fan-out: goroutines that exit only when
	// OnStopped closes their job channels.
	pipelineCh := make(chan srtVideoJob, 4)
	relayVideoCh := make(chan srtVideoJob, 4)
	audioCh := make(chan srtAudioJob, 8)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for range pipelineCh {
		}
	}()
	go func() {
		defer wg.Done()
		for range relayVideoCh {
		}
	}()
	go func() {
		defer wg.Done()
		for range audioCh {
		}
	}()

	src := srt.NewSource(srt.SourceConfig{Key: "srt:start-fail", Mode: srt.ModeListener}, nopConn{}, nil, nil)

	var stopped atomic.Int32
	src.OnStopped = func(key string) {
		if key != "srt:start-fail" {
			t.Errorf("OnStopped key = %q, want %q", key, "srt:start-fail")
		}
		stopped.Add(1)
		close(pipelineCh)
		close(relayVideoCh)
		close(audioCh)
	}

	// Simulate the Start() error path (Start fails at decoder creation,
	// before launching any goroutine).
	failSRTSourceStart(src, "srt:start-fail")

	if got := stopped.Load(); got != 1 {
		t.Fatalf("OnStopped called %d times after Start failure, want 1 — "+
			"fan-out goroutines, channels, and GPU/replay registrations leak", got)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("fan-out goroutines did not exit after Start-failure cleanup")
	}
}

// TestFailSRTSourceStart_NilOnStopped verifies the helper tolerates a source
// with no OnStopped callback (wireSRTSource always sets one, but the helper
// must not panic if wiring failed earlier).
func TestFailSRTSourceStart_NilOnStopped(t *testing.T) {
	src := srt.NewSource(srt.SourceConfig{Key: "srt:nil-onstopped"}, nopConn{}, nil, nil)
	failSRTSourceStart(src, "srt:nil-onstopped") // must not panic
}

// TestNewSRTPipelineFrame_ManagedRefcount pins the frame-lifetime contract for
// SRT pipeline ingest: frames handed to IngestRawVideo are pool-backed AND
// managed (refs initialized to 1). The frame-sync ring retains the frame (and
// serves it as the freeze-repeat lastRawVideo) while sync-loop consumers take
// and release borrowed references. On an unmanaged frame Ref() is a no-op
// while ReleaseYUV frees immediately, so a consumer-side release would return
// the pool buffer while the ring still aliased it (use-after-release) and the
// ring's own release would double-free it.
func TestNewSRTPipelineFrame_ManagedRefcount(t *testing.T) {
	pool := switcher.NewFramePool(2, 64, 48, switcher.ColorYUV420_8bit)
	yuv := pool.Acquire()

	pf := newSRTPipelineFrame(srtVideoJob{yuv: yuv, w: 64, h: 48, pts: 9000}, pool)

	if got := pf.Refs(); got != 1 {
		t.Fatalf("Refs() = %d, want 1 (frame must be managed so Ref/ReleaseYUV are symmetric)", got)
	}

	// Borrow + release (what the sync loop does) must NOT free the buffer.
	pf.Ref()
	cp := *pf
	cp.ReleaseYUV()
	if pf.Data == nil {
		t.Fatal("borrowed-copy release freed the buffer while the ring still owned it")
	}
	if got := pf.Refs(); got != 1 {
		t.Fatalf("Refs() after borrow/release = %d, want 1", got)
	}

	// The owner's release drops the last reference and frees the buffer.
	pf.ReleaseYUV()
	if pf.Data != nil {
		t.Fatal("owner release did not free the buffer")
	}

	// nil pool: still managed, release is a safe no-op on the pool.
	heap := make([]byte, 64*48*3/2)
	pf2 := newSRTPipelineFrame(srtVideoJob{yuv: heap, w: 64, h: 48, pts: 0}, nil)
	if got := pf2.Refs(); got != 1 {
		t.Fatalf("nil-pool frame Refs() = %d, want 1", got)
	}
	pf2.ReleaseYUV()
}
