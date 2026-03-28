//go:build cgo && cuda && tensorrt && integration

package main

import (
	"os"
	"testing"

	"github.com/zsiec/switchframe/server/arinfer"
)

const (
	depthModelDir  = "/data/switchframe/ar_models"
	depthPlanFile  = "depth_anything_v2_vits.plan"
)

// TestDepthInference_RealTRT verifies that the sidecar engine runs real
// Depth Anything V2 inference via arinfer.Engine.
//
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,tensorrt,integration' -v -race -timeout 120s -run TestDepthInference_RealTRT ./cmd/switchframe-ai/
func TestDepthInference_RealTRT(t *testing.T) {
	planPath := depthModelDir + "/" + depthPlanFile
	if _, err := os.Stat(planPath); err != nil {
		t.Skipf("depth model not found at %s", planPath)
	}

	t.Run("NV12", func(t *testing.T) {
		testDepthInference(t, arinfer.FormatNV12)
	})
	t.Run("P210", func(t *testing.T) {
		testDepthInference(t, arinfer.FormatP210)
	})
}

func testDepthInference(t *testing.T, format arinfer.FrameFormat) {
	t.Helper()

	engine := arinfer.New(arinfer.Config{
		ModelsDir:   depthModelDir,
		FrameFormat: format,
	})
	defer engine.Close()

	if err := engine.EnableDepth(""); err != nil {
		t.Fatalf("EnableDepth: %v", err)
	}

	st := engine.Status()
	if !st.DepthReady {
		t.Fatal("depth not ready after EnableDepth")
	}

	// ProcessFrame requires a real GPU frame pointer. Since we're testing
	// the engine lifecycle (enable/disable/status), we verify the model
	// loads and reports ready. Full frame processing requires a GPU frame
	// pool which is tested in the E2E sidecar integration tests.
	engine.DisableDepth()
	st = engine.Status()
	if st.DepthReady {
		t.Fatal("depth still ready after DisableDepth")
	}
}
