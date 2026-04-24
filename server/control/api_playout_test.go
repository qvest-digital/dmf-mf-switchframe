package control

import (
	"encoding/json"
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
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

func newTestPlayoutManager() *playout.Manager {
	frames := make([]playout.DecodedFrame, 10)
	for i := range frames {
		frames[i] = playout.DecodedFrame{
			YUV:        make([]byte, switcher.ColorYUV420_8bit.FrameSize(1920, 1080)),
			PTS:        int64(i) * 3003,
			IsKeyframe: i == 0,
			Width:      1920,
			Height:     1080,
		}
	}
	return playout.NewManager(playout.ManagerConfig{
		AssetProvider:        playout.NewFakeAssetProvider(frames),
		AllowedLocalPrefixes: []string{"/"},
	})
}

func setupTestPlayoutAPI(t *testing.T) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mgr := newTestPlayoutManager()
	api := NewAPI(sw, WithPlayoutManager(mgr))
	return api
}

func validPlaylistJSON() string {
	return `{
		"id": "pl-1",
		"name": "Test Playlist",
		"default_transition": {"type": "cut"},
		"items": [
			{
				"id": "item-1",
				"type": "clip",
				"title": "First Clip",
				"asset_uri": "local:///tmp/test1.ts",
				"duration_ms": 5000
			},
			{
				"id": "item-2",
				"type": "clip",
				"title": "Second Clip",
				"asset_uri": "local:///tmp/test2.ts",
				"duration_ms": 3000
			}
		]
	}`
}

