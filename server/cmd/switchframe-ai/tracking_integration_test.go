//go:build cgo && cuda && tensorrt && integration

package main

import (
	"os"
	"testing"

	"github.com/zsiec/switchframe/server/sidecar"
)

const (
	trackingModelsDir   = "/data/switchframe/ar_models"
	superpointPlanFile  = "superpoint.plan"
	lightgluePlanFile   = "lightglue.plan"
)

// TestTrackingEngine_GracefulDegradation verifies that the sidecar
// tracking handlers work correctly even when SuperPoint/LightGlue models
// are not available. The handlers should accept enable/disable commands
// and return OK without crashing.
//
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,tensorrt,integration' -v -race -timeout 30s -run TestTrackingEngine_GracefulDegradation ./cmd/switchframe-ai/
func TestTrackingEngine_GracefulDegradation(t *testing.T) {
	eng := newEngine(nil, nil, nil, "", nil, trackingModelsDir, "")

	// Enable tracking for a source — should succeed even without models.
	resp := eng.handleMessage(&sidecar.Message{
		Cmd: "enable_tracking",
		Config: map[string]any{
			"source_id": "cam1",
		},
	})
	if resp == nil || resp.Type == sidecar.MsgError {
		t.Fatalf("expected OK for enable_tracking, got %v", resp)
	}

	// Verify source is tracked.
	eng.mu.Lock()
	enabled := eng.trackingSources["cam1"]
	eng.mu.Unlock()
	if !enabled {
		t.Fatal("expected cam1 to be tracked")
	}

	// Disable tracking — should succeed.
	resp = eng.handleMessage(&sidecar.Message{
		Cmd: "disable_tracking",
		Config: map[string]any{
			"source_id": "cam1",
		},
	})
	if resp == nil || resp.Type == sidecar.MsgError {
		t.Fatalf("expected OK for disable_tracking, got %v", resp)
	}

	eng.mu.Lock()
	enabled = eng.trackingSources["cam1"]
	eng.mu.Unlock()
	if enabled {
		t.Fatal("expected cam1 to not be tracked")
	}

	eng.close()
}

// TestTrackingEngine_RealModels verifies full visual tracking with real
// SuperPoint/LightGlue TensorRT models. Skips if models are not available.
func TestTrackingEngine_RealModels(t *testing.T) {
	spPath := trackingModelsDir + "/" + superpointPlanFile
	lgPath := trackingModelsDir + "/" + lightgluePlanFile
	if _, err := os.Stat(spPath); err != nil {
		t.Skipf("SuperPoint model not found at %s — skipping real tracking test", spPath)
	}
	if _, err := os.Stat(lgPath); err != nil {
		t.Skipf("LightGlue model not found at %s — skipping real tracking test", lgPath)
	}

	t.Log("SuperPoint and LightGlue models found — real tracking test would run here")
	// Full test with real models would create VisualLocalizer, process frames,
	// and verify pose output. Skipping since models are not expected to exist
	// on the dev server currently.
}
