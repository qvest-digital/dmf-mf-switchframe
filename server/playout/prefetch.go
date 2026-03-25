package playout

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PrefetcherConfig configures the schedule-aware prefetch scheduler.
type PrefetcherConfig struct {
	Cache          *Cache
	Resolver       *AssetResolver
	Transcoder     *Transcoder // optional: nil = skip transcode
	LookaheadItems int         // how many items ahead to pre-fetch (default 5)
	OnFetched      func(uri string, err error)
}

// Prefetcher watches the playlist lookahead window and queues cache fetches
// for upcoming clip items. It runs a background goroutine that rescans on
// playlist/cursor updates or a periodic 30-second tick.
type Prefetcher struct {
	config    PrefetcherConfig
	checker   TranscodeChecker
	mu        sync.Mutex
	playlist  *Playlist
	cursor    int
	cancel    context.CancelFunc
	done      chan struct{}
	triggerCh chan struct{} // buffered 1, signal to re-scan
	inflight  map[string]bool
}

// NewPrefetcher creates a prefetcher. LookaheadItems defaults to 5 if zero.
func NewPrefetcher(config PrefetcherConfig) *Prefetcher {
	if config.LookaheadItems <= 0 {
		config.LookaheadItems = 5
	}
	return &Prefetcher{
		config:    config,
		triggerCh: make(chan struct{}, 1),
		inflight:  make(map[string]bool),
	}
}

// Start launches the background prefetch goroutine.
func (p *Prefetcher) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	p.done = make(chan struct{})

	go p.run(ctx)
}

// Stop cancels the background goroutine and waits for it to exit.
func (p *Prefetcher) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		<-p.done
	}
}

// UpdatePlaylist updates the target playlist and cursor position, then
// triggers a rescan of the lookahead window.
func (p *Prefetcher) UpdatePlaylist(pl *Playlist, cursor int) {
	p.mu.Lock()
	p.playlist = pl
	p.cursor = cursor
	p.mu.Unlock()

	// Non-blocking signal to rescan.
	select {
	case p.triggerCh <- struct{}{}:
	default:
	}
}

// scanNeeded returns asset URIs that need fetching: clip items within the
// lookahead window that are not already cached and not currently inflight.
func (p *Prefetcher) scanNeeded(pl *Playlist, cursor int) []string {
	if pl == nil || len(pl.Items) == 0 {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	end := cursor + p.config.LookaheadItems
	if end > len(pl.Items) {
		end = len(pl.Items)
	}

	seen := make(map[string]bool)
	var uris []string

	for i := cursor; i < end; i++ {
		item := &pl.Items[i]

		// Only clip items have fetchable assets.
		if item.Type != ItemClip {
			continue
		}
		if item.AssetURI == "" {
			continue
		}
		// Deduplicate within this scan.
		if seen[item.AssetURI] {
			continue
		}
		seen[item.AssetURI] = true

		// Skip if already inflight.
		if p.inflight[item.AssetURI] {
			continue
		}

		// Skip if already cached (and not in error state).
		entry, err := p.config.Cache.Get(item.AssetURI)
		if err == nil && entry.State == AssetCached {
			continue
		}

		uris = append(uris, item.AssetURI)
	}

	return uris
}

// run is the background goroutine loop: waits for triggers or periodic ticks,
// then scans and launches fetch goroutines (bounded by semaphore).
func (p *Prefetcher) run(ctx context.Context) {
	defer close(p.done)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Semaphore for max 2 concurrent fetches.
	sem := make(chan struct{}, 2)

	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-p.triggerCh:
		case <-ticker.C:
		}

		// Snapshot current playlist/cursor under lock.
		p.mu.Lock()
		pl := p.playlist
		cursor := p.cursor
		p.mu.Unlock()

		uris := p.scanNeeded(pl, cursor)
		for _, uri := range uris {
			p.mu.Lock()
			if p.inflight[uri] {
				p.mu.Unlock()
				continue
			}
			p.inflight[uri] = true
			p.mu.Unlock()

			// Create a pending cache entry.
			localPath, err := p.config.Cache.Reserve(uri)
			if err != nil {
				p.mu.Lock()
				delete(p.inflight, uri)
				p.mu.Unlock()
				if p.config.OnFetched != nil {
					p.config.OnFetched(uri, fmt.Errorf("reserve cache path: %w", err))
				}
				continue
			}
			_ = p.config.Cache.Put(&CacheEntry{
				AssetURI:     uri,
				LocalPath:    localPath,
				State:        AssetPending,
				LastAccessed: time.Now(),
			})

			fetchURI := uri
			fetchPath := localPath

			wg.Add(1)
			go func() {
				defer wg.Done()

				// Acquire semaphore slot.
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					p.mu.Lock()
					delete(p.inflight, fetchURI)
					p.mu.Unlock()
					if p.config.OnFetched != nil {
						p.config.OnFetched(fetchURI, ctx.Err())
					}
					return
				}
				defer func() { <-sem }()

				p.fetchOne(ctx, fetchURI, fetchPath)
			}()
		}
	}
}

