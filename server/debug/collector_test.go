package debug

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type mockProvider struct{}

func (m *mockProvider) DebugSnapshot() map[string]any {
	return map[string]any{"frames": 42}
}

func TestCollector_Snapshot(t *testing.T) {
	c := NewCollector()
	c.Register("test", &mockProvider{})
	c.EventLog().Add("test_event", nil)

	snap := c.Snapshot()

	require.NotNil(t, snap["test"], "expected test provider in snapshot")
	provider := snap["test"].(map[string]any)
	require.Equal(t, 42, provider["frames"])
	require.NotNil(t, snap["uptime_ms"], "expected uptime_ms in snapshot")
	require.NotNil(t, snap["events"], "expected events in snapshot")
}

func TestCollector_HandleSnapshot(t *testing.T) {
	c := NewCollector()
	c.Register("test", &mockProvider{})

	req := httptest.NewRequest("GET", "/api/debug/snapshot", nil)
	w := httptest.NewRecorder()
	c.HandleSnapshot(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var result map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err, "invalid JSON")
	require.NotNil(t, result["test"], "expected test in response")
}
