package switcher

import (
	"sync"

	"github.com/zsiec/switchframe/server/mmaputil"
)

// FramePool manages a fixed set of pre-allocated YUV420 buffers.
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
	bufSize int      // YUV420 buffer size (width * height * 3/2)
	cap     int      // total capacity (pre-allocated count)

	// Diagnostics
	hits   uint64
	misses uint64

	// mmapBufs tracks only the mmap-backed pre-allocated buffers so
	// Close can munmap them safely without touching heap-allocated
	// fallback buffers.
	mmapBufs [][]byte
}

// NewFramePool creates a pool with n pre-allocated buffers of the given
// YUV420 dimensions. Buffers are allocated via mmap to avoid GC overhead;
// if mmap fails, the pool falls back to heap allocation.
func NewFramePool(n int, width, height int) *FramePool {
	bufSize := width * height * 3 / 2
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
func (fp *FramePool) Release(buf []byte) {
	if cap(buf) < fp.bufSize {
		return // wrong size — discard
	}
	fp.mu.Lock()
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

// Close releases all pre-allocated buffers. Mmap-backed buffers are
// munmapped. Safe to call during graceful shutdown. After Close,
// Acquire falls back to make() (pool is empty).
func (fp *FramePool) Close() {
	fp.mu.Lock()
	fp.free = fp.free[:0]
	mmapBufs := fp.mmapBufs
	fp.mmapBufs = nil
	fp.mu.Unlock()

	for _, buf := range mmapBufs {
		_ = mmaputil.Free(buf)
	}
}
