package output

import (
	"context"
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
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	// Write TS-aligned keyframe packets for ~200ms to trigger multiple rotations.
	// Keyframes are required since rotation defers until a keyframe arrives.
	packet := makeTSPacket(0x100, true)

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
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	// Write 30 keyframe TS packets. With maxSize=188*10, after 10 packets
	// the file is at the limit, and the 11th write triggers rotation.
	// Keyframes are required since rotation defers until a keyframe arrives.
	packet := makeTSPacket(0x100, true)
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
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	// Keyframe packets required since rotation defers until a keyframe arrives.
	packet := makeTSPacket(0x100, true)

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
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

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

func TestRotation_RotatedPATMPTHasDiscontinuityIndicator(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: 188 * 3, // Rotate after 3 packets.
	})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	// Build a TS payload that includes PAT (PID 0), PMT (PID 0x1000),
	// and video data. The PAT/PMT are payload-only packets (no adaptation field).
	// Real PAT/PMT packets have 0xFF stuffing in their trailing bytes.
	pat := makeTSPacket(0x0000, false) // PAT: adaptation_field_control = 01 (payload only)
	pat[tsPacketSize-1] = 0xFF
	pat[tsPacketSize-2] = 0xFF
	pmt := makeTSPacket(0x1000, false) // PMT: adaptation_field_control = 01 (payload only)
	pmt[tsPacketSize-1] = 0xFF
	pmt[tsPacketSize-2] = 0xFF
	video := makeTSPacket(0x0100, false)

	// Set non-zero CC values on PAT and PMT to verify they are preserved (not reset to 0).
	pat[3] = (pat[3] & 0xF0) | 0x05 // CC = 5
	pmt[3] = (pmt[3] & 0xF0) | 0x0A // CC = 10

	// Write PAT+PMT+video (fills file to limit).
	data := append(append(pat, pmt...), video...)
	_, err := r.Write(data)
	require.NoError(t, err)

	// Write a keyframe to trigger rotation.
	keyframe := makeTSPacket(0x0100, true)
	_, err = r.Write(keyframe)
	require.NoError(t, err)

	secondFile := r.Filename()

	// Close recorder to flush.
	require.NoError(t, r.Close())

	// Read the rotated file.
	content, err := os.ReadFile(filepath.Join(dir, secondFile))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(content), 188*2,
		"rotated file should contain at least PAT+PMT")

	// Verify the first two TS packets (PAT and PMT) have discontinuity_indicator set.
	for i := 0; i < 2; i++ {
		offset := i * tsPacketSize
		pkt := content[offset : offset+tsPacketSize]

		require.Equal(t, byte(0x47), pkt[0], "packet %d: sync byte", i)

		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		require.True(t, pid == 0x0000 || pid == 0x1000,
			"packet %d: expected PAT or PMT PID, got 0x%04X", i, pid)

		// Check adaptation_field_control (bits 5-4 of byte 3).
		// Must be 11 (both adaptation field and payload) or 10 (adaptation only).
		afc := (pkt[3] >> 4) & 0x03
		require.GreaterOrEqual(t, afc, byte(2),
			"packet %d (PID 0x%04X): adaptation_field_control must indicate adaptation field present (got %d)",
			i, pid, afc)

		// Adaptation field length at byte 4 must be >= 1 to hold the flags byte.
		afLen := pkt[4]
		require.GreaterOrEqual(t, afLen, byte(1),
			"packet %d (PID 0x%04X): adaptation_field_length must be >= 1 (got %d)",
			i, pid, afLen)

		// Discontinuity indicator is bit 7 of the adaptation flags byte (byte 5).
		discFlag := pkt[5] & 0x80
		require.NotZero(t, discFlag,
			"packet %d (PID 0x%04X): discontinuity_indicator (bit 7 of byte 5) must be set",
			i, pid)
	}
}

func TestSetTSDiscontinuityIndicator_PayloadOnly(t *testing.T) {
	// Test with a payload-only packet (adaptation_field_control = 01).
	pkt := makeTSPacket(0x0000, false) // PAT, payload only
	pkt[3] = 0x15                      // AFC=01 (payload only), CC=5

	// PAT/PMT packets typically have 0xFF stuffing in their trailing bytes.
	pkt[tsPacketSize-1] = 0xFF
	pkt[tsPacketSize-2] = 0xFF

	require.Equal(t, byte(0x01), (pkt[3]>>4)&0x03,
		"precondition: AFC should be 01 (payload only)")

	setTSDiscontinuityIndicator(pkt)

	// AFC should now be 11 (adaptation + payload).
	afc := (pkt[3] >> 4) & 0x03
	require.Equal(t, byte(3), afc,
		"AFC should be changed to 11 (adaptation + payload)")

	// CC should be preserved.
	cc := pkt[3] & 0x0F
	require.Equal(t, byte(5), cc, "CC should be preserved")

	// Adaptation field length should be 1.
	require.Equal(t, byte(1), pkt[4], "adaptation_field_length should be 1")

	// Discontinuity indicator should be set (bit 7 of flags byte).
	require.NotZero(t, pkt[5]&0x80, "discontinuity_indicator should be set")
}

