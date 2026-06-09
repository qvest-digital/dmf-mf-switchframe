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
		_ = shmClose(fd)
		_ = shmUnlink(name)
		return nil, fmt.Errorf("shmring: %w", err)
	}

	// Map it into memory.
	base, err := shmMmap(fd, totalSize)
	if err != nil {
		_ = shmClose(fd)
		_ = shmUnlink(name)
		return nil, fmt.Errorf("shmring: %w", err)
	}

	// Close the fd; the mmap keeps the segment alive.
	_ = shmClose(fd)

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

	// Set metadata on first write. Published with atomic stores so a
	// cross-process consumer (the C++ ASR sidecar) observes a consistent,
	// fully-written value rather than a torn or zero field. These stores are
	// sequenced before the WritePos advance below, so any consumer that sees
	// WritePos > 0 is guaranteed to also see the metadata.
	if !w.initialized {
		atomic.StoreUint32(headerSampleRate(w.base), uint32(sampleRate))
		atomic.StoreUint32(headerChannels(w.base), uint32(channels))
		atomic.StoreInt64(headerBasePTS(w.base), pts)
		w.initialized = true
	}

	writePos := atomic.LoadUint64(headerWritePos(w.base))
	mask := w.capacityBytes - 1 // works because capacity is power-of-2
	dataBase := unsafe.Add(w.base, HeaderSize)

	src := unsafe.Pointer(unsafe.SliceData(pcm))
	nbytes := uint64(len(pcm) * 4)

	// A single write larger than the ring can hold would overwrite its own
	// oldest bytes — and, in the wrap branch, write past the end of the mmap'd
	// segment. Keep only the newest capacityBytes (the older bytes would be
	// overwritten anyway) while still advancing WritePos by the full logical
	// amount so the consumer fast-forwards correctly.
	if nbytes > w.capacityBytes {
		drop := nbytes - w.capacityBytes
		src = unsafe.Add(src, int(drop))
		writePos += drop
		nbytes = w.capacityBytes
	}

	// Publish the end position of this write BEFORE copying any bytes
	// (seqlock-style). A consumer that copied a region overlapping the
	// physical slots this write targets re-checks WriteReserve after its copy:
	// if the reserve extends past readPos+capacity, the copied data may be
	// torn and is discarded. Publishing only after the copy (as WritePos is)
	// would make an in-flight overwrite invisible to the consumer.
	atomic.StoreUint64(headerWriteReserve(w.base), writePos+nbytes)

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
