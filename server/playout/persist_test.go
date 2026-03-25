package playout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChannel_Snapshot(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(5),
	})

	pl := testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	snap := ch.Snapshot()

	if snap.ID != 1 {
		t.Errorf("Snapshot().ID = %d, want 1", snap.ID)
	}
	if snap.State != StateCued {
		t.Errorf("Snapshot().State = %v, want StateCued", snap.State)
	}
	if snap.Mode != ModeAutomated {
		t.Errorf("Snapshot().Mode = %v, want ModeAutomated", snap.Mode)
	}
	if snap.Cursor != 0 {
		t.Errorf("Snapshot().Cursor = %d, want 0", snap.Cursor)
	}
	if snap.Playlist == nil {
		t.Fatal("Snapshot().Playlist is nil")
	}
	if snap.Playlist.ID != "test-pl" {
		t.Errorf("Snapshot().Playlist.ID = %q, want %q", snap.Playlist.ID, "test-pl")
	}
	if len(snap.Playlist.Items) != 3 {
		t.Errorf("Snapshot().Playlist.Items len = %d, want 3", len(snap.Playlist.Items))
	}

	// Verify deep copy: mutating snapshot must not affect original.
	snap.Playlist.Items[0].Title = "MUTATED"
	if ch.playlist.Items[0].Title == "MUTATED" {
		t.Error("Snapshot playlist shares Items slice with channel")
	}
}

func TestChannel_Snapshot_NoPlaylist(t *testing.T) {
	ch := NewChannel(2, ChannelConfig{})
	snap := ch.Snapshot()

	if snap.ID != 2 {
		t.Errorf("Snapshot().ID = %d, want 2", snap.ID)
	}
	if snap.State != StateStopped {
		t.Errorf("Snapshot().State = %v, want StateStopped", snap.State)
	}
	if snap.Playlist != nil {
		t.Error("Snapshot().Playlist should be nil when no playlist loaded")
	}
}

func TestManager_Snapshot(t *testing.T) {
	provider := fakeProvider(5)
	m := NewManager(ManagerConfig{AssetProvider: provider})

	// Load a playlist on channel 1.
	pl := testPlaylist(clipItem("a"), clipItem("b"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	snap := m.Snapshot()

	if snap.Version != 1 {
		t.Errorf("Snapshot().Version = %d, want 1", snap.Version)
	}
	if snap.Timestamp.IsZero() {
		t.Error("Snapshot().Timestamp is zero")
	}
	if len(snap.Channels) != MaxChannels {
		t.Fatalf("Snapshot().Channels len = %d, want %d", len(snap.Channels), MaxChannels)
	}

	// Channel 1 should be cued with playlist.
	ch1 := snap.Channels[0]
	if ch1.ID != 1 {
		t.Errorf("Channels[0].ID = %d, want 1", ch1.ID)
	}
	if ch1.State != StateCued {
		t.Errorf("Channels[0].State = %v, want StateCued", ch1.State)
	}
	if ch1.Playlist == nil {
		t.Fatal("Channels[0].Playlist is nil")
	}
	if len(ch1.Playlist.Items) != 2 {
		t.Errorf("Channels[0].Playlist.Items len = %d, want 2", len(ch1.Playlist.Items))
	}

	// Channel 2 should be stopped with no playlist.
	ch2 := snap.Channels[1]
	if ch2.ID != 2 {
		t.Errorf("Channels[1].ID = %d, want 2", ch2.ID)
	}
	if ch2.State != StateStopped {
		t.Errorf("Channels[1].State = %v, want StateStopped", ch2.State)
	}
	if ch2.Playlist != nil {
		t.Error("Channels[1].Playlist should be nil")
	}
}

func TestManager_Snapshot_IncludesPods(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(5)})

	// Add a pod to channel 1's board.
	pb, _ := m.PodBoard(1)
	pod, err := pb.CreatePod(PodConfig{
		Type:       "commercial",
		DurationMs: 30000,
		AutoReturn: true,
		Priority:   PriorityHigh,
	})
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}

	snap := m.Snapshot()
	if len(snap.Channels[0].Pods) != 1 {
		t.Fatalf("Channels[0].Pods len = %d, want 1", len(snap.Channels[0].Pods))
	}

	pp := snap.Channels[0].Pods[0]
	if pp.ID != pod.ID {
		t.Errorf("Pod.ID = %q, want %q", pp.ID, pod.ID)
	}
	if pp.Config.Type != "commercial" {
		t.Errorf("Pod.Config.Type = %q, want %q", pp.Config.Type, "commercial")
	}
	if pp.Config.DurationMs != 30000 {
		t.Errorf("Pod.Config.DurationMs = %d, want 30000", pp.Config.DurationMs)
	}
	if pp.Config.Priority != PriorityHigh {
		t.Errorf("Pod.Config.Priority = %v, want %v", pp.Config.Priority, PriorityHigh)
	}
}

