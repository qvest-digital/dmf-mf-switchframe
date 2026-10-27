package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/switcher"
)

// TestPrewarmSTMapCacheCancelled verifies that prewarmSTMapCache aborts on a
// cancelled context before doing any expensive LUT generation, so a SIGTERM
// during the multi-minute first-boot warm does not block bgWG.Wait() until the
// full (~28 GB / ~3 min) generation completes.
func TestPrewarmSTMapCacheCancelled(t *testing.T) {
	// Precondition: the warm only matters if there is at least one animated
	// generator to iterate over; otherwise the cancellation check is vacuous.
	if len(stmap.AnimatedGeneratorInfoList()) == 0 {
		t.Skip("no animated generators registered")
	}

	relay := distribution.NewRelay()
	sw := switcher.New(relay)
	defer sw.Close()

	// DefaultFormat is non-zero, so prewarmSTMapCache reaches the generator
	// loop (it returns early only when the pipeline format is 0x0).
	pf := sw.PipelineFormat()
	if pf.Width == 0 || pf.Height == 0 {
		t.Fatalf("expected non-zero default pipeline format, got %dx%d", pf.Width, pf.Height)
	}

	cacheDir := t.TempDir()
	app := &App{
		sw:  sw,
		cfg: AppConfig{CacheDir: cacheDir},
	}

	// Already-cancelled context: the loop must return on its first iteration,
	// before calling GenerateAnimated / BuildProcessorsCached.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	app.bgWG.Add(1)
	done := make(chan struct{})
	go func() {
		app.prewarmSTMapCache(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Returned. The real uncancelled run takes minutes; returning at all
		// under this timeout shows the cancellation check fired.
	case <-time.After(30 * time.Second):
		t.Fatal("prewarmSTMapCache did not return on cancelled context")
	}

	// bgWG.Done() must have been called so closeSubsystems' Wait() unblocks.
	app.bgWG.Wait()

	// No cache files should have been written: aborting before generation
	// means the stmap cache directory stays empty (or is never created).
	stmapDir := filepath.Join(cacheDir, "stmap")
	entries, err := os.ReadDir(stmapDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read stmap cache dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no cache entries after cancelled prewarm, found %d", len(entries))
	}
}

// TestStartSTMapPrewarm verifies that --stmap-prewarm=false keeps the
// startup prewarm from writing ST map caches while a cache directory is
// set, so that directory stays usable for effects generated on demand.
func TestStartSTMapPrewarm(t *testing.T) {
	if len(stmap.AnimatedGeneratorInfoList()) == 0 {
		t.Skip("no animated generators registered")
	}

	for _, tc := range []struct {
		prewarm bool
		want    bool
	}{
		{prewarm: true, want: true},
		{prewarm: false, want: false},
	} {
		t.Run(fmt.Sprintf("prewarm=%v", tc.prewarm), func(t *testing.T) {
			relay := distribution.NewRelay()
			sw := switcher.New(relay)
			defer sw.Close()

			// A small format keeps the prewarm, when it runs, to a few MB.
			f := switcher.DefaultFormat
			f.Width, f.Height = 64, 36
			if err := sw.SetPipelineFormat(f); err != nil {
				t.Fatalf("set pipeline format: %v", err)
			}

			cacheDir := t.TempDir()
			app := &App{
				sw:    sw,
				cfg:   AppConfig{CacheDir: cacheDir, STMapPrewarm: tc.prewarm},
				bgCtx: context.Background(),
			}
			app.startSTMapPrewarm()
			app.bgWG.Wait()

			entries, err := os.ReadDir(filepath.Join(cacheDir, "stmap"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("read stmap cache dir: %v", err)
			}
			if got := len(entries) > 0; got != tc.want {
				t.Fatalf("stmap cache written = %v (%d entries), want %v", got, len(entries), tc.want)
			}
		})
	}
}
