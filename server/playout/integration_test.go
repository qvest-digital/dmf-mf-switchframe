package playout

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegration_PlaylistExecution(t *testing.T) {
	var frameCount atomic.Int64

	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(10),
	})
	defer m.Close()

	m.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	pl := testPlaylist(clipItem("clip-1"), clipItem("clip-2"), clipItem("clip-3"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 10*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})

	count := frameCount.Load()
	if count < 30 {
		t.Errorf("total frames = %d, want >= 30 (10 per clip x 3 clips)", count)
	}

	states := m.ChannelStates()
	if states[0].State != "completed" {
		t.Errorf("final state = %q, want %q", states[0].State, "completed")
	}
}

func TestIntegration_ManualOverrideAndReturn(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(10),
	})
	defer m.Close()

	// hold item blocks after playback; regular item completes.
	pl := testPlaylist(holdClipItem("item-1"), clipItem("item-2"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for on-air state.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	// Take manual control.
	if err := m.TakeManual(1); err != nil {
		t.Fatalf("TakeManual: %v", err)
	}

	states := m.ChannelStates()
	if states[0].Mode != "manual" {
		t.Errorf("mode after TakeManual = %q, want %q", states[0].Mode, "manual")
	}

	// Advance past the hold item; manual mode should block before next item.
	_ = m.Next(1)
	time.Sleep(150 * time.Millisecond)

	// Should still be on-air in manual mode.
	states = m.ChannelStates()
	if states[0].State != "on_air" {
		t.Errorf("state during manual = %q, want %q", states[0].State, "on_air")
	}

	// Return to auto — should resume and complete.
	if err := m.ReturnToAuto(1); err != nil {
		t.Fatalf("ReturnToAuto: %v", err)
	}

	states = m.ChannelStates()
	if states[0].Mode != "automated" {
		t.Errorf("mode after ReturnToAuto = %q, want %q", states[0].Mode, "automated")
	}

	waitFor(t, 10*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_NextSkipsItem(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(200), // long clip: 200 frames at 30fps ~ 6.6s
	})
	defer m.Close()

	pl := testPlaylist(clipItem("long-clip"), clipItem("short-clip"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for on-air state.
	waitFor(t, 2*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	// Let a few frames play, then skip.
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if err := m.Next(1); err != nil {
		t.Fatalf("Next: %v", err)
	}

	// Wait for completion — should be much faster than playing all 400 frames.
	waitFor(t, 10*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})

	elapsed := time.Since(start)
	// Playing 200+200 frames at 30fps would take ~13s. With skip it should be
	// under 10s (only the second 200-frame clip plays fully).
	if elapsed > 12*time.Second {
		t.Errorf("elapsed = %v, expected much less than full playback time", elapsed)
	}
}

func TestIntegration_JumpToItem(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(5),
	})
	defer m.Close()

	pl := testPlaylist(
		holdClipItem("item-1"),
		holdClipItem("item-2"),
		holdClipItem("item-3"),
		holdClipItem("item-4"),
	)
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for item-1 to be on-air (hold items play then block).
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air" && states[0].CurrentItemID == "item-1"
	})

	// Jump to item-3.
	if err := m.JumpTo(1, "item-3"); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	// Wait for current item to be item-3.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-3"
	})

	// Advance past item-3's hold to item-4.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next past item-3: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-4"
	})

	// Advance past item-4's hold to complete.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next past item-4: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_PreviousItem(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(5),
	})
	defer m.Close()

	pl := testPlaylist(
		holdClipItem("item-1"),
		holdClipItem("item-2"),
		holdClipItem("item-3"),
	)
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for item-1 on-air.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air" && states[0].CurrentItemID == "item-1"
	})

	// Advance to item-2.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next to item-2: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-2"
	})

	// Go back to item-1.
	if err := m.Previous(1); err != nil {
		t.Fatalf("Previous to item-1: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-1"
	})

	// Advance through all to completion: item-1 → item-2 → item-3 → done.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next from item-1 (second time): %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-2"
	})

	if err := m.Next(1); err != nil {
		t.Fatalf("Next from item-2: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-3"
	})

	if err := m.Next(1); err != nil {
		t.Fatalf("Next from item-3: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_LiveItemHold(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(5),
	})
	defer m.Close()

	pl := testPlaylist(
		clipItem("intro"),
		liveItem("live-cam", "camera-1"),
		clipItem("outro"),
	)
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the live item to become current.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "live-cam"
	})

	// Channel should hold on the live item.
	time.Sleep(200 * time.Millisecond)
	states := m.ChannelStates()
	if states[0].CurrentItemID != "live-cam" {
		t.Errorf("still on live item: currentItemID = %q, want %q", states[0].CurrentItemID, "live-cam")
	}
	if states[0].State != "on_air" {
		t.Errorf("state = %q, want %q", states[0].State, "on_air")
	}

	// Advance past live item.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next past live: %v", err)
	}

	// Wait for completion (outro clip plays out).
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_LifecycleCallbacks(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(5),
	})

	var startCalled atomic.Int32
	var stopCalled atomic.Int32
	var startChID atomic.Int32
	var stopChID atomic.Int32
	var startKey atomic.Value
	var stopKey atomic.Value

	m.OnChannelLifecycle(
		func(channelID int, key string) {
			startChID.Store(int32(channelID))
			startKey.Store(key)
			startCalled.Add(1)
		},
		func(channelID int, key string) {
			stopChID.Store(int32(channelID))
			stopKey.Store(key)
			stopCalled.Add(1)
		},
	)

	pl := testPlaylist(liveItem("live-1", "cam"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify onStart called.
	waitFor(t, 2*time.Second, func() bool {
		return startCalled.Load() >= 1
	})

	if startChID.Load() != 1 {
		t.Errorf("onStart channelID = %d, want 1", startChID.Load())
	}
	sk, _ := startKey.Load().(string)
	if sk != "playout:1" {
		t.Errorf("onStart key = %q, want %q", sk, "playout:1")
	}

	// Stop the channel.
	if err := m.Stop(1); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify onStop called.
	waitFor(t, 2*time.Second, func() bool {
		return stopCalled.Load() >= 1
	})

	if stopChID.Load() != 1 {
		t.Errorf("onStop channelID = %d, want 1", stopChID.Load())
	}
	spk, _ := stopKey.Load().(string)
	if spk != "playout:1" {
		t.Errorf("onStop key = %q, want %q", spk, "playout:1")
	}
}

func TestIntegration_StopMidPlayback(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(5),
	})
	defer m.Close()

	// Load a playlist with a hold item that will block.
	pl := testPlaylist(holdClipItem("hold-1"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for on-air.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	// Stop mid-playback.
	if err := m.Stop(1); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	states := m.ChannelStates()
	if states[0].State != "stopped" {
		t.Errorf("state after stop = %q, want %q", states[0].State, "stopped")
	}

	// Verify we can load a new playlist and start again.
	pl2 := testPlaylist(clipItem("new-clip"))
	if err := m.LoadPlaylist(1, pl2); err != nil {
		t.Fatalf("LoadPlaylist (second): %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start (second): %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_TwoChannels(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(10),
	})
	defer m.Close()

	var ch1Frames atomic.Int64
	var ch2Frames atomic.Int64

	m.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		switch key {
		case "playout:1":
			ch1Frames.Add(1)
		case "playout:2":
			ch2Frames.Add(1)
		}
	})

	// Channel 1: 2 clips.
	pl1 := testPlaylist(clipItem("ch1-a"), clipItem("ch1-b"))
	if err := m.LoadPlaylist(1, pl1); err != nil {
		t.Fatalf("LoadPlaylist ch1: %v", err)
	}

	// Channel 2: 3 clips.
	pl2 := testPlaylist(clipItem("ch2-a"), clipItem("ch2-b"), clipItem("ch2-c"))
	if err := m.LoadPlaylist(2, pl2); err != nil {
		t.Fatalf("LoadPlaylist ch2: %v", err)
	}

	// Start both channels.
	if err := m.Start(1); err != nil {
		t.Fatalf("Start ch1: %v", err)
	}
	if err := m.Start(2); err != nil {
		t.Fatalf("Start ch2: %v", err)
	}

	// Wait for both to complete.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		waitFor(t, 10*time.Second, func() bool {
			states := m.ChannelStates()
			return states[0].State == "completed"
		})
	}()

	go func() {
		defer wg.Done()
		waitFor(t, 10*time.Second, func() bool {
			states := m.ChannelStates()
			return states[1].State == "completed"
		})
	}()

	wg.Wait()

	states := m.ChannelStates()
	if states[0].State != "completed" {
		t.Errorf("ch1 final state = %q, want %q", states[0].State, "completed")
	}
	if states[1].State != "completed" {
		t.Errorf("ch2 final state = %q, want %q", states[1].State, "completed")
	}

	// Channel 1: 2 clips x 10 frames = 20 frames.
	if ch1Frames.Load() < 20 {
		t.Errorf("ch1 frames = %d, want >= 20", ch1Frames.Load())
	}

	// Channel 2: 3 clips x 10 frames = 30 frames.
	if ch2Frames.Load() < 30 {
		t.Errorf("ch2 frames = %d, want >= 30", ch2Frames.Load())
	}
}