func TestStatePersister_SavesOnInterval(t *testing.T) {
	provider := fakeProvider(5)
	m := NewManager(ManagerConfig{AssetProvider: provider})

	pl := testPlaylist(clipItem("a"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	dir := t.TempDir()
	p := NewStatePersister(m, StatePersisterConfig{
		Dir:      dir,
		Interval: 50 * time.Millisecond,
	})
	p.Start()
	defer p.Stop()

	// Wait for the file to appear.
	path := filepath.Join(dir, "playout-state.json")
	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})

	// Load and verify.
	loaded, err := loadPersistedState(path)
	if err != nil {
		t.Fatalf("loadPersistedState: %v", err)
	}
	if loaded.Version != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version)
	}
	if len(loaded.Channels) != MaxChannels {
		t.Fatalf("Channels len = %d, want %d", len(loaded.Channels), MaxChannels)
	}
	if loaded.Channels[0].State != StateCued {
		t.Errorf("Channels[0].State = %v, want StateCued", loaded.Channels[0].State)
	}
	if loaded.Channels[0].Playlist == nil {
		t.Fatal("Channels[0].Playlist is nil")
	}
}

func TestStatePersister_FinalSaveOnStop(t *testing.T) {
	provider := fakeProvider(5)
	m := NewManager(ManagerConfig{AssetProvider: provider})

	pl := testPlaylist(clipItem("a"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	dir := t.TempDir()
	p := NewStatePersister(m, StatePersisterConfig{
		Dir:      dir,
		Interval: 1 * time.Hour, // very long — should NOT fire in test time
	})
	p.Start()

	// Stop should trigger a final save even though interval hasn't fired.
	p.Stop()

	path := filepath.Join(dir, "playout-state.json")
	loaded, err := loadPersistedState(path)
	if err != nil {
		t.Fatalf("loadPersistedState after Stop: %v", err)
	}
	if loaded.Version != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version)
	}
	if loaded.Channels[0].State != StateCued {
		t.Errorf("Channels[0].State = %v, want StateCued", loaded.Channels[0].State)
	}
}

func TestStatePersister_Path(t *testing.T) {
	m := NewManager(ManagerConfig{})
	dir := t.TempDir()
	p := NewStatePersister(m, StatePersisterConfig{
		Dir: dir,
	})

	want := filepath.Join(dir, "playout-state.json")
	if p.Path() != want {
		t.Errorf("Path() = %q, want %q", p.Path(), want)
	}
}

func TestStatePersister_CreatesDir(t *testing.T) {
	m := NewManager(ManagerConfig{})
	dir := filepath.Join(t.TempDir(), "nested", "state")
	p := NewStatePersister(m, StatePersisterConfig{
		Dir:      dir,
		Interval: 50 * time.Millisecond,
	})
	p.Start()
	defer p.Stop()

	// Wait for the directory and file to appear.
	path := filepath.Join(dir, "playout-state.json")
	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})

	// Verify directory was created.
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
}

