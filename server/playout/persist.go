package playout

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// PersistedState captures everything needed to resume playback after a crash.
// This is a JSON-serializable snapshot, not the full runtime objects.
//
// 1+1 Sync Protocol Design (future implementation):
//
// The active/standby pair uses this same PersistedState format for state
// replication. The active node broadcasts its state to the standby at the
// configured persist interval. The standby continuously applies incoming
// state to its own Manager via Recover().
//
// Heartbeat protocol:
//   - Active sends PersistedState every persist interval (default: 5s)
//   - Standby expects heartbeat within 3x the interval (15s default)
//   - If standby misses 3 consecutive heartbeats, it promotes itself
//   - On promotion: standby loads last received state, starts channels
//   - Fencing: promoted standby acquires a distributed lock before going
//     on-air to prevent split-brain (e.g., etcd lease or file lock on
//     shared storage)
//
// State format is sufficient for replication because it captures:
//   - Full playlist (not just a reference — standby may not share storage)
//   - Exact cursor position and channel state
//   - All pod states with wall-clock timestamps for elapsed adjustment
//   - Version field for forward-compatible schema evolution
//
// Transport options (not yet chosen):
//   - UDP multicast (LAN only, lowest latency)
//   - TCP with length-prefixed JSON (simple, reliable)
//   - Shared file on NFS/CIFS (operational simplicity, higher latency)
type PersistedState struct {
	Version   int                `json:"version"`
	Timestamp time.Time          `json:"timestamp"`
	Channels  []PersistedChannel `json:"channels"`
	AsRunFile string             `json:"asrun_file,omitempty"`
}

// PersistedChannel is the serializable state of a single playout channel.
type PersistedChannel struct {
	ID            int          `json:"id"`
	State         ChannelState `json:"state"`
	Mode          OnAirMode    `json:"mode"`
	Cursor        int          `json:"cursor"`
	ItemStartTime time.Time   `json:"item_start_time"`
	Playlist      *Playlist    `json:"playlist,omitempty"`
	Pods          []PersistedPod `json:"pods,omitempty"`
}

// PersistedPod is the serializable state of a single pod instance.
type PersistedPod struct {
	ID             string    `json:"id"`
	Config         PodConfig `json:"config"`
	State          PodState  `json:"state"`
	Armed          bool      `json:"armed"`
	ElapsedMs      int64     `json:"elapsed_ms"`
	FiredAt        int64     `json:"fired_at,omitempty"`
	SCTE35EventID  uint32    `json:"scte35_event_id,omitempty"`
}

// atomicWriteJSON writes v as JSON to path using a temp file + rename
// to ensure the file is never partially written.
func atomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".persist-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("sync temp: %w", err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// loadPersistedState reads and unmarshals a PersistedState from disk.
func loadPersistedState(path string) (*PersistedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state PersistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if state.Version != 1 {
		return nil, fmt.Errorf("persist: unsupported state version %d", state.Version)
	}

	return &state, nil
}

// StatePersisterConfig configures the background state persister.
type StatePersisterConfig struct {
	Dir      string
	Interval time.Duration
}

// stateSnapshotter is the interface needed by StatePersister.
type stateSnapshotter interface {
	Snapshot() PersistedState
}

// StatePersister periodically snapshots the manager state and writes it to disk.
// It performs a final save on Stop to minimize data loss on graceful shutdown.
type StatePersister struct {
	snapshotter stateSnapshotter
	config      StatePersisterConfig
	cancel      context.CancelFunc
	done        chan struct{}
}

// NewStatePersister creates a StatePersister. Call Start to begin periodic saves.
func NewStatePersister(s stateSnapshotter, config StatePersisterConfig) *StatePersister {
	if config.Interval <= 0 {
		config.Interval = 5 * time.Second
	}
	return &StatePersister{
		snapshotter: s,
		config:      config,
	}
}

// Path returns the full file path of the persisted state file.
func (p *StatePersister) Path() string {
	return filepath.Join(p.config.Dir, "playout-state.json")
}

// Start launches the background save goroutine.
func (p *StatePersister) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go p.run(ctx)
}

// Stop cancels the background goroutine, waits for it to finish, and performs
// a final save to ensure the most recent state is on disk.
func (p *StatePersister) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		<-p.done
	}
	// Final save on shutdown.
	p.save()
}

// run is the background goroutine that periodically snapshots and saves.
func (p *StatePersister) run(ctx context.Context) {
	defer close(p.done)

	ticker := time.NewTicker(p.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.save()
		}
	}
}

// save takes a snapshot and writes it atomically to disk.
func (p *StatePersister) save() {
	// Ensure directory exists.
	if err := os.MkdirAll(p.config.Dir, 0755); err != nil {
		slog.Warn("playout: persist dir creation failed", "dir", p.config.Dir, "error", err)
		return
	}

	snap := p.snapshotter.Snapshot()
	if err := atomicWriteJSON(p.Path(), &snap); err != nil {
		slog.Warn("playout: state persist failed", "error", err)
	}
}
