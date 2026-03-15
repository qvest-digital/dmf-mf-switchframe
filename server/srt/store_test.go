package srt

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func validCallerConfig(key, streamID string) SourceConfig {
	return SourceConfig{
		Key:       key,
		Mode:      ModeCaller,
		Address:   "srt://192.168.1.100:9000",
		StreamID:  streamID,
		Label:     "Camera 1",
		LatencyMs: 120,
	}
}

func validListenerConfig(key, streamID string) SourceConfig {
	return SourceConfig{
		Key:      key,
		Mode:     ModeListener,
		StreamID: streamID,
		Label:    "Studio Feed",
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "srt_sources.json")

	// Create store, save a config.
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	cfg := validCallerConfig("srt:cam1", "live/cam1")
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Get it back from the same store instance.
	got, ok := s.Get("srt:cam1")
	if !ok {
		t.Fatal("Get returned false for saved key")
	}
	if got.Key != cfg.Key || got.Mode != cfg.Mode || got.Address != cfg.Address ||
		got.StreamID != cfg.StreamID || got.Label != cfg.Label || got.LatencyMs != cfg.LatencyMs {
		t.Fatalf("Get returned %+v, want %+v", got, cfg)
	}

	// Reload from disk — verify persistence.
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	got2, ok := s2.Get("srt:cam1")
	if !ok {
		t.Fatal("Get after reload returned false")
	}
	if got2.Key != cfg.Key || got2.StreamID != cfg.StreamID {
		t.Fatalf("Get after reload: got %+v, want %+v", got2, cfg)
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "srt_sources.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	cfg := validListenerConfig("srt:studio", "live/studio")
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.Delete("srt:studio"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok := s.Get("srt:studio")
	if ok {
		t.Fatal("Get returned true after Delete")
	}

	// Verify persistence of deletion.
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	_, ok = s2.Get("srt:studio")
	if ok {
		t.Fatal("Get after reload returned true for deleted key")
	}
}

func TestStoreList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "srt_sources.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	cfg1 := validCallerConfig("srt:cam1", "live/cam1")
	cfg2 := validListenerConfig("srt:studio", "live/studio")

	if err := s.Save(cfg1); err != nil {
		t.Fatalf("Save cfg1: %v", err)
	}
	if err := s.Save(cfg2); err != nil {
		t.Fatalf("Save cfg2: %v", err)
	}

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("List returned %d items, want 2", len(list))
	}

	// Sort by key for deterministic comparison (map iteration order is random).
	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })

	if list[0].Key != "srt:cam1" {
		t.Errorf("list[0].Key = %q, want %q", list[0].Key, "srt:cam1")
	}
	if list[1].Key != "srt:studio" {
		t.Errorf("list[1].Key = %q, want %q", list[1].Key, "srt:studio")
	}
}

func TestStoreUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "srt_sources.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	cfg := validCallerConfig("srt:cam1", "live/cam1")
	cfg.Label = "Original"
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Update same key with new label.
	cfg.Label = "Updated"
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	got, ok := s.Get("srt:cam1")
	if !ok {
		t.Fatal("Get returned false after update")
	}
	if got.Label != "Updated" {
		t.Errorf("Label = %q, want %q", got.Label, "Updated")
	}

	// List should still return exactly 1 entry.
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("List returned %d items after update, want 1", len(list))
	}
}

func TestStoreValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "srt_sources.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Missing key.
	err = s.Save(SourceConfig{Mode: ModeCaller, Address: "srt://host:9000", StreamID: "live/x"})
	if err == nil {
		t.Fatal("expected error for missing key")
	}

	// Bad prefix.
	err = s.Save(SourceConfig{Key: "cam1", Mode: ModeCaller, Address: "srt://host:9000", StreamID: "live/x"})
	if err == nil {
		t.Fatal("expected error for missing srt: prefix")
	}

	// Bad mode.
	err = s.Save(SourceConfig{Key: "srt:cam1", Mode: "push", StreamID: "live/x"})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}

	// Caller without address.
	err = s.Save(SourceConfig{Key: "srt:cam1", Mode: ModeCaller, StreamID: "live/x"})
	if err == nil {
		t.Fatal("expected error for caller without address")
	}

	// Missing streamID.
	err = s.Save(SourceConfig{Key: "srt:cam1", Mode: ModeListener})
	if err == nil {
		t.Fatal("expected error for missing streamID")
	}

	// Latency out of range.
	err = s.Save(SourceConfig{Key: "srt:cam1", Mode: ModeListener, StreamID: "live/x", LatencyMs: -1})
	if err == nil {
		t.Fatal("expected error for negative latency")
	}

	err = s.Save(SourceConfig{Key: "srt:cam1", Mode: ModeListener, StreamID: "live/x", LatencyMs: MaxLatency + 1})
	if err == nil {
		t.Fatal("expected error for latency above max")
	}

	// Delay out of range.
	err = s.Save(SourceConfig{Key: "srt:cam1", Mode: ModeListener, StreamID: "live/x", DelayMs: MaxDelay + 1})
	if err == nil {
		t.Fatal("expected error for delay above max")
	}

	// Verify nothing was persisted.
	list := s.List()
	if len(list) != 0 {
		t.Fatalf("List returned %d items, want 0 after validation failures", len(list))
	}
}

func TestStoreNewFromNonexistent(t *testing.T) {
	dir := t.TempDir()
	// Nested path that doesn't exist yet.
	path := filepath.Join(dir, "sub", "deep", "srt_sources.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore from nonexistent: %v", err)
	}

	list := s.List()
	if len(list) != 0 {
		t.Fatalf("List returned %d items, want 0 for new store", len(list))
	}

	// Save should create the directory hierarchy.
	cfg := validCallerConfig("srt:cam1", "live/cam1")
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save to new path: %v", err)
	}

	// Verify the file was created.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Reload from the new path.
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore reload from new path: %v", err)
	}
	got, ok := s2.Get("srt:cam1")
	if !ok {
		t.Fatal("Get after reload from new path returned false")
	}
	if got.Key != cfg.Key {
		t.Fatalf("Key mismatch after reload: got %q, want %q", got.Key, cfg.Key)
	}
}
