package graphics

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A layer's sequence is reported as it was uploaded, while it loads and
// once it is loaded, with its speed, so another engine can load the same.
func TestSequenceSources(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	se := NewSequenceEngine(c)
	defer se.Close()
	require.NoError(t, c.AddLayerWithID(2))

	data := makeTestSequenceZIP(t)
	require.NoError(t, se.UploadAsync(2, data, 25))
	src, ok := se.Sources()[2]
	require.True(t, ok, "a loading sequence is not reported")
	sum := sha256.Sum256(data)
	require.Equal(t, hex.EncodeToString(sum[:]), src.Hash)
	require.EqualValues(t, len(data), src.Size)
	rc, size, err := se.OpenSource(2, src.Hash)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.EqualValues(t, len(data), size)
	_, _, err = se.OpenSource(2, "other")
	require.ErrorIs(t, err, ErrSequenceNotFound)
	require.InDelta(t, 25, src.SourceFPS, 0)

	require.Eventually(t, func() bool { return se.HasSequence(2) }, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, se.SetSpeed(2, 2))
	require.InDelta(t, 2, se.Sources()[2].Speed, 0)

	require.NoError(t, c.RemoveLayer(2))
	require.Empty(t, se.Sources(), "a removed layer's sequence is reported")
}

// A sequence deleted, or replaced by a later upload, while it is decoded
// is not installed when its decode completes.
func TestSequenceDeletedWhileLoadingIsNotInstalled(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	se := NewSequenceEngine(c)
	defer se.Close()
	require.NoError(t, c.AddLayerWithID(1))

	require.NoError(t, se.UploadAsync(1, makeTestSequenceZIP(t), 25))
	require.NoError(t, se.Delete(1))
	require.Empty(t, se.Sources())
	require.Never(t, func() bool { return se.HasSequence(1) }, 200*time.Millisecond, 5*time.Millisecond)
	require.ErrorIs(t, se.Delete(1), ErrSequenceNotFound)
}

func TestTickerConfigs(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	require.NoError(t, c.AddLayerWithID(1))
	te := NewTickerEngine(c, newTestRenderer(t))
	defer te.Close()

	cfg := TickerConfig{Text: "headline", FontSize: 30, Speed: 80, Loop: true}
	require.NoError(t, te.Start(1, cfg))
	require.Equal(t, map[int]TickerConfig{1: cfg}, te.Configs())
	require.NoError(t, te.UpdateText(1, "update"))
	cfg.Text = "update"
	require.Equal(t, map[int]TickerConfig{1: cfg}, te.Configs())
	require.NoError(t, te.Stop(1))
	require.Empty(t, te.Configs())
}

// With a source directory a sequence's ZIP is kept on disk, once per
// content, not on the heap, and its file goes once no layer holds it.
func TestSequenceSourcesOnDisk(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	se := NewSequenceEngine(c)
	defer se.Close()
	dir := filepath.Join(t.TempDir(), "zips")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stale.zip"), []byte("old"), 0o644))
	require.NoError(t, se.SetSourceDir(dir))
	files := func() []string {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	require.Empty(t, files(), "a ZIP of an earlier process kept")

	require.NoError(t, c.AddLayerWithID(1))
	require.NoError(t, c.AddLayerWithID(2))
	data := makeTestSequenceZIP(t)
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:]) + ".zip"
	require.NoError(t, se.UploadAsync(1, data, 25))
	require.NoError(t, se.UploadAsync(2, data, 25))
	require.Equal(t, []string{name}, files())
	src := se.Sources()[1]
	require.Nil(t, src.zip, "the ZIP is held in memory")

	require.Eventually(t, func() bool { return se.HasSequence(1) && se.HasSequence(2) }, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, se.Delete(1))
	require.Equal(t, []string{name}, files(), "a ZIP another layer holds was removed")
	rc, _, err := se.OpenSource(2, "")
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	require.Equal(t, data, got)

	require.NoError(t, c.RemoveLayer(2))
	require.Empty(t, se.Sources())
	require.Empty(t, files(), "the ZIP of a removed layer was kept")
}
