//go:build !windows

package asr

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var echoSidecarBin string

func TestMain(m *testing.M) {
	// Build the echo-sidecar test helper binary.
	tmpDir, err := os.MkdirTemp("", "asr-sidecar-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "echo-sidecar")
	cmd := exec.Command("go", "build", "-o", binPath, "./testdata/echo-sidecar")
	cmd.Dir = filepath.Join(".")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build echo-sidecar: %v\n", err)
		os.Exit(1)
	}
	echoSidecarBin = binPath

	os.Exit(m.Run())
}

func TestSidecarBackendName(t *testing.T) {
	sb := &SidecarBackend{}
	if got := sb.Name(); got != "sidecar" {
		t.Errorf("Name() = %q, want %q", got, "sidecar")
	}
}

func TestSidecarBackendStartStop(t *testing.T) {
	shmName := fmt.Sprintf("/asr-test-%d", os.Getpid())

	sb, err := NewSidecarBackend(SidecarConfig{
		BinPath:  echoSidecarBin,
		ShmName:  shmName,
		ModelDir: t.TempDir(),
		Device:   0,
	})
	if err != nil {
		t.Fatalf("NewSidecarBackend: %v", err)
	}

	var mu sync.Mutex
	var received []TranscriptWord
	var receivedFinal bool

	err = sb.Start(func(words []TranscriptWord, isFinal bool) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, words...)
		if isFinal {
			receivedFinal = true
		}
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Write some audio data — the echo sidecar will respond with a transcript.
	pcm := make([]float32, 480) // 10ms at 48kHz
	for i := range pcm {
		pcm[i] = 0.5
	}
	sb.IngestAudio(pcm, 0, 48000, 1)

	// Wait for transcript to arrive.
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		got := len(received)
		mu.Unlock()
		if got > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for transcript")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if !receivedFinal {
		t.Error("expected isFinal=true")
	}
	if len(received) == 0 {
		t.Fatal("no words received")
	}
	if received[0].Text != "echo" {
		t.Errorf("word text = %q, want %q", received[0].Text, "echo")
	}
	if received[0].Confidence < 0.98 {
		t.Errorf("confidence = %f, want >= 0.98", received[0].Confidence)
	}

	// Verify ModelName was set from status message.
	// Give a moment for the status message to be processed.
	time.Sleep(100 * time.Millisecond)
	if name := sb.ModelName(); name != "echo-test-model" {
		t.Errorf("ModelName() = %q, want %q", name, "echo-test-model")
	}

	sb.Close()

	// Verify alive is false after close.
	if sb.alive.Load() {
		t.Error("alive should be false after Close")
	}
}

func TestSidecarBackendReset(t *testing.T) {
	shmName := fmt.Sprintf("/asr-reset-%d", os.Getpid())

	sb, err := NewSidecarBackend(SidecarConfig{
		BinPath:  echoSidecarBin,
		ShmName:  shmName,
		ModelDir: t.TempDir(),
		Device:   0,
	})
	if err != nil {
		t.Fatalf("NewSidecarBackend: %v", err)
	}

	err = sb.Start(func(words []TranscriptWord, isFinal bool) {
		// No-op callback.
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Reset should not crash.
	sb.Reset()

	// Brief pause to let the command propagate.
	time.Sleep(100 * time.Millisecond)

	// Should still be alive after reset.
	if !sb.alive.Load() {
		t.Error("sidecar should still be alive after Reset")
	}

	sb.Close()
}