func TestPersistedState_MarshalRoundTrip(t *testing.T) {
	now := time.Date(2026, 3, 25, 10, 30, 0, 0, time.UTC)
	itemStart := time.Date(2026, 3, 25, 10, 29, 45, 0, time.UTC)

	state := PersistedState{
		Version:   1,
		Timestamp: now,
		Channels: []PersistedChannel{
			{
				ID:            1,
				State:         StateOnAir,
				Mode:          ModeAutomated,
				Cursor:        3,
				ItemStartTime: itemStart,
				Playlist: &Playlist{
					ID:   "pl-001",
					Name: "Evening News",
					DefaultTransition: TransitionConfig{
						Type:       "dissolve",
						DurationMs: 500,
					},
					Items: []PlaylistItem{
						{
							ID:         "item-001",
							Type:       ItemClip,
							AssetURI:   "local:///mnt/media/opener.ts",
							Title:      "Opening Bumper",
							DurationMs: 15000,
						},
						{
							ID:     "item-002",
							Type:   ItemLive,
							Source: "camera-1",
							Title:  "Host Camera",
							Hold:   true,
						},
					},
				},
				Pods: []PersistedPod{
					{
						ID: "pod-001",
						Config: PodConfig{
							Type:       "commercial",
							DurationMs: 30000,
							AutoReturn: true,
							Priority:   PriorityNormal,
						},
						State:     PodBreakActive,
						Armed:     true,
						ElapsedMs: 12000,
						FiredAt:       now.Add(-12 * time.Second).UnixMilli(),
						SCTE35EventID: 42,
					},
				},
			},
		},
		AsRunFile: "/var/log/switchframe/asrun-2026-03-25.jsonl",
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got PersistedState
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Top-level fields.
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if !got.Timestamp.Equal(now) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, now)
	}
	if got.AsRunFile != state.AsRunFile {
		t.Errorf("AsRunFile = %q, want %q", got.AsRunFile, state.AsRunFile)
	}

	// Channel fields.
	if len(got.Channels) != 1 {
		t.Fatalf("Channels len = %d, want 1", len(got.Channels))
	}
	ch := got.Channels[0]
	if ch.ID != 1 {
		t.Errorf("Channel.ID = %d, want 1", ch.ID)
	}
	if ch.State != StateOnAir {
		t.Errorf("Channel.State = %d, want %d", ch.State, StateOnAir)
	}
	if ch.Mode != ModeAutomated {
		t.Errorf("Channel.Mode = %d, want %d", ch.Mode, ModeAutomated)
	}
	if ch.Cursor != 3 {
		t.Errorf("Channel.Cursor = %d, want 3", ch.Cursor)
	}
	if !ch.ItemStartTime.Equal(itemStart) {
		t.Errorf("Channel.ItemStartTime = %v, want %v", ch.ItemStartTime, itemStart)
	}

	// Playlist.
	if ch.Playlist == nil {
		t.Fatal("Channel.Playlist is nil")
	}
	if ch.Playlist.ID != "pl-001" {
		t.Errorf("Playlist.ID = %q, want %q", ch.Playlist.ID, "pl-001")
	}
	if ch.Playlist.Name != "Evening News" {
		t.Errorf("Playlist.Name = %q, want %q", ch.Playlist.Name, "Evening News")
	}
	if ch.Playlist.DefaultTransition.Type != "dissolve" {
		t.Errorf("Playlist.DefaultTransition.Type = %q, want %q", ch.Playlist.DefaultTransition.Type, "dissolve")
	}
	if ch.Playlist.DefaultTransition.DurationMs != 500 {
		t.Errorf("Playlist.DefaultTransition.DurationMs = %d, want 500", ch.Playlist.DefaultTransition.DurationMs)
	}
	if len(ch.Playlist.Items) != 2 {
		t.Fatalf("Playlist.Items len = %d, want 2", len(ch.Playlist.Items))
	}
	if ch.Playlist.Items[0].Type != ItemClip {
		t.Errorf("Playlist.Items[0].Type = %q, want %q", ch.Playlist.Items[0].Type, ItemClip)
	}
	if ch.Playlist.Items[0].Title != "Opening Bumper" {
		t.Errorf("Playlist.Items[0].Title = %q, want %q", ch.Playlist.Items[0].Title, "Opening Bumper")
	}
	if ch.Playlist.Items[1].Type != ItemLive {
		t.Errorf("Playlist.Items[1].Type = %q, want %q", ch.Playlist.Items[1].Type, ItemLive)
	}
	if ch.Playlist.Items[1].Source != "camera-1" {
		t.Errorf("Playlist.Items[1].Source = %q, want %q", ch.Playlist.Items[1].Source, "camera-1")
	}
	if !ch.Playlist.Items[1].Hold {
		t.Error("Playlist.Items[1].Hold = false, want true")
	}

	// Pods.
	if len(ch.Pods) != 1 {
		t.Fatalf("Pods len = %d, want 1", len(ch.Pods))
	}
	pod := ch.Pods[0]
	if pod.ID != "pod-001" {
		t.Errorf("Pod.ID = %q, want %q", pod.ID, "pod-001")
	}
	if pod.Config.Type != "commercial" {
		t.Errorf("Pod.Config.Type = %q, want %q", pod.Config.Type, "commercial")
	}
	if pod.Config.DurationMs != 30000 {
		t.Errorf("Pod.Config.DurationMs = %d, want 30000", pod.Config.DurationMs)
	}
	if !pod.Config.AutoReturn {
		t.Error("Pod.Config.AutoReturn = false, want true")
	}
	if pod.Config.Priority != PriorityNormal {
		t.Errorf("Pod.Config.Priority = %d, want %d", pod.Config.Priority, PriorityNormal)
	}
	if pod.State != PodBreakActive {
		t.Errorf("Pod.State = %d, want %d", pod.State, PodBreakActive)
	}
	if !pod.Armed {
		t.Error("Pod.Armed = false, want true")
	}
	if pod.ElapsedMs != 12000 {
		t.Errorf("Pod.ElapsedMs = %d, want 12000", pod.ElapsedMs)
	}
	expectedFiredAt := now.Add(-12 * time.Second).UnixMilli()
	if pod.FiredAt != expectedFiredAt {
		t.Errorf("Pod.FiredAt = %d, want %d", pod.FiredAt, expectedFiredAt)
	}
	if pod.SCTE35EventID != 42 {
		t.Errorf("Pod.SCTE35EventID = %d, want 42", pod.SCTE35EventID)
	}
}

