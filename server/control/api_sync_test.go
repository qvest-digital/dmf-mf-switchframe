package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncHealth_ReturnsFields(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("GET", "/api/sync/health", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		PTPOffsetUS    int64  `json:"ptpOffsetUs"`
		LastCommandSeq uint64 `json:"lastCommandSeq"`
		UptimeMS       int64  `json:"uptimeMs"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.True(t, resp.UptimeMS >= 0, "uptime should be non-negative")
}
