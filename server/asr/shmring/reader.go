//go:build !windows

package shmring

import (
	"errors"
	"fmt"
	"sync/atomic"
	"unsafe"
)

// Reader is the consumer side of a POSIX shared memory SPSC ring buffer.
// It opens an existing shared memory segment created by a Writer.
// The reader does NOT own the segment lifecycle — Close unmaps but does not unlink.
type Reader struct {
	base          unsafe.Pointer // mmap'd base address
	totalSize     int            // total mmap'd size (header + data)
	capacityBytes uint64         // data region size in bytes (power of 2)
	readPos       uint64         // local monotonic read position (bytes)
	closed        bool
}

// NewReader opens an existing POSIX shared memory segment by name.
// Returns an error if the segment doesn't exist or the magic doesn't match.
func NewReader(name string) (*Reader, error) {
	if len(name) == 0 || name[0] != '/' {
		return nil, errors.New("shmring: name must start with '/'")
	}

	// Open existing segment (O_RDWR for writing ReadPos).
	fd, err := shmOpen(name, false)
	if err != nil {
		return nil, fmt.Errorf("shmring: %w", err)
	}

	// Get the segment size via fstat.
	totalSize, err := shmFstat(fd)
	if err != nil {
		shmClose(fd)
		return nil, fmt.Errorf("shmring: %w", err)
	}

	if totalSize < HeaderSize {
		shmClose(fd)
		return nil, fmt.Errorf("shmring: segment too small (%d bytes, need at least %d)", totalSize, HeaderSize)
	}

	// Map into memory.
	base, err := shmMmap(fd, totalSize)
	if err != nil {
		shmClose(fd)
		return nil, fmt.Errorf("shmring: %w", err)
	}

	// Close the fd; the mmap keeps the segment accessible.
	shmClose(fd)

	// Validate magic. Read into a local var before any potential munmap,
	// since h points into the mmap'd region.
	h := (*header)(base)
	magic := h.Magic
	if magic != Magic {
		shmMunmap(base, totalSize)
		return nil, fmt.Errorf("shmring: invalid magic 0x%08X, want 0x%08X", magic, Magic)
	}

	capacityBytes := h.CapacityBytes
	if capacityBytes == 0 || capacityBytes&(capacityBytes-1) != 0 {
		shmMunmap(base, totalSize)
		return nil, fmt.Errorf("shmring: invalid capacity %d (must be a positive power of 2)", capacityBytes)
	}

	// Initialize local readPos from the shared ReadPos field.
	readPos := atomic.LoadUint64(headerReadPos(base))

	return &Reader{
		base:          base,
		totalSize:     totalSize,
		capacityBytes: capacityBytes,
		readPos:       readPos,
	}, nil
}

// Read returns all available samples since the last Read call.
// If the writer has lapped the reader (overflow), fast-forwards to the latest data.
// Returns nil if no new data is available (reader is caught up).
func (r *Reader) Read() []float32 {
	writePos := atomic.LoadUint64(headerWritePos(r.base))
	readPos := r.readPos

	if writePos <= readPos {
		return nil
	}

	available := writePos - readPos

	// Overflow detection: writer has lapped the reader.
	if available > r.capacityBytes {
		// Fast-forward to the oldest still-valid data.
		readPos = writePos - r.capacityBytes
		available = r.capacityBytes
	}

	// available is in bytes; convert to float32 sample count.
	nsamples := int(available / 4)
	if nsamples == 0 {
		return nil
	}

	result := make([]float32, nsamples)
	mask := r.capacityBytes - 1
	dataBase := unsafe.Add(r.base, HeaderSize)

	offset := readPos & mask
	tailSpace := r.capacityBytes - offset
	nbytes := available

	if nbytes <= tailSpace {
		// No wrap: single copy.
		src := unsafe.Add(dataBase, int(offset))
		dst := unsafe.Pointer(unsafe.SliceData(result))
		copyBytes(dst, src, int(nbytes))
	} else {
		// Wrap: split into two copies.
		dst := unsafe.Pointer(unsafe.SliceData(result))

		// First: copy from offset to end of buffer.
		src1 := unsafe.Add(dataBase, int(offset))
		copyBytes(dst, src1, int(tailSpace))

		// Second: copy from beginning of buffer.
		src2 := dataBase
		dst2 := unsafe.Add(dst, int(tailSpace))
		copyBytes(dst2, src2, int(nbytes-tailSpace))
	}

	// Advance readPos (monotonic).
	newReadPos := readPos + available
	r.readPos = newReadPos
	atomic.StoreUint64(headerReadPos(r.base), newReadPos)

	return result
}

// SampleRate returns the sample rate from the header.
func (r *Reader) SampleRate() uint32 {
	h := (*header)(r.base)
	return h.SampleRate
}

// Channels returns the channel count from the header.
func (r *Reader) Channels() uint32 {
	h := (*header)(r.base)
	return h.Channels
}

// Close unmaps the shared memory segment.
// It does NOT unlink the segment — the writer owns the lifecycle.
func (r *Reader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return shmMunmap(r.base, r.totalSize)
}
