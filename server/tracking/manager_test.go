package tracking

import (
	"sync/atomic"
	"testing"
)

// mockTracker implements TrackingSource for testing.
type mockTracker struct {
	name    string
	typ     string
	pose    atomic.Pointer[CameraPose]
	running atomic.Bool
	closed  atomic.Bool

	startErr error
	stopErr  error
}

func newMockTracker(name, typ string) *mockTracker {
	return &mockTracker{name: name, typ: typ}
}

func (m *mockTracker) Name() string       { return m.name }
func (m *mockTracker) Type() string       { return m.typ }
func (m *mockTracker) Running() bool      { return m.running.Load() }
func (m *mockTracker) LatestPose() *CameraPose { return m.pose.Load() }

func (m *mockTracker) Start() error {
	if m.startErr != nil {
		return m.startErr
	}
	m.running.Store(true)
	return nil
}

func (m *mockTracker) Stop() error {
	m.running.Store(false)
	return m.stopErr
}

func (m *mockTracker) Close() error {
	m.closed.Store(true)
	m.running.Store(false)
	return nil
}

func (m *mockTracker) setPose(pose *CameraPose) {
	m.pose.Store(pose)
}

func TestManager_AddRemoveSource(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	tracker := newMockTracker("freed-cam1", "freed")

	// Add should succeed.
	if err := mgr.AddSource("cam1", tracker); err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	// Sources should list the tracker.
	srcs := mgr.Sources()
	infos, ok := srcs["cam1"]
	if !ok {
		t.Fatal("cam1 not found in Sources()")
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 tracker, got %d", len(infos))
	}
	if infos[0].Name != "freed-cam1" {
		t.Errorf("Name = %q, want %q", infos[0].Name, "freed-cam1")
	}
	if infos[0].Type != "freed" {
		t.Errorf("Type = %q, want %q", infos[0].Type, "freed")
	}

	// Remove should succeed.
	if err := mgr.RemoveSource("cam1", "freed-cam1"); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}

	srcs = mgr.Sources()
	infos, ok = srcs["cam1"]
	if ok && len(infos) > 0 {
		t.Errorf("expected no trackers after remove, got %d", len(infos))
	}
}

func TestManager_DuplicateSourceError(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	tracker := newMockTracker("freed-cam1", "freed")
	if err := mgr.AddSource("cam1", tracker); err != nil {
		t.Fatalf("first AddSource: %v", err)
	}

	// Adding a tracker with the same name to the same source should fail.
	dup := newMockTracker("freed-cam1", "freed")
	err := mgr.AddSource("cam1", dup)
	if err == nil {
		t.Fatal("expected error adding duplicate tracker name")
	}
	if err != ErrSourceExists {
		t.Errorf("expected ErrSourceExists, got %v", err)
	}
}

func TestManager_RemoveNonExistent(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	err := mgr.RemoveSource("cam1", "nope")
	if err != ErrSourceNotFound {
		t.Errorf("expected ErrSourceNotFound, got %v", err)
	}
}

func TestManager_LatestPose_NilWhenNoData(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	// No sources registered.
	pose := mgr.LatestPose("cam1")
	if pose != nil {
		t.Error("expected nil pose for unregistered source")
	}

	// Source registered but no data yet.
	tracker := newMockTracker("freed-cam1", "freed")
	mgr.AddSource("cam1", tracker)

	pose = mgr.LatestPose("cam1")
	if pose != nil {
		t.Error("expected nil pose before any data")
	}
}

func TestManager_Poll_UpdatesLatest(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	tracker := newMockTracker("freed-cam1", "freed")
	tracker.setPose(&CameraPose{
		Position:    [3]float64{1.0, 2.0, 3.0},
		Orientation: [3]float64{10.0, 20.0, 30.0},
		Confidence:  1.0,
		Timestamp:   3000,
		Source:      "freed",
	})

	mgr.AddSource("cam1", tracker)

	// Poll should read from tracker and update latest.
	mgr.Poll()

	pose := mgr.LatestPose("cam1")
	if pose == nil {
		t.Fatal("expected non-nil pose after Poll")
	}

	// Values should be approximately correct (filter may adjust slightly).
	if pose.Source != "freed" {
		t.Errorf("Source = %q, want %q", pose.Source, "freed")
	}
}

func TestManager_Poll_PriorityBlending(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	// Hardware tracker with high confidence.
	hardware := newMockTracker("freed-cam1", "freed")
	hardware.setPose(&CameraPose{
		Position:    [3]float64{1.0, 0, 0},
		Orientation: [3]float64{0, 0, 0},
		Confidence:  1.0,
		Timestamp:   3000,
		Source:      "freed",
	})

	// Visual tracker with lower confidence.
	visual := newMockTracker("visual-cam1", "visual")
	visual.setPose(&CameraPose{
		Position:    [3]float64{99.0, 0, 0},
		Orientation: [3]float64{0, 0, 0},
		Confidence:  0.7,
		Timestamp:   3000,
		Source:      "visual",
	})

	mgr.AddSource("cam1", hardware)
	mgr.AddSource("cam1", visual)

	mgr.Poll()

	pose := mgr.LatestPose("cam1")
	if pose == nil {
		t.Fatal("expected non-nil pose after Poll")
	}

	// Should have used the hardware tracker's data (confidence=1.0).
	if pose.Source != "freed" {
		t.Errorf("Source = %q, want %q (highest confidence)", pose.Source, "freed")
	}
}

