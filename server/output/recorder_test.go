package output

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileRecorder_ID(t *testing.T) {
	r := NewFileRecorder(t.TempDir())
	require.Equal(t, "recorder", r.ID())
}

func TestFileRecorder_StartCreatesFile(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(dir)
	err := r.Start(nil)
	require.NoError(t, err)
	defer r.Close()
	status := r.Status()
	require.Equal(t, StateActive, status.State)
	require.NotEmpty(t, r.Filename())
	path := filepath.Join(dir, r.Filename())
	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestFileRecorder_WriteBytes(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(dir)
	require.NoError(t, r.Start(nil))
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
	r := NewFileRecorder(dir)
	require.NoError(t, r.Start(nil))
	require.NoError(t, r.Close())
	status := r.Status()
	require.Equal(t, StateStopped, status.State)
}

func TestFileRecorder_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(dir)
	require.NoError(t, r.Start(nil))
	require.NoError(t, r.Close())
	_, err := r.Write([]byte{0x47})
	require.Error(t, err)
}

func TestFileRecorder_StatusDuration(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(dir)
	require.NoError(t, r.Start(nil))
	time.Sleep(10 * time.Millisecond)
	status := r.Status()
	require.Equal(t, StateActive, status.State)
	require.NoError(t, r.Close())
}

func TestFileRecorder_DoubleStart(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(dir)
	require.NoError(t, r.Start(nil))
	defer r.Close()
	err := r.Start(nil)
	require.Error(t, err)
}

func TestFileRecorder_DoubleClose(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(dir)
	require.NoError(t, r.Start(nil))
	require.NoError(t, r.Close())
	// Second close should be a no-op, not an error.
	require.NoError(t, r.Close())
}

func TestFileRecorder_FilenameFormat(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(dir)
	require.NoError(t, r.Start(nil))
	defer r.Close()
	name := r.Filename()
	require.Regexp(t, `^program_\d{8}_\d{6}\.ts$`, name)
}

func TestFileRecorder_RecordingStatusSnapshot(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(dir)

	// Before start: inactive.
	snap := r.RecordingStatusSnapshot()
	require.False(t, snap.Active)
	require.Empty(t, snap.Filename)

	// After start: active.
	require.NoError(t, r.Start(nil))
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
	r := NewFileRecorder(t.TempDir())
	_, err := r.Write([]byte{0x47})
	require.Error(t, err)
}
