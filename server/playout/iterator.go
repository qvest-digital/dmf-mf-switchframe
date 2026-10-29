package playout

import (
	"fmt"
	"os"
	"strings"
)

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

// LoopingIterator is a FrameIterator that plays its asset again without
// reopening it. Reopening a file demuxes it again and starts a fresh decoder,
// which returns nothing until its pipeline has filled; on the channel's tick
// goroutine that stalls the output at every pass.
type LoopingIterator interface {
	FrameIterator
	// SetLoop says whether another pass follows the current one.
	SetLoop(loop bool)
	// Rewind starts the next pass at the first frame. It is called once Next
	// has reported the end of the current pass.
	Rewind() error
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

// SetLoop is a no-op: a slice needs nothing to play it again.
func (s *SliceIterator) SetLoop(bool) {}

// Rewind starts the slice over.
func (s *SliceIterator) Rewind() error {
	s.pos = 0
	return nil
}

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
	inPTS := t.inMs * 90   // ms → 90kHz ticks
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

// Seek repositions the iterator to targetFrame on the trimmed timeline (frame
// 0 is the first frame at/after the in-point). It requires the inner iterator
// to support seeking; otherwise an error is returned. Before the inner seek,
// the stream's base PTS is established from the very first inner frame so the
// in/out-point math in Next stays anchored to the true stream start rather
// than the seek target.
func (t *TrimIterator) Seek(targetFrame int) error {
	seeker, ok := t.inner.(interface{ Seek(int) error })
	if !ok {
		return fmt.Errorf("playout: trim seek: inner iterator does not support seeking")
	}
	if targetFrame < 0 {
		targetFrame = 0
	}

	// Anchor basePTS to the stream start. Without this, the first frame Next
	// sees after the seek would become the base, shifting all relative-PTS
	// trim math by the seek offset.
	if !t.started {
		if err := seeker.Seek(0); err != nil {
			return fmt.Errorf("playout: trim seek: rewind: %w", err)
		}
		first, err := t.inner.Next()
		if err != nil {
			return fmt.Errorf("playout: trim seek: read first frame: %w", err)
		}
		if first == nil {
			return fmt.Errorf("playout: trim seek: empty stream")
		}
		t.basePTS = first.PTS
		t.started = true
	}

	// Translate the trimmed-timeline frame index to the full timeline by
	// adding the in-point's frame offset.
	fps := t.inner.EstimateFPS()
	if fps <= 0 {
		fps = 30.0
	}
	inFrames := int(float64(t.inMs) * fps / 1000.0)
	return seeker.Seek(targetFrame + inFrames)
}

// SetLoop passes loop on to the inner iterator.
func (t *TrimIterator) SetLoop(loop bool) {
	if l, ok := t.inner.(LoopingIterator); ok {
		l.SetLoop(loop)
	}
}

// Rewind starts the inner iterator over. The base PTS stays: every pass
// carries the same source PTS.
func (t *TrimIterator) Rewind() error {
	l, ok := t.inner.(LoopingIterator)
	if !ok {
		return fmt.Errorf("playout: trim rewind: inner iterator cannot rewind")
	}
	return l.Rewind()
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
// It looks up the cached file by URI, then uses a demux function to get a FrameIterator.
type CacheAssetProvider struct {
	cache   *Cache
	demuxFn func(path string) (FrameIterator, error)
}

// NewCacheAssetProvider creates an AssetProvider backed by the disk cache.
// demuxFn opens a local file path and returns a streaming FrameIterator.
// May be nil (will error on Open).
func NewCacheAssetProvider(cache *Cache, demuxFn func(path string) (FrameIterator, error)) *CacheAssetProvider {
	return &CacheAssetProvider{cache: cache, demuxFn: demuxFn}
}

// Open looks up the asset in the cache and opens it via demuxFn.
// If the asset is not in the cache, it falls back to opening the file
// directly if the resolved path exists on disk (e.g., clip store files).
func (p *CacheAssetProvider) Open(assetURI string) (FrameIterator, error) {
	// Try cache first.
	entry, err := p.cache.Get(assetURI)
	if err == nil && entry.State == AssetCached {
		if p.demuxFn == nil {
			return nil, fmt.Errorf("no demux function configured")
		}
		return p.demuxFn(entry.LocalPath)
	}

	// Fallback: try as a direct local file path.
	_, path := ParseAssetURI(assetURI)
	if strings.Contains(path, "..") {
		return nil, fmt.Errorf("path traversal rejected: %s", assetURI)
	}
	if p.demuxFn != nil && fileExists(path) {
		return p.demuxFn(path)
	}

	// Original error if cache lookup failed for a reason other than "not found".
	if err != nil {
		return nil, fmt.Errorf("cache lookup: %w", err)
	}
	return nil, ErrAssetNotReady
}

// fileExists returns true if the path exists on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
