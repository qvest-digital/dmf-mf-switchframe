package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

func newTestPlayoutManager() *playout.Manager {
	return playout.NewManager(playout.ManagerConfig{
		DemuxFunc: func(path string) ([]playout.DecodedFrame, error) {
			frames := make([]playout.DecodedFrame, 10)
			for i := range frames {
				frames[i] = playout.DecodedFrame{
					YUV:        make([]byte, 1920*1080*3/2),
					PTS:        int64(i) * 3003,
					IsKeyframe: i == 0,
					Width:      1920,
					Height:     1080,
				}
			}
			return frames, nil
		},
	})
}

func setupTestPlayoutAPI(t *testing.T) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
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
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw)

	req := httptest.NewRequest("GET", "/api/playout/channels", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Without playout manager, routes aren't registered — expect 405 or 404.
	require.NotEqual(t, http.StatusOK, rec.Code)
}
