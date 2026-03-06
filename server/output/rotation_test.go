package output

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Integration-level rotation tests that verify TS packet alignment, per-file
// byte counts, and multi-rotation naming consistency. The unit-level rotation
// tests live in recorder_test.go; these tests focus on observable properties
// of the output files themselves.

func TestRotation_TimeBasedCreatesMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		RotateAfter: 50 * time.Millisecond,
	})
	require.NoError(t, r.Start(nil))
	defer r.Close()

	// Write TS-aligned data for ~200ms to trigger multiple rotations.
	packet := make([]byte, 188)
	packet[0] = 0x47 // TS sync byte

	start := time.Now()
	for time.Since(start) < 200*time.Millisecond {
		_, err := r.Write(packet)
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, r.Close())

	// Verify multiple .ts files were created.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 2, "expected at least 2 rotated files")

	// Verify each file starts with the 0x47 sync byte and is 188-byte aligned.
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		require.NotEmpty(t, data, "file %s should not be empty", entry.Name())
		require.Equal(t, byte(0x47), data[0],
			"file %s should start with TS sync byte 0x47", entry.Name())
		require.Equal(t, 0, len(data)%188,
			"file %s should be 188-byte aligned (got %d bytes)", entry.Name(), len(data))
	}
}

func TestRotation_SizeBasedCreatesMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	maxSize := int64(188 * 10) // 1880 bytes
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: maxSize,
	})
	require.NoError(t, r.Start(nil))
	defer r.Close()

	// Write 30 TS packets. With maxSize=188*10, after 10 packets the file
	// is at the limit, and the 11th write triggers rotation.
	packet := make([]byte, 188)
	packet[0] = 0x47
	for i := 0; i < 30; i++ {
		_, err := r.Write(packet)
		require.NoError(t, err)
	}
	require.NoError(t, r.Close())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 2, "expected at least 2 files from 30 packets with max 10 packets/file")

	// Verify per-file byte counts: each file except possibly the last
	// should be <= maxSize + 1 packet (188 bytes).
	for _, entry := range entries {
		info, err := entry.Info()
		require.NoError(t, err)
		require.LessOrEqual(t, info.Size(), maxSize+188,
			"file %s size %d exceeds maxFileSize+1 packet", entry.Name(), info.Size())
	}

	// Verify TS alignment in each file.
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		require.NotEmpty(t, data)
		require.Equal(t, byte(0x47), data[0],
			"file %s should start with 0x47 sync byte", entry.Name())
		require.Equal(t, 0, len(data)%188,
			"file %s should be 188-byte aligned", entry.Name())
	}
}

func TestRotation_SequentialNamingAcrossThreeRotations(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: 188, // rotate after every packet
	})
	require.NoError(t, r.Start(nil))
	defer r.Close()

	packet := make([]byte, 188)
	packet[0] = 0x47

	// Collect filenames. With MaxFileSize=188:
	// - Initial file is _001
	// - First write fills _001 to the limit
	// - Second write rotates to _002, third to _003, fourth to _004
	names := []string{r.Filename()}
	for i := 0; i < 4; i++ {
		_, err := r.Write(packet)
		require.NoError(t, err)
		names = append(names, r.Filename())
	}

	// Verify we have at least 3 distinct filenames (_001, _002, _003).
	uniqueNames := make(map[string]bool)
	for _, n := range names {
		uniqueNames[n] = true
	}
	require.GreaterOrEqual(t, len(uniqueNames), 3,
		"expected at least 3 unique filenames, got %v", uniqueNames)

	// Verify sequential ordering.
	require.Regexp(t, `_001\.ts$`, names[0], "first file should be _001")

	// Find the indices of _002 and _003.
	found002, found003 := false, false
	for _, n := range names {
		if matched, _ := filepath.Match("*_002.ts", n); matched {
			found002 = true
		}
		if matched, _ := filepath.Match("*_003.ts", n); matched {
			found003 = true
		}
	}
	require.True(t, found002, "should have a _002 file")
	require.True(t, found003, "should have a _003 file")

	// Verify all filenames match the expected format.
	for _, n := range names {
		require.Regexp(t, `^program_\d{8}_\d{6}_\d{3}\.ts$`, n,
			"filename %q should match program_YYYYMMDD_HHMMSS_NNN.ts", n)
	}
}

func TestRotation_NewFileContainsParsableTS(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: 188 * 3, // rotate after 3 packets
	})
	require.NoError(t, r.Start(nil))
	defer r.Close()

	// Write valid TS packets with different PIDs to differentiate them.
	for i := 0; i < 10; i++ {
		pkt := makeTSPacket(uint16(0x100+i), i%3 == 0)
		_, err := r.Write(pkt)
		require.NoError(t, err)
	}
	require.NoError(t, r.Close())

	// Read all files and verify every file's data is parsable as TS packets.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 2, "expected at least 2 files")

	totalPackets := 0
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		require.NotEmpty(t, data, "file %s should not be empty", entry.Name())

		// Verify the file is an exact multiple of 188 bytes.
		require.Equal(t, 0, len(data)%188,
			"file %s has %d bytes, not 188-byte aligned", entry.Name(), len(data))

		// Walk each 188-byte packet and check the sync byte.
		for offset := 0; offset+188 <= len(data); offset += 188 {
			require.Equal(t, byte(0x47), data[offset],
				"file %s packet at offset %d missing sync byte", entry.Name(), offset)
			totalPackets++
		}
	}
	require.Equal(t, 10, totalPackets, "total packets across all files should be 10")
}