// ---------------------------------------------------------------------------
// Phase 2 integration tests: cache → fetch → streaming iterator → playback
// ---------------------------------------------------------------------------

func TestIntegration_CacheFetchAndPlay(t *testing.T) {
	dir := t.TempDir()

	// 1. Create cache.
	cache, err := NewCache(CacheConfig{Dir: filepath.Join(dir, "cache"), MaxBytes: 100 * 1024 * 1024})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer cache.Close()

	// 2. Create a "media" file on disk (fake .ts content).
	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll media: %v", err)
	}
	mediaPath := filepath.Join(mediaDir, "clip.ts")
	if err := os.WriteFile(mediaPath, []byte("fake ts data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// 3. Set up asset resolver + prefetcher with OnFetched callback.
	resolver := NewAssetResolver(nil)

	var fetched sync.WaitGroup
	fetched.Add(1)

	prefetcher := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       resolver,
		LookaheadItems: 5,
		OnFetched: func(uri string, err error) {
			fetched.Done()
		},
	})

	// 4. Create playlist referencing the local file.
	assetURI := "local://" + mediaPath
	pl := &Playlist{
		ID:   "test",
		Name: "Test Playlist",
		Items: []PlaylistItem{
			{ID: "item-1", Type: ItemClip, Title: "Test Clip", AssetURI: assetURI},
		},
	}

	// 5. Start prefetcher and trigger fetch.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefetcher.Start(ctx)
	defer prefetcher.Stop()

	prefetcher.UpdatePlaylist(pl, 0)
	fetched.Wait()

	// 6. Verify asset is cached.
	entry, err := cache.Get(assetURI)
	if err != nil {
		t.Fatalf("cache.Get after fetch: %v", err)
	}
	if entry.State != AssetCached {
		t.Fatalf("cache entry state = %s, want cached", entry.State)
	}

	// 7. Create CacheAssetProvider with a fake demux that returns a FrameIterator.
	provider := NewCacheAssetProvider(cache, func(path string) (FrameIterator, error) {
		return NewSliceIterator(fakeFrames(5)), nil
	})

	// 8. Create channel with provider, load playlist, play.
	ch := NewChannel(1, ChannelConfig{AssetProvider: provider})
	var outputCount atomic.Int32
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		outputCount.Add(1)
	})

	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for completion.
	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	count := outputCount.Load()
	if count != 5 {
		t.Errorf("output frame count = %d, want 5", count)
	}
}

