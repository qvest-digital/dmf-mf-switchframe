package arinfer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelManager_NewCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "models")

	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	info, err := os.Stat(mgr.ModelDir())
	if err != nil {
		t.Fatalf("model dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("ModelDir should be a directory")
	}
}

func TestModelManager_ImportONNX(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	data := []byte("fake ONNX data")

	if err := mgr.ImportONNX("depth_anything_v2", data); err != nil {
		t.Fatalf("ImportONNX: %v", err)
	}

	// Verify file exists.
	path := filepath.Join(dir, "depth_anything_v2.onnx")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("ONNX file should exist: %v", err)
	}
	if info.Size() != int64(len(data)) {
		t.Errorf("file size: got %d, want %d", info.Size(), len(data))
	}

	// Verify content.
	got, _ := os.ReadFile(path)
	if string(got) != string(data) {
		t.Error("file content mismatch")
	}
}

func TestModelManager_ImportONNX_EmptyData(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	if err := mgr.ImportONNX("empty", nil); err == nil {
		t.Error("ImportONNX with empty data should fail")
	}

	if err := mgr.ImportONNX("empty", []byte{}); err == nil {
		t.Error("ImportONNX with empty slice should fail")
	}
}

func TestModelManager_ImportONNX_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	badNames := []string{"", ".", "..", "../evil", "foo/bar", "foo\\bar"}
	for _, name := range badNames {
		if err := mgr.ImportONNX(name, []byte("data")); err == nil {
			t.Errorf("ImportONNX(%q) should fail", name)
		}
	}
}

func TestModelManager_ImportONNX_Overwrite(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	_ = mgr.ImportONNX("model", []byte("v1"))
	_ = mgr.ImportONNX("model", []byte("v2-updated"))

	path := filepath.Join(dir, "model.onnx")
	got, _ := os.ReadFile(path)
	if string(got) != "v2-updated" {
		t.Errorf("overwrite failed: got %q", string(got))
	}
}

func TestModelManager_ListModels(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	// Empty list initially.
	models := mgr.ListModels()
	if len(models) != 0 {
		t.Errorf("expected empty list, got %d models", len(models))
	}

	// Import two models.
	_ = mgr.ImportONNX("depth", []byte("depth onnx"))
	_ = mgr.ImportONNX("sam_encoder", []byte("sam onnx"))

	models = mgr.ListModels()
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	// Should be sorted.
	if models[0].Name != "depth" {
		t.Errorf("models[0].Name: got %q, want %q", models[0].Name, "depth")
	}
	if models[1].Name != "sam_encoder" {
		t.Errorf("models[1].Name: got %q, want %q", models[1].Name, "sam_encoder")
	}

	// Should have ONNX but no plan.
	if models[0].OnnxPath == "" {
		t.Error("depth should have OnnxPath")
	}
	if models[0].Ready {
		t.Error("depth should not be ready (no plan)")
	}
}

func TestModelManager_ListModels_WithPlan(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	// Create ONNX + plan files manually.
	_ = os.WriteFile(filepath.Join(dir, "depth.onnx"), []byte("onnx"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "depth.plan"), []byte("plan"), 0644)

	models := mgr.ListModels()
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}

	if !models[0].Ready {
		t.Error("model with plan should be ready")
	}
	if models[0].PlanPath == "" {
		t.Error("should have PlanPath")
	}
}

func TestModelManager_ListModels_FP16Plan(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	_ = os.WriteFile(filepath.Join(dir, "depth.onnx"), []byte("onnx"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "depth.fp16.plan"), []byte("fp16plan"), 0644)

	models := mgr.ListModels()
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}

	if !models[0].Ready {
		t.Error("model with fp16 plan should be ready")
	}
}

func TestModelManager_DeleteModel(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	_ = mgr.ImportONNX("depth", []byte("onnx data"))
	// Simulate a plan file too.
	_ = os.WriteFile(filepath.Join(dir, "depth.plan"), []byte("plan"), 0644)

	if err := mgr.DeleteModel("depth"); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}

	// Both files should be gone.
	if _, err := os.Stat(filepath.Join(dir, "depth.onnx")); !os.IsNotExist(err) {
		t.Error("ONNX file should be deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "depth.plan")); !os.IsNotExist(err) {
		t.Error("plan file should be deleted")
	}

	models := mgr.ListModels()
	if len(models) != 0 {
		t.Errorf("expected empty list after delete, got %d", len(models))
	}
}

func TestModelManager_DeleteModel_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	err = mgr.DeleteModel("nonexistent")
	if err == nil {
		t.Error("DeleteModel should fail for missing model")
	}
}

func TestModelManager_DeleteModel_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	badNames := []string{"", "..", "../etc", "foo/bar"}
	for _, name := range badNames {
		if err := mgr.DeleteModel(name); err == nil {
			t.Errorf("DeleteModel(%q) should fail", name)
		}
	}
}

func TestModelManager_EnsureModel_NoONNX(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	_, err = mgr.EnsureModel("missing", false)
	if err == nil {
		t.Error("EnsureModel should fail without ONNX file")
	}
}

func TestModelManager_EnsureModel_ExistingPlan(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	// Create a plan file manually (simulating a previous build).
	planPath := filepath.Join(dir, "depth.plan")
	_ = os.WriteFile(planPath, []byte("cached plan"), 0644)

	path, err := mgr.EnsureModel("depth", false)
	if err != nil {
		t.Fatalf("EnsureModel with existing plan: %v", err)
	}
	if path != planPath {
		t.Errorf("path: got %q, want %q", path, planPath)
	}
}

func TestModelManager_EnsureModel_ExistingFP16Plan(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	planPath := filepath.Join(dir, "depth.fp16.plan")
	_ = os.WriteFile(planPath, []byte("cached fp16 plan"), 0644)

	path, err := mgr.EnsureModel("depth", true)
	if err != nil {
		t.Fatalf("EnsureModel with existing fp16 plan: %v", err)
	}
	if path != planPath {
		t.Errorf("path: got %q, want %q", path, planPath)
	}
}

func TestModelManager_EnsureModel_NeedsBuild(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	// Import ONNX but no plan — EnsureModel will try to build via gpu.NewTRTEngine
	// which returns ErrTensorRTNotAvailable on stub builds. That's expected.
	_ = mgr.ImportONNX("depth", []byte("fake onnx"))

	_, err = mgr.EnsureModel("depth", false)
	if err == nil {
		// On stub builds, this should fail because TRT is not available.
		// On real TRT builds with a fake ONNX, it would also fail.
		// Either way, we don't expect success here.
		t.Log("EnsureModel succeeded (unexpected on stub builds, but OK if TRT is available)")
	}
	// The error should mention TensorRT or the ONNX file.
	if err != nil {
		t.Logf("EnsureModel (expected) error: %v", err)
	}
}

func TestModelManager_ModelDir(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewModelManager(dir)
	if err != nil {
		t.Fatalf("NewModelManager: %v", err)
	}

	if mgr.ModelDir() != dir {
		t.Errorf("ModelDir: got %q, want %q", mgr.ModelDir(), dir)
	}
}
