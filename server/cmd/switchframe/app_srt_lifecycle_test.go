package main

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/srt"
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