func TestIntegration_PrefetchAdvancesWithCursor(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: filepath.Join(dir, "cache"), MaxBytes: 100 * 1024 * 1024})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer cache.Close()

	// Create 6 "media" files.
	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	items := make([]PlaylistItem, 6)
	for i := 0; i < 6; i++ {
		path := filepath.Join(mediaDir, fmt.Sprintf("clip%d.ts", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("data-%d", i)), 0o644); err != nil {
			t.Fatalf("WriteFile clip%d: %v", i, err)
		}
		items[i] = PlaylistItem{
			ID:       fmt.Sprintf("item-%d", i),
			Type:     ItemClip,
			Title:    fmt.Sprintf("Clip %d", i),
			AssetURI: "local://" + path,
		}
	}

	pl := &Playlist{ID: "test", Name: "Test", Items: items}

	resolver := NewAssetResolver(nil)

	var mu sync.Mutex
	fetchedURIs := map[string]bool{}

	prefetcher := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       resolver,
		LookaheadItems: 3,
		OnFetched: func(uri string, err error) {
			if err == nil {
				mu.Lock()
				fetchedURIs[uri] = true
				mu.Unlock()
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prefetcher.Start(ctx)
	defer prefetcher.Stop()

	// Cursor at 0, lookahead 3 -> should fetch items 0, 1, 2.
	prefetcher.UpdatePlaylist(pl, 0)

	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		n := len(fetchedURIs)
		mu.Unlock()
		return n >= 3
	})

	mu.Lock()
	firstBatch := len(fetchedURIs)
	mu.Unlock()
	if firstBatch < 3 {
		t.Fatalf("after cursor=0: fetched %d URIs, want >= 3", firstBatch)
	}

	// Advance cursor to 3, should fetch items 3, 4, 5.
	prefetcher.UpdatePlaylist(pl, 3)

	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		n := len(fetchedURIs)
		mu.Unlock()
		return n >= 6
	})

	mu.Lock()
	total := len(fetchedURIs)
	mu.Unlock()
	if total < 6 {
		t.Errorf("after cursor=3: fetched %d URIs, want >= 6", total)
	}
}

