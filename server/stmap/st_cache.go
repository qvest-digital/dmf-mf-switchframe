package stmap

import (
	"encoding/binary"
	"math"
	"os"
	"unsafe"

	"github.com/zsiec/switchframe/server/mmaputil"
)

// loadOrWriteSTCache loads pre-built float32 S/T arrays from a .st cache file,
// or writes the file from the STMap's float32 data and mmap's it back.
//
// File format: [S: w*h × float32 LE][T: w*h × float32 LE]
// At 1080p: 1920*1080*4*2 = 16,588,800 bytes (~15.8 MB).
func loadOrWriteSTCache(path string, stmap *STMap) stArrayEntry {
	if stmap == nil {
		return stArrayEntry{}
	}

	n := stmap.Width * stmap.Height
	expectedSize := n * 4 * 2

	// Try loading existing cache.
	if fi, err := os.Stat(path); err == nil && fi.Size() == int64(expectedSize) {
		if entry, err := loadSTCache(path, n); err == nil {
			return entry
		}
	}

	// Write cache from float32 data.
	buf := make([]byte, expectedSize)
	off := 0
	for _, v := range stmap.S {
		binary.LittleEndian.PutUint32(buf[off:], math.Float32bits(v))
		off += 4
	}
	for _, v := range stmap.T {
		binary.LittleEndian.PutUint32(buf[off:], math.Float32bits(v))
		off += 4
	}

	mapped, err := mmaputil.WriteFile(path, buf)
	if err != nil {
		return stArrayEntry{}
	}

	return stArrayEntry{
		S:       byteSliceToFloat32(mapped[:n*4]),
		T:       byteSliceToFloat32(mapped[n*4:]),
		backing: mapped,
	}
}

// loadSTCache mmap's an existing .st file and returns float32 S/T slices.
func loadSTCache(path string, n int) (stArrayEntry, error) {
	mapped, err := mmaputil.MapFile(path)
	if err != nil {
		return stArrayEntry{}, err
	}
	return stArrayEntry{
		S:       byteSliceToFloat32(mapped[:n*4]),
		T:       byteSliceToFloat32(mapped[n*4:]),
		backing: mapped,
	}, nil
}

// byteSliceToFloat32 reinterprets a byte slice as a float32 slice (zero-copy).
// The byte slice must be page-aligned (mmap'd) and sized as a multiple of 4.
//
// IMPORTANT: Assumes native little-endian byte order, matching the LE
// serialization in loadOrWriteSTCache. Both target platforms (linux/amd64,
// linux/arm64, darwin/arm64) are little-endian.
func byteSliceToFloat32(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(&b[0])), len(b)/4)
}