func TestPlayoutRoutes_ListChannels(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("GET", "/api/playout/channels", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var channels []playout.ChannelStatus
	err := json.NewDecoder(rec.Body).Decode(&channels)
	require.NoError(t, err)
	require.Len(t, channels, playout.MaxChannels)

	// All channels should be stopped initially.
	for _, ch := range channels {
		require.Equal(t, "stopped", ch.State)
	}
}

func TestPlayoutRoutes_GetChannel(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("GET", "/api/playout/channels/1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var ch playout.ChannelStatus
	err := json.NewDecoder(rec.Body).Decode(&ch)
	require.NoError(t, err)
	require.Equal(t, 1, ch.ID)
	require.Equal(t, "stopped", ch.State)
}

func TestPlayoutRoutes_GetChannel_InvalidID(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("GET", "/api/playout/channels/99", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutRoutes_GetChannel_NonNumericID(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("GET", "/api/playout/channels/abc", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutRoutes_LoadPlaylist(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	body := validPlaylistJSON()
	req := httptest.NewRequest("POST", "/api/playout/channels/1/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var ch playout.ChannelStatus
	err := json.NewDecoder(rec.Body).Decode(&ch)
	require.NoError(t, err)
	require.Equal(t, 1, ch.ID)
	require.Equal(t, "cued", ch.State)
	require.Equal(t, "Test Playlist", ch.PlaylistName)
	require.Equal(t, 2, ch.ItemCount)
}

func TestPlayoutRoutes_LoadPlaylist_InvalidJSON(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("POST", "/api/playout/channels/1/load", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutRoutes_LoadPlaylist_EmptyPlaylist(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	body := `{"id": "pl-1", "name": "Empty", "items": []}`
	req := httptest.NewRequest("POST", "/api/playout/channels/1/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutLoad_RejectsEtcPasswdAssetURI(t *testing.T) {
	// Manager configured with /tmp as the only allowed local prefix —
	// /etc/passwd is outside it, so loading this playlist must return 400
	// and the channel must remain in stopped state with no playlist cued.
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	frames := []playout.DecodedFrame{
		{
			YUV:        make([]byte, switcher.ColorYUV420_8bit.FrameSize(1920, 1080)),
			IsKeyframe: true, Width: 1920, Height: 1080,
		},
	}
	mgr := playout.NewManager(playout.ManagerConfig{
		AssetProvider:        playout.NewFakeAssetProvider(frames),
		AllowedLocalPrefixes: []string{"/tmp"},
	})
	api := NewAPI(sw, WithPlayoutManager(mgr))

	body := `{"id":"exfil","name":"exfil","items":[{"id":"x","type":"clip","asset_uri":"/etc/passwd","duration_ms":1000}]}`
	req := httptest.NewRequest("POST", "/api/playout/channels/1/load", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())

	// Channel must remain stopped with no playlist cued.
	getReq := httptest.NewRequest("GET", "/api/playout/channels/1", nil)
	getRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)
	var ch playout.ChannelStatus
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&ch))
	require.Equal(t, "stopped", ch.State)
	require.Equal(t, 0, ch.ItemCount)
}

func TestPlayoutRoutes_StartWithoutPlaylist(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("POST", "/api/playout/channels/1/start", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutRoutes_StartAfterLoad(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	// Load a playlist first.
	body := validPlaylistJSON()
	loadReq := httptest.NewRequest("POST", "/api/playout/channels/1/load", strings.NewReader(body))
	loadReq.Header.Set("Content-Type", "application/json")
	loadRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(loadRec, loadReq)
	require.Equal(t, http.StatusOK, loadRec.Code)

	// Start playback.
	startReq := httptest.NewRequest("POST", "/api/playout/channels/1/start", nil)
	startRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(startRec, startReq)

	require.Equal(t, http.StatusOK, startRec.Code)

	var ch playout.ChannelStatus
	err := json.NewDecoder(startRec.Body).Decode(&ch)
	require.NoError(t, err)
	require.Equal(t, "on_air", ch.State)

	// Stop to clean up the run goroutine.
	stopReq := httptest.NewRequest("POST", "/api/playout/channels/1/stop", nil)
	stopRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(stopRec, stopReq)
	require.Equal(t, http.StatusOK, stopRec.Code)
}

func TestPlayoutRoutes_Stop(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	// Load and start.
	body := validPlaylistJSON()
	loadReq := httptest.NewRequest("POST", "/api/playout/channels/1/load", strings.NewReader(body))
	loadReq.Header.Set("Content-Type", "application/json")
	loadRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(loadRec, loadReq)
	require.Equal(t, http.StatusOK, loadRec.Code)

	startReq := httptest.NewRequest("POST", "/api/playout/channels/1/start", nil)
	startRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(startRec, startReq)
	require.Equal(t, http.StatusOK, startRec.Code)

	// Stop.
	stopReq := httptest.NewRequest("POST", "/api/playout/channels/1/stop", nil)
	stopRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(stopRec, stopReq)

	require.Equal(t, http.StatusOK, stopRec.Code)

	var ch playout.ChannelStatus
	err := json.NewDecoder(stopRec.Body).Decode(&ch)
	require.NoError(t, err)
	require.Equal(t, "stopped", ch.State)
}

func TestPlayoutRoutes_InvalidChannel(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/playout/channels/99"},
		{"POST", "/api/playout/channels/0/load"},
		{"POST", "/api/playout/channels/99/start"},
		{"POST", "/api/playout/channels/99/stop"},
		{"POST", "/api/playout/channels/99/next"},
		{"POST", "/api/playout/channels/99/previous"},
		{"POST", "/api/playout/channels/99/jump"},
		{"POST", "/api/playout/channels/99/manual"},
		{"POST", "/api/playout/channels/99/auto"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			var body string
			if ep.method == "POST" && strings.HasSuffix(ep.path, "/load") {
				body = validPlaylistJSON()
			} else if strings.HasSuffix(ep.path, "/jump") {
				body = `{"itemId": "item-1"}`
			}
			req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for %s %s, got %d: %s",
				ep.method, ep.path, rec.Code, rec.Body.String())
		})
	}
}

func TestPlayoutRoutes_Transport(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	// Load and start a playlist with live items that hold for advance signals.
	body := `{
		"id": "pl-1",
		"name": "Transport Test",
		"default_transition": {"type": "cut"},
		"items": [
			{"id": "item-1", "type": "live", "title": "Live 1", "source": "camera1"},
			{"id": "item-2", "type": "live", "title": "Live 2", "source": "camera2"},
			{"id": "item-3", "type": "live", "title": "Live 3", "source": "camera3"}
		]
	}`
	loadReq := httptest.NewRequest("POST", "/api/playout/channels/1/load", strings.NewReader(body))
	loadReq.Header.Set("Content-Type", "application/json")
	loadRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(loadRec, loadReq)
	require.Equal(t, http.StatusOK, loadRec.Code)

	startReq := httptest.NewRequest("POST", "/api/playout/channels/1/start", nil)
	startRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(startRec, startReq)
	require.Equal(t, http.StatusOK, startRec.Code)

	// Next.
	nextReq := httptest.NewRequest("POST", "/api/playout/channels/1/next", nil)
	nextRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(nextRec, nextReq)
	require.Equal(t, http.StatusOK, nextRec.Code)

	// Jump to item-3.
	jumpBody := `{"itemId": "item-3"}`
	jumpReq := httptest.NewRequest("POST", "/api/playout/channels/1/jump", strings.NewReader(jumpBody))
	jumpReq.Header.Set("Content-Type", "application/json")
	jumpRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(jumpRec, jumpReq)
	require.Equal(t, http.StatusOK, jumpRec.Code)

	// Previous.
	prevReq := httptest.NewRequest("POST", "/api/playout/channels/1/previous", nil)
	prevRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(prevRec, prevReq)
	require.Equal(t, http.StatusOK, prevRec.Code)

	// Manual.
	manualReq := httptest.NewRequest("POST", "/api/playout/channels/1/manual", nil)
	manualRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(manualRec, manualReq)
	require.Equal(t, http.StatusOK, manualRec.Code)

	var manualCh playout.ChannelStatus
	err := json.NewDecoder(manualRec.Body).Decode(&manualCh)
	require.NoError(t, err)
	require.Equal(t, "manual", manualCh.Mode)

	// Auto.
	autoReq := httptest.NewRequest("POST", "/api/playout/channels/1/auto", nil)
	autoRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(autoRec, autoReq)
	require.Equal(t, http.StatusOK, autoRec.Code)

	var autoCh playout.ChannelStatus
	err = json.NewDecoder(autoRec.Body).Decode(&autoCh)
	require.NoError(t, err)
	require.Equal(t, "automated", autoCh.Mode)

	// Stop to clean up.
	stopReq := httptest.NewRequest("POST", "/api/playout/channels/1/stop", nil)
	stopRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(stopRec, stopReq)
	require.Equal(t, http.StatusOK, stopRec.Code)
}

func TestPlayoutRoutes_JumpInvalidJSON(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("POST", "/api/playout/channels/1/jump", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutRoutes_JumpMissingItemID(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("POST", "/api/playout/channels/1/jump", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutRoutes_NextWithoutOnAir(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	// Next on a stopped channel should fail.
	req := httptest.NewRequest("POST", "/api/playout/channels/1/next", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutRoutes_PreviousWithoutOnAir(t *testing.T) {
	api := setupTestPlayoutAPI(t)

	req := httptest.NewRequest("POST", "/api/playout/channels/1/previous", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutRoutes_NoManager(t *testing.T) {
	// API without playout manager should not register routes.
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw)

	req := httptest.NewRequest("GET", "/api/playout/channels", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Without playout manager, routes aren't registered — expect 405 or 404.
	require.NotEqual(t, http.StatusOK, rec.Code)
}

// ---------------------------------------------------------------------------
// Cache endpoint tests
// ---------------------------------------------------------------------------

func setupTestPlayoutAPIWithCache(t *testing.T) (*API, *playout.Cache, string) {
	t.Helper()
	dir := t.TempDir()
	cache, err := playout.NewCache(playout.CacheConfig{Dir: dir, MaxBytes: 1 << 30})
	require.NoError(t, err)

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mgr := newTestPlayoutManager()
	api := NewAPI(sw, WithPlayoutManager(mgr), WithPlayoutCache(cache))
	return api, cache, dir
}

func TestPlayoutCacheStatus(t *testing.T) {
	api, _, _ := setupTestPlayoutAPIWithCache(t)

	req := httptest.NewRequest("GET", "/api/playout/cache/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var status playout.CacheStatus
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Equal(t, int64(1<<30), status.MaxBytes)
	require.Equal(t, 0, status.ItemCount)
}

func TestPlayoutCacheStatus_NilCache(t *testing.T) {
	// API without cache should return 501.
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mgr := newTestPlayoutManager()
	api := NewAPI(sw, WithPlayoutManager(mgr))

	req := httptest.NewRequest("GET", "/api/playout/cache/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestPlayoutCacheLookup(t *testing.T) {
	api, cache, dir := setupTestPlayoutAPIWithCache(t)

	// Put a cached entry with local path under the cache directory.
	localPath, err := cache.Reserve("local:///media/test.ts")
	require.NoError(t, err)
	_ = dir // dir is the cache root

	err = cache.Put(&playout.CacheEntry{
		AssetURI:  "local:///media/test.ts",
		LocalPath: localPath,
		State:     playout.AssetCached,
		ByteSize:  1024,
	})
	require.NoError(t, err)

	body := `{"asset_uri": "local:///media/test.ts"}`
	req := httptest.NewRequest("POST", "/api/playout/cache/lookup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var entry playout.CacheEntry
	err = json.NewDecoder(rec.Body).Decode(&entry)
	require.NoError(t, err)
	require.Equal(t, "local:///media/test.ts", entry.AssetURI)
}

func TestPlayoutCacheLookup_NotFound(t *testing.T) {
	api, _, _ := setupTestPlayoutAPIWithCache(t)

	body := `{"asset_uri": "local:///nonexistent.ts"}`
	req := httptest.NewRequest("POST", "/api/playout/cache/lookup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPlayoutCacheLookup_BadJSON(t *testing.T) {
	api, _, _ := setupTestPlayoutAPIWithCache(t)

	req := httptest.NewRequest("POST", "/api/playout/cache/lookup", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutCacheLookup_MissingURI(t *testing.T) {
	api, _, _ := setupTestPlayoutAPIWithCache(t)

	req := httptest.NewRequest("POST", "/api/playout/cache/lookup", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutCacheEvict(t *testing.T) {
	api, cache, _ := setupTestPlayoutAPIWithCache(t)

	// Put an entry with local path under the cache directory.
	localPath, err := cache.Reserve("test-asset")
	require.NoError(t, err)

	err = cache.Put(&playout.CacheEntry{
		AssetURI:  "test-asset",
		LocalPath: localPath,
		State:     playout.AssetCached,
		ByteSize:  1024,
	})
	require.NoError(t, err)

	body := `{"asset_uri": "test-asset"}`
	req := httptest.NewRequest("POST", "/api/playout/cache/evict", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Verify it's gone.
	status := cache.Status()
	require.Equal(t, 0, status.ItemCount)
}

func TestPlayoutCacheEvict_NotFound(t *testing.T) {
	api, _, _ := setupTestPlayoutAPIWithCache(t)

	body := `{"asset_uri": "nonexistent"}`
	req := httptest.NewRequest("POST", "/api/playout/cache/evict", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// ---------------------------------------------------------------------------
// Clip library endpoint tests
// ---------------------------------------------------------------------------

func TestPlayoutClips_BothSources(t *testing.T) {
	// Create a clip store with one clip.
	clipDir := t.TempDir()
	cs, err := clip.NewStore(clipDir, 1<<30)
	require.NoError(t, err)

	c := &clip.Clip{
		Name:       "Intro Bumper",
		Filename:   "intro.ts",
		Source:     clip.SourceUpload,
		DurationMs: 15000,
		ByteSize:   1024,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, cs.Add(c))

	// Write the file so the path is valid.
	require.NoError(t, os.WriteFile(filepath.Join(clipDir, "intro.ts"), []byte("data"), 0o644))

	// Create sample clips.
	samples := []playout.SampleClip{
		{Name: "holding_slate", Title: "Holding Slate", Path: "/tmp/samples/holding.ts", Duration: 10},
		{Name: "bumper_in", Title: "Bumper In", Path: "/tmp/samples/bumper.ts", Duration: 3},
	}

	// Create API with both clip store and samples.
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mgr := newTestPlayoutManager()
	api := NewAPI(sw, WithPlayoutManager(mgr), WithClipStore(cs), WithPlayoutSamples(samples))

	req := httptest.NewRequest("GET", "/api/playout/clips", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	type playoutClip struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Duration int    `json:"duration"`
		Source   string `json:"source"`
	}
	var clips []playoutClip
	err = json.NewDecoder(rec.Body).Decode(&clips)
	require.NoError(t, err)
	require.Len(t, clips, 3) // 1 clip store + 2 samples

	// Verify clip store entry.
	require.Equal(t, "Intro Bumper", clips[0].Name)
	require.Equal(t, filepath.Join(clipDir, "intro.ts"), clips[0].Path)
	require.Equal(t, 15, clips[0].Duration)
	require.Equal(t, "clip-store", clips[0].Source)

	// Verify sample entries.
	require.Equal(t, "Holding Slate", clips[1].Name)
	require.Equal(t, "sample", clips[1].Source)
	require.Equal(t, "Bumper In", clips[2].Name)
	require.Equal(t, "sample", clips[2].Source)
}

func TestPlayoutClips_NoClipStoreNoSamples(t *testing.T) {
	// API with neither clip store nor samples should return empty array.
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mgr := newTestPlayoutManager()
	api := NewAPI(sw, WithPlayoutManager(mgr))

	req := httptest.NewRequest("GET", "/api/playout/clips", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var clips []json.RawMessage
	err := json.NewDecoder(rec.Body).Decode(&clips)
	require.NoError(t, err)
	require.Len(t, clips, 0)
}

func TestPlayoutClips_RecordingSource(t *testing.T) {
	// A clip with source "recording" should be tagged accordingly.
	clipDir := t.TempDir()
	cs, err := clip.NewStore(clipDir, 1<<30)
	require.NoError(t, err)

	c := &clip.Clip{
		Name:       "Recording Clip",
		Filename:   "rec.ts",
		Source:     clip.SourceRecording,
		DurationMs: 60000,
		ByteSize:   2048,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, cs.Add(c))

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mgr := newTestPlayoutManager()
	api := NewAPI(sw, WithPlayoutManager(mgr), WithClipStore(cs))

	req := httptest.NewRequest("GET", "/api/playout/clips", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	type playoutClip struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Duration int    `json:"duration"`
		Source   string `json:"source"`
	}
	var clips []playoutClip
	err = json.NewDecoder(rec.Body).Decode(&clips)
	require.NoError(t, err)
	require.Len(t, clips, 1)
	require.Equal(t, "recording", clips[0].Source)
}

// ---------------------------------------------------------------------------
// Saved playlist CRUD tests
// ---------------------------------------------------------------------------

func setupTestPlayoutAPIWithSaveDir(t *testing.T) (*API, string) {
	t.Helper()
	saveDir := t.TempDir()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mgr := newTestPlayoutManager()
	api := NewAPI(sw, WithPlayoutManager(mgr), WithPlayoutSaveDir(saveDir))
	return api, saveDir
}

func TestPlayoutSaved_ListEmpty(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithSaveDir(t)

	req := httptest.NewRequest("GET", "/api/playout/saved", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var names []string
	err := json.NewDecoder(rec.Body).Decode(&names)
	require.NoError(t, err)
	require.Len(t, names, 0)
}

func TestPlayoutSaved_SaveAndList(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithSaveDir(t)

	// Save a playlist.
	body := validPlaylistJSON()
	req := httptest.NewRequest("POST", "/api/playout/saved", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// List should now contain it.
	listReq := httptest.NewRequest("GET", "/api/playout/saved", nil)
	listRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(listRec, listReq)

	require.Equal(t, http.StatusOK, listRec.Code)

	var names []string
	err := json.NewDecoder(listRec.Body).Decode(&names)
	require.NoError(t, err)
	require.Len(t, names, 1)
	require.Equal(t, "Test Playlist", names[0])
}

func TestPlayoutSaved_SaveAndGet(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithSaveDir(t)

	// Save.
	body := validPlaylistJSON()
	req := httptest.NewRequest("POST", "/api/playout/saved", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Get.
	getReq := httptest.NewRequest("GET", "/api/playout/saved/Test%20Playlist", nil)
	getRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(getRec, getReq)

	require.Equal(t, http.StatusOK, getRec.Code)

	var pl playout.Playlist
	err := json.NewDecoder(getRec.Body).Decode(&pl)
	require.NoError(t, err)
	require.Equal(t, "Test Playlist", pl.Name)
	require.Len(t, pl.Items, 2)
}

func TestPlayoutSaved_GetNotFound(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithSaveDir(t)

	req := httptest.NewRequest("GET", "/api/playout/saved/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPlayoutSaved_Delete(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithSaveDir(t)

	// Save first.
	body := validPlaylistJSON()
	req := httptest.NewRequest("POST", "/api/playout/saved", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Delete.
	delReq := httptest.NewRequest("DELETE", "/api/playout/saved/Test%20Playlist", nil)
	delRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(delRec, delReq)
	require.Equal(t, http.StatusOK, delRec.Code)

	// List should be empty now.
	listReq := httptest.NewRequest("GET", "/api/playout/saved", nil)
	listRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(listRec, listReq)

	var names []string
	err := json.NewDecoder(listRec.Body).Decode(&names)
	require.NoError(t, err)
	require.Len(t, names, 0)
}

func TestPlayoutSaved_DeleteNotFound(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithSaveDir(t)

	req := httptest.NewRequest("DELETE", "/api/playout/saved/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPlayoutSaved_InvalidName(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithSaveDir(t)

	// Name with path traversal.
	body := `{"id":"pl-1","name":"../evil","default_transition":{"type":"cut"},"items":[{"id":"i1","type":"clip","title":"x","asset_uri":"/tmp/x.ts"}]}`
	req := httptest.NewRequest("POST", "/api/playout/saved", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlayoutSaved_NotConfigured(t *testing.T) {
	// API without save dir should return 501.
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mgr := newTestPlayoutManager()
	api := NewAPI(sw, WithPlayoutManager(mgr))

	req := httptest.NewRequest("GET", "/api/playout/saved", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestSanitizePlaylistName(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"My Playlist", "My Playlist", false},
		{"  spaces  ", "spaces", false},
		{"", "", true},
		{"   ", "", true}, // whitespace-only
		{"hello-world_123", "hello-world_123", false},
		{"../evil", "", true},
		{"..\\evil", "", true}, // backslash traversal
		{"has/slash", "", true},
		{"has\\backslash", "", true},
		{"has@symbol", "", true},
		{"---", "---", false}, // all-dash is valid
		{strings.Repeat("a", 101), "", true},
		{strings.Repeat("a", 100), strings.Repeat("a", 100), false}, // exactly 100 chars ok
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := sanitizePlaylistName(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}
