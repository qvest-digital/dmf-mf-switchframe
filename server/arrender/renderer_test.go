package arrender

import (
	"errors"
	"testing"

	"github.com/zsiec/switchframe/server/tracking"
)

func TestNewRenderer_Stub_ReturnsError(t *testing.T) {
	_, err := NewRenderer(nil, DefaultRenderSettings())
	if !errors.Is(err, ErrRendererNotReady) {
		t.Errorf("NewRenderer = %v, want ErrRendererNotReady", err)
	}
}

func TestRenderer_Stub_NilSafe(t *testing.T) {
	// All methods on a nil *Renderer should not panic.
	var r *Renderer

	if scene := r.Scene(); scene != nil {
		t.Error("Scene() on nil renderer should return nil")
	}

	s := r.Settings()
	defaults := DefaultRenderSettings()
	if s != defaults {
		t.Errorf("Settings() = %+v, want defaults %+v", s, defaults)
	}

	if err := r.SetSettings(RenderSettings{}); !errors.Is(err, ErrRendererNotReady) {
		t.Errorf("SetSettings = %v, want ErrRendererNotReady", err)
	}

	if err := r.Render(nil); !errors.Is(err, ErrRendererNotReady) {
		t.Errorf("Render = %v, want ErrRendererNotReady", err)
	}

	if ptr := r.RenderedFrame(); ptr != 0 {
		t.Errorf("RenderedFrame = %d, want 0", ptr)
	}

	state := r.SceneState()
	if len(state.Objects) != 0 || len(state.Lights) != 0 {
		t.Error("SceneState should be empty on stub")
	}

	if err := r.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
}

func TestRenderer_Stub_SetIntrinsics_NilSafe(t *testing.T) {
	var r *Renderer
	// Should not panic.
	r.SetIntrinsics(nil)
	r.SetIntrinsics(&tracking.CameraIntrinsics{})
}

func TestRenderer_Stub_InitPipeline_ReturnsNotReady(t *testing.T) {
	var r *Renderer
	err := r.InitPipeline([]byte("fake ptx"))
	if !errors.Is(err, ErrRendererNotReady) {
		t.Errorf("InitPipeline on nil renderer = %v, want ErrRendererNotReady", err)
	}
}

func TestRenderer_Stub_Render_ReturnsNotReady(t *testing.T) {
	// On stub builds, NewRenderer returns error and nil renderer.
	// But even if someone has a zero-value renderer, Render should
	// return ErrRendererNotReady because pipeline is nil.
	var r *Renderer
	pose := &tracking.CameraPose{
		Position:    [3]float64{0, 0, 5},
		Orientation: [3]float64{0, 0, 0},
		FocalLength: 50,
		Confidence:  1.0,
	}
	err := r.Render(pose)
	if !errors.Is(err, ErrRendererNotReady) {
		t.Errorf("Render on nil renderer = %v, want ErrRendererNotReady", err)
	}
}

func TestDefaultCameraPose(t *testing.T) {
	pose := DefaultCameraPose()
	if pose == nil {
		t.Fatal("DefaultCameraPose returned nil")
	}
	if pose.Position != [3]float64{0, 0, 0} {
		t.Errorf("position = %v, want origin", pose.Position)
	}
	if pose.Orientation != [3]float64{0, 0, 0} {
		t.Errorf("orientation = %v, want zero", pose.Orientation)
	}
	if pose.FocalLength != 50 {
		t.Errorf("focalLength = %v, want 50", pose.FocalLength)
	}
	if pose.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0", pose.Confidence)
	}
	if pose.Source != "default" {
		t.Errorf("source = %q, want %q", pose.Source, "default")
	}
}

func TestRenderer_Stub_MeshesForAsset_NilSafe(t *testing.T) {
	var r *Renderer
	meshes, ok := r.MeshesForAsset("test-id")
	if ok {
		t.Error("MeshesForAsset on nil renderer should return false")
	}
	if meshes != nil {
		t.Error("MeshesForAsset on nil renderer should return nil")
	}
}

func TestRenderer_Stub_LoadAssetMeshes_NilSafe(t *testing.T) {
	var r *Renderer
	// Should not panic.
	r.LoadAssetMeshes("test-id", []GLTFMesh{{Name: "test"}})
}

func TestRenderer_Stub_PipelineReady(t *testing.T) {
	var r *Renderer
	if r.PipelineReady() {
		t.Error("PipelineReady on nil renderer should return false")
	}
}

// TestDenoiser_Stub_ReturnsError tests the denoiser stub.
func TestDenoiser_Stub_ReturnsError(t *testing.T) {
	_, err := NewDenoiser(nil, 1280, 720, false)
	if !errors.Is(err, ErrRendererNotReady) {
		t.Errorf("NewDenoiser = %v, want ErrRendererNotReady", err)
	}
}

func TestDenoiser_Stub_NilSafe(t *testing.T) {
	var d *Denoiser

	if w := d.Width(); w != 0 {
		t.Errorf("Width = %d, want 0", w)
	}
	if h := d.Height(); h != 0 {
		t.Errorf("Height = %d, want 0", h)
	}
	if d.Temporal() {
		t.Error("Temporal should be false")
	}

	// Should not panic.
	d.Close()

	err := d.Denoise(nil, nil, nil, nil)
	if !errors.Is(err, ErrRendererNotReady) {
		t.Errorf("Denoise = %v, want ErrRendererNotReady", err)
	}
}
