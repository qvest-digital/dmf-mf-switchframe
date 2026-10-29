//go:build linux

package output

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/createonly/renamewatch"
)

// Archiving never renames a file into the archive: on an object-store
// mount a rename loses the file or leaves it empty.
func TestFileRecorder_NeverRenamesIntoTheArchive(t *testing.T) {
	dir, archive := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "program_old_001.ts"), []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archive, "program_old_001.ts"), []byte("o"), 0o644))
	renames := renamewatch.Watch(t, archive)

	r := NewFileRecorder(RecorderConfig{Dir: dir, MaxFileSize: 188 * 3, ArchiveDir: archive})
	require.NoError(t, r.Start(context.TODO()))
	pkt := makeTSPacket(0x100, true)
	for i := 0; i < 8; i++ {
		_, err := r.Write(pkt)
		require.NoError(t, err)
	}
	require.NoError(t, r.Close())
	r.WaitArchived()
	require.Len(t, listNames(t, archive), 4, "the leftover and three rotated files")
	require.Empty(t, renames(), "renamed in the archive")
}