func TestAtomicWriteJSON_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	state := PersistedState{
		Version:   1,
		Timestamp: now,
		Channels: []PersistedChannel{
			{
				ID:    1,
				State: StateCued,
				Mode:  ModeManual,
			},
		},
	}

	if err := atomicWriteJSON(path, &state); err != nil {
		t.Fatalf("atomicWriteJSON: %v", err)
	}

	loaded, err := loadPersistedState(path)
	if err != nil {
		t.Fatalf("loadPersistedState: %v", err)
	}

	if loaded.Version != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version)
	}
	if !loaded.Timestamp.Equal(now) {
		t.Errorf("Timestamp = %v, want %v", loaded.Timestamp, now)
	}
	if len(loaded.Channels) != 1 {
		t.Fatalf("Channels len = %d, want 1", len(loaded.Channels))
	}
	if loaded.Channels[0].ID != 1 {
		t.Errorf("Channel.ID = %d, want 1", loaded.Channels[0].ID)
	}
	if loaded.Channels[0].State != StateCued {
		t.Errorf("Channel.State = %d, want %d", loaded.Channels[0].State, StateCued)
	}
	if loaded.Channels[0].Mode != ModeManual {
		t.Errorf("Channel.Mode = %d, want %d", loaded.Channels[0].Mode, ModeManual)
	}

	// Verify no temp files remain.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Errorf("unexpected file in dir: %q", e.Name())
		}
	}
}

func TestLoadPersistedState_FileNotFound(t *testing.T) {
	_, err := loadPersistedState("/nonexistent/path/state.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist error, got: %v", err)
	}
}

