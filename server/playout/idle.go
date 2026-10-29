package playout

import (
	"log/slog"
	"sync"

	"github.com/zsiec/switchframe/server/internal/video"
)

// IdleLoop wraps an AssetProvider with auto-rewind capability. When it reaches
// the end of the clip, it starts from the beginning again. If no asset
// is configured or the asset fails to open, it returns a pre-allocated black
// frame (BT.709: Y=16, UV=128).
//
// IdleLoop does NOT run its own goroutine — it is called synchronously per tick.
// It is safe for concurrent use.
type IdleLoop struct {
	mu       sync.Mutex
	assetURI string
	provider AssetProvider
	iter     FrameIterator
	width    int
	height   int
	black    []byte // pre-allocated BT.709 black frame
	pts      int64  // incrementing PTS for idle frames
	fps      float64
	dirty    bool // true when SetAsset was called and iter needs re-open
}

// NewIdleLoop creates an idle loop. If assetURI is empty, only black frames
// are produced. width/height define the black frame dimensions.
func NewIdleLoop(assetURI string, provider AssetProvider, w, h int) *IdleLoop {
	return &IdleLoop{
		assetURI: assetURI,
		provider: provider,
		width:    w,
		height:   h,
		black:    video.NewBlackFrame(video.YUV420_8bit, w, h),
		fps:      30.0,
	}
}

// NextFrame returns the next frame. Never returns nil — falls back to black.
// Safe for concurrent use.
func (l *IdleLoop) NextFrame() *DecodedFrame {
	l.mu.Lock()
	defer l.mu.Unlock()

	// If SetAsset was called, close the current iterator so we re-open.
	if l.dirty {
		if l.iter != nil {
			l.iter.Close()
			l.iter = nil
		}
		l.dirty = false
	}

	// No asset configured — return black.
	if l.assetURI == "" || l.provider == nil {
		return l.blackFrame()
	}

	// Open iterator on first call or after rewind.
	if l.iter == nil {
		iter, err := openLooping(l.provider, l.assetURI)
		if err != nil {
			slog.Debug("playout: idle loop asset open failed, returning black", "uri", l.assetURI, "error", err)
			return l.blackFrame()
		}
		l.iter = iter
		l.fps = iter.EstimateFPS()
	}

	// Try to read the next frame.
	frame, err := l.iter.Next()
	if err != nil {
		slog.Debug("playout: idle loop frame read error, returning black", "error", err)
		l.iter.Close()
		l.iter = nil
		return l.blackFrame()
	}

	if frame != nil {
		// Got a valid frame — update PTS and return it.
		frame.PTS = l.pts
		l.pts += int64(90000.0 / l.fps)
		return frame
	}

	// EOF -- auto-rewind, in place when the iterator can.
	iter, err := restartLoop(l.iter, l.provider, l.assetURI)
	if err != nil {
		l.iter = nil
		slog.Debug("playout: idle loop rewind failed, returning black", "uri", l.assetURI, "error", err)
		return l.blackFrame()
	}
	l.iter = iter
	l.fps = iter.EstimateFPS()

	frame, err = l.iter.Next()
	if err != nil || frame == nil {
		slog.Debug("playout: idle loop first frame after rewind failed, returning black", "error", err)
		l.iter.Close()
		l.iter = nil
		return l.blackFrame()
	}

	frame.PTS = l.pts
	l.pts += int64(90000.0 / l.fps)
	return frame
}

// openLooping opens uri to play over and over.
func openLooping(provider AssetProvider, uri string) (FrameIterator, error) {
	iter, err := provider.Open(uri)
	if err != nil {
		return nil, err
	}
	if l, ok := iter.(LoopingIterator); ok {
		l.SetLoop(true)
	}
	return iter, nil
}

// restartLoop starts the next pass of iter, which has reported its end: in
// place when it can rewind, else by closing it and reopening uri.
func restartLoop(iter FrameIterator, provider AssetProvider, uri string) (FrameIterator, error) {
	if l, ok := iter.(LoopingIterator); ok && l.Rewind() == nil {
		return iter, nil
	}
	iter.Close()
	return openLooping(provider, uri)
}

// Close releases the current iterator.
func (l *IdleLoop) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.iter != nil {
		l.iter.Close()
		l.iter = nil
	}
}

// SetAsset changes the idle asset. Closes the current iterator and opens the
// new one on the next NextFrame call.
func (l *IdleLoop) SetAsset(assetURI string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.assetURI = assetURI
	l.dirty = true
}

// blackFrame returns a DecodedFrame with a fresh copy of the pre-allocated
// black buffer. Each caller gets its own copy so downstream consumers can
// mutate the YUV data without corrupting the shared template.
// Must be called with l.mu held.
func (l *IdleLoop) blackFrame() *DecodedFrame {
	yuvCopy := make([]byte, len(l.black))
	copy(yuvCopy, l.black)
	pts := l.pts
	l.pts += int64(90000.0 / l.fps)
	return &DecodedFrame{
		YUV:        yuvCopy,
		PTS:        pts,
		Width:      l.width,
		Height:     l.height,
		IsKeyframe: true,
	}
}
