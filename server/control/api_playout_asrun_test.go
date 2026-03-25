package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupTestPlayoutAPIWithAsRun(t *testing.T) (*API, *playout.AsRunLogger) {
	t.Helper()
	dir := t.TempDir()
	logger, err := playout.NewAsRunLogger(playout.AsRunLoggerConfig{Dir: dir, RingCapacity: 100})
	require.NoError(t, err)
	t.Cleanup(func() { logger.Close() })

	mgr := newTestPlayoutManager()
	mgr.SetAsRunLogger(logger)

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw, WithPlayoutManager(mgr))
	return api, logger
}

func TestHandleAsRunQuery(t *testing.T) {
	api, logger := setupTestPlayoutAPIWithAsRun(t)

	logger.Log(playout.AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      playout.AsRunItemStart,
		ChannelID: 1,
		ItemID:    "item-001",
		Title:     "Test",
	})

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var events []playout.AsRunEvent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &events))
	require.Len(t, events, 1)
	require.Equal(t, "item-001", events[0].ItemID)
}

func TestHandleAsRunQuery_ChannelFilter(t *testing.T) {
	api, logger := setupTestPlayoutAPIWithAsRun(t)

	logger.Log(playout.AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      playout.AsRunItemStart,
		ChannelID: 1,
		ItemID:    "ch1-item",
	})
	logger.Log(playout.AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      playout.AsRunItemStart,
		ChannelID: 2,
		ItemID:    "ch2-item",
	})

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var events []playout.AsRunEvent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &events))
	require.Len(t, events, 1)
	require.Equal(t, "ch1-item", events[0].ItemID)
}

func TestHandleAsRunQuery_TypeFilter(t *testing.T) {
	api, logger := setupTestPlayoutAPIWithAsRun(t)

	logger.Log(playout.AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      playout.AsRunItemStart,
		ChannelID: 1,
		ItemID:    "start-item",
	})
	logger.Log(playout.AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      playout.AsRunItemEnd,
		ChannelID: 1,
		ItemID:    "end-item",
	})

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun?type=item_start", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var events []playout.AsRunEvent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &events))
	require.Len(t, events, 1)
	require.Equal(t, "start-item", events[0].ItemID)
}

func TestHandleAsRunQuery_Limit(t *testing.T) {
	api, logger := setupTestPlayoutAPIWithAsRun(t)

	for i := 0; i < 5; i++ {
		logger.Log(playout.AsRunEvent{
			Timestamp: time.Now().UTC(),
			Type:      playout.AsRunItemStart,
			ChannelID: 1,
			ItemID:    "item",
		})
	}

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun?limit=2", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var events []playout.AsRunEvent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &events))
	require.Len(t, events, 2)
}

func TestHandleAsRunQuery_NoLogger(t *testing.T) {
	mgr := newTestPlayoutManager()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw, WithPlayoutManager(mgr))

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestHandleAsRunQuery_InvalidChannel(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithAsRun(t)

	req := httptest.NewRequest("GET", "/api/playout/channels/99/asrun", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}
