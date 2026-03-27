// Package shmring provides a POSIX shared memory SPSC ring buffer
// for passing PCM audio between the switchframe server and the ASR sidecar.
package shmring

import (
	"sync/atomic"
	"unsafe"
)

const (
	// Magic is the header magic value: "ASRH" in little-endian.
	Magic uint32 = 0x41535248

	// HeaderSize is the fixed size of the ring buffer header (256 bytes).
	HeaderSize = 256

	// DefaultCapacity is the default ring buffer capacity in float32 samples.
	// 192K samples = ~2 seconds of 48kHz stereo audio.
	DefaultCapacity = 192 * 1024
)

// header is the in-memory layout of the ring buffer header, mapped directly
// onto the beginning of the shared memory segment. Fields accessed by both
// producer and consumer use atomic operations via unsafe.Pointer arithmetic.
//
// Layout (256 bytes):
//
//	Offset  Field           Type      Notes
//	0x00    magic           uint32    0x41535248 ("ASRH")
//	0x04    _pad0           uint32    align to 8
//	0x08    write_pos       uint64    atomic, monotonic byte counter
//	0x10    read_pos        uint64    atomic, monotonic byte counter
//	0x18    sample_rate     uint32    e.g. 48000
//	0x1C    channels        uint32    e.g. 2
//	0x20    base_pts        int64     90kHz PTS of first sample
//	0x28    write_seq       uint64    atomic, monotonic liveness counter
//	0x30    capacity_bytes  uint64    data region size in bytes (power of 2)
//	0x38    reserved        [200]byte pad to 256
type header struct {
	Magic         uint32
	_pad0         uint32
	WritePos      uint64
	ReadPos       uint64
	SampleRate    uint32
	Channels      uint32
	BasePTS       int64
	WriteSeq      uint64
	CapacityBytes uint64
	_reserved     [200]byte
}

// Compile-time check that header is exactly HeaderSize bytes.
var _ [HeaderSize]byte = [unsafe.Sizeof(header{})]byte{}

// HeaderSnapshot is a read-only snapshot of the ring buffer header fields.
type HeaderSnapshot struct {
	Magic      uint32
	WritePos   uint64
	ReadPos    uint64
	SampleRate uint32
	Channels   uint32
	BasePTS    int64
	WriteSeq   uint64
}

// headerWritePos returns a pointer to the WritePos field for atomic operations.
func headerWritePos(base unsafe.Pointer) *uint64 {
	return (*uint64)(unsafe.Add(base, 0x08))
}

// headerReadPos returns a pointer to the ReadPos field for atomic operations.
func headerReadPos(base unsafe.Pointer) *uint64 {
	return (*uint64)(unsafe.Add(base, 0x10))
}

// headerWriteSeq returns a pointer to the WriteSeq field for atomic operations.
func headerWriteSeq(base unsafe.Pointer) *uint64 {
	return (*uint64)(unsafe.Add(base, 0x28))
}

// headerSnapshot reads all header fields, using atomic loads for shared fields.
func headerSnapshot(base unsafe.Pointer) HeaderSnapshot {
	h := (*header)(base)
	return HeaderSnapshot{
		Magic:      h.Magic,
		WritePos:   atomic.LoadUint64(headerWritePos(base)),
		ReadPos:    atomic.LoadUint64(headerReadPos(base)),
		SampleRate: h.SampleRate,
		Channels:   h.Channels,
		BasePTS:    h.BasePTS,
		WriteSeq:   atomic.LoadUint64(headerWriteSeq(base)),
	}
}
