package playout

import (
	"log/slog"
	"sync"

	"github.com/zsiec/switchframe/server/internal/video"
)

// NewIdleFrame10 creates a YUV422P10LE black frame.
// BT.709 limited-range: Y=64 (black), Cb/Cr=512 (neutral chroma).
// Layout: Y plane (w*h*2 bytes) + Cb plane (w/2*h*2 bytes) + Cr plane (w/2*h*2 bytes).
// Total size: w*h*4 bytes.
func NewIdleFrame10(width, height int) []byte {
	return video.NewBlackFrame(video.YUV422_10bit, width, height)
}

// makeBlackFrame10 allocates a BT.709-legal black YUV422P10LE DecodedFrame.
func makeBlackFrame10(w, h int) *DecodedFrame {
	return &DecodedFrame{
		YUV:        NewIdleFrame10(w, h),
		Width:      w,
		Height:     h,
		IsKeyframe: true,
	}
}

// IdleLoop10 wraps an AssetProvider with auto-rewind for 10-bit output.
// When no asset is configured or the asset fails, it returns pre-allocated
// 10-bit black frames (BT.709: Y=64, Cb/Cr=512).
//
// IdleLoop10 does NOT run its own goroutine -- it is called synchronously per tick.
// It is safe for concurrent use.
type IdleLoop10 struct {
	mu       sync.Mutex
	assetURI string
	provider AssetProvider
	iter     FrameIterator
	width    int
	height   int
	black    []byte // pre-allocated 10-bit BT.709 black frame
	pts      int64
	fps      float64
	dirty    bool
}

// NewIdleLoop10 creates a 10-bit idle loop. If assetURI is empty, only black
// frames are produced. width/height define the black frame dimensions.
func NewIdleLoop10(assetURI string, provider AssetProvider, w, h int) *IdleLoop10 {
	return &IdleLoop10{
		assetURI: assetURI,
		provider: provider,
		width:    w,
		height:   h,
		black:    NewIdleFrame10(w, h),
		fps:      30.0,
	}
}

// NextFrame returns the next frame. Never returns nil -- falls back to black.
// Safe for concurrent use.
func (l *IdleLoop10) NextFrame() *DecodedFrame {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.dirty {
		if l.iter != nil {
			l.iter.Close()
			l.iter = nil
		}
		l.dirty = false
	}

	if l.assetURI == "" || l.provider == nil {
		return l.blackFrame()
	}

	if l.iter == nil {
		iter, err := openLooping(l.provider, l.assetURI)
		if err != nil {
			slog.Debug("playout: 10-bit idle loop asset open failed, returning black", "uri", l.assetURI, "error", err)
			return l.blackFrame()
		}
		l.iter = iter
		l.fps = iter.EstimateFPS()
	}

	frame, err := l.iter.Next()
	if err != nil {
		slog.Debug("playout: 10-bit idle loop frame read error, returning black", "error", err)
		l.iter.Close()
		l.iter = nil
		return l.blackFrame()
	}

	if frame != nil {
		frame.PTS = l.pts
		l.pts += int64(90000.0 / l.fps)
		return frame
	}

	// EOF -- auto-rewind, in place when the iterator can.
	iter, err := restartLoop(l.iter, l.provider, l.assetURI)
	if err != nil {
		l.iter = nil
		slog.Debug("playout: 10-bit idle loop rewind failed, returning black", "uri", l.assetURI, "error", err)
		return l.blackFrame()
	}
	l.iter = iter
	l.fps = iter.EstimateFPS()

	frame, err = l.iter.Next()
	if err != nil || frame == nil {
		slog.Debug("playout: 10-bit idle loop first frame after rewind failed, returning black", "error", err)
		l.iter.Close()
		l.iter = nil
		return l.blackFrame()
	}

	frame.PTS = l.pts
	l.pts += int64(90000.0 / l.fps)
	return frame
}

// Close releases the current iterator.
func (l *IdleLoop10) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.iter != nil {
		l.iter.Close()
		l.iter = nil
	}
}

// SetAsset changes the idle asset.
func (l *IdleLoop10) SetAsset(assetURI string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.assetURI = assetURI
	l.dirty = true
}

// blackFrame returns a DecodedFrame with a fresh copy of the pre-allocated
// 10-bit black buffer. Must be called with l.mu held.
func (l *IdleLoop10) blackFrame() *DecodedFrame {
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
