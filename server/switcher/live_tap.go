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

type liveTapEntry struct {
	lastRead     atomic.Int64 // UnixNano
	frame        atomic.Pointer[LiveTapFrame]
	warnedFormat atomic.Bool
}

// ArmLiveTap starts or keeps copying sourceKey's pictures without reading
// one, so a live item that is about to air has a picture on its first
// tick. Like a read, it lasts liveTapIdle.
func (s *Switcher) ArmLiveTap(sourceKey string) {
	s.armLiveTap(sourceKey)
}

func (s *Switcher) armLiveTap(sourceKey string) *liveTapEntry {
	v, _ := s.liveTaps.entries.LoadOrStore(sourceKey, &liveTapEntry{})
	e := v.(*liveTapEntry)
	e.lastRead.Store(time.Now().UnixNano())
	return e
}

// ReadLiveTap returns a copy of the latest picture of sourceKey, or nil
// before one has arrived. The first read of a source nothing armed starts
// copying its pictures as they arrive, so it returns nil. Each call returns a buffer
// of its own, since the caller ingests it into the pipeline like any
// produced frame. Builds with a GPU source manager feed that cache
// instead, so this stays empty there.
func (s *Switcher) ReadLiveTap(sourceKey string) *LiveTapFrame {
	f := s.armLiveTap(sourceKey).frame.Load()
	if f == nil {
		return nil
	}
	out := *f
	out.Data = append([]byte(nil), f.Data...)
	return &out
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
	e.frame.Store(&LiveTapFrame{
		Data:   append([]byte(nil), pf.Data...),
		Width:  pf.Width,
		Height: pf.Height,
		PTS:    pf.PTS,
	})
}
