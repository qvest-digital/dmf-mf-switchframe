package mmaputil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAlloc_Basic(t *testing.T) {
	buf, err := Alloc(4096)
	require.NoError(t, err)
	require.Len(t, buf, 4096)
	buf[0] = 0xAB
	buf[4095] = 0xCD
	require.Equal(t, byte(0xAB), buf[0])
	require.Equal(t, byte(0xCD), buf[4095])
	require.NoError(t, Free(buf))
}

func TestAlloc_Large(t *testing.T) {
	size := 20 * 1024 * 1024
	buf, err := Alloc(size)
	require.NoError(t, err)
	require.Len(t, buf, size)
	buf[0] = 1
	buf[size-1] = 2
	require.Equal(t, byte(1), buf[0])
	require.Equal(t, byte(2), buf[size-1])
	require.NoError(t, Free(buf))
}

func TestAlloc_ZeroSize(t *testing.T) {
	_, err := Alloc(0)
	require.Error(t, err)
}

func TestAlloc_NegativeSize(t *testing.T) {
	_, err := Alloc(-1)
	require.Error(t, err)
}

func TestFree_Nil(t *testing.T) {
	require.NoError(t, Free(nil))
}

func TestFree_Empty(t *testing.T) {
	require.NoError(t, Free([]byte{}))
}

func TestWriteAndMapFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	mapped, err := WriteFile(path, data)
	require.NoError(t, err)
	require.Equal(t, data, mapped)
	require.NoError(t, Free(mapped))

	mapped2, err := MapFile(path)
	require.NoError(t, err)
	require.Equal(t, data, mapped2)
	require.NoError(t, Free(mapped2))
}

func TestMapFile_NotFound(t *testing.T) {
	_, err := MapFile("/nonexistent/path/file.bin")
	require.Error(t, err)
}

func TestMapFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")
	require.NoError(t, os.WriteFile(path, []byte{}, 0644))
	_, err := MapFile(path)
	require.Error(t, err)
}

func TestWriteFile_LargeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.bin")
	size := 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	mapped, err := WriteFile(path, data)
	require.NoError(t, err)
	require.Equal(t, data, mapped)
	require.NoError(t, Free(mapped))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, int64(size), fi.Size())

	mapped2, err := MapFile(path)
	require.NoError(t, err)
	for i := 0; i < size; i++ {
		if mapped2[i] != byte(i%251) {
			t.Fatalf("mismatch at byte %d: got %d, want %d", i, mapped2[i], byte(i%251))
		}
	}
	require.NoError(t, Free(mapped2))
}
