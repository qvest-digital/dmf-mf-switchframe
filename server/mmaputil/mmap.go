// Package mmaputil provides memory-mapped allocation utilities.
// Returned byte slices live outside the Go heap (via mmap), so the garbage
// collector never scans them. Callers must call Free on every returned slice
// when it is no longer needed.
//
// IMPORTANT: Free unmaps using the slice's base pointer and length, which must
// match the original mapping exactly. Callers MUST retain the exact slice
// returned by Alloc/WriteFile/MapFile and pass it unmodified to Free. Passing a
// reslice (e.g. buf[:n] or buf[off:]) to Free is a bug: depending on the
// platform it may unmap only part of the region or fail, silently leaking the
// mapping. Reslice freely for reads/writes, but free the original.
package mmaputil

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Alloc returns an anonymous mmap region of the given size.
// The returned slice is backed by memory outside the Go heap
// (MAP_PRIVATE|MAP_ANON), so the GC will not scan it.
// The caller must call Free when the slice is no longer needed.
func Alloc(size int) ([]byte, error) {
	if size <= 0 {
		return nil, errors.New("mmaputil: size must be positive")
	}
	b, err := syscall.Mmap(-1, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, fmt.Errorf("mmaputil: anonymous mmap(%d): %w", size, err)
	}
	return b, nil
}

// Free releases a memory-mapped region previously returned by Alloc,
// WriteFile, or MapFile. It is a no-op for nil or empty slices.
//
// The slice passed MUST be the exact slice returned by the allocating call,
// not a reslice of it: munmap requires the original base pointer and length.
// Passing a reslice may partially unmap or fail and leak the mapping.
func Free(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	if err := syscall.Munmap(b); err != nil {
		return fmt.Errorf("mmaputil: munmap: %w", err)
	}
	return nil
}

// WriteFile writes data to path, then memory-maps the file back read-only.
// The returned slice is outside the Go heap. The caller must call Free
// when the slice is no longer needed.
func WriteFile(path string, data []byte) ([]byte, error) {
	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, fmt.Errorf("mmaputil: write %s: %w", path, err)
	}
	return MapFile(path)
}

// MapFile memory-maps an existing file read-only and returns its contents
// as a byte slice outside the Go heap. Returns an error if the file does
// not exist or is empty. The caller must call Free when done.
func MapFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mmaputil: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("mmaputil: stat %s: %w", path, err)
	}

	size := int(fi.Size())
	if size == 0 {
		return nil, fmt.Errorf("mmaputil: %s is empty", path)
	}

	b, err := syscall.Mmap(int(f.Fd()), 0, size,
		syscall.PROT_READ, syscall.MAP_PRIVATE)
	if err != nil {
		return nil, fmt.Errorf("mmaputil: mmap %s: %w", path, err)
	}
	return b, nil
}
