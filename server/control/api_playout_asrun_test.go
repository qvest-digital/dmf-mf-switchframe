package control

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/playout/bxf"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupTestPlayoutAPIWithAsRun(t *testing.T) (*API, *playout.AsRunLogger) {
	t.Helper()
	dir := t.TempDir()
	logger, err := playout.NewAsRunLogger(playout.AsRunLoggerConfig{Dir: dir, RingCapacity: 100})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	mgr := newTestPlayoutManager()
	mgr.SetAsRunLogger(logger)

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
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
	sw := switcher.NewTestSwitcher(t, programRelay)
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

// ---------------------------------------------------------------------------
// Task 12: GET /api/playout/channels/{n}/asrun/export
// ---------------------------------------------------------------------------

func TestHandleAsRunExportJSON(t *testing.T) {
	api, logger := setupTestPlayoutAPIWithAsRun(t)

	logger.Log(playout.AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      playout.AsRunItemStart,
		ChannelID: 1,
		ItemID:    "item-001",
		Title:     "Test Clip",
	})

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun/export", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var events []playout.AsRunEvent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &events))
	require.Len(t, events, 1)
	require.Equal(t, "item-001", events[0].ItemID)
}

func TestHandleAsRunExportJSON_ExplicitFormat(t *testing.T) {
	api, logger := setupTestPlayoutAPIWithAsRun(t)

	logger.Log(playout.AsRunEvent{
		Timestamp: time.Now().UTC(),
		Type:      playout.AsRunItemStart,
		ChannelID: 1,
		ItemID:    "item-002",
	})

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun/export?format=json", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var events []playout.AsRunEvent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &events))
	require.Len(t, events, 1)
}

func TestHandleAsRunExportBXF(t *testing.T) {
	api, logger := setupTestPlayoutAPIWithAsRun(t)

	now := time.Now().UTC()
	logger.Log(playout.AsRunEvent{
		Timestamp: now,
		Type:      playout.AsRunItemStart,
		ChannelID: 1,
		ItemID:    "item-001",
		Title:     "Test Clip",
	})
	logger.Log(playout.AsRunEvent{
		Timestamp:        now.Add(5 * time.Second),
		Type:             playout.AsRunItemEnd,
		ChannelID:        1,
		ItemID:           "item-001",
		Title:            "Test Clip",
		ActualDurationMs: 5000,
	})

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun/export?format=bxf", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/xml")

	var msg bxf.BxfMessage
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &msg))
	require.NotNil(t, msg.BxfData.AsRun)
	require.Len(t, msg.BxfData.AsRun.Details, 1)
	require.Equal(t, "item-001", msg.BxfData.AsRun.Details[0].Content.ContentId.HouseNumber)
	require.Equal(t, "Test Clip", msg.BxfData.AsRun.Details[0].Content.Name)
}

func TestHandleAsRunExport_NoLogger(t *testing.T) {
	mgr := newTestPlayoutManager()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw, WithPlayoutManager(mgr))

	req := httptest.NewRequest("GET", "/api/playout/channels/1/asrun/export?format=bxf", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusNotImplemented, w.Code)
}

// ---------------------------------------------------------------------------
// Task 13: POST /api/playout/import/bxf
// ---------------------------------------------------------------------------

func TestHandleBXFImport(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithAsRun(t)

	bxfXML := `<?xml version="1.0" encoding="UTF-8"?>
<BxfMessage>
  <BxfData>
    <Schedule>
      <ScheduleDetail>
        <Content>
          <ContentId><HouseNumber>CLIP001</HouseNumber></ContentId>
          <Name>Morning News</Name>
          <Duration>PT30.000S</Duration>
        </Content>
        <ScheduledDuration>PT30.000S</ScheduledDuration>
        <Type>program</Type>
      </ScheduleDetail>
      <ScheduleDetail>
        <Content>
          <ContentId><HouseNumber>CLIP002</HouseNumber></ContentId>
          <Name>Weather Update</Name>
          <Duration>PT10.000S</Duration>
        </Content>
        <ScheduledDuration>PT10.000S</ScheduledDuration>
        <Type>program</Type>
      </ScheduleDetail>
    </Schedule>
  </BxfData>
</BxfMessage>`

	req := httptest.NewRequest("POST", "/api/playout/import/bxf?channel=1&name=morning-block",
		strings.NewReader(bxfXML))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var ch playout.ChannelStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ch))
	require.Equal(t, "cued", ch.State)
	require.Equal(t, "morning-block", ch.PlaylistName)
	require.Equal(t, 2, ch.ItemCount)
}

func TestHandleBXFImport_DefaultChannelAndName(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithAsRun(t)

	bxfXML := `<BxfMessage><BxfData><Schedule><ScheduleDetail>
		<Content><ContentId><HouseNumber>C1</HouseNumber></ContentId><Name>Test</Name>
		<Duration>PT5.000S</Duration></Content>
		<ScheduledDuration>PT5.000S</ScheduledDuration>
	</ScheduleDetail></Schedule></BxfData></BxfMessage>`

	req := httptest.NewRequest("POST", "/api/playout/import/bxf", strings.NewReader(bxfXML))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var ch playout.ChannelStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ch))
	require.Equal(t, "cued", ch.State)
	require.Equal(t, "bxf-import", ch.PlaylistName)
}

func TestHandleBXFImportInvalidXML(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithAsRun(t)

	req := httptest.NewRequest("POST", "/api/playout/import/bxf", strings.NewReader("not xml at all"))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleBXFImport_InvalidChannel(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithAsRun(t)

	bxfXML := `<BxfMessage><BxfData><Schedule><ScheduleDetail>
		<Content><ContentId><HouseNumber>C1</HouseNumber></ContentId><Name>Test</Name>
		<Duration>PT5.000S</Duration></Content>
		<ScheduledDuration>PT5.000S</ScheduledDuration>
	</ScheduleDetail></Schedule></BxfData></BxfMessage>`

	req := httptest.NewRequest("POST", "/api/playout/import/bxf?channel=99", strings.NewReader(bxfXML))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleBXFImport_EmptySchedule(t *testing.T) {
	api, _ := setupTestPlayoutAPIWithAsRun(t)

	bxfXML := `<BxfMessage><BxfData><Schedule></Schedule></BxfData></BxfMessage>`

	req := httptest.NewRequest("POST", "/api/playout/import/bxf", strings.NewReader(bxfXML))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}
