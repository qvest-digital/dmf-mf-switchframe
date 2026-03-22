package stmap

import (
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"github.com/zsiec/switchframe/server/mmaputil"
)

// NewProcessorCached creates a Processor whose LUT slices are backed by
// a memory-mapped cache file. If cachePath already exists with the
// correct size, the file is mmap'd and slices point directly into it
// (zero-copy). Otherwise the LUTs are computed from stmap, serialized
// as flat little-endian int32, and written to cachePath via mmaputil.WriteFile.
//
// If stmap is nil and cachePath exists, the processor is loaded from
// cache only (the STMap's float32 S/T arrays are not available, but
// the int32 LUTs are). If stmap is nil and no cache exists, an error
// is returned.
//
// The caller must call Close() to munmap the backing when the Processor
// is no longer needed.
func NewProcessorCached(stmap *STMap, cachePath string) (*Processor, error) {
	// Try loading from existing cache first.
	if fi, err := os.Stat(cachePath); err == nil {
		fileSize := fi.Size()
		if stmap != nil {
			expected := expectedCacheSize(stmap.Width, stmap.Height)
			if fileSize == int64(expected) {
				return loadFromCache(stmap, cachePath)
			}
			// Size mismatch — stmap changed, recompute below.
		} else {
			// No stmap — load from cache, infer dimensions from file size.
			return loadFromCacheInferred(cachePath)
		}
	} else if stmap == nil {
		return nil, fmt.Errorf("stmap: no cache at %s and stmap is nil", cachePath)
	}

	// Compute LUTs from stmap and write to cache.
	p := &Processor{stmap: stmap}
	p.buildLUT()

	data := serializeLUTs(p.lutSX, p.lutSY, p.lutCSX, p.lutCSY)

	mapped, err := mmaputil.WriteFile(cachePath, data)
	if err != nil {
		return nil, fmt.Errorf("stmap: write cache %s: %w", cachePath, err)
	}

	// Replace heap LUT slices with mmap-backed views.
	w := stmap.Width
	h := stmap.Height
	n := w * h
	cn := (w / 2) * (h / 2)

	p.lutSX = byteSliceToInt32(mapped[0 : n*4])
	p.lutSY = byteSliceToInt32(mapped[n*4 : 2*n*4])
	p.lutCSX = byteSliceToInt32(mapped[2*n*4 : (2*n+cn)*4])
	p.lutCSY = byteSliceToInt32(mapped[(2*n+cn)*4 : (2*n+2*cn)*4])
	p.mmapBacking = mapped

	return p, nil
}

// Close releases the mmap backing if present. Safe to call multiple times.
func (p *Processor) Close() error {
	if p.mmapBacking == nil {
		return nil
	}
	err := mmaputil.Free(p.mmapBacking)
	p.mmapBacking = nil
	p.lutSX = nil
	p.lutSY = nil
	p.lutCSX = nil
	p.lutCSY = nil
	return err
}

// expectedCacheSize returns the expected file size in bytes for a cache
// file holding LUTs for the given dimensions.
// Layout: [lutSX: w*h*4][lutSY: w*h*4][lutCSX: cw*ch*4][lutCSY: cw*ch*4]
func expectedCacheSize(w, h int) int {
	n := w * h
	cn := (w / 2) * (h / 2)
	return (2*n + 2*cn) * 4
}

// loadFromCache mmap's an existing cache file and creates a Processor
// with LUT slices pointing into the mapped region. stmap may be nil.
func loadFromCache(stmap *STMap, cachePath string) (*Processor, error) {
	mapped, err := mmaputil.MapFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("stmap: load cache %s: %w", cachePath, err)
	}

	w := stmap.Width
	h := stmap.Height
	expected := expectedCacheSize(w, h)
	if len(mapped) != expected {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stmap: cache %s size %d != expected %d", cachePath, len(mapped), expected)
	}

	n := w * h
	cn := (w / 2) * (h / 2)

	return &Processor{
		stmap:       stmap,
		lutSX:       byteSliceToInt32(mapped[0 : n*4]),
		lutSY:       byteSliceToInt32(mapped[n*4 : 2*n*4]),
		lutCSX:      byteSliceToInt32(mapped[2*n*4 : (2*n+cn)*4]),
		lutCSY:      byteSliceToInt32(mapped[(2*n+cn)*4 : (2*n+2*cn)*4]),
		mmapBacking: mapped,
	}, nil
}

// loadFromCacheInferred mmap's an existing cache file and infers
// the LUT layout from the file size. Used when stmap is nil.
//
// File size = (2*n + 2*(n/4)) * 4 = 10*n bytes, where n = w*h.
func loadFromCacheInferred(cachePath string) (*Processor, error) {
	mapped, err := mmaputil.MapFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("stmap: load cache %s: %w", cachePath, err)
	}

	fileSize := len(mapped)
	// n = fileSize / 10 (since fileSize = 10*n)
	if fileSize%10 != 0 || fileSize == 0 {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stmap: cache %s size %d is not valid (must be 10*n for some n)", cachePath, fileSize)
	}
	n := fileSize / 10
	cn := n / 4

	// Verify: (2*n + 2*cn) * 4 == fileSize
	if (2*n+2*cn)*4 != fileSize {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stmap: cache %s internal consistency check failed", cachePath)
	}

	return &Processor{
		lutSX:       byteSliceToInt32(mapped[0 : n*4]),
		lutSY:       byteSliceToInt32(mapped[n*4 : 2*n*4]),
		lutCSX:      byteSliceToInt32(mapped[2*n*4 : (2*n+cn)*4]),
		lutCSY:      byteSliceToInt32(mapped[(2*n+cn)*4 : (2*n+2*cn)*4]),
		mmapBacking: mapped,
	}, nil
}

// serializeLUTs writes the four LUT arrays as flat little-endian int32.
func serializeLUTs(sx, sy, csx, csy []int32) []byte {
	total := (len(sx) + len(sy) + len(csx) + len(csy)) * 4
	buf := make([]byte, total)
	off := 0
	for _, lut := range [][]int32{sx, sy, csx, csy} {
		for _, v := range lut {
			binary.LittleEndian.PutUint32(buf[off:], uint32(v))
			off += 4
		}
	}
	return buf
}

// byteSliceToInt32 reinterprets a byte slice as an int32 slice without
// copying. The byte slice must be aligned (mmap'd regions are page-aligned)
// and its length must be a multiple of 4.
//
// IMPORTANT: This assumes native little-endian byte order, matching the
// little-endian serialization in serializeLUTs. Both target platforms
// (linux/amd64, linux/arm64, darwin/arm64) are little-endian.
func byteSliceToInt32(b []byte) []int32 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*int32)(unsafe.Pointer(&b[0])), len(b)/4)
}
