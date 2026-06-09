package switcher

import (
	"sync"

	"github.com/zsiec/switchframe/server/mmaputil"
)

// FramePool manages a fixed set of pre-allocated pixel buffers.
// Buffers are acquired and released via a mutex-guarded free list.
// The pool is sized at init time and never grows. If all buffers are in
// use, Acquire falls back to a fresh allocation (logged as a pool miss).
//
// Pre-allocated buffers use mmap (MAP_PRIVATE|MAP_ANON) so they live
// outside the Go heap and are invisible to the GC. At 1080p with 512
// buffers this removes ~1.5 GB from GC scanning.
type FramePool struct {
	mu      sync.Mutex
	free    [][]byte // stack of available buffers (LIFO for cache warmth)
	bufSize int      // frame buffer size in bytes (determined by color format)
	cap     int      // total capacity (pre-allocated count)

	// Diagnostics
	hits   uint64
	misses uint64

	// mmapBufs tracks only the mmap-backed pre-allocated buffers so
	// Close can munmap them safely without touching heap-allocated
	// fallback buffers. After Close, it holds the mmap buffers still
	// checked out by in-flight frames; Release munmaps them as they
	// come back.
	mmapBufs [][]byte

	// closed is set by Close. Once closed, Release never recycles buffers
	// (a recycled munmapped buffer handed out by Acquire would fault on
	// the next write) and Acquire always falls back to heap allocation.
	closed bool
}

// NewFramePool creates a pool with n pre-allocated buffers for the given
// dimensions and color format. Buffers are allocated via mmap to avoid GC
// overhead; if mmap fails, the pool falls back to heap allocation.
func NewFramePool(n int, width, height int, colorFmt PipelineColorFormat) *FramePool {
	bufSize := colorFmt.FrameSize(width, height)
	fp := &FramePool{
		free:     make([][]byte, 0, n),
		mmapBufs: make([][]byte, 0, n),
		bufSize:  bufSize,
		cap:      n,
	}
	for i := 0; i < n; i++ {
		buf, err := mmaputil.Alloc(bufSize)
		if err != nil {
			// Fallback to heap allocation if mmap fails.
			buf = make([]byte, bufSize)
		} else {
			fp.mmapBufs = append(fp.mmapBufs, buf)
		}
		fp.free = append(fp.free, buf)
	}
	return fp
}

// Acquire returns a YUV buffer. If the pool is exhausted, allocates fresh.
func (fp *FramePool) Acquire() []byte {
	fp.mu.Lock()
	if len(fp.free) > 0 {
		buf := fp.free[len(fp.free)-1]
		fp.free = fp.free[:len(fp.free)-1]
		fp.hits++
		fp.mu.Unlock()
		return buf[:fp.bufSize]
	}
	fp.misses++
	fp.mu.Unlock()
	return make([]byte, fp.bufSize)
}

// Release returns a buffer to the pool. Wrong-sized buffers are discarded.
// Duplicate releases of the same buffer are detected and silently ignored
// to prevent buffer aliasing corruption.
//
// After Close, buffers are never recycled: an mmap-backed buffer released
// late (by an in-flight frame that outlived Close) is munmapped here, and
// heap buffers are simply discarded.
func (fp *FramePool) Release(buf []byte) {
	if cap(buf) < fp.bufSize {
		return // wrong size — discard
	}
	fp.mu.Lock()
	if fp.closed {
		// Pool is closed — never put the buffer back in the free list.
		// If it is one of our mmap-backed buffers, its holder is done with
		// it now, so complete the deferred munmap.
		ptr := &buf[:cap(buf)][0]
		for i, mb := range fp.mmapBufs {
			if &mb[0] == ptr {
				fp.mmapBufs = append(fp.mmapBufs[:i], fp.mmapBufs[i+1:]...)
				fp.mu.Unlock()
				_ = mmaputil.Free(mb)
				return
			}
		}
		fp.mu.Unlock()
		return
	}
	if len(fp.free) < fp.cap {
		// Check for duplicate (prevent double-release corruption).
		// Scan is O(n) where n ≤ pool capacity (typically 4-16), negligible.
		ptr := &buf[:cap(buf)][0]
		for _, existing := range fp.free {
			if &existing[0] == ptr {
				fp.mu.Unlock()
				return // already in pool — ignore duplicate release
			}
		}
		fp.free = append(fp.free, buf[:cap(buf)])
	}
	// else: pool full — discard (extra fallback allocs draining)
	fp.mu.Unlock()
}

// BufSize returns the buffer size in bytes (for external size checks).
func (fp *FramePool) BufSize() int {
	return fp.bufSize
}

// Stats returns hit/miss counts for diagnostics.
func (fp *FramePool) Stats() (hits, misses uint64) {
	fp.mu.Lock()
	h, m := fp.hits, fp.misses
	fp.mu.Unlock()
	return h, m
}

// Close releases the pre-allocated buffers currently in the free list.
// Mmap-backed buffers still checked out by in-flight frames stay mapped —
// they are munmapped by Release when their holders return them (and are
// never recycled). Safe to call during graceful shutdown. After Close,
// Acquire falls back to make() (pool is empty and closed). Idempotent.
func (fp *FramePool) Close() {
	fp.mu.Lock()
	if fp.closed {
		fp.mu.Unlock()
		return
	}
	fp.closed = true

	// Partition mmap buffers into free (munmap now) vs checked out
	// (deferred to Release).
	freeSet := make(map[*byte]struct{}, len(fp.free))
	for _, buf := range fp.free {
		if cap(buf) > 0 {
			freeSet[&buf[:cap(buf)][0]] = struct{}{}
		}
	}
	var toFree, outstanding [][]byte
	for _, buf := range fp.mmapBufs {
		if _, ok := freeSet[&buf[0]]; ok {
			toFree = append(toFree, buf)
		} else {
			outstanding = append(outstanding, buf)
		}
	}
	fp.free = fp.free[:0]
	fp.mmapBufs = outstanding
	fp.mu.Unlock()

	for _, buf := range toFree {
		_ = mmaputil.Free(buf)
	}
}
