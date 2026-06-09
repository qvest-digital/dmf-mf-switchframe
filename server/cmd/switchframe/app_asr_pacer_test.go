package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"

	"github.com/zsiec/switchframe/server/asr"
	"github.com/zsiec/switchframe/server/caption"
)

// captureManager wires a caption.Manager in ModeAuto and records every authored
// caption frame so pacer tests can assert on the produced layout.
func captureManager(t *testing.T) (*caption.Manager, func() []string) {
	t.Helper()
	mgr := caption.NewManager()
	mgr.SetMode(caption.ModeAuto)

	var mu sync.Mutex
	var frames []string
	mgr.SetBroadcastSink(func(f *ccx.CaptionFrame) {
		mu.Lock()
		frames = append(frames, f.Text)
		mu.Unlock()
	})
	return mgr, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(frames))
		copy(out, frames)
		return out
	}
}

// drainPacer runs emitNextWord until the queue and any held word are flushed,
// without relying on the background ticker (deterministic for tests).
func drainPacer(p *captionPacer) {
	for i := 0; i < pacerMaxQueueWords*4; i++ {
		p.mu.Lock()
		done := len(p.queue) == 0 && p.held == nil
		p.mu.Unlock()
		if done {
			return
		}
		p.emitNextWord()
	}
}

func repeatWords(text string, n int) []asr.TranscriptWord {
	out := make([]asr.TranscriptWord, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, asr.TranscriptWord{Text: text, Confidence: 1.0})
	}
	return out
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// TestPacerLineWrapCountsDisplayCellsNotBytes verifies the line-wrap math uses
// CEA-608 display cells, not UTF-8 byte length. "café" is 5 bytes but 4 display
// cells; six of them with single-space separators are 6*4 + 5 = 29 cells
// (<= 32, no wrap), yet 6*5 + 5 = 35 bytes (> 32, would wrap prematurely under
// the old byte-counting logic).
func TestPacerLineWrapCountsDisplayCellsNotBytes(t *testing.T) {
	mgr, snapshot := captureManager(t)
	p := newCaptionPacer(mgr)
	defer p.Stop()

	p.EnqueueTranscriptWords(repeatWords("café", 6), false)
	drainPacer(p)

	// linePos must reflect display cells: 6*4 + 5 spaces = 29, no wrap yet.
	p.mu.Lock()
	gotLinePos := p.linePos
	p.mu.Unlock()
	require.Equal(t, 29, gotLinePos, "linePos must count display cells, not bytes")

	frames := snapshot()
	require.NotEmpty(t, frames)
	last := frames[len(frames)-1]
	require.Equal(t, 1, lineCount(last), "should remain on a single line: %q", last)
}

// TestPacerWrapsWhenDisplayCellsExceedLimit confirms wrapping still triggers
// when the true display width exceeds the 32-cell limit.
func TestPacerWrapsWhenDisplayCellsExceedLimit(t *testing.T) {
	mgr, snapshot := captureManager(t)
	p := newCaptionPacer(mgr)
	defer p.Stop()

	// "café" * 8 = 8*4 + 7 spaces = 39 cells > 32 → must wrap at least once.
	p.EnqueueTranscriptWords(repeatWords("café", 8), false)
	drainPacer(p)

	frames := snapshot()
	require.NotEmpty(t, frames)
	last := frames[len(frames)-1]
	require.GreaterOrEqual(t, lineCount(last), 2, "39 display cells must wrap: %q", last)
}

// TestPacerEmitWordDoesNotHoldQueueLockAcrossManager guards Finding 2: emitWord
// must not hold p.mu while calling into the caption manager (whose sink fans out
// to network broadcast). We install a broadcast sink that, on first call, tries
// to acquire p.mu; if emitWord held p.mu across the manager call this would
// deadlock and the test would time out.
func TestPacerEmitWordDoesNotHoldQueueLockAcrossManager(t *testing.T) {
	mgr := caption.NewManager()
	mgr.SetMode(caption.ModeAuto)
	p := newCaptionPacer(mgr)
	defer p.Stop()

	acquired := make(chan struct{})
	var once sync.Once
	mgr.SetBroadcastSink(func(*ccx.CaptionFrame) {
		once.Do(func() {
			// Must be able to take the pacer lock from inside a manager/broadcast
			// callback — proving the pacer lock is released across this call.
			p.mu.Lock()
			p.mu.Unlock()
			close(acquired)
		})
	})

	p.EnqueueTranscriptWords([]asr.TranscriptWord{{Text: "hello", Confidence: 1.0}}, false)
	p.emitNextWord()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("emitWord appears to hold p.mu across caption-manager call (lock not reacquirable from sink)")
	}
}