func TestLoadPersistedState_UnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := os.WriteFile(path, []byte(`{"version":99,"timestamp":"2026-03-25T10:00:00Z","channels":[]}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := loadPersistedState(path)
	if err == nil {
		t.Fatal("expected error for unsupported version, got nil")
	}
	want := "persist: unsupported state version 99"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestManager_Recover_FromCued(t *testing.T) {
	provider := fakeProvider(5)

	// Step 1: Create a manager, load a playlist, snapshot, write to disk.
	m1 := NewManager(ManagerConfig{AssetProvider: provider})
	pl := testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"))
	if err := m1.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	snap := m1.Snapshot()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "playout-state.json")
	if err := atomicWriteJSON(statePath, &snap); err != nil {
		t.Fatalf("atomicWriteJSON: %v", err)
	}

	// Step 2: Create a fresh manager and recover.
	m2 := NewManager(ManagerConfig{AssetProvider: provider})
	if err := m2.Recover(statePath); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Step 3: Verify state.
	states := m2.ChannelStates()
	if states[0].State != "cued" {
		t.Errorf("state = %q, want %q", states[0].State, "cued")
	}
	if states[0].PlaylistName != "Test Playlist" {
		t.Errorf("playlistName = %q, want %q", states[0].PlaylistName, "Test Playlist")
	}
	if states[0].ItemCount != 3 {
		t.Errorf("itemCount = %d, want 3", states[0].ItemCount)
	}
}

func TestManager_Recover_FromOnAir_RecuesAtCurrentItem(t *testing.T) {
	provider := fakeProvider(5)

	// Manually build a PersistedState with ch1 OnAir at cursor=1 with 3 items.
	state := PersistedState{
		Version:   1,
		Timestamp: time.Now().UTC(),
		Channels: []PersistedChannel{
			{
				ID:     1,
				State:  StateOnAir,
				Mode:   ModeAutomated,
				Cursor: 1,
				Playlist: &Playlist{
					ID:   "test-pl",
					Name: "Test Playlist",
					Items: []PlaylistItem{
						clipItem("a"),
						clipItem("b"),
						clipItem("c"),
					},
				},
			},
			{
				ID:    2,
				State: StateStopped,
			},
		},
	}

	dir := t.TempDir()
	statePath := filepath.Join(dir, "playout-state.json")
	if err := atomicWriteJSON(statePath, &state); err != nil {
		t.Fatalf("atomicWriteJSON: %v", err)
	}

	m := NewManager(ManagerConfig{AssetProvider: provider})
	if err := m.Recover(statePath); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// OnAir channels should be re-cued (not started).
	states := m.ChannelStates()
	if states[0].State != "cued" {
		t.Errorf("state = %q, want %q", states[0].State, "cued")
	}
	if states[0].ItemIndex != 1 {
		t.Errorf("itemIndex = %d, want 1", states[0].ItemIndex)
	}
	if states[0].CurrentItemID != "b" {
		t.Errorf("currentItemID = %q, want %q", states[0].CurrentItemID, "b")
	}
}

func TestManager_Recover_PodElapsedAdjustment(t *testing.T) {
	provider := fakeProvider(5)

	// Build state with snapshot 10s ago, pod fired 15s ago with 5000ms elapsed.
	now := time.Now().UTC()
	snapshotTime := now.Add(-10 * time.Second)
	firedAt := now.Add(-15 * time.Second)

	state := PersistedState{
		Version:   1,
		Timestamp: snapshotTime,
		Channels: []PersistedChannel{
			{
				ID:    1,
				State: StateOnAir,
				Mode:  ModeAutomated,
				Playlist: &Playlist{
					ID:    "test-pl",
					Name:  "Test Playlist",
					Items: []PlaylistItem{clipItem("a")},
				},
				Pods: []PersistedPod{
					{
						ID: "pod-adj-1",
						Config: PodConfig{
							Type:       "commercial",
							DurationMs: 60000,
							AutoReturn: true,
						},
						State:     PodBreakActive,
						Armed:     true,
						ElapsedMs: 5000,
						FiredAt:   firedAt.UnixMilli(),
					},
				},
			},
			{
				ID:    2,
				State: StateStopped,
			},
		},
	}

	dir := t.TempDir()
	statePath := filepath.Join(dir, "playout-state.json")
	if err := atomicWriteJSON(statePath, &state); err != nil {
		t.Fatalf("atomicWriteJSON: %v", err)
	}

	m := NewManager(ManagerConfig{AssetProvider: provider})
	if err := m.Recover(statePath); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Pod elapsed should be adjusted: ~15000ms (time since fired).
	pb, _ := m.PodBoard(1)
	pod, err := pb.GetPod("pod-adj-1")
	if err != nil {
		t.Fatalf("GetPod: %v", err)
	}

	// Allow +/- 5 seconds tolerance for timing.
	if pod.ElapsedMs < 10000 || pod.ElapsedMs > 20000 {
		t.Errorf("pod.ElapsedMs = %d, want ~15000 (±5000)", pod.ElapsedMs)
	}
}

func TestManager_Recover_EmitsAsRunError(t *testing.T) {
	provider := fakeProvider(5)

	state := PersistedState{
		Version:   1,
		Timestamp: time.Now().UTC().Add(-5 * time.Second),
		Channels: []PersistedChannel{
			{
				ID:    1,
				State: StateOnAir,
				Mode:  ModeAutomated,
				Playlist: &Playlist{
					ID:    "test-pl",
					Name:  "Test Playlist",
					Items: []PlaylistItem{clipItem("a")},
				},
			},
			{
				ID:    2,
				State: StateStopped,
			},
		},
	}

	dir := t.TempDir()
	statePath := filepath.Join(dir, "playout-state.json")
	if err := atomicWriteJSON(statePath, &state); err != nil {
		t.Fatalf("atomicWriteJSON: %v", err)
	}

	m := NewManager(ManagerConfig{AssetProvider: provider})

	asRunDir := t.TempDir()
	logger, err := NewAsRunLogger(AsRunLoggerConfig{Dir: asRunDir})
	if err != nil {
		t.Fatalf("NewAsRunLogger: %v", err)
	}
	defer logger.Close()
	m.SetAsRunLogger(logger)

	if err := m.Recover(statePath); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Verify an AsRunError event was emitted.
	events := logger.Query(AsRunQuery{Type: AsRunError})
	if len(events) == 0 {
		t.Fatal("expected at least 1 AsRunError event, got 0")
	}
	if events[0].Message == "" {
		t.Error("AsRunError event has empty message")
	}
}

func TestManager_Recover_SkipsTerminalPods(t *testing.T) {
	provider := fakeProvider(5)

	state := PersistedState{
		Version:   1,
		Timestamp: time.Now().UTC(),
		Channels: []PersistedChannel{
			{
				ID:    1,
				State: StateCued,
				Playlist: &Playlist{
					ID:    "test-pl",
					Name:  "Test Playlist",
					Items: []PlaylistItem{clipItem("a")},
				},
				Pods: []PersistedPod{
					{
						ID:     "pod-terminal",
						Config: PodConfig{Type: "commercial", DurationMs: 30000},
						State:  PodRejoined, // terminal
					},
					{
						ID:     "pod-active",
						Config: PodConfig{Type: "promo", DurationMs: 15000},
						State:  PodScheduled, // non-terminal
						Armed:  true,
					},
				},
			},
			{
				ID:    2,
				State: StateStopped,
			},
		},
	}

	dir := t.TempDir()
	statePath := filepath.Join(dir, "playout-state.json")
	if err := atomicWriteJSON(statePath, &state); err != nil {
		t.Fatalf("atomicWriteJSON: %v", err)
	}

	m := NewManager(ManagerConfig{AssetProvider: provider})
	if err := m.Recover(statePath); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Only the non-terminal pod should be restored.
	pb, _ := m.PodBoard(1)
	pods := pb.ListPods()
	if len(pods) != 1 {
		t.Fatalf("pod count = %d, want 1", len(pods))
	}
	if pods[0].ID != "pod-active" {
		t.Errorf("pod.ID = %q, want %q", pods[0].ID, "pod-active")
	}
	if !pods[0].Armed {
		t.Error("pod.Armed = false, want true")
	}
}
