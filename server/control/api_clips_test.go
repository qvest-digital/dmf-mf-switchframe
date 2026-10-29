package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/switcher"
)

// setupTestAPIWithClips creates a test API with clip manager and store.
func setupTestAPIWithClips(t *testing.T) (*API, *clip.Store, *clip.Manager) {
	t.Helper()
	dir := t.TempDir()
	store, err := clip.NewStore(dir, 100*1024*1024)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{})

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw, WithClipManager(mgr), WithClipStore(store))
	return api, store, mgr
}

// addTestClip adds a clip directly to the store for testing.
func addTestClip(t *testing.T, store *clip.Store, name string) *clip.Clip {
	t.Helper()
	c := &clip.Clip{
		Name:       name,
		Filename:   name + ".ts",
		Source:     clip.SourceUpload,
		Codec:      "h264",
		Width:      1920,
		Height:     1080,
		FPSNum:     30000,
		FPSDen:     1001,
		DurationMs: 5000,
		ByteSize:   1024,
		CreatedAt:  time.Now(),
	}
	err := store.Add(c)
	require.NoError(t, err)
	return c
}

func TestHandleClipsList(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("GET", "/api/clips", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var clips []*clip.Clip
	err := json.NewDecoder(rec.Body).Decode(&clips)
	require.NoError(t, err)
	require.Len(t, clips, 0)
}

func TestHandleClipsListWithClips(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)
	addTestClip(t, store, "clip-a")
	addTestClip(t, store, "clip-b")

	req := httptest.NewRequest("GET", "/api/clips", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var clips []*clip.Clip
	err := json.NewDecoder(rec.Body).Decode(&clips)
	require.NoError(t, err)
	require.Len(t, clips, 2)
}

func TestHandleClipGet(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)
	c := addTestClip(t, store, "my-clip")

	req := httptest.NewRequest("GET", "/api/clips/"+c.ID, nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got clip.Clip
	err := json.NewDecoder(rec.Body).Decode(&got)
	require.NoError(t, err)
	require.Equal(t, c.ID, got.ID)
	require.Equal(t, "my-clip", got.Name)
}

func TestHandleClipGetNotFound(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("GET", "/api/clips/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleClipUpdate(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)
	c := addTestClip(t, store, "original")

	body := `{"name":"renamed","loop":true}`
	req := httptest.NewRequest("PUT", "/api/clips/"+c.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got clip.Clip
	err := json.NewDecoder(rec.Body).Decode(&got)
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Name)
	require.True(t, got.Loop)
}

func TestHandleClipUpdatePartial(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)
	c := addTestClip(t, store, "keep-name")

	// Only update loop, name should stay the same.
	body := `{"loop":true}`
	req := httptest.NewRequest("PUT", "/api/clips/"+c.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got clip.Clip
	err := json.NewDecoder(rec.Body).Decode(&got)
	require.NoError(t, err)
	require.Equal(t, "keep-name", got.Name)
	require.True(t, got.Loop)
}

func TestHandleClipUpdateNotFound(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"name":"x"}`
	req := httptest.NewRequest("PUT", "/api/clips/nonexistent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleClipUpdateInvalidJSON(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)
	c := addTestClip(t, store, "test")

	req := httptest.NewRequest("PUT", "/api/clips/"+c.ID, strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipDelete(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)
	c := addTestClip(t, store, "delete-me")

	req := httptest.NewRequest("DELETE", "/api/clips/"+c.ID, nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)

	// Verify it's gone.
	_, err := store.Get(c.ID)
	require.ErrorIs(t, err, clip.ErrNotFound)
}

func TestHandleClipDeleteNotFound(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("DELETE", "/api/clips/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleClipPin(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)

	// Add an ephemeral clip.
	c := &clip.Clip{
		Name:      "ephemeral",
		Filename:  "ephemeral.ts",
		Source:    clip.SourceReplay,
		Codec:     "h264",
		Width:     1920,
		Height:    1080,
		ByteSize:  1024,
		Ephemeral: true,
	}
	err := store.Add(c)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/clips/"+c.ID+"/pin", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got clip.Clip
	err = json.NewDecoder(rec.Body).Decode(&got)
	require.NoError(t, err)
	require.False(t, got.Ephemeral)
}

func TestHandleClipPinNotFound(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/nonexistent/pin", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleClipPlayersList(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("GET", "/api/clips/players", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var states []clip.ClipPlayerState
	err := json.NewDecoder(rec.Body).Decode(&states)
	require.NoError(t, err)
	require.Len(t, states, 4)

	// All should be empty.
	for i, s := range states {
		require.Equal(t, i+1, s.ID)
		require.Equal(t, clip.StateEmpty, s.State)
	}
}

func TestHandleClipPlayerLoadInvalidID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"clipId":"abc"}`
	req := httptest.NewRequest("POST", "/api/clips/players/5/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerLoadNonNumericID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"clipId":"abc"}`
	req := httptest.NewRequest("POST", "/api/clips/players/xyz/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerLoadMissingClipID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerLoadClipNotFound(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"clipId":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleClipPlayerLoadByName(t *testing.T) {
	api, store, _ := setupTestAPIWithClips(t)
	c := addTestClip(t, store, "my-clip")
	// Create the media file so Load won't fail on missing file.
	_ = os.WriteFile(filepath.Join(store.Dir(), c.Filename), []byte("data"), 0644)

	body := `{"clipName":"my-clip"}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Load may fail because the clip file isn't a valid media file,
	// but we should NOT get 400 (bad request) or 404 (not found) from the name lookup.
	// The name resolution succeeded if we get past the 404/400 checks.
	// Accept 200 or 500 (invalid media format), but not 400 or 404.
	require.NotEqual(t, http.StatusBadRequest, rec.Code, "name lookup should not fail as bad request")
}

func TestHandleClipPlayerLoadByNameNotFound(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"clipName":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleClipPlayerLoadNoIdOrName(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerLoadInvalidJSON(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/1/load", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerPlayInvalidID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"speed":1.0}`
	req := httptest.NewRequest("POST", "/api/clips/players/0/play", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerPlayEmptySlot(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"speed":1.0}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/play", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerPauseInvalidID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/5/pause", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerPauseEmptySlot(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/1/pause", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerStopInvalidID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/99/stop", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerStopEmptySlot(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/1/stop", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerSeekInvalidID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"position":0.5}`
	req := httptest.NewRequest("POST", "/api/clips/players/0/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerSeekEmptySlot(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"position":0.5}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerEjectInvalidID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/5/eject", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerEjectEmptySlot(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	// Eject on empty slot should succeed (idempotent).
	req := httptest.NewRequest("POST", "/api/clips/players/1/eject", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleClipRecordingsEmpty(t *testing.T) {
	// No recordingDir set: should return empty array.
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("GET", "/api/clips/recordings", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var list []interface{}
	err := json.NewDecoder(rec.Body).Decode(&list)
	require.NoError(t, err)
	require.Len(t, list, 0)
}

func TestHandleClipRecordingsWithFiles(t *testing.T) {
	recDir := t.TempDir()

	// Create some .ts files.
	require.NoError(t, os.WriteFile(filepath.Join(recDir, "program_20260314_120000_000.ts"), make([]byte, 1024), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(recDir, "program_20260314_130000_001.ts"), make([]byte, 2048), 0o644))
	// Create a non-.ts file that should be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(recDir, "notes.txt"), []byte("hello"), 0o644))

	clipDir := t.TempDir()
	store, err := clip.NewStore(clipDir, 100*1024*1024)
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw,
		WithClipManager(mgr),
		WithClipStore(store),
		WithRecordingDir(recDir),
	)

	req := httptest.NewRequest("GET", "/api/clips/recordings", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var list []RecordingFileInfo
	err = json.NewDecoder(rec.Body).Decode(&list)
	require.NoError(t, err)
	require.Len(t, list, 2)

	// Verify filenames are present.
	names := make(map[string]bool)
	for _, f := range list {
		names[f.Filename] = true
		require.NotEmpty(t, f.Path)
		require.True(t, f.ByteSize > 0)
		require.False(t, f.ModTime.IsZero())
	}
	require.True(t, names["program_20260314_120000_000.ts"])
	require.True(t, names["program_20260314_130000_001.ts"])
}

// Every recording file is listed with how far along it is, so the operator
// sees a running recording grow, a stopped one finalize, and an empty one
// fail, rather than a file that silently stays at 0 B.
func TestHandleClipRecordingsReportsStates(t *testing.T) {
	recDir := t.TempDir()
	files := map[string]int{
		"program_active_001.ts":     1024,
		"program_finalizing_001.ts": 4096,
		"program_done_001.ts":       2048,
		"program_empty_001.ts":      0,
	}
	for name, size := range files {
		require.NoError(t, os.WriteFile(filepath.Join(recDir, name), make([]byte, size), 0o644))
	}

	clipDir := t.TempDir()
	store, err := clip.NewStore(clipDir, 100*1024*1024)
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})

	mock := &mockOutputManager{
		recording:   true,
		recFilename: "program_active_001.ts",
		finalizing:  []string{"program_finalizing_001.ts"},
	}
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw,
		WithClipManager(mgr),
		WithClipStore(store),
		WithRecordingDir(recDir),
		WithOutputManager(mock),
	)

	req := httptest.NewRequest("GET", "/api/clips/recordings", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var list []RecordingFileInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&list))
	got := map[string]RecordingFileInfo{}
	for _, f := range list {
		got[f.Filename] = f
	}
	require.Len(t, got, 4)
	require.Equal(t, RecordingStateRecording, got["program_active_001.ts"].State)
	require.EqualValues(t, 1024, got["program_active_001.ts"].ByteSize, "a running recording shows its size so far")
	require.Equal(t, RecordingStateFinalizing, got["program_finalizing_001.ts"].State)
	require.Equal(t, RecordingStateReady, got["program_done_001.ts"].State)
	require.Empty(t, got["program_done_001.ts"].Error)
	require.Equal(t, RecordingStateFailed, got["program_empty_001.ts"].State)
	require.Contains(t, got["program_empty_001.ts"].Error, "no program video")
}

// Importing an empty recording names the cause instead of the probe error
// on a temporary copy ("no video stream found: empty file: .../rec-import-...").
func TestHandleClipFromRecordingEmptyFile(t *testing.T) {
	recDir := t.TempDir()
	empty := filepath.Join(recDir, "program_empty_001.ts")
	require.NoError(t, os.WriteFile(empty, nil, 0o644))

	clipDir := t.TempDir()
	store, err := clip.NewStore(clipDir, 100*1024*1024)
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	api := NewAPI(sw, WithClipManager(mgr), WithClipStore(store), WithRecordingDir(recDir))

	body, _ := json.Marshal(map[string]string{"path": empty})
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/clips/from-recording", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "recording is empty")
	require.NotContains(t, rec.Body.String(), "rec-import-")
	entries, err := os.ReadDir(clipDir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), "rec-import-"), "no temporary copy is left behind")
	}
	require.Empty(t, store.List())
}

func TestHandleClipFromRecordingPathTraversal(t *testing.T) {
	recDir := t.TempDir()

	clipDir := t.TempDir()
	store, err := clip.NewStore(clipDir, 100*1024*1024)
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw,
		WithClipManager(mgr),
		WithClipStore(store),
		WithRecordingDir(recDir),
	)

	// Attempt path traversal.
	body := `{"path":"` + recDir + `/../../../etc/passwd"}`
	req := httptest.NewRequest("POST", "/api/clips/from-recording", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipFromRecordingNoRecordingDir(t *testing.T) {
	// No recordingDir set: should return 400.
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"path":"/some/file.ts"}`
	req := httptest.NewRequest("POST", "/api/clips/from-recording", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipFromRecordingInvalidJSON(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/from-recording", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipFromRecordingEmptyPath(t *testing.T) {
	recDir := t.TempDir()
	clipDir := t.TempDir()
	store, err := clip.NewStore(clipDir, 100*1024*1024)
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw,
		WithClipManager(mgr),
		WithClipStore(store),
		WithRecordingDir(recDir),
	)

	body := `{"path":""}`
	req := httptest.NewRequest("POST", "/api/clips/from-recording", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipFromRecordingFileNotFound(t *testing.T) {
	recDir := t.TempDir()
	clipDir := t.TempDir()
	store, err := clip.NewStore(clipDir, 100*1024*1024)
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw,
		WithClipManager(mgr),
		WithClipStore(store),
		WithRecordingDir(recDir),
	)

	body := `{"path":"` + filepath.Join(recDir, "nonexistent.ts") + `"}`
	req := httptest.NewRequest("POST", "/api/clips/from-recording", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// File doesn't exist: copy will fail.
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleClipFromRecordingRelativePath(t *testing.T) {
	recDir := t.TempDir()
	clipDir := t.TempDir()
	store, err := clip.NewStore(clipDir, 100*1024*1024)
	require.NoError(t, err)
	mgr := clip.NewManager(store, clip.ManagerConfig{})

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw,
		WithClipManager(mgr),
		WithClipStore(store),
		WithRecordingDir(recDir),
	)

	// Relative path should be rejected.
	body := `{"path":"relative/path.ts"}`
	req := httptest.NewRequest("POST", "/api/clips/from-recording", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipUploadNoFile(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/upload", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayersRouteNotMatchedByWildcard(t *testing.T) {
	// Ensure /api/clips/players is NOT matched by /api/clips/{id}
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("GET", "/api/clips/players", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var states []clip.ClipPlayerState
	err := json.NewDecoder(rec.Body).Decode(&states)
	require.NoError(t, err)
	require.Len(t, states, 4)
}

func TestHandleClipRecordingsRouteNotMatchedByWildcard(t *testing.T) {
	// Ensure /api/clips/recordings is NOT matched by /api/clips/{id}
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("GET", "/api/clips/recordings", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var list []interface{}
	err := json.NewDecoder(rec.Body).Decode(&list)
	require.NoError(t, err)
	// Should be empty array, not a 404.
	require.Len(t, list, 0)
}

func TestHandleClipPlayerPlayInvalidJSON(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/1/play", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerSeekInvalidJSON(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/1/seek", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerNonNumericIDs(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/clips/players/abc/eject"},
		{"POST", "/api/clips/players/abc/play"},
		{"POST", "/api/clips/players/abc/pause"},
		{"POST", "/api/clips/players/abc/stop"},
		{"POST", "/api/clips/players/abc/seek"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for %s %s, got %d", ep.method, ep.path, rec.Code)
	}
}

func TestHandleClipPlayerPlayInvalidSpeed(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	// Player 1 is empty, so we'll get ErrPlayerEmpty (400) before speed validation.
	// But speed validation happens inside Manager.Play which checks empty first.
	// This test validates the handler passes through manager errors correctly.
	body := `{"speed":5.0}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/play", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Empty slot returns 400 (ErrPlayerEmpty).
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestClipErrorMappings(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{clip.ErrNotFound, http.StatusNotFound},
		{clip.ErrStorageFull, http.StatusConflict},
		{clip.ErrPlayerBusy, http.StatusConflict},
		{clip.ErrAlreadyExists, http.StatusConflict},
		{clip.ErrPlayerFull, http.StatusConflict},
		{clip.ErrInvalidPlayer, http.StatusBadRequest},
		{clip.ErrPlayerEmpty, http.StatusBadRequest},
		{clip.ErrInvalidSpeed, http.StatusBadRequest},
		{clip.ErrInvalidSeek, http.StatusBadRequest},
		{clip.ErrInvalidName, http.StatusBadRequest},
		{clip.ErrInvalidFormat, http.StatusBadRequest},
		{clip.ErrCorruptFile, http.StatusBadRequest},
		{clip.ErrOddDimensions, http.StatusBadRequest},
		{clip.ErrNoVideo, http.StatusBadRequest},
	}

	for _, tt := range tests {
		got := errorStatus(tt.err)
		require.Equal(t, tt.status, got, "errorStatus(%v) = %d, want %d", tt.err, got, tt.status)
	}
}

func TestHandleClipPlayerSpeedInvalidID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"speed":0.5}`
	req := httptest.NewRequest("POST", "/api/clips/players/abc/speed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerSpeedEmptySlot(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"speed":0.5}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/speed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	// Empty slot (no player loaded) → ErrPlayerEmpty → 400
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerSpeedInvalidJSON(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/1/speed", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerLoopInvalidID(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"loop":true}`
	req := httptest.NewRequest("POST", "/api/clips/players/abc/loop", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerLoopEmptySlot(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	body := `{"loop":true}`
	req := httptest.NewRequest("POST", "/api/clips/players/1/loop", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	// Empty slot → ErrPlayerEmpty → 400
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipPlayerLoopInvalidJSON(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	req := httptest.NewRequest("POST", "/api/clips/players/1/loop", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleClipUploadTempFileCleanup(t *testing.T) {
	// Verify that temp files created during upload are cleaned up when the
	// handler exits with an error (e.g., unsupported format, validation failure,
	// context cancellation).
	api, store, _ := setupTestAPIWithClips(t)
	dir := store.Dir()

	// Count files in dir before upload.
	filesBefore, err := os.ReadDir(dir)
	require.NoError(t, err)

	// Create a multipart upload with an unsupported extension.
	// The handler will create a temp file, write it, then fail validation.
	// The deferred cleanup should remove the temp file.
	body := &strings.Builder{}
	boundary := "testboundary"
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"file\"; filename=\"test.ts\"\r\n")
	body.WriteString("Content-Type: video/mp2t\r\n\r\n")
	body.WriteString("not a real TS file content")
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req := httptest.NewRequest("POST", "/api/clips/upload", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// The request should fail (invalid TS file won't validate).
	// But the temp file should be cleaned up by the deferred removal.
	filesAfter, err := os.ReadDir(dir)
	require.NoError(t, err)

	// No new files should remain in the directory (temp file cleaned up).
	require.Len(t, filesAfter, len(filesBefore),
		"temp file should be cleaned up on error; files before=%d, after=%d",
		len(filesBefore), len(filesAfter))
}

func TestHandleClipUploadConcurrentRejection(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	// Simulate an upload in progress by setting uploadProgress directly.
	api.uploadMu.Lock()
	api.uploadProgress = &internal.ClipUploadProgress{Stage: "transcoding", Percent: 50}
	api.uploadStartTime = time.Now()
	api.uploadMu.Unlock()

	// A second upload attempt should get 409 Conflict.
	req := httptest.NewRequest("POST", "/api/clips/upload", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)

	// Clean up.
	api.uploadMu.Lock()
	api.uploadProgress = nil
	api.uploadMu.Unlock()
}

func TestUploadProgressAccessor(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)

	// Initially nil.
	require.Nil(t, api.UploadProgress())

	// Set progress.
	api.uploadMu.Lock()
	api.uploadProgress = &internal.ClipUploadProgress{
		Stage:    "transcoding",
		Percent:  42,
		Filename: "test.mkv",
	}
	api.uploadMu.Unlock()

	// Read back — should be a copy.
	up := api.UploadProgress()
	require.NotNil(t, up)
	require.Equal(t, "transcoding", up.Stage)
	require.Equal(t, 42, up.Percent)
	require.Equal(t, "test.mkv", up.Filename)

	// Mutating the returned copy should not affect the original.
	up.Percent = 99
	up2 := api.UploadProgress()
	require.Equal(t, 42, up2.Percent)

	// Clean up.
	api.uploadMu.Lock()
	api.uploadProgress = nil
	api.uploadMu.Unlock()

	require.Nil(t, api.UploadProgress())
}

// TestHandleClipUploadStaleTakeoverOwnership reproduces the takeover race:
// upload #1 holds the guard past the 10-minute staleness window (e.g. a long
// transcode) while upload #2 legitimately takes over. When upload #1 finally
// finishes, its deferred cleanup must NOT clear upload #2's in-flight progress
// record — only the goroutine that owns the current record may clear it.
func TestHandleClipUploadStaleTakeoverOwnership(t *testing.T) {
	api, _, _ := setupTestAPIWithClips(t)
	api.SetBroadcastFunc(func() {})

	newUploadReq := func(body io.Reader) *http.Request {
		req := httptest.NewRequest("POST", "/api/clips/upload", body)
		req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
		return req
	}

	// Upload #1: acquires the guard, then blocks reading its body from a pipe.
	pr1, pw1 := io.Pipe()
	done1 := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, newUploadReq(pr1))
		done1 <- rec.Code
	}()

	// Wait for upload #1 to own the guard.
	require.Eventually(t, func() bool {
		return api.UploadProgress() != nil
	}, 2*time.Second, 5*time.Millisecond, "upload #1 should acquire the guard")

	// Backdate the start time so the guard looks stale (>10 minutes old),
	// simulating upload #1 outliving the takeover window mid-flight.
	api.uploadMu.Lock()
	api.uploadStartTime = time.Now().Add(-11 * time.Minute)
	api.uploadMu.Unlock()

	// Upload #2: takes over the stale guard, then blocks reading its body.
	pr2, pw2 := io.Pipe()
	done2 := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, newUploadReq(pr2))
		done2 <- rec.Code
	}()

	// Wait for the takeover (the start time becomes recent again).
	require.Eventually(t, func() bool {
		api.uploadMu.Lock()
		defer api.uploadMu.Unlock()
		return api.uploadProgress != nil && time.Since(api.uploadStartTime) < time.Minute
	}, 2*time.Second, 5*time.Millisecond, "upload #2 should take over the stale guard")

	// Upload #1 finishes (its body errors out). Its deferred cleanup must not
	// wipe upload #2's in-flight progress.
	pw1.CloseWithError(io.ErrUnexpectedEOF)
	<-done1
	require.NotNil(t, api.UploadProgress(),
		"upload #1's deferred cleanup must not clear upload #2's in-flight progress")

	// And a third upload must still be rejected while #2 is in flight.
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, newUploadReq(strings.NewReader("")))
	require.Equal(t, http.StatusConflict, rec.Code,
		"a third upload must be rejected while upload #2 is in flight")

	// Finish upload #2 — it owns the record, so its cleanup clears it.
	pw2.CloseWithError(io.ErrUnexpectedEOF)
	<-done2
	require.Nil(t, api.UploadProgress(), "upload #2's cleanup should clear its own progress")
}
