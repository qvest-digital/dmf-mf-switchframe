package playout

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PersistedState captures everything needed to resume playback after a crash.
// This is a JSON-serializable snapshot, not the full runtime objects.
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
