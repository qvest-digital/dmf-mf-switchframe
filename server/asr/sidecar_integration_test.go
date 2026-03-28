//go:build !windows

package asr

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSidecarIntegration_MultipleChunks(t *testing.T) {
	if echoSidecarBin == "" {
		t.Skip("echo-sidecar not built")
	}

	sb, err := NewSidecarBackend(SidecarConfig{
		BinPath:  echoSidecarBin,
		ShmName:  integrationShmName(t),
		ModelDir: t.TempDir(),
		Device:   0,
	})
	if err != nil {
		t.Fatalf("NewSidecarBackend: %v", err)
	}
	defer sb.Close()

	var mu sync.Mutex
	var allWords []TranscriptWord

	err = sb.Start(func(words []TranscriptWord, isFinal bool) {
		mu.Lock()
		allWords = append(allWords, words...)
		mu.Unlock()
	})
	if err != nil {
		if strings.Contains(err.Error(), "shared memory not available") {
			t.Skip("shared memory not available (no cgo)")
		}
		t.Fatalf("Start: %v", err)
	}

	// Feed multiple audio chunks with pauses between them,
	// simulating real mixer output.
	sampleRate := 48000
	channels := 2
	chunkSize := 1024 * channels // ~10ms of stereo audio

	for i := 0; i < 10; i++ {
		chunk := make([]float32, chunkSize)
		for j := 0; j < len(chunk)/2; j++ {
			val := float32(0.3 * math.Sin(2*math.Pi*440*float64(j)/float64(sampleRate)))
			chunk[j*2] = val
			chunk[j*2+1] = val
		}
		sb.IngestAudio(chunk, int64(i*chunkSize/channels)*90000/int64(sampleRate), sampleRate, channels)
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for transcripts.
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(allWords)
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for transcripts from sidecar")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	mu.Lock()
	t.Logf("received %d transcript words", len(allWords))
	for _, w := range allWords {
		t.Logf("  word=%q confidence=%.2f gap=%d", w.Text, w.Confidence, w.GapBefore)
	}
	mu.Unlock()
}

func TestSidecarIntegration_ResetClearsState(t *testing.T) {
	if echoSidecarBin == "" {
		t.Skip("echo-sidecar not built")
	}

	sb, err := NewSidecarBackend(SidecarConfig{
		BinPath:  echoSidecarBin,
		ShmName:  integrationShmName(t),
		ModelDir: t.TempDir(),
		Device:   0,
	})
	if err != nil {
		t.Fatalf("NewSidecarBackend: %v", err)
	}
	defer sb.Close()

	transcripts := make(chan []TranscriptWord, 10)
	err = sb.Start(func(words []TranscriptWord, isFinal bool) {
		transcripts <- words
	})
	if err != nil {
		if strings.Contains(err.Error(), "shared memory not available") {
			t.Skip("shared memory not available (no cgo)")
		}
		t.Fatalf("Start: %v", err)
	}

	// Feed audio, get transcript.
	chunk := make([]float32, 2048)
	for i := range chunk {
		chunk[i] = 0.5
	}
	sb.IngestAudio(chunk, 0, 48000, 2)

	select {
	case <-transcripts:
		// Got first transcript.
	case <-time.After(5 * time.Second):
		t.Fatal("timeout on first transcript")
	}

	// Reset and feed more audio.
	sb.Reset()
	time.Sleep(100 * time.Millisecond)

	sb.IngestAudio(chunk, 90000, 48000, 2)

	select {
	case <-transcripts:
		// Got transcript after reset — sidecar is still alive.
	case <-time.After(5 * time.Second):
		t.Fatal("timeout on transcript after reset")
	}
}

func TestSidecarIntegration_StopAndClose(t *testing.T) {
	if echoSidecarBin == "" {
		t.Skip("echo-sidecar not built")
	}

	sb, err := NewSidecarBackend(SidecarConfig{
		BinPath:  echoSidecarBin,
		ShmName:  integrationShmName(t),
		ModelDir: t.TempDir(),
		Device:   0,
	})
	if err != nil {
		t.Fatalf("NewSidecarBackend: %v", err)
	}
	defer sb.Close()

	err = sb.Start(func(words []TranscriptWord, isFinal bool) {})
	if err != nil {
		if strings.Contains(err.Error(), "shared memory not available") {
			t.Skip("shared memory not available (no cgo)")
		}
		t.Fatalf("Start: %v", err)
	}

	// Stop and verify no crash.
	sb.Stop()
	time.Sleep(100 * time.Millisecond)

	// Should still be able to close cleanly.
}

// integrationShmName generates a unique shm name for this test.
func integrationShmName(t *testing.T) string {
	name := t.Name()
	h := uint32(0)
	for _, c := range name {
		h = h*31 + uint32(c)
	}
	return fmt.Sprintf("/test-asr-%x", h)
}
