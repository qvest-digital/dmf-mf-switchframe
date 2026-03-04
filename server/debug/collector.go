package debug

import (
	"encoding/json"
	"net/http"
	"time"
)

// SnapshotProvider is implemented by components that contribute to debug snapshots.
type SnapshotProvider interface {
	DebugSnapshot() map[string]any
}

// Collector aggregates debug snapshots from registered providers.
type Collector struct {
	startTime time.Time
	providers map[string]SnapshotProvider
	eventLog  *EventLog
}

// NewCollector creates a Collector with a 100-entry event log.
func NewCollector() *Collector {
	return &Collector{
		startTime: time.Now(),
		providers: make(map[string]SnapshotProvider),
		eventLog:  NewEventLog(100),
	}
}

// Register adds a named provider.
func (c *Collector) Register(name string, p SnapshotProvider) {
	c.providers[name] = p
}

// EventLog returns the shared event log for components to write to.
func (c *Collector) EventLog() *EventLog {
	return c.eventLog
}

// Snapshot collects data from all providers into a single map.
func (c *Collector) Snapshot() map[string]any {
	result := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"uptime_ms": time.Since(c.startTime).Milliseconds(),
	}
	for name, p := range c.providers {
		result[name] = p.DebugSnapshot()
	}
	result["events"] = c.eventLog.Snapshot()
	return result
}

// HandleSnapshot is the HTTP handler for GET /api/debug/snapshot.
func (c *Collector) HandleSnapshot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c.Snapshot())
}