func TestManager_Poll_FallbackToLowerConfidence(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	// Hardware tracker with no data.
	hardware := newMockTracker("freed-cam1", "freed")
	// No pose set - LatestPose() returns nil.

	// Visual tracker with data.
	visual := newMockTracker("visual-cam1", "visual")
	visual.setPose(&CameraPose{
		Position:    [3]float64{5.0, 0, 0},
		Orientation: [3]float64{0, 0, 0},
		Confidence:  0.8,
		Timestamp:   3000,
		Source:      "visual",
	})

	mgr.AddSource("cam1", hardware)
	mgr.AddSource("cam1", visual)

	mgr.Poll()

	pose := mgr.LatestPose("cam1")
	if pose == nil {
		t.Fatal("expected non-nil pose after Poll (should fall back to visual)")
	}
	if pose.Source != "visual" {
		t.Errorf("Source = %q, want %q (fallback)", pose.Source, "visual")
	}
}

func TestManager_SetGetCalibration(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	tracker := newMockTracker("freed-cam1", "freed")
	mgr.AddSource("cam1", tracker)

	cal := CameraIntrinsics{
		SensorWidth:    36.0,
		PrincipalPoint: [2]float64{0.5, 0.5},
		Distortion:     [5]float64{0.1, -0.2, 0, 0, 0.05},
	}

	if err := mgr.SetCalibration("cam1", cal); err != nil {
		t.Fatalf("SetCalibration: %v", err)
	}

	got := mgr.GetCalibration("cam1")
	if got == nil {
		t.Fatal("GetCalibration returned nil")
	}
	if got.SensorWidth != 36.0 {
		t.Errorf("SensorWidth = %f, want 36.0", got.SensorWidth)
	}
}

func TestManager_SetCalibration_UnknownSource(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	err := mgr.SetCalibration("nonexistent", CameraIntrinsics{})
	if err != ErrSourceNotFound {
		t.Errorf("expected ErrSourceNotFound, got %v", err)
	}
}

func TestManager_GetCalibration_UnknownSource(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	cal := mgr.GetCalibration("nonexistent")
	if cal != nil {
		t.Error("expected nil calibration for unknown source")
	}
}

func TestManager_Close_StopsAllSources(t *testing.T) {
	mgr := NewManager()

	t1 := newMockTracker("freed-cam1", "freed")
	t2 := newMockTracker("visual-cam2", "visual")

	mgr.AddSource("cam1", t1)
	mgr.AddSource("cam2", t2)

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !t1.closed.Load() {
		t.Error("tracker t1 not closed")
	}
	if !t2.closed.Load() {
		t.Error("tracker t2 not closed")
	}
}

func TestManager_Close_Idempotent(t *testing.T) {
	mgr := NewManager()

	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestManager_AddAfterClose(t *testing.T) {
	mgr := NewManager()
	mgr.Close()

	tracker := newMockTracker("freed-cam1", "freed")
	err := mgr.AddSource("cam1", tracker)
	if err == nil {
		t.Error("expected error adding source after close")
	}
}

func TestManager_SetOnPose_Callback(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	var receivedKey string
	var receivedPose CameraPose

	mgr.SetOnPose(func(sourceKey string, pose CameraPose) {
		receivedKey = sourceKey
		receivedPose = pose
	})

	tracker := newMockTracker("freed-cam1", "freed")
	tracker.setPose(&CameraPose{
		Position:   [3]float64{1.0, 2.0, 3.0},
		Confidence: 1.0,
		Timestamp:  3000,
		Source:     "freed",
	})

	mgr.AddSource("cam1", tracker)
	mgr.Poll()

	if receivedKey != "cam1" {
		t.Errorf("callback sourceKey = %q, want %q", receivedKey, "cam1")
	}
	if receivedPose.Source != "freed" {
		t.Errorf("callback pose source = %q, want %q", receivedPose.Source, "freed")
	}
}

func TestManager_SourcesInfo(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	t1 := newMockTracker("freed-cam1", "freed")
	t1.running.Store(true)
	t1.setPose(&CameraPose{Confidence: 0.95})

	t2 := newMockTracker("visual-cam1", "visual")

	mgr.AddSource("cam1", t1)
	mgr.AddSource("cam1", t2)

	srcs := mgr.Sources()
	infos := srcs["cam1"]
	if len(infos) != 2 {
		t.Fatalf("expected 2 trackers, got %d", len(infos))
	}

	// Find the freed tracker info.
	var freedInfo TrackingSourceInfo
	for _, info := range infos {
		if info.Name == "freed-cam1" {
			freedInfo = info
			break
		}
	}

	if !freedInfo.Running {
		t.Error("expected freed tracker to report running")
	}
	if freedInfo.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", freedInfo.Confidence)
	}
}

func TestManager_MultipleSourceKeys(t *testing.T) {
	mgr := NewManager()
	defer mgr.Close()

	t1 := newMockTracker("freed-cam1", "freed")
	t1.setPose(&CameraPose{
		Position:   [3]float64{1.0, 0, 0},
		Confidence: 1.0,
		Timestamp:  3000,
		Source:     "freed",
	})

	t2 := newMockTracker("freed-cam2", "freed")
	t2.setPose(&CameraPose{
		Position:   [3]float64{5.0, 0, 0},
		Confidence: 1.0,
		Timestamp:  3000,
		Source:     "freed",
	})

	mgr.AddSource("cam1", t1)
	mgr.AddSource("cam2", t2)

	mgr.Poll()

	p1 := mgr.LatestPose("cam1")
	p2 := mgr.LatestPose("cam2")

	if p1 == nil || p2 == nil {
		t.Fatal("expected non-nil poses for both cameras")
	}

	// Poses should be independent.
	if p1.Position[0] == p2.Position[0] {
		t.Error("poses should differ between cameras")
	}
}
