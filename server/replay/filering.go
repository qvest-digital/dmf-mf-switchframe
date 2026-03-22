package replay

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// fileRing is a circular file-backed buffer for H.264 replay frames.
// Frame data is written sequentially to a pre-allocated file (typically on tmpfs).
// Only a lightweight in-memory index is kept on the Go heap.
type fileRing struct {
	mu           sync.RWMutex
	f            *os.File
	path         string
	maxBytes     int64
	writePos     int64
	totalWritten int64 // monotonic, for wrap detection

	// In-memory index — small structs, not frame data
	frames []frameEntry
}

// frameEntry is the in-memory metadata for one frame (~64 bytes on heap).
type frameEntry struct {
	offset   int64
	size     int32
	pts      int64
	wallTime time.Time
	isIDR    bool
	writeSeq int64 // monotonic sequence for invalidation on wrap
}

// newFileRing creates a file-backed ring buffer at the given path.
// The file is pre-allocated to maxBytes via ftruncate.
func newFileRing(path string, maxBytes int64) (*fileRing, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("filering: open: %w", err)
	}
	// Pre-allocate to avoid fragmentation.
	if err := f.Truncate(maxBytes); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("filering: truncate: %w", err)
	}
	return &fileRing{
		f:        f,
		path:     path,
		maxBytes: maxBytes,
	}, nil
}

// Write appends frame data to the ring file. Wraps to the beginning
// when reaching maxBytes, invalidating old entries that overlap.
func (r *fileRing) Write(data []byte, pts int64, wallTime time.Time, isIDR bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	size := int64(len(data))
	if size > r.maxBytes {
		return fmt.Errorf("filering: frame too large (%d > %d)", size, r.maxBytes)
	}

	// Check if we need to wrap.
	if r.writePos+size > r.maxBytes {
		r.writePos = 0
	}

	writeStart := r.writePos

	// Write frame data at current position.
	_, err := r.f.WriteAt(data, writeStart)
	if err != nil {
		return fmt.Errorf("filering: write: %w", err)
	}

	r.totalWritten += size
	entry := frameEntry{
		offset:   writeStart,
		size:     int32(size),
		pts:      pts,
		wallTime: wallTime,
		isIDR:    isIDR,
		writeSeq: r.totalWritten,
	}

	// Invalidate any old frames that this write overwrites.
	writeEnd := writeStart + size
	r.invalidateRange(writeStart, writeEnd)

	r.frames = append(r.frames, entry)
	r.writePos = writeStart + size

	return nil
}

// invalidateRange removes frames whose file region overlaps [start, end).
func (r *fileRing) invalidateRange(start, end int64) {
	valid := r.frames[:0]
	for _, f := range r.frames {
		fEnd := f.offset + int64(f.size)
		// Keep frame if it doesn't overlap with the write region.
		if fEnd <= start || f.offset >= end {
			valid = append(valid, f)
		}
	}
	r.frames = valid
}

// Read reads frame data from the ring file at the given offset and size.
// Uses pread (positional read) — thread-safe, no seek.
func (r *fileRing) Read(offset int64, size int32) ([]byte, error) {
	buf := make([]byte, size)
	_, err := r.f.ReadAt(buf, offset)
	if err != nil {
		return nil, fmt.Errorf("filering: read: %w", err)
	}
	return buf, nil
}

// FrameCount returns the number of frames currently in the ring.
func (r *fileRing) FrameCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.frames)
}

// BytesUsed returns the approximate bytes of frame data stored.
func (r *fileRing) BytesUsed() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total int64
	for _, f := range r.frames {
		total += int64(f.size)
	}
	return total
}

// Frames returns a snapshot of all frame entries (for ExtractClip).
func (r *fileRing) Frames() []frameEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]frameEntry, len(r.frames))
	copy(out, r.frames)
	return out
}

// Close closes the file handle. Does NOT remove the file.
func (r *fileRing) Close() error {
	return r.f.Close()
}

// CloseAndRemove closes and deletes the backing file.
func (r *fileRing) CloseAndRemove() error {
	err := r.f.Close()
	os.Remove(r.path)
	return err
}
