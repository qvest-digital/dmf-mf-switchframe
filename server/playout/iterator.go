package playout

import "fmt"

// FrameIterator provides streaming access to decoded frames.
type FrameIterator interface {
	// Next returns the next frame, or (nil, nil) at end of stream.
	Next() (*DecodedFrame, error)
	// EstimateFPS peeks at frame PTS values to estimate framerate.
	// May buffer internally. Returns 30.0 as default.
	EstimateFPS() float64
	// TotalFrames returns total frame count if known, or -1 if unknown (streaming).
	TotalFrames() int
	// Close releases resources.
	Close()
}

// AssetProvider opens asset URIs and returns frame iterators.
type AssetProvider interface {
	Open(assetURI string) (FrameIterator, error)
}

// ---------------------------------------------------------------------------
// SliceIterator — wraps []DecodedFrame as a FrameIterator.
// ---------------------------------------------------------------------------

// SliceIterator wraps a pre-decoded slice of frames as a FrameIterator.
// Used for tests and backward compatibility with the old DemuxFunc approach.
type SliceIterator struct {
	frames []DecodedFrame
	pos    int
}

// NewSliceIterator creates a FrameIterator over a pre-decoded frame slice.
func NewSliceIterator(frames []DecodedFrame) *SliceIterator {
	return &SliceIterator{frames: frames}
}

// Next returns the next frame, or (nil, nil) at end of stream.
func (s *SliceIterator) Next() (*DecodedFrame, error) {
	if s.pos >= len(s.frames) {
		return nil, nil
	}
	f := &s.frames[s.pos]
	s.pos++
	return f, nil
}

// EstimateFPS uses PTS spacing to estimate frames per second.
func (s *SliceIterator) EstimateFPS() float64 {
	return estimateFPS(s.frames)
}

// TotalFrames returns the total number of frames in the slice.
func (s *SliceIterator) TotalFrames() int {
	return len(s.frames)
}

// Close is a no-op for SliceIterator.
func (s *SliceIterator) Close() {}

// ---------------------------------------------------------------------------
// TrimIterator — applies in/out point trimming to another iterator.
// ---------------------------------------------------------------------------

// TrimIterator wraps another FrameIterator and applies in/out point trimming.
type TrimIterator struct {
	inner   FrameIterator
	inMs    int64
	outMs   int64
	basePTS int64
	started bool
}

// NewTrimIterator wraps inner with in/out point trimming in milliseconds.
// inMs=0 means start from beginning; outMs=0 means play to end.
func NewTrimIterator(inner FrameIterator, inMs, outMs int64) *TrimIterator {
	return &TrimIterator{
		inner: inner,
		inMs:  inMs,
		outMs: outMs,
	}
}

// Next returns the next frame within the trim range, or (nil, nil) at end.
func (t *TrimIterator) Next() (*DecodedFrame, error) {
	inPTS := t.inMs * 90  // ms → 90kHz ticks
	outPTS := t.outMs * 90 // ms → 90kHz ticks

	for {
		frame, err := t.inner.Next()
		if err != nil {
			return nil, err
		}
		if frame == nil {
			return nil, nil
		}

		// Record basePTS from the first frame seen.
		if !t.started {
			t.basePTS = frame.PTS
			t.started = true
		}

		rel := frame.PTS - t.basePTS

		// Check out point first (past the out point = end of stream).
		if t.outMs > 0 && rel >= outPTS {
			return nil, nil
		}

		// Skip frames before in point.
		if t.inMs > 0 && rel < inPTS {
			continue
		}

		return frame, nil
	}
}

// EstimateFPS delegates to the inner iterator.
func (t *TrimIterator) EstimateFPS() float64 {
	return t.inner.EstimateFPS()
}

// TotalFrames returns -1 (unknown after trim).
func (t *TrimIterator) TotalFrames() int {
	return -1
}

// Close closes the inner iterator.
func (t *TrimIterator) Close() {
	t.inner.Close()
}

// ---------------------------------------------------------------------------
// FakeAssetProvider — test helper that returns SliceIterators.
// ---------------------------------------------------------------------------

// FakeAssetProvider is a test AssetProvider that returns SliceIterators
// over a fixed set of frames, regardless of the asset URI.
type FakeAssetProvider struct {
	frames []DecodedFrame
}

// NewFakeAssetProvider creates an AssetProvider that always returns the given frames.
func NewFakeAssetProvider(frames []DecodedFrame) *FakeAssetProvider {
	return &FakeAssetProvider{frames: frames}
}

// Open returns a new SliceIterator over the provider's frames.
func (f *FakeAssetProvider) Open(assetURI string) (FrameIterator, error) {
	return NewSliceIterator(f.frames), nil
}

// ---------------------------------------------------------------------------
// CacheAssetProvider — opens assets from the local disk cache.
// ---------------------------------------------------------------------------

// CacheAssetProvider opens assets from the local disk cache.
// It looks up the cached file by URI, then uses a demux function to get frames.
type CacheAssetProvider struct {
	cache   *Cache
	demuxFn func(path string) ([]DecodedFrame, error)
}

// NewCacheAssetProvider creates an AssetProvider backed by the disk cache.
// demuxFn decodes a local file path into frames. May be nil (will error on Open).
func NewCacheAssetProvider(cache *Cache, demuxFn func(path string) ([]DecodedFrame, error)) *CacheAssetProvider {
	return &CacheAssetProvider{cache: cache, demuxFn: demuxFn}
}

// Open looks up the asset in the cache and decodes it via demuxFn.
func (p *CacheAssetProvider) Open(assetURI string) (FrameIterator, error) {
	entry, err := p.cache.Get(assetURI)
	if err != nil {
		return nil, fmt.Errorf("cache lookup: %w", err)
	}
	if entry.State != AssetCached {
		return nil, ErrAssetNotReady
	}
	if p.demuxFn == nil {
		return nil, fmt.Errorf("no demux function configured")
	}
	frames, err := p.demuxFn(entry.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("demux %s: %w", entry.LocalPath, err)
	}
	return NewSliceIterator(frames), nil
}
