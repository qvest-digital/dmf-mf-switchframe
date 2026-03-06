package output

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileRecorder_ID(t *testing.T) {
	r := NewFileRecorder(RecorderConfig{Dir: t.TempDir()})
	require.Equal(t, "recorder", r.ID())
}

func TestFileRecorder_StartCreatesFile(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	err := r.Start(context.TODO())
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	status := r.Status()
	require.Equal(t, StateActive, status.State)
	require.NotEmpty(t, r.Filename())
	path := filepath.Join(dir, r.Filename())
	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestFileRecorder_WriteBytes(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	require.NoError(t, r.Start(context.TODO()))
	data := make([]byte, 188*7)
	n, err := r.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), n)
	status := r.Status()
	require.Equal(t, int64(188*7), status.BytesWritten)
	require.NoError(t, r.Close())
	path := filepath.Join(dir, r.Filename())
	info, _ := os.Stat(path)
	require.Equal(t, int64(188*7), info.Size())
}

func TestFileRecorder_Close(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	require.NoError(t, r.Start(context.TODO()))
	require.NoError(t, r.Close())
	status := r.Status()
	require.Equal(t, StateStopped, status.State)
}

func TestFileRecorder_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	require.NoError(t, r.Start(context.TODO()))
	require.NoError(t, r.Close())
	_, err := r.Write([]byte{0x47})
	require.Error(t, err)
}

func TestFileRecorder_StatusDuration(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	require.NoError(t, r.Start(context.TODO()))
	time.Sleep(10 * time.Millisecond)
	status := r.Status()
	require.Equal(t, StateActive, status.State)
	require.NoError(t, r.Close())
}

func TestFileRecorder_DoubleStart(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()
	err := r.Start(context.TODO())
	require.Error(t, err)
}

func TestFileRecorder_DoubleClose(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	require.NoError(t, r.Start(context.TODO()))
	require.NoError(t, r.Close())
	// Second close should be a no-op, not an error.
	require.NoError(t, r.Close())
}

func TestFileRecorder_FilenameFormat(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()
	name := r.Filename()
	// Without rotation, first file gets _001 suffix.
	require.Regexp(t, `^program_\d{8}_\d{6}_001\.ts$`, name)
}

func TestFileRecorder_RecordingStatusSnapshot(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})

	// Before start: inactive.
	snap := r.RecordingStatusSnapshot()
	require.False(t, snap.Active)
	require.Empty(t, snap.Filename)

	// After start: active.
	require.NoError(t, r.Start(context.TODO()))
	data := make([]byte, 188)
	_, _ = r.Write(data)
	snap = r.RecordingStatusSnapshot()
	require.True(t, snap.Active)
	require.NotEmpty(t, snap.Filename)
	require.Equal(t, int64(188), snap.BytesWritten)
	require.True(t, snap.DurationSecs >= 0)

	// After close: inactive.
	require.NoError(t, r.Close())
	snap = r.RecordingStatusSnapshot()
	require.False(t, snap.Active)
}

func TestFileRecorder_WriteBeforeStart(t *testing.T) {
	r := NewFileRecorder(RecorderConfig{Dir: t.TempDir()})
	_, err := r.Write([]byte{0x47})
	require.Error(t, err)
}

// --- Rotation tests ---

func TestFileRecorder_RotateAfterDuration(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		RotateAfter: 50 * time.Millisecond,
	})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	firstFile := r.Filename()

	// Write before rotation time.
	data := make([]byte, 188)
	_, err := r.Write(data)
	require.NoError(t, err)
	require.Equal(t, firstFile, r.Filename(), "should not rotate yet")

	// Wait past rotation threshold.
	time.Sleep(60 * time.Millisecond)

	// Next write should trigger rotation.
	_, err = r.Write(data)
	require.NoError(t, err)

	secondFile := r.Filename()
	require.NotEqual(t, firstFile, secondFile, "should have rotated to a new file")

	// Verify both files exist on disk.
	_, err = os.Stat(filepath.Join(dir, firstFile))
	require.NoError(t, err, "first file should still exist")
	_, err = os.Stat(filepath.Join(dir, secondFile))
	require.NoError(t, err, "second file should exist")
}

