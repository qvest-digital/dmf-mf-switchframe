package arinfer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zsiec/switchframe/server/gpu"
)

// ModelManager manages ML model files (ONNX sources and TensorRT engine plans).
// It handles importing ONNX models, building TensorRT engines from them, and
// querying what models are available.
type ModelManager struct {
	dir string // base directory for all model files
}

// ModelInfo describes a model's files and readiness.
type ModelInfo struct {
	Name     string `json:"name"`
	OnnxPath string `json:"onnxPath,omitempty"`
	PlanPath string `json:"planPath,omitempty"`
	OnnxSize int64  `json:"onnxSize,omitempty"`
	PlanSize int64  `json:"planSize,omitempty"`
	Ready    bool   `json:"ready"` // true if .plan exists
}

// NewModelManager creates a model manager backed by the given directory.
// The directory is created if it does not exist.
func NewModelManager(dir string) (*ModelManager, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("arinfer: create model dir: %w", err)
	}
	return &ModelManager{dir: dir}, nil
}

// ModelDir returns the base directory for model files.
func (m *ModelManager) ModelDir() string {
	return m.dir
}

// ImportONNX copies an ONNX model into the model directory.
// The model name must be a simple filename without path separators.
// If a model with the same name already exists, it is overwritten.
func (m *ModelManager) ImportONNX(name string, data []byte) error {
	if err := validateModelName(name); err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("arinfer: empty ONNX data")
	}

	onnxPath := filepath.Join(m.dir, name+".onnx")

	// Write atomically.
	tmpPath := onnxPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("arinfer: write ONNX: %w", err)
	}
	if err := os.Rename(tmpPath, onnxPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("arinfer: rename ONNX: %w", err)
	}

	return nil
}

// EnsureModel checks for a cached TensorRT engine plan and builds one from
// the ONNX source if needed. Returns the path to the .plan file ready for
// inference. If fp16 is true, the engine is built with FP16 precision.
//
// On builds without TensorRT support, this returns an error from the gpu
// package indicating TRT is not available. The model manager still manages
// files correctly in that case.
func (m *ModelManager) EnsureModel(name string, fp16 bool) (string, error) {
	if err := validateModelName(name); err != nil {
		return "", err
	}

	suffix := ".plan"
	if fp16 {
		suffix = ".fp16.plan"
	}
	planPath := filepath.Join(m.dir, name+suffix)

	// If the plan file already exists, return it.
	if info, err := os.Stat(planPath); err == nil && info.Size() > 0 {
		return planPath, nil
	}

	// Need to build from ONNX.
	onnxPath := filepath.Join(m.dir, name+".onnx")
	if _, err := os.Stat(onnxPath); os.IsNotExist(err) {
		return "", fmt.Errorf("%w: %s", ErrModelMissing, name+".onnx")
	}

	// Build TensorRT engine. This calls into the gpu package which is
	// build-tag gated — on stub builds it returns ErrTensorRTNotAvailable.
	_, err := gpu.NewTRTEngine(onnxPath, gpu.TRTEngineOpts{
		MaxBatchSize:  1,
		UseFP16:       fp16,
		PlanCachePath: planPath,
	})
	if err != nil {
		return "", fmt.Errorf("arinfer: build TRT engine for %s: %w", name, err)
	}

	return planPath, nil
}

// ListModels returns info about all known models in the model directory.
// A model is identified by its base name (stem without extension). Both
// .onnx and .plan files are discovered.
func (m *ModelManager) ListModels() []ModelInfo {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil
	}

	// Collect all model names and their files.
	type modelFiles struct {
		onnxPath string
		onnxSize int64
		planPath string
		planSize int64
	}
	models := make(map[string]*modelFiles)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()

		var baseName string
		var isOnnx, isPlan bool

		switch {
		case strings.HasSuffix(name, ".onnx"):
			baseName = strings.TrimSuffix(name, ".onnx")
			isOnnx = true
		case strings.HasSuffix(name, ".fp16.plan"):
			baseName = strings.TrimSuffix(name, ".fp16.plan")
			isPlan = true
		case strings.HasSuffix(name, ".plan"):
			baseName = strings.TrimSuffix(name, ".plan")
			isPlan = true
		default:
			continue
		}

		if baseName == "" {
			continue
		}

		mf, ok := models[baseName]
		if !ok {
			mf = &modelFiles{}
			models[baseName] = mf
		}

		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}

		if isOnnx {
			mf.onnxPath = filepath.Join(m.dir, name)
			mf.onnxSize = size
		}
		if isPlan {
			// Prefer fp16 plan if both exist, but don't overwrite if already set.
			if mf.planPath == "" {
				mf.planPath = filepath.Join(m.dir, name)
				mf.planSize = size
			}
		}
	}

	// Convert to sorted slice.
	result := make([]ModelInfo, 0, len(models))
	for name, mf := range models {
		result = append(result, ModelInfo{
			Name:     name,
			OnnxPath: mf.onnxPath,
			PlanPath: mf.planPath,
			OnnxSize: mf.onnxSize,
			PlanSize: mf.planSize,
			Ready:    mf.planPath != "",
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

// DeleteModel removes a model's ONNX and plan files from disk.
func (m *ModelManager) DeleteModel(name string) error {
	if err := validateModelName(name); err != nil {
		return err
	}

	found := false

	// Remove all files matching the model name.
	for _, suffix := range []string{".onnx", ".plan", ".fp16.plan"} {
		path := filepath.Join(m.dir, name+suffix)
		if _, err := os.Stat(path); err == nil {
			found = true
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("arinfer: remove %s: %w", filepath.Base(path), err)
			}
		}
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrModelMissing, name)
	}

	return nil
}

// validateModelName rejects names that could cause path traversal.
func validateModelName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("arinfer: invalid model name: %q", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("arinfer: invalid model name: %q", name)
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("arinfer: invalid model name: %q", name)
	}
	return nil
}
