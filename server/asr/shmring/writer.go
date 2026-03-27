//go:build !windows

package shmring

import (
	"errors"
	"fmt"
	"sync/atomic"
	"unsafe"
)

// Writer is the producer side of a POSIX shared memory SPSC ring buffer.
// It creates and owns the shared memory segment lifecycle.
type Writer struct {
	name          string
	base          unsafe.Pointer // mmap'd base address
	totalSize     int            // total mmap'd size (header + data)
	capacityBytes uint64         // data region size in bytes (power of 2)
	initialized   bool           // whether BasePTS/SampleRate/Channels have been set
	closed        bool
}

// NewWriter creates a POSIX shared memory segment and mmaps it.
// name must start with '/'. capacitySamples is rounded up to the next power of 2.
func NewWriter(name string, capacitySamples int) (*Writer, error) {
	if len(name) == 0 || name[0] != '/' {
		return nil, errors.New("shmring: name must start with '/'")
	}
	if capacitySamples <= 0 {
		return nil, errors.New("shmring: capacity must be positive")
	}

	// Round up to next power of 2.
	cap := nextPowerOf2(capacitySamples)
	capacityBytes := cap * 4 // sizeof(float32)
	totalSize := HeaderSize + capacityBytes

	// Clean up any stale segment from a previous crash.
	_ = shmUnlink(name)

	// Create the shared memory segment.
	fd, err := shmOpen(name, true)
	if err != nil {
		return nil, fmt.Errorf("shmring: %w", err)
	}

	// Set the size.
	if err := shmFtruncate(fd, totalSize); err != nil {
		shmClose(fd)
		shmUnlink(name)
		return nil, fmt.Errorf("shmring: %w", err)
	}

	// Map it into memory.
	base, err := shmMmap(fd, totalSize)
	if err != nil {
		shmClose(fd)
		shmUnlink(name)
		return nil, fmt.Errorf("shmring: %w", err)
	}

	// Close the fd; the mmap keeps the segment alive.
	shmClose(fd)

	// Initialize the header.
	h := (*header)(base)
	h.Magic = Magic
	h.CapacityBytes = uint64(capacityBytes)
	// WritePos, ReadPos, WriteSeq are zero (mmap'd memory is zero-filled).

	return &Writer{
		name:          name,
		base:          base,
		totalSize:     totalSize,
		capacityBytes: uint64(capacityBytes),
	}, nil
}

// Write copies PCM float32 samples into the ring buffer and advances WritePos
// atomically. On the first call, it sets SampleRate, Channels, and BasePTS in
// the header.
func (w *Writer) Write(pcm []float32, pts int64, sampleRate, channels int) {
	if len(pcm) == 0 {
		return
	}

	// Set metadata on first write.
	if !w.initialized {
		h := (*header)(w.base)
		h.SampleRate = uint32(sampleRate)
		h.Channels = uint32(channels)
		h.BasePTS = pts
		w.initialized = true
	}

	writePos := atomic.LoadUint64(headerWritePos(w.base))
	mask := w.capacityBytes - 1 // works because capacity is power-of-2
	dataBase := unsafe.Add(w.base, HeaderSize)

	src := unsafe.Pointer(unsafe.SliceData(pcm))
	nbytes := uint64(len(pcm) * 4)

	// Calculate how much space is available before the wrap point.
	offset := writePos & mask
	tailSpace := w.capacityBytes - offset

	if nbytes <= tailSpace {
		// No wrap: single copy.
		dst := unsafe.Add(dataBase, int(offset))
		copyBytes(dst, src, int(nbytes))
	} else {
		// Wrap: split into two copies.
		// First: fill to end of buffer.
		dst1 := unsafe.Add(dataBase, int(offset))
		copyBytes(dst1, src, int(tailSpace))
		// Second: wrap to beginning.
		dst2 := dataBase
		src2 := unsafe.Add(src, int(tailSpace))
		copyBytes(dst2, src2, int(nbytes-tailSpace))
	}

	// Advance write position atomically.
	newWritePos := writePos + nbytes
	atomic.StoreUint64(headerWritePos(w.base), newWritePos)

	// Bump liveness counter.
	atomic.AddUint64(headerWriteSeq(w.base), 1)
}

// Header returns a snapshot of the current header fields.
func (w *Writer) Header() HeaderSnapshot {
	return headerSnapshot(w.base)
}

// Capacity returns the capacity in float32 samples.
func (w *Writer) Capacity() int {
	return int(w.capacityBytes / 4)
}

// Close unmaps the shared memory region and unlinks the segment.
// The writer owns the segment lifecycle, so Close removes it.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	var errs []error
	if err := shmMunmap(w.base, w.totalSize); err != nil {
		errs = append(errs, err)
	}
	if err := shmUnlink(w.name); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	return n + 1
}

// copyBytes copies n bytes from src to dst using a simple byte-level copy.
//
//go:nosplit
func copyBytes(dst, src unsafe.Pointer, n int) {
	dstSlice := unsafe.Slice((*byte)(dst), n)
	srcSlice := unsafe.Slice((*byte)(src), n)
	copy(dstSlice, srcSlice)
}
