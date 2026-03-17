package switcher

import (
	"sync"
)

// FramePool manages a fixed set of pre-allocated YUV420 buffers.
// Buffers are acquired and released via a mutex-guarded free list.
// The pool is sized at init time and never grows. If all buffers are in
// use, Acquire falls back to a fresh allocation (logged as a pool miss).
type FramePool struct {
	mu      sync.Mutex
	free    [][]byte // stack of available buffers (LIFO for cache warmth)
	bufSize int      // YUV420 buffer size (width * height * 3/2)
	cap     int      // total capacity (pre-allocated count)

	// Diagnostics
	hits   uint64
	misses uint64
}

// NewFramePool creates a pool with n pre-allocated buffers of the given
// YUV420 dimensions.
func NewFramePool(n int, width, height int) *FramePool {
	bufSize := width * height * 3 / 2
	fp := &FramePool{
		free:    make([][]byte, 0, n),
		bufSize: bufSize,
		cap:     n,
	}
	for i := 0; i < n; i++ {
		fp.free = append(fp.free, make([]byte, bufSize))
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
func (fp *FramePool) Release(buf []byte) {
	if cap(buf) < fp.bufSize {
		return // wrong size — discard
	}
	fp.mu.Lock()
	if len(fp.free) < fp.cap {
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

// Close releases all pre-allocated buffers. Safe to call during graceful
// shutdown. After Close, Acquire falls back to make() (pool is empty).
func (fp *FramePool) Close() {
	fp.mu.Lock()
	fp.free = fp.free[:0]
	fp.mu.Unlock()
}