func TestIntegration_HTTPAssetFetch(t *testing.T) {
	// 1. httptest server serving fake content.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("http media data"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: filepath.Join(dir, "cache"), MaxBytes: 100 * 1024 * 1024})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer cache.Close()

	resolver := NewAssetResolver(nil)

	fetchDone := make(chan struct{})
	var fetchErr error

	prefetcher := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       resolver,
		LookaheadItems: 3,
		OnFetched: func(uri string, err error) {
			fetchErr = err
			close(fetchDone)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefetcher.Start(ctx)
	defer prefetcher.Stop()

	assetURI := ts.URL + "/clip.ts"
	pl := &Playlist{
		ID: "test", Name: "Test",
		Items: []PlaylistItem{
			{ID: "1", Type: ItemClip, Title: "HTTP Clip", AssetURI: assetURI},
		},
	}
	prefetcher.UpdatePlaylist(pl, 0)

	select {
	case <-fetchDone:
	case <-ctx.Done():
		t.Fatal("timed out waiting for HTTP fetch")
	}

	if fetchErr != nil {
		t.Fatalf("HTTP fetch error: %v", fetchErr)
	}

	// Verify cached.
	entry, err := cache.Get(assetURI)
	if err != nil {
		t.Fatalf("cache.Get: %v", err)
	}
	if entry.State != AssetCached {
		t.Errorf("entry state = %s, want cached", entry.State)
	}
	if entry.ByteSize <= 0 {
		t.Errorf("entry ByteSize = %d, want > 0", entry.ByteSize)
	}
}

func TestIntegration_CacheEvictionDuringPrefetch(t *testing.T) {
	dir := t.TempDir()
	// Small cache: only fits 2 items (200 bytes total, each file ~80 bytes).
	cache, err := NewCache(CacheConfig{Dir: filepath.Join(dir, "cache"), MaxBytes: 200})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer cache.Close()

	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create 3 files, each ~80 bytes.
	items := make([]PlaylistItem, 3)
	for i := 0; i < 3; i++ {
		path := filepath.Join(mediaDir, fmt.Sprintf("c%d.ts", i))
		if err := os.WriteFile(path, make([]byte, 80), 0o644); err != nil {
			t.Fatalf("WriteFile c%d: %v", i, err)
		}
		items[i] = PlaylistItem{
			ID:       fmt.Sprintf("item-%d", i),
			Type:     ItemClip,
			Title:    fmt.Sprintf("C%d", i),
			AssetURI: "local://" + path,
		}
	}

	pl := &Playlist{ID: "test", Name: "Test", Items: items}

	resolver := NewAssetResolver(nil)
	var wg sync.WaitGroup
	wg.Add(3)
	prefetcher := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       resolver,
		LookaheadItems: 5,
		OnFetched:      func(uri string, err error) { wg.Done() },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefetcher.Start(ctx)
	defer prefetcher.Stop()

	prefetcher.UpdatePlaylist(pl, 0)
	wg.Wait()

	// Only 2 items fit in cache (200 bytes, each ~80 bytes). LRU eviction
	// keeps the total at or below MaxBytes.
	status := cache.Status()
	if status.ItemCount > 2 {
		t.Errorf("cache ItemCount = %d, expected <= 2 (cache is 200 bytes, items are 80 bytes)", status.ItemCount)
	}
	if status.UsedBytes > 200 {
		t.Errorf("cache UsedBytes = %d, expected <= 200", status.UsedBytes)
	}
}
