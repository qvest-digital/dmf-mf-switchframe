package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/switcher"
)

// setupLibraryAPI returns an API whose clip store has the library lib
// attached, as --clip-library-dir does.
func setupLibraryAPI(t *testing.T, lib string, opts ...APIOption) (*API, *clip.Store) {
	t.Helper()
	store, err := clip.NewStore(t.TempDir(), 100*1024*1024)
	require.NoError(t, err)
	require.NoError(t, store.AttachLibrary(lib, ""))
	_, err = store.ScanLibrary()
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	opts = append([]APIOption{WithClipManager(mgr), WithClipStore(store)}, opts...)
	return NewAPI(sw, opts...), store
}

// An upload lands in the clip library, so the next booking attaching the
// same library lists it under the same ID.
func TestHandleClipUploadPublishesToLibrary(t *testing.T) {
	lib := t.TempDir()
	api, store := setupLibraryAPI(t, lib)
	const id = "0123456789abcdef0123456789abcdef"

	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, clipUploadRequest(t, id, uploadTestTS(t)))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	c, err := store.Get(id)
	require.NoError(t, err)
	require.Equal(t, id+".ts", c.LibraryPath)
	_, err = os.Stat(filepath.Join(lib, id+".ts"))
	require.NoError(t, err)

	_, next := setupLibraryAPI(t, lib)
	got, err := next.Get(id)
	require.NoError(t, err, "next booking does not list the upload")
	require.Equal(t, "shared", got.Name)
}

// Rescan lists clips put into the library while running, and drops clips
// deleted from it by someone else.
func TestHandleClipRescan(t *testing.T) {
	lib := t.TempDir()
	api, store := setupLibraryAPI(t, lib)
	require.NoError(t, os.WriteFile(filepath.Join(lib, "Late.ts"), uploadTestTS(t), 0o644))

	rescan := func() clip.LibraryScan {
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/clips/rescan", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var res clip.LibraryScan
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&res))
		return res
	}
	require.Equal(t, 1, rescan().Added)
	require.Len(t, store.List(), 1)

	require.NoError(t, os.Remove(filepath.Join(lib, "Late.ts")))
	require.Equal(t, 1, rescan().Removed)
	require.Empty(t, store.List())
}

func TestHandleClipRescanWithoutLibrary(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/clips/rescan", nil))
	require.Equal(t, http.StatusConflict, rec.Code)
}

// The UI deletes a library clip from the library itself.
func TestHandleClipDeleteRemovesFromLibrary(t *testing.T) {
	lib := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(lib, "Opener.ts"), uploadTestTS(t), 0o644))
	api, store := setupLibraryAPI(t, lib)
	require.Len(t, store.List(), 1)
	id := store.List()[0].ID

	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/clips/"+id, nil))
	require.Equal(t, http.StatusNoContent, rec.Code)
	_, err := os.Stat(filepath.Join(lib, "Opener.ts"))
	require.True(t, os.IsNotExist(err), "library file kept: %v", err)
}

// Finished recordings archived into the library are listed and importable
// beside those still in the recording directory.
func TestHandleClipRecordingsIncludesArchive(t *testing.T) {
	recDir, archive := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(recDir, "program_b_001.ts"), uploadTestTS(t), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archive, "program_a_001.ts"), uploadTestTS(t), 0o644))
	// Archived but not yet removed locally: listed once.
	require.NoError(t, os.WriteFile(filepath.Join(archive, "program_b_001.ts"), uploadTestTS(t), 0o644))

	api, store := setupLibraryAPI(t, t.TempDir(), WithRecordingDir(recDir), WithRecordingArchiveDir(archive, archive))

	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/clips/recordings", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var list []RecordingFileInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&list))
	names := map[string]string{}
	for _, f := range list {
		names[f.Filename] = f.Path
	}
	require.Len(t, list, 2)
	require.Equal(t, filepath.Join(archive, "program_a_001.ts"), names["program_a_001.ts"])

	body, _ := json.Marshal(map[string]string{"path": filepath.Join(archive, "program_a_001.ts")})
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/clips/from-recording", bytes.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, store.List(), 1)

	// Anything outside both directories is still refused.
	body, _ = json.Marshal(map[string]string{"path": filepath.Join(filepath.Dir(archive), "x.ts")})
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/clips/from-recording", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// Directories are compared as absolute paths, as the download handler did
// before the archive was added: a recording directory configured relative
// to the working directory still serves its files.
func TestRecordingDirsCompareAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.MkdirAll("recordings", 0o755))
	require.NoError(t, os.WriteFile(filepath.Join("recordings", "program_a_001.ts"), uploadTestTS(t), 0o644))
	api, _ := setupLibraryAPI(t, t.TempDir(), WithRecordingDir("recordings"))

	abs := filepath.Join(root, "recordings", "program_a_001.ts")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/clips/recordings/download?path="+abs, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/clips/recordings/download?path="+filepath.Join(root, "elsewhere.ts"), nil))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// A booking finds the recordings any engine archived into the library in
// an earlier run, each under its own label, also when it runs another
// engine or none: same-second files of two engines are both offered.
func TestHandleClipRecordingsListsEveryEnginesArchive(t *testing.T) {
	recDir, root := t.TempDir(), t.TempDir()
	for _, p := range []string{"a/program_x_001.ts", "b/program_x_001.ts", "program_y_001.ts"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, p), uploadTestTS(t), 0o644))
	}
	// This engine's own file, still on its way out of the recording dir.
	require.NoError(t, os.WriteFile(filepath.Join(recDir, "program_x_001.ts"), uploadTestTS(t), 0o644))

	api, store := setupLibraryAPI(t, t.TempDir(), WithRecordingDir(recDir), WithRecordingArchiveDir(root, filepath.Join(root, "a")))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/clips/recordings", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var list []RecordingFileInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&list))
	var paths []string
	for _, f := range list {
		paths = append(paths, f.Path)
	}
	require.ElementsMatch(t, []string{
		filepath.Join(recDir, "program_x_001.ts"),
		filepath.Join(root, "b", "program_x_001.ts"),
		filepath.Join(root, "program_y_001.ts"),
	}, paths, "the other engine's file and an unlabelled run's file are listed; this engine's archived copy of its local file once")

	body, _ := json.Marshal(map[string]string{"path": filepath.Join(root, "b", "program_x_001.ts")})
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/clips/from-recording", bytes.NewReader(body)))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, store.List(), 1)
}
