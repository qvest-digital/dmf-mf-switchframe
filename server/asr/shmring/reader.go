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
		_ = shmClose(fd)
		return nil, fmt.Errorf("shmring: %w", err)
	}

	if totalSize < HeaderSize {
		_ = shmClose(fd)
		return nil, fmt.Errorf("shmring: segment too small (%d bytes, need at least %d)", totalSize, HeaderSize)
	}

	// Map into memory.
	base, err := shmMmap(fd, totalSize)
	if err != nil {
		_ = shmClose(fd)
		return nil, fmt.Errorf("shmring: %w", err)
	}

	// Close the fd; the mmap keeps the segment accessible.
	_ = shmClose(fd)

	// Validate magic. Read into a local var before any potential munmap,
	// since h points into the mmap'd region.
	h := (*header)(base)
	magic := h.Magic
	if magic != Magic {
		_ = shmMunmap(base, totalSize)
		return nil, fmt.Errorf("shmring: invalid magic 0x%08X, want 0x%08X", magic, Magic)
	}

	capacityBytes := h.CapacityBytes
	if capacityBytes == 0 || capacityBytes&(capacityBytes-1) != 0 {
		_ = shmMunmap(base, totalSize)
		return nil, fmt.Errorf("shmring: invalid capacity %d (must be a positive power of 2)", capacityBytes)
	}

	// CapacityBytes is written by another process; every unsafe read in this
	// reader is bounded by it, so it must fit the fstat-derived mapping or
	// copyOut would read past the end of the segment (SIGBUS or garbage).
	if capacityBytes > uint64(totalSize)-HeaderSize {
		_ = shmMunmap(base, totalSize)
		return nil, fmt.Errorf("shmring: capacity %d exceeds segment size %d (header %d)", capacityBytes, totalSize, HeaderSize)
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
//
// Torn-read protection (seqlock-style): the single producer writes forward and
// laps the buffer. A read copies the logical span [readPos, writePos); the
// producer overwrites the physical slot of logical position p once a write
// reaches logical position p+capacityBytes. The producer publishes the END of
// each write to WriteReserve BEFORE copying any bytes, and to WritePos only
// AFTER the copy completes, so:
//
//   - data in [reserve-capacityBytes, writePos) is stable: the in-flight write
//     (if any) targets the physical slots of [writePos, reserve), which alias
//     logical positions below reserve-capacityBytes, never this span;
//   - after copying we re-read WriteReserve: if it extends past
//     readPos+capacityBytes, a write that began during our copy may have
//     overwritten the start of the copied region, so we discard the
//     possibly-torn result and re-sync — the caller simply reads again.
//
// Strict ">" in both checks: a reader exactly capacityBytes behind an idle
// producer (reserve == readPos+capacityBytes) has not had its oldest slot
// overwritten and is returned intact.
//
// This reader is the production consumer (the switchframe-asr sidecar drains
// the ring with it); the ring is lossy by design under sustained overrun, but
// every returned batch is an exact, untorn span of the written stream.
func (r *Reader) Read() []float32 {
	writePos := atomic.LoadUint64(headerWritePos(r.base))
	readPos := r.readPos

	if writePos <= readPos {
		return nil
	}

	// reserve is the end of the furthest write that may have started; data at
	// logical positions below reserve-capacityBytes may be mid-overwrite.
	// max() with writePos keeps compatibility with segments written before
	// WriteReserve existed (field reads as 0).
	reserve := max(atomic.LoadUint64(headerWriteReserve(r.base)), writePos)

	// Overflow / in-flight overwrite detection: fast-forward past any region
	// the producer has already (possibly partially) reclaimed.
	if reserve > readPos+r.capacityBytes {
		readPos = reserve - r.capacityBytes
		if writePos <= readPos {
			// The entire published region is being rewritten; nothing is
			// safely readable until the producer publishes the new WritePos.
			r.readPos = readPos
			atomic.StoreUint64(headerReadPos(r.base), readPos)
			return nil
		}
	}
	available := writePos - readPos

	// available is in bytes; convert to float32 sample count.
	nsamples := int(available / 4)
	if nsamples == 0 {
		return nil
	}

	result := make([]float32, nsamples)
	r.copyOut(result, readPos, available)

	// Seqlock-style re-validation: if a write that may overwrite the start of
	// the region we just copied has begun (reserve beyond readPos+capacityBytes),
	// discard the possibly-torn result and re-sync for the next Read.
	reserveAfter := max(atomic.LoadUint64(headerWriteReserve(r.base)), atomic.LoadUint64(headerWritePos(r.base)))
	if reserveAfter > readPos+r.capacityBytes {
		newReadPos := reserveAfter - r.capacityBytes
		r.readPos = newReadPos
		atomic.StoreUint64(headerReadPos(r.base), newReadPos)
		return nil
	}

	// Advance readPos (monotonic).
	newReadPos := readPos + available
	r.readPos = newReadPos
	atomic.StoreUint64(headerReadPos(r.base), newReadPos)

	return result
}

// copyOut copies `nbytes` bytes of ring data starting at logical position
// `from` into result, handling the physical wrap. result must hold at least
// nbytes/4 float32 samples and from+nbytes must not exceed one capacity span.
func (r *Reader) copyOut(result []float32, from, nbytes uint64) {
	mask := r.capacityBytes - 1
	dataBase := unsafe.Add(r.base, HeaderSize)

	offset := from & mask
	tailSpace := r.capacityBytes - offset
	dst := unsafe.Pointer(unsafe.SliceData(result))

	if nbytes <= tailSpace {
		// No wrap: single copy.
		src := unsafe.Add(dataBase, int(offset))
		copyBytes(dst, src, int(nbytes))
		return
	}

	// Wrap: split into two copies.
	// First: copy from offset to end of buffer.
	src1 := unsafe.Add(dataBase, int(offset))
	copyBytes(dst, src1, int(tailSpace))

	// Second: copy from beginning of buffer.
	dst2 := unsafe.Add(dst, int(tailSpace))
	copyBytes(dst2, dataBase, int(nbytes-tailSpace))
}

// SampleRate returns the sample rate from the header.
// Loaded atomically — the producer publishes it once with an atomic store
// ordered before the first WritePos advance, so any reader that has observed
// data also observes the correct sample rate.
func (r *Reader) SampleRate() uint32 {
	return atomic.LoadUint32(headerSampleRate(r.base))
}

// Channels returns the channel count from the header.
// Loaded atomically for the same publication guarantee as SampleRate.
func (r *Reader) Channels() uint32 {
	return atomic.LoadUint32(headerChannels(r.base))
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
