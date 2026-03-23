package stinger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteAndLoadCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")

	// Create a small test clip.
	clip := &Clip{
		Name:     "test",
		Width:    8,
		Height:   4,
		CutPoint: 0.5,
		Frames: []Frame{
			{
				YUV:   make([]byte, 8*4*3/2), // 48 bytes
				Alpha: make([]byte, 8*4),     // 32 bytes
			},
			{
				YUV:   make([]byte, 8*4*3/2),
				Alpha: make([]byte, 8*4),
			},
		},
	}
	// Fill with test data.
	for i := range clip.Frames[0].YUV {
		clip.Frames[0].YUV[i] = byte(i)
	}
	for i := range clip.Frames[0].Alpha {
		clip.Frames[0].Alpha[i] = byte(i + 100)
	}
	for i := range clip.Frames[1].YUV {
		clip.Frames[1].YUV[i] = byte(i + 50)
	}
	for i := range clip.Frames[1].Alpha {
		clip.Frames[1].Alpha[i] = byte(i + 200)
	}

	// Write cache.
	err := WriteCache(path, clip)
	require.NoError(t, err)

	// Verify file exists and has expected size.
	fi, err := os.Stat(path)
	require.NoError(t, err)
	expectedSize := int64(cacheHeaderSize + 2*(8*4*3/2+8*4))
	require.Equal(t, expectedSize, fi.Size())

	// Load cache.
	loaded, err := LoadCache(path)
	require.NoError(t, err)
	require.Equal(t, 8, loaded.Width)
	require.Equal(t, 4, loaded.Height)
	require.Equal(t, 0.5, loaded.CutPoint)
	require.Len(t, loaded.Frames, 2)
	require.Equal(t, clip.Frames[0].YUV, loaded.Frames[0].YUV)
	require.Equal(t, clip.Frames[0].Alpha, loaded.Frames[0].Alpha)
	require.Equal(t, clip.Frames[1].YUV, loaded.Frames[1].YUV)
	require.Equal(t, clip.Frames[1].Alpha, loaded.Frames[1].Alpha)

	// Verify mmap backing is set.
	require.NotNil(t, loaded.mmapBacking)

	require.NoError(t, loaded.Close())
}

func TestWriteAndLoadCache_PreservesCutPoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")

	clip := &Clip{
		Width:    4,
		Height:   2,
		CutPoint: 0.75,
		Frames: []Frame{
			{
				YUV:   make([]byte, 4*2*3/2),
				Alpha: make([]byte, 4*2),
			},
		},
	}

	require.NoError(t, WriteCache(path, clip))
	loaded, err := LoadCache(path)
	require.NoError(t, err)
	require.Equal(t, 0.75, loaded.CutPoint)
	require.NoError(t, loaded.Close())
}

func TestLoadCache_InvalidMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.bin")
	// Write enough data to pass the header size check but with wrong magic.
	data := make([]byte, 64)
	copy(data[0:4], "BAAD")
	require.NoError(t, os.WriteFile(path, data, 0644))
	_, err := LoadCache(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid magic")
}

func TestLoadCache_WrongVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "badver.bin")
	data := make([]byte, 64)
	copy(data[0:4], cacheMagic)
	data[4] = 99 // version 99
	require.NoError(t, os.WriteFile(path, data, 0644))
	_, err := LoadCache(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported version")
}

func TestLoadCache_TooShort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.bin")
	require.NoError(t, os.WriteFile(path, []byte("SFST"), 0644))
	_, err := LoadCache(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too short")
}

func TestLoadCache_SizeMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mismatch.bin")

	// Write a valid header but not enough frame data.
	clip := &Clip{
		Width:    8,
		Height:   4,
		CutPoint: 0.5,
		Frames: []Frame{
			{
				YUV:   make([]byte, 8*4*3/2),
				Alpha: make([]byte, 8*4),
			},
		},
	}
	require.NoError(t, WriteCache(path, clip))

	// Truncate the file to create a size mismatch.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data[:len(data)-5], 0644))

	_, err = LoadCache(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "size")
}

func TestClipClose_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	clip := &Clip{
		Width:    8,
		Height:   4,
		CutPoint: 0.5,
		Frames: []Frame{
			{
				YUV:   make([]byte, 8*4*3/2),
				Alpha: make([]byte, 8*4),
			},
		},
	}
	require.NoError(t, WriteCache(path, clip))

	loaded, err := LoadCache(path)
	require.NoError(t, err)
	require.NoError(t, loaded.Close())
	require.NoError(t, loaded.Close()) // idempotent
	require.Nil(t, loaded.mmapBacking)
	require.Nil(t, loaded.Frames)
}

func TestClipClose_NonMmap(t *testing.T) {
	// Close on a non-mmap clip is a no-op.
	clip := &Clip{
		Width:  8,
		Height: 4,
		Frames: []Frame{
			{
				YUV:   make([]byte, 48),
				Alpha: make([]byte, 32),
			},
		},
	}
	require.NoError(t, clip.Close())
	// Frames should NOT be cleared for non-mmap clips.
	require.Len(t, clip.Frames, 1)
}

func TestLoadCache_NoFile(t *testing.T) {
	_, err := LoadCache("/nonexistent/path/test.bin")
	require.Error(t, err)
}

func TestWriteAndLoadCache_EmptyFrames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")

	clip := &Clip{
		Width:    8,
		Height:   4,
		CutPoint: 0.5,
		Frames:   []Frame{},
	}

	require.NoError(t, WriteCache(path, clip))

	loaded, err := LoadCache(path)
	require.NoError(t, err)
	require.Len(t, loaded.Frames, 0)
	require.NoError(t, loaded.Close())
}
