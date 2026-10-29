package output

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A finished file, whether rotated out or closed at stop, moves to the
// archive whole; the file being written stays local, where the periodic
// fsync is harmless.
func TestFileRecorder_ArchivesFinishedFiles(t *testing.T) {
	dir, archive := t.TempDir(), filepath.Join(t.TempDir(), "recordings", "a")
	r := NewFileRecorder(RecorderConfig{Dir: dir, MaxFileSize: 188 * 3, ArchiveDir: archive})
	require.NoError(t, r.Start(context.TODO()))

	first := r.Filename()
	pkt := makeTSPacket(0x100, true)
	for i := 0; i < 4; i++ {
		_, err := r.Write(pkt)
		require.NoError(t, err)
	}
	second := r.Filename()
	require.NotEqual(t, first, second)
	r.WaitArchived()

	require.Equal(t, []string{first}, listNames(t, archive))
	require.Equal(t, []string{second}, listNames(t, dir))
	data, err := os.ReadFile(filepath.Join(archive, first))
	require.NoError(t, err)
	require.Len(t, data, 188*3)

	require.NoError(t, r.Close())
	r.WaitArchived()
	require.ElementsMatch(t, []string{first, second}, listNames(t, archive))
	require.Empty(t, listNames(t, dir))
}

// A file a previous run left behind, for instance cut short by a restart,
// is archived when recording starts again; a complete archive of it is
// kept, so only the local copy goes.
func TestFileRecorder_ArchivesLeftoversAtStart(t *testing.T) {
	dir, archive := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "program_old_001.ts"), []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "program_dup_001.ts"), []byte("dup"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archive, "program_dup_001.ts"), []byte("dup"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "program_diff_001.ts"), []byte("local"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archive, "program_diff_001.ts"), []byte("archived"), 0o644))

	r := NewFileRecorder(RecorderConfig{Dir: dir, ArchiveDir: archive})
	require.NoError(t, r.Start(context.TODO()))
	r.WaitArchived()

	old, err := os.ReadFile(filepath.Join(archive, "program_old_001.ts"))
	require.NoError(t, err)
	require.Equal(t, "old", string(old))
	// An archive of another size is a copy cut short: replaced.
	diff, err := os.ReadFile(filepath.Join(archive, "program_diff_001.ts"))
	require.NoError(t, err)
	require.Equal(t, "local", string(diff))
	require.ElementsMatch(t, []string{r.Filename()}, listNames(t, dir))

	require.NoError(t, r.Close())
	r.WaitArchived()
}

// recordSomething writes one packet into the manager's running recording,
// which an empty recording, removed on stop, would not leave behind.
func recordSomething(t *testing.T, m *Manager) {
	t.Helper()
	m.mu.Lock()
	r := m.recorder
	m.mu.Unlock()
	_, err := r.Write(make([]byte, 188))
	require.NoError(t, err)
}

// Without an ArchiveDir nothing moves.
func TestFileRecorder_NoArchiveKeepsFiles(t *testing.T) {
	dir := t.TempDir()
	r := NewFileRecorder(RecorderConfig{Dir: dir})
	require.NoError(t, r.Start(context.TODO()))
	name := r.Filename()
	_, err := r.Write(make([]byte, 188))
	require.NoError(t, err)
	require.NoError(t, r.Close())
	r.WaitArchived()
	require.Equal(t, []string{name}, listNames(t, dir))
}

// A recording started without an ArchiveDir of its own, as the UI and a
// restored snapshot start one, takes the manager's.
func TestManager_StartRecordingTakesArchiveDir(t *testing.T) {
	dir, archive := t.TempDir(), t.TempDir()
	m := NewManager()
	defer func() { _ = m.Close() }()
	m.SetRecordingArchiveDir(archive)
	require.NoError(t, m.StartRecording(RecorderConfig{Dir: dir}))
	name := m.RecordingStatus().Filename
	recordSomething(t, m)
	require.NoError(t, m.StopRecording())
	require.Eventually(t, func() bool {
		names := listNames(t, archive)
		return len(names) == 1 && names[0] == name
	}, 5*time.Second, 10*time.Millisecond)
}

// Two engines share one archive and name their recordings after the same
// start second; each archives under its own label, so neither file is lost.
func TestManager_EnginesSharingAnArchiveNeverCollide(t *testing.T) {
	archive := t.TempDir()
	var names [2]string
	var managers [2]*Manager
	for i, label := range []string{"a", "b"} {
		m := NewManager()
		defer func() { _ = m.Close() }()
		m.SetEngineLabel(label)
		m.SetRecordingArchiveDir(archive)
		require.Equal(t, filepath.Join(archive, label), m.RecordingArchiveDir())
		managers[i] = m
	}
	for i, m := range managers {
		require.NoError(t, m.StartRecording(RecorderConfig{Dir: t.TempDir()}))
		names[i] = m.RecordingStatus().Filename
		recordSomething(t, m)
	}
	for _, m := range managers {
		require.NoError(t, m.StopRecording())
	}
	require.Eventually(t, func() bool {
		return len(listNames(t, filepath.Join(archive, "a"))) == 1 && len(listNames(t, filepath.Join(archive, "b"))) == 1
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{names[0]}, listNames(t, filepath.Join(archive, "a")))
	require.Equal(t, []string{names[1]}, listNames(t, filepath.Join(archive, "b")))
}

// A copy into the archive that a restart cut short still left a file; the
// next start replaces it with the whole recording instead of keeping both
// apart forever.
func TestFileRecorder_ReplacesACutShortArchive(t *testing.T) {
	dir, archive := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "program_x_001.ts"), []byte("whole recording"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archive, "program_x_001.ts"), []byte("whole"), 0o644))

	r := NewFileRecorder(RecorderConfig{Dir: dir, ArchiveDir: archive})
	require.NoError(t, r.Start(context.TODO()))
	r.WaitArchived()
	data, err := os.ReadFile(filepath.Join(archive, "program_x_001.ts"))
	require.NoError(t, err)
	require.Equal(t, "whole recording", string(data))
	require.Equal(t, []string{r.Filename()}, listNames(t, dir))
	require.NoError(t, r.Close())
	r.WaitArchived()
}
