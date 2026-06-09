package stinger

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/mmaputil"
)

const cacheMagic = "SFST"
const cacheVersion = 1
const cacheHeaderSize = 28 // 4 (magic) + 4 (version) + 4 (width) + 4 (height) + 4 (frameCount) + 8 (cutPoint)

// WriteCache writes a clip's decoded frames to a flat binary cache file.
//
// File format:
//
//	[magic: "SFST" 4 bytes][version: uint32 LE][width: uint32 LE][height: uint32 LE]
//	[frameCount: uint32 LE][cutPoint: float64 LE]
//	[frame0 YUV: video.YUV420FrameSize(w,h) bytes][frame0 Alpha: w*h bytes]
//	[frame1 YUV: video.YUV420FrameSize(w,h) bytes][frame1 Alpha: w*h bytes]
//	...
func WriteCache(path string, clip *Clip) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("stinger cache: create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Write header.
	var hdr [cacheHeaderSize]byte
	copy(hdr[0:4], cacheMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], cacheVersion)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(clip.Width))
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(clip.Height))
	binary.LittleEndian.PutUint32(hdr[16:20], uint32(len(clip.Frames)))
	binary.LittleEndian.PutUint64(hdr[20:28], math.Float64bits(clip.CutPoint))
	if _, err := f.Write(hdr[:]); err != nil {
		return fmt.Errorf("stinger cache: write header: %w", err)
	}

	// Stream frames directly to file (avoids large heap allocation).
	for i := range clip.Frames {
		if _, err := f.Write(clip.Frames[i].YUV); err != nil {
			return fmt.Errorf("stinger cache: write frame %d YUV: %w", i, err)
		}
		if _, err := f.Write(clip.Frames[i].Alpha); err != nil {
			return fmt.Errorf("stinger cache: write frame %d alpha: %w", i, err)
		}
	}

	return f.Close()
}

// LoadCache loads a clip from a cache file via mmap. The returned Clip's
// Frame.YUV and Frame.Alpha slices point into the mmap'd region.
// Call clip.Close() to munmap when done.
func LoadCache(path string) (*Clip, error) {
	mapped, err := mmaputil.MapFile(path)
	if err != nil {
		return nil, fmt.Errorf("stinger cache: mmap %s: %w", path, err)
	}

	if len(mapped) < cacheHeaderSize {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stinger cache: %s too short (%d bytes)", path, len(mapped))
	}

	// Validate magic.
	magic := string(mapped[0:4])
	if magic != cacheMagic {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stinger cache: %s invalid magic %q", path, magic)
	}

	// Validate version.
	version := binary.LittleEndian.Uint32(mapped[4:8])
	if version != cacheVersion {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stinger cache: %s unsupported version %d", path, version)
	}

	w := int(binary.LittleEndian.Uint32(mapped[8:12]))
	h := int(binary.LittleEndian.Uint32(mapped[12:16]))
	frameCount := int(binary.LittleEndian.Uint32(mapped[16:20]))
	cutPoint := math.Float64frombits(binary.LittleEndian.Uint64(mapped[20:28]))

	// The header fields come from an on-disk file that can be truncated or
	// corrupted; compute frame sizes with explicit overflow checks so a crafted
	// w/h/frameCount cannot wrap past the size guard below and cause the
	// per-frame slicing (mapped[off:off+...]) to run off the mapped region.
	// w, h and frameCount are read from uint32, so each fits non-negative in a
	// 64-bit int, but the products (w*h up to ~1.8e19, then *3, then
	// *frameCount) can exceed int64. Guard every multiply with a division-based
	// overflow check; legitimate sizes can never exceed len(mapped) (the size of
	// the mmap'd file) anyway, so any overflow here means the file is corrupt.
	alphaSize := w * h
	if w != 0 && alphaSize/w != h {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stinger cache: %s dimensions %dx%d overflow", path, w, h)
	}
	// video.YUV420FrameSize computes alphaSize*3/2; guard the *3 so the helper
	// cannot overflow, keeping it as the single source of truth for the formula.
	if alphaSize > math.MaxInt/3 {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stinger cache: %s dimensions %dx%d overflow", path, w, h)
	}
	yuvSize := video.YUV420FrameSize(w, h)
	frameSize := yuvSize + alphaSize // <= alphaSize*5/2 < alphaSize*3, cannot overflow

	// expectedSize = cacheHeaderSize + frameCount*frameSize, computed defensively:
	// detect frameCount*frameSize overflow before it can coincidentally equal
	// len(mapped) and slip past the size guard below.
	frameBytes := frameCount * frameSize
	if frameSize != 0 && frameBytes/frameSize != frameCount {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stinger cache: %s frame data size overflow (count=%d, size=%d)", path, frameCount, frameSize)
	}
	expectedSize := cacheHeaderSize + frameBytes
	if expectedSize < frameBytes { // cacheHeaderSize + frameBytes wrapped
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stinger cache: %s total size overflow (count=%d, size=%d)", path, frameCount, frameSize)
	}

	if len(mapped) != expectedSize {
		_ = mmaputil.Free(mapped)
		return nil, fmt.Errorf("stinger cache: %s size %d != expected %d", path, len(mapped), expectedSize)
	}

	frames := make([]Frame, frameCount)
	off := cacheHeaderSize
	for i := 0; i < frameCount; i++ {
		frames[i] = Frame{
			YUV:   mapped[off : off+yuvSize],
			Alpha: mapped[off+yuvSize : off+yuvSize+alphaSize],
		}
		off += frameSize
	}

	return &Clip{
		Width:       w,
		Height:      h,
		CutPoint:    cutPoint,
		Frames:      frames,
		mmapBacking: mapped,
	}, nil
}
