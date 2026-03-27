//go:build !windows

package asr

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/caption"
)

// TestSidecarFullCaptionPipeline tests the complete path from audio ingestion
// through the sidecar to caption text output, wired like the real app.
//
// Pipeline: audio → shm → echo-sidecar → socket → transcript callback → caption.Manager.IngestText
//
// The captionPacer from cmd/switchframe is in the main package and not importable,
// so this test wires the transcript callback directly to the caption manager,
// simulating what the pacer does (minus the timing/holdback logic).
func TestSidecarFullCaptionPipeline(t *testing.T) {
	if echoSidecarBin == "" {
		t.Skip("echo-sidecar not built")
	}

	// 1. Create caption manager (same as app_captions.go initCaptions).
	captionMgr := caption.NewManager()

	// 2. Set caption mode to Auto (required for manager to accept IngestText).
	captionMgr.SetMode(caption.ModeAuto)

	// 3. Track transcript words received from the sidecar.
	var mu sync.Mutex
	var captionTexts []string
	textReceived := make(chan struct{}, 10)

	// 4. Create sidecar backend.
	sb, err := NewSidecarBackend(SidecarConfig{
		BinPath:  echoSidecarBin,
		ShmName:  fmt.Sprintf("/test-caption-%d", time.Now().UnixNano()%100000),
		ModelDir: t.TempDir(),
		Device:   0,
	})
	if err != nil {
		t.Fatalf("NewSidecarBackend: %v", err)
	}
	defer sb.Close()

	// 5. Wire transcript callback — simulates what initASR + captionPacer does:
	//    sidecar emits words → callback feeds each word to caption.Manager.IngestText.
	err = sb.Start(func(words []TranscriptWord, isFinal bool) {
		mu.Lock()
		for _, w := range words {
			captionTexts = append(captionTexts, w.Text)
			// Feed each word to the caption manager directly,
			// simulating what the pacer's emitWord does.
			captionMgr.IngestText(w.Text)
		}
		mu.Unlock()
		select {
		case textReceived <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 6. Feed audio — the echo sidecar returns "echo" for any non-empty audio.
	audio := make([]float32, 2048)
	for i := range audio {
		audio[i] = 0.3 // non-zero so it's not silence
	}
	sb.IngestAudio(audio, 0, 48000, 2)

	// 7. Wait for transcript words to arrive via the callback.
	select {
	case <-textReceived:
		// Words arrived!
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: no transcript words received from sidecar")
	}

	// 8. Verify words were received at the callback level.
	mu.Lock()
	texts := make([]string, len(captionTexts))
	copy(texts, captionTexts)
	mu.Unlock()

	if len(texts) == 0 {
		t.Fatal("expected caption texts, got none")
	}

	t.Logf("Caption pipeline received %d words: %v", len(texts), texts)

	// 9. Verify the echo sidecar's known word ("echo") made it through.
	found := false
	for _, text := range texts {
		if text == "echo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected word 'echo' from echo sidecar, got: %v", texts)
	}

	// 10. Verify the caption manager received the text via IngestText.
	//     The manager stores authored text in its authorBuffer, visible via State().
	state := captionMgr.State()
	if state.Mode != "auto" {
		t.Errorf("caption mode = %q, want %q", state.Mode, "auto")
	}
	if !strings.Contains(state.AuthorBuffer, "echo") {
		t.Errorf("caption manager authorBuffer = %q, want it to contain 'echo'", state.AuthorBuffer)
	}

	t.Log("Full caption pipeline test PASSED: audio -> shm -> echo-sidecar -> socket -> callback -> caption.Manager")
}