// fetchOne fetches a single asset to the cache, optionally transcodes it,
// and updates the cache entry state.
func (p *Prefetcher) fetchOne(ctx context.Context, uri string, localPath string) {
	defer func() {
		p.mu.Lock()
		delete(p.inflight, uri)
		p.mu.Unlock()
	}()

	// Mark as fetching.
	_ = p.config.Cache.Put(&CacheEntry{
		AssetURI:     uri,
		LocalPath:    localPath,
		State:        AssetFetching,
		LastAccessed: time.Now(),
	})

	// Resolve fetcher and source path.
	fetcher, err := p.config.Resolver.FetcherFor(uri)
	if err != nil {
		p.markError(uri, localPath, fmt.Errorf("resolve fetcher: %w", err))
		return
	}
	sourcePath := p.config.Resolver.SourcePath(uri)

	// Fetch the asset.
	_, err = fetcher.Fetch(ctx, sourcePath, localPath)
	if err != nil {
		p.markError(uri, localPath, fmt.Errorf("fetch: %w", err))
		return
	}

	finalPath := localPath

	// Transcode if needed.
	ext := filepath.Ext(localPath)
	if p.checker.NeedsByExtension(ext) && p.config.Transcoder != nil {
		_ = p.config.Cache.Put(&CacheEntry{
			AssetURI:     uri,
			LocalPath:    localPath,
			State:        AssetTranscoding,
			LastAccessed: time.Now(),
		})

		outputPath, err := p.config.Transcoder.Transcode(ctx, localPath, nil)
		if err != nil {
			p.markError(uri, localPath, fmt.Errorf("transcode: %w", err))
			return
		}

		// Remove original fetched file, use transcoded output.
		_ = os.Remove(localPath)
		finalPath = outputPath
	}

	// Stat the final file for size.
	info, err := os.Stat(finalPath)
	if err != nil {
		p.markError(uri, finalPath, fmt.Errorf("stat: %w", err))
		return
	}

	// Mark as cached.
	_ = p.config.Cache.Put(&CacheEntry{
		AssetURI:     uri,
		LocalPath:    finalPath,
		State:        AssetCached,
		ByteSize:     info.Size(),
		Transcoded:   finalPath != localPath,
		FetchedAt:    time.Now(),
		LastAccessed: time.Now(),
	})

	if p.config.OnFetched != nil {
		p.config.OnFetched(uri, nil)
	}
}

// markError updates the cache entry to error state and invokes the callback.
func (p *Prefetcher) markError(uri string, localPath string, err error) {
	_ = p.config.Cache.Put(&CacheEntry{
		AssetURI:     uri,
		LocalPath:    localPath,
		State:        AssetError,
		ErrorMsg:     err.Error(),
		LastAccessed: time.Now(),
	})
	if p.config.OnFetched != nil {
		p.config.OnFetched(uri, err)
	}
}