func TestFileRecorder_RotateAfterFileSize(t *testing.T) {
	dir := t.TempDir()
	maxSize := int64(188 * 3) // 564 bytes
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: maxSize,
	})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	firstFile := r.Filename()
	data := make([]byte, 188)

	// Write up to the limit (3 packets = 564 bytes, at the limit).
	for i := 0; i < 3; i++ {
		_, err := r.Write(data)
		require.NoError(t, err)
	}
	require.Equal(t, firstFile, r.Filename(), "should not rotate at exactly the limit")

	// One more write should trigger rotation.
	_, err := r.Write(data)
	require.NoError(t, err)

	secondFile := r.Filename()
	require.NotEqual(t, firstFile, secondFile, "should have rotated")

	// Verify both files exist.
	_, err = os.Stat(filepath.Join(dir, firstFile))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, secondFile))
	require.NoError(t, err)
}

func TestFileRecorder_SequentialNaming(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: 188, // Rotate after every packet.
	})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	data := make([]byte, 188)
	files := []string{r.Filename()}

	// Write 3 packets. First write fills file _001 to the limit.
	// Second write triggers rotation to _002. Third triggers to _003.
	_, _ = r.Write(data)
	files = append(files, r.Filename())

	_, _ = r.Write(data)
	files = append(files, r.Filename())

	_, _ = r.Write(data)
	files = append(files, r.Filename())

	// files[0] = _001 (initial), files[1] = _001 (first write, at limit, not yet rotated)
	// files[2] = _002 (second write triggers rotation), files[3] = _003

	require.Regexp(t, `_001\.ts$`, files[0], "first file should be _001")
	// After first write, file size equals MaxFileSize (188), rotation not triggered yet.
	require.Regexp(t, `_001\.ts$`, files[1], "still _001 after first write")
	// After second write, shouldRotate sees fileBytes >= MaxFileSize, rotates first, then writes.
	require.Regexp(t, `_002\.ts$`, files[2], "rotated to _002")
	require.Regexp(t, `_003\.ts$`, files[3], "rotated to _003")
}

func TestFileRecorder_NoRotationWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir: dir,
		// RotateAfter: 0 (disabled), MaxFileSize: 0 (unlimited)
	})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	firstFile := r.Filename()
	data := make([]byte, 188)

	// Write many packets.
	for i := 0; i < 100; i++ {
		_, err := r.Write(data)
		require.NoError(t, err)
	}

	require.Equal(t, firstFile, r.Filename(), "should never rotate when disabled")
}

func TestFileRecorder_RotationProducesValidFilenameFormat(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: 188, // Rotate after every packet.
	})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	data := make([]byte, 188)

	// Collect filenames across several rotations.
	names := map[string]bool{}
	names[r.Filename()] = true

	for i := 0; i < 5; i++ {
		_, _ = r.Write(data)
		names[r.Filename()] = true
	}

	for name := range names {
		require.Regexp(t, `^program_\d{8}_\d{6}_\d{3}\.ts$`, name,
			"all filenames must match program_YYYYMMDD_HHMMSS_NNN.ts format")
	}
}

func TestFileRecorder_RotationResetsFileBytes(t *testing.T) {
	dir := t.TempDir()
	maxSize := int64(188 * 2) // 376 bytes
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: maxSize,
	})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	data := make([]byte, 188)

	// Write 2 packets to fill first file.
	_, _ = r.Write(data)
	_, _ = r.Write(data)
	firstFile := r.Filename()

	// Next write triggers rotation.
	_, _ = r.Write(data)
	secondFile := r.Filename()
	require.NotEqual(t, firstFile, secondFile)

	// Write another packet -- should still be in second file (only 2*188 = 376 written).
	_, _ = r.Write(data)
	require.Equal(t, secondFile, r.Filename(), "should still be in second file")

	// Third write to second file should trigger another rotation.
	_, _ = r.Write(data)
	thirdFile := r.Filename()
	require.NotEqual(t, secondFile, thirdFile, "should have rotated again")
}

func TestFileRecorder_TotalBytesAcrossRotations(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{
		Dir:         dir,
		MaxFileSize: 188,
	})
	require.NoError(t, r.Start(context.TODO()))
	defer func() { _ = r.Close() }()

	data := make([]byte, 188)
	for i := 0; i < 5; i++ {
		_, _ = r.Write(data)
	}

	status := r.Status()
	require.Equal(t, int64(188*5), status.BytesWritten,
		"total bytes should accumulate across rotations")
}

func TestFileRecorder_ZeroConfigDisablesRotation(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})

	// Zero values mean rotation is disabled.
	require.Equal(t, time.Duration(0), r.config.RotateAfter,
		"zero RotateAfter means rotation disabled")
	require.Equal(t, int64(0), r.config.MaxFileSize,
		"zero MaxFileSize means no size limit")
}