func TestSetTSDiscontinuityIndicator_AdaptationFieldPresent(t *testing.T) {
	// Test with a packet that already has an adaptation field (e.g., keyframe packet).
	pkt := makeTSPacket(0x0100, true) // Video, adaptation + payload
	require.Equal(t, byte(0x03), (pkt[3]>>4)&0x03,
		"precondition: AFC should be 11 (adaptation + payload)")
	require.Equal(t, byte(0x40), pkt[5]&0x40,
		"precondition: RAI flag should be set")

	// Discontinuity flag should NOT be set initially.
	require.Zero(t, pkt[5]&0x80, "precondition: discontinuity should not be set")

	setTSDiscontinuityIndicator(pkt)

	// AFC should remain 11.
	afc := (pkt[3] >> 4) & 0x03
	require.Equal(t, byte(3), afc, "AFC should remain 11")

	// Discontinuity indicator should now be set.
	require.NotZero(t, pkt[5]&0x80, "discontinuity_indicator should be set")

	// RAI flag should still be set (other flags preserved).
	require.NotZero(t, pkt[5]&0x40, "RAI flag should be preserved")
}

func TestSetTSDiscontinuityIndicator_MultiplePackets(t *testing.T) {
	// Test that it handles a buffer containing multiple TS packets.
	pat := makeTSPacket(0x0000, false)
	pat[tsPacketSize-1] = 0xFF
	pat[tsPacketSize-2] = 0xFF
	pmt := makeTSPacket(0x1000, false)
	pmt[tsPacketSize-1] = 0xFF
	pmt[tsPacketSize-2] = 0xFF
	buf := append(pat, pmt...)

	setTSDiscontinuityIndicator(buf)

	// Both packets should have discontinuity flag set.
	for i := 0; i < 2; i++ {
		offset := i * tsPacketSize
		afc := (buf[offset+3] >> 4) & 0x03
		require.GreaterOrEqual(t, afc, byte(2),
			"packet %d: AFC should indicate adaptation field present", i)
		require.NotZero(t, buf[offset+5]&0x80,
			"packet %d: discontinuity_indicator should be set", i)
	}
}

func TestSetTSDiscontinuityIndicator_PayloadOnlyWithStuffing(t *testing.T) {
	// Test the happy path: payload-only packet where the last 2 bytes ARE
	// 0xFF stuffing. The function should insert the adaptation field and
	// set the discontinuity indicator.
	pkt := makeTSPacket(0x0000, false) // PAT, payload only (AFC=01)
	pkt[3] = 0x15                      // AFC=01, CC=5

	// Fill the trailing bytes with 0xFF stuffing (as PAT/PMT typically have).
	pkt[tsPacketSize-1] = 0xFF
	pkt[tsPacketSize-2] = 0xFF

	original := make([]byte, tsPacketSize)
	copy(original, pkt)

	setTSDiscontinuityIndicator(pkt)

	// AFC should now be 11 (adaptation + payload).
	afc := (pkt[3] >> 4) & 0x03
	require.Equal(t, byte(3), afc, "AFC should be changed to 11")

	// CC should be preserved.
	cc := pkt[3] & 0x0F
	require.Equal(t, byte(5), cc, "CC should be preserved")

	// Adaptation field length should be 1.
	require.Equal(t, byte(1), pkt[4], "adaptation_field_length should be 1")

	// Discontinuity indicator should be set.
	require.NotZero(t, pkt[5]&0x80, "discontinuity_indicator should be set")
}

func TestSetTSDiscontinuityIndicator_PayloadOnlyNonStuffingTrailingBytes(t *testing.T) {
	// Test the bug fix: payload-only packet where the last 2 bytes are NOT
	// 0xFF stuffing. Shifting payload would corrupt the packet by dropping
	// significant data. The function should skip modification for this packet.
	pkt := makeTSPacket(0x0000, false) // PAT, payload only (AFC=01)
	pkt[3] = 0x15                      // AFC=01, CC=5

	// Set the last 2 bytes to non-stuffing values (simulate a full PMT with
	// a large descriptor loop that uses all available space).
	pkt[tsPacketSize-1] = 0xAB
	pkt[tsPacketSize-2] = 0xCD

	// Save original packet for comparison.
	original := make([]byte, tsPacketSize)
	copy(original, pkt)

	setTSDiscontinuityIndicator(pkt)

	// The packet should NOT be modified since truncating would corrupt data.
	require.Equal(t, original, pkt,
		"packet with non-stuffing trailing bytes should not be modified")
}

func TestSetTSDiscontinuityIndicator_PayloadOnlyOneByteNonStuffing(t *testing.T) {
	// Test edge case: last byte is 0xFF but second-to-last is not.
	// Should still skip modification since we'd lose the non-0xFF byte.
	pkt := makeTSPacket(0x0000, false)
	pkt[3] = 0x15 // AFC=01, CC=5

	pkt[tsPacketSize-1] = 0xFF
	pkt[tsPacketSize-2] = 0xCD // non-stuffing

	original := make([]byte, tsPacketSize)
	copy(original, pkt)

	setTSDiscontinuityIndicator(pkt)

	// The packet should NOT be modified.
	require.Equal(t, original, pkt,
		"packet with one non-stuffing trailing byte should not be modified")
}
