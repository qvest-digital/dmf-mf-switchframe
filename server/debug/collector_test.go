package debug

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

	if snap["test"] == nil {
		t.Fatal("expected test provider in snapshot")
	}
	provider := snap["test"].(map[string]any)
	if provider["frames"] != 42 {
		t.Errorf("expected frames=42, got %v", provider["frames"])
	}
	if snap["uptime_ms"] == nil {
		t.Error("expected uptime_ms in snapshot")
	}
	if snap["events"] == nil {
		t.Error("expected events in snapshot")
	}
}

func TestCollector_HandleSnapshot(t *testing.T) {
	c := NewCollector()
	c.Register("test", &mockProvider{})

	req := httptest.NewRequest("GET", "/api/debug/snapshot", nil)
	w := httptest.NewRecorder()
	c.HandleSnapshot(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %s", w.Header().Get("Content-Type"))
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["test"] == nil {
		t.Error("expected test in response")
	}
}
