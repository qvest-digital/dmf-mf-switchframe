package playout

import (
	"log/slog"
	"sync"
)

// IdleLoop wraps an AssetProvider with auto-rewind capability. When it reaches
// the end of the clip, it re-opens and starts from the beginning. If no asset
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
	ySize := w * h
	uvSize := ySize / 4
	black := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		black[i] = 16 // BT.709 Y black
	}
	for i := ySize; i < len(black); i++ {
		black[i] = 128 // BT.709 UV neutral
	}

	return &IdleLoop{
		assetURI: assetURI,
		provider: provider,
		width:    w,
		height:   h,
		black:    black,
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
		iter, err := l.provider.Open(l.assetURI)
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

	// EOF — auto-rewind: close current iterator and re-open.
	l.iter.Close()
	l.iter = nil

	iter, err := l.provider.Open(l.assetURI)
	if err != nil {
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

// blackFrame returns a DecodedFrame backed by the pre-allocated black buffer.
// Must be called with l.mu held.
func (l *IdleLoop) blackFrame() *DecodedFrame {
	pts := l.pts
	l.pts += int64(90000.0 / l.fps)
	return &DecodedFrame{
		YUV:    l.black,
		PTS:    pts,
		Width:  l.width,
		Height: l.height,
	}
}
