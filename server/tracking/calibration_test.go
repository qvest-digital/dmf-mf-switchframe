package tracking

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCalibrationStore_SetGet(t *testing.T) {
	dir := t.TempDir()

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	cal := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{
			{0, 18},
			{0.5, 35},
			{1.0, 70},
		},
		PrincipalPoint: [2]float64{0.5, 0.5},
		Distortion:     [5]float64{-0.1, 0.02, 0, 0, 0.001},
		SensorWidth:    23.5,
	}

	// Set a calibration.
	if err := store.Set("cam1", cal); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get it back.
	got := store.Get("cam1")
	if got == nil {
		t.Fatal("Get returned nil for cam1")
	}

	if len(got.FocalLengthCurve) != 3 {
		t.Errorf("FocalLengthCurve length: got %d, want 3", len(got.FocalLengthCurve))
	}
	if got.FocalLengthCurve[1][1] != 35 {
		t.Errorf("FocalLengthCurve[1][1]: got %f, want 35", got.FocalLengthCurve[1][1])
	}
	if got.PrincipalPoint != [2]float64{0.5, 0.5} {
		t.Errorf("PrincipalPoint: got %v, want [0.5, 0.5]", got.PrincipalPoint)
	}
	if got.SensorWidth != 23.5 {
		t.Errorf("SensorWidth: got %f, want 23.5", got.SensorWidth)
	}
}

func TestCalibrationStore_GetMissing(t *testing.T) {
	dir := t.TempDir()

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	got := store.Get("nonexistent")
	if got != nil {
		t.Error("Get should return nil for missing key")
	}
}

func TestCalibrationStore_Delete(t *testing.T) {
	dir := t.TempDir()

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	cal := CameraIntrinsics{SensorWidth: 36}
	if err := store.Set("cam2", cal); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Delete it.
	if err := store.Delete("cam2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify gone from memory.
	if got := store.Get("cam2"); got != nil {
		t.Error("Get should return nil after delete")
	}

	// Verify gone from disk.
	path := filepath.Join(dir, "cam2.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should not exist after delete")
	}
}

func TestCalibrationStore_DeleteNotFound(t *testing.T) {
	dir := t.TempDir()

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	err = store.Delete("nonexistent")
	if err == nil {
		t.Error("Delete of missing key should return error")
	}
}

func TestCalibrationStore_List(t *testing.T) {
	dir := t.TempDir()

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	// Empty list initially.
	if l := store.List(); len(l) != 0 {
		t.Errorf("List should be empty, got %d entries", len(l))
	}

	_ = store.Set("cam1", CameraIntrinsics{SensorWidth: 36})
	_ = store.Set("cam2", CameraIntrinsics{SensorWidth: 23.5})

	l := store.List()
	if len(l) != 2 {
		t.Fatalf("List should have 2 entries, got %d", len(l))
	}
	if l["cam1"] == nil || l["cam2"] == nil {
		t.Error("List missing expected keys")
	}
}

func TestCalibrationStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Create store and add calibration.
	store1, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	cal := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{{0, 24}, {1, 70}},
		PrincipalPoint:   [2]float64{0.49, 0.51},
		SensorWidth:      35.0,
	}
	if err := store1.Set("persistent-cam", cal); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Create a new store from the same directory — should load persisted data.
	store2, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore (reopen): %v", err)
	}

	got := store2.Get("persistent-cam")
	if got == nil {
		t.Fatal("persisted calibration not found after reopen")
	}
	if len(got.FocalLengthCurve) != 2 {
		t.Errorf("FocalLengthCurve length: got %d, want 2", len(got.FocalLengthCurve))
	}
	if got.FocalLengthCurve[0][1] != 24 {
		t.Errorf("FocalLengthCurve[0][1]: got %f, want 24", got.FocalLengthCurve[0][1])
	}
	if got.PrincipalPoint[0] != 0.49 {
		t.Errorf("PrincipalPoint[0]: got %f, want 0.49", got.PrincipalPoint[0])
	}
}

func TestCalibrationStore_MissingDirCreation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "calibrations")

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore with missing nested dir: %v", err)
	}

	// Should be able to set and get.
	if err := store.Set("cam1", CameraIntrinsics{SensorWidth: 36}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := store.Get("cam1"); got == nil {
		t.Error("Get returned nil after set in created dir")
	}
}

func TestCalibrationStore_PathTraversal(t *testing.T) {
	dir := t.TempDir()

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	badKeys := []string{"", ".", "..", "../etc/passwd", "foo/bar", "foo\\bar"}
	for _, key := range badKeys {
		if err := store.Set(key, CameraIntrinsics{}); err == nil {
			t.Errorf("Set(%q) should fail with path traversal protection", key)
		}
	}
}

func TestCalibrationStore_GetReturnsCopy(t *testing.T) {
	dir := t.TempDir()

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	cal := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{{0, 18}},
		SensorWidth:      36,
	}
	_ = store.Set("cam1", cal)

	// Mutate the returned value.
	got := store.Get("cam1")
	got.SensorWidth = 999
	got.FocalLengthCurve[0][1] = 999

	// Original should be unchanged.
	got2 := store.Get("cam1")
	if got2.SensorWidth != 36 {
		t.Errorf("SensorWidth mutated: got %f, want 36", got2.SensorWidth)
	}
	if got2.FocalLengthCurve[0][1] != 18 {
		t.Errorf("FocalLengthCurve mutated: got %f, want 18", got2.FocalLengthCurve[0][1])
	}
}

func TestCalibrationStore_Overwrite(t *testing.T) {
	dir := t.TempDir()

	store, err := NewCalibrationStore(dir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}

	_ = store.Set("cam1", CameraIntrinsics{SensorWidth: 36})
	_ = store.Set("cam1", CameraIntrinsics{SensorWidth: 23.5})

	got := store.Get("cam1")
	if got.SensorWidth != 23.5 {
		t.Errorf("SensorWidth after overwrite: got %f, want 23.5", got.SensorWidth)
	}
}
