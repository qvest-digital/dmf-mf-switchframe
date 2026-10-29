package switcher

import (
	"sync"
	"sync/atomic"
	"time"
)

// liveTapIdle is how long a source keeps being copied after its last read
// or arm. A playout channel reads its live source, and arms the one it airs
// next, every tick, so a source untouched for this long is neither.
const liveTapIdle = 2 * time.Second

// LiveTapFrame is a copy of a source's latest 8-bit YUV420 picture.
type LiveTapFrame struct {
	Data   []byte
	Width  int
	Height int
	PTS    int64
}

// liveTaps holds the latest picture of each source a playout channel airs
// as a live item, the CPU counterpart of the GPU source manager's frame
// cache. Only sources read within liveTapIdle are copied, so sources nothing
// airs cost nothing.
type liveTaps struct {
	entries sync.Map // source key -> *liveTapEntry
}

// liveTapEntry keeps two buffers per source and swaps them, so a source
// costs no allocation per picture: the writer fills the spare, which no
// reader can see, and publishes it under mu, which waits for reads of the
// one it replaces to finish.
type liveTapEntry struct {
	lastRead     atomic.Int64 // UnixNano
	warnedFormat atomic.Bool

	writeMu sync.Mutex    // one writer fills spare at a time
	spare   liveTapBuffer // guarded by writeMu

	mu  sync.RWMutex
	cur liveTapBuffer // guarded by mu; data is nil before the first picture
}

type liveTapBuffer struct {
	data          []byte
	width, height int
	pts           int64
}

// ArmLiveTap starts or keeps copying sourceKey's pictures without reading
// one, so a live item that is about to air has a picture on its first
// tick. Like a read, it lasts liveTapIdle.
func (s *Switcher) ArmLiveTap(sourceKey string) {
	s.armLiveTap(sourceKey)
}

func (s *Switcher) armLiveTap(sourceKey string) *liveTapEntry {
	v, ok := s.liveTaps.entries.Load(sourceKey)
	if !ok {
		v, _ = s.liveTaps.entries.LoadOrStore(sourceKey, &liveTapEntry{})
	}
	e := v.(*liveTapEntry)
	e.lastRead.Store(time.Now().UnixNano())
	return e
}

// ReadLiveTap copies the latest picture of sourceKey into dst, which it
// grows only if dst is too small, and reports false before one has
// arrived. The first read of a source nothing armed starts copying its
// pictures as they arrive, so it reports false. The caller owns the copy:
// it ingests it into the pipeline like any produced frame. Builds with a
// GPU source manager feed that cache instead, so this stays empty there.
func (s *Switcher) ReadLiveTap(sourceKey string, dst []byte) (LiveTapFrame, bool) {
	e := s.armLiveTap(sourceKey)
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cur.data == nil {
		return LiveTapFrame{}, false
	}
	return LiveTapFrame{
		Data:   append(dst[:0], e.cur.data...),
		Width:  e.cur.width,
		Height: e.cur.height,
		PTS:    e.cur.pts,
	}, true
}

// recordLiveTap keeps a copy of pf when a live item reads sourceKey. The
// copy is needed because pf's buffer goes back to the frame pool. A
// 10-bit picture is not kept: playout carries 8-bit YUV420 only.
func (s *Switcher) recordLiveTap(sourceKey string, pf *ProcessingFrame) {
	v, ok := s.liveTaps.entries.Load(sourceKey)
	if !ok {
		return
	}
	e := v.(*liveTapEntry)
	if time.Since(time.Unix(0, e.lastRead.Load())) > liveTapIdle {
		s.liveTaps.entries.CompareAndDelete(sourceKey, e)
		return
	}
	if pf.Format != ColorYUV420_8bit {
		// The channel airs its idle picture and reports the source as not
		// available, which reads as a missing or unconnected input.
		if e.warnedFormat.CompareAndSwap(false, true) {
			s.log.Warn("playout live item cannot air a source of this format without a GPU",
				"source", sourceKey, "format", pf.Format)
		}
		return
	}
	if len(pf.Data) != ColorYUV420_8bit.FrameSize(pf.Width, pf.Height) {
		return
	}
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	e.spare = liveTapBuffer{
		data:   append(e.spare.data[:0], pf.Data...),
		width:  pf.Width,
		height: pf.Height,
		pts:    pf.PTS,
	}
	e.mu.Lock()
	e.cur, e.spare = e.spare, e.cur
	e.mu.Unlock()
}
