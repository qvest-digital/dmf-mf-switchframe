package main

import (
	"sync"
	"testing"
)

// TestGPUStateSwapConcurrentWithReaders covers finding #77: the GPU pipeline
// rebuild on format change (rebuildGPUPipelineForFormat) replaces the App's
// GPU state from whatever goroutine called SetPipelineFormat, while live
// callbacks read it concurrently from their own goroutines (SRT decode
// callbacks, clip/playout lifecycle hooks, OnViewerAdded). The state pointer
// must therefore be swapped atomically via the gpu()/setGPUState accessors.
//
// Run under -race: a plain pointer field is flagged as a data race.
func TestGPUStateSwapConcurrentWithReaders(t *testing.T) {
	app := &App{}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers: mimic the hot-path access pattern used by OnViewerAdded and
	// the clip/playout lifecycle hooks (snapshot, nil-check, deref).
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if gs := app.gpu(); gs != nil && gs.sourceManager != nil {
					t.Error("sourceManager unexpectedly non-nil")
					return
				}
			}
		}()
	}

	// Writer: mimic the format-change rebuild (publish nil during teardown,
	// then the freshly built state).
	for i := 0; i < 10000; i++ {
		app.setGPUState(nil)
		app.setGPUState(&gpuState{})
	}

	close(stop)
	wg.Wait()
}

// The GPU pipeline runs every node on every frame, so the squeezeback
// adapter must pass frames through when the app has no squeezeback.
func TestSqueezeStateAdapter_NilSqueeze(t *testing.T) {
	a := &squeezeStateAdapter{}
	if _, _, ok := a.Squeeze(1920, 1080); ok {
		t.Fatal("no squeezeback must leave the frame alone")
	}
}
