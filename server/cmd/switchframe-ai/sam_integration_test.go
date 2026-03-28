//go:build cgo && cuda && tensorrt && integration

package main

import (
	"os"
	"testing"

	"github.com/zsiec/switchframe/server/arinfer"
)

const (
	samModelDir      = "/data/switchframe/ar_models"
	samEncoderPlan   = "sam2_encoder.plan"
	samDecoderPlan   = "sam2_decoder.plan"
)

// TestSAMInference_RealTRT verifies that the sidecar engine runs real
// SAM 2 inference (encoder + decoder) via arinfer.Engine.
//
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,tensorrt,integration' -v -race -timeout 120s -run TestSAMInference_RealTRT ./cmd/switchframe-ai/
func TestSAMInference_RealTRT(t *testing.T) {
	encPath := samModelDir + "/" + samEncoderPlan
	decPath := samModelDir + "/" + samDecoderPlan
	if _, err := os.Stat(encPath); err != nil {
		t.Skipf("SAM encoder model not found at %s", encPath)
	}
	if _, err := os.Stat(decPath); err != nil {
		t.Skipf("SAM decoder model not found at %s", decPath)
	}

	t.Run("NV12", func(t *testing.T) {
		testSAMInference(t, arinfer.FormatNV12)
	})
	t.Run("P210", func(t *testing.T) {
		testSAMInference(t, arinfer.FormatP210)
	})
}

func testSAMInference(t *testing.T, format arinfer.FrameFormat) {
	t.Helper()

	engine := arinfer.New(arinfer.Config{
		ModelsDir:   samModelDir,
		FrameFormat: format,
	})
	defer engine.Close()

	if err := engine.EnableSAM("", ""); err != nil {
		t.Fatalf("EnableSAM: %v", err)
	}

	st := engine.Status()
	if !st.SAMReady {
		t.Fatal("SAM not ready after EnableSAM")
	}

	// Test prompt management.
	prompt := arinfer.SAMPrompt{
		ID:     "test-point",
		Type:   "point",
		Points: [][2]int{{960, 540}},
		Label:  "test object",
	}
	if err := engine.AddPrompt(prompt); err != nil {
		t.Fatalf("AddPrompt: %v", err)
	}

	prompts := engine.Prompts()
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts))
	}
	if prompts[0].ID != "test-point" {
		t.Fatalf("expected prompt ID 'test-point', got %q", prompts[0].ID)
	}

	if err := engine.RemovePrompt("test-point"); err != nil {
		t.Fatalf("RemovePrompt: %v", err)
	}
	if len(engine.Prompts()) != 0 {
		t.Fatal("expected 0 prompts after removal")
	}

	engine.DisableSAM()
	st = engine.Status()
	if st.SAMReady {
		t.Fatal("SAM still ready after DisableSAM")
	}
}
