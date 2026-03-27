package arrender

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestNewScene(t *testing.T) {
	s := NewScene()
	if s == nil {
		t.Fatal("NewScene returned nil")
	}
	if !s.IsDirty() {
		t.Error("new scene should be dirty")
	}
	if len(s.Objects()) != 0 {
		t.Error("new scene should have no objects")
	}
	if len(s.Lights()) != 0 {
		t.Error("new scene should have no lights")
	}
}

func TestAddObject(t *testing.T) {
	s := NewScene()
	s.ClearDirty()

	obj := SceneObject{
		ID:       "cube-1",
		AssetID:  "asset-abc",
		Position: [3]float64{1, 2, 3},
		Scale:    [3]float64{1, 1, 1},
		Visible:  true,
	}

	if err := s.AddObject(obj); err != nil {
		t.Fatalf("AddObject: %v", err)
	}

	if !s.IsDirty() {
		t.Error("scene should be dirty after AddObject")
	}

	got, err := s.GetObject("cube-1")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if got.AssetID != "asset-abc" {
		t.Errorf("AssetID = %q, want %q", got.AssetID, "asset-abc")
	}
	if got.Position != [3]float64{1, 2, 3} {
		t.Errorf("Position = %v, want [1 2 3]", got.Position)
	}
}

func TestAddObject_DuplicateID(t *testing.T) {
	s := NewScene()
	obj := SceneObject{ID: "cube-1", Scale: [3]float64{1, 1, 1}, Visible: true}

	if err := s.AddObject(obj); err != nil {
		t.Fatalf("first AddObject: %v", err)
	}

	err := s.AddObject(obj)
	if !errors.Is(err, ErrDuplicateID) {
		t.Errorf("second AddObject = %v, want ErrDuplicateID", err)
	}
}

func TestUpdateObject(t *testing.T) {
	s := NewScene()
	obj := SceneObject{ID: "cube-1", Position: [3]float64{0, 0, 0}, Scale: [3]float64{1, 1, 1}, Visible: true}
	_ = s.AddObject(obj)
	s.ClearDirty()

	updated := SceneObject{Position: [3]float64{5, 10, 15}, Scale: [3]float64{2, 2, 2}, Visible: false}
	if err := s.UpdateObject("cube-1", updated); err != nil {
		t.Fatalf("UpdateObject: %v", err)
	}

	if !s.IsDirty() {
		t.Error("scene should be dirty after UpdateObject")
	}

	got, _ := s.GetObject("cube-1")
	if got.ID != "cube-1" {
		t.Errorf("ID = %q, want %q (should preserve original ID)", got.ID, "cube-1")
	}
	if got.Position != [3]float64{5, 10, 15} {
		t.Errorf("Position = %v, want [5 10 15]", got.Position)
	}
	if got.Visible {
		t.Error("Visible should be false after update")
	}
}

func TestUpdateObject_NotFound(t *testing.T) {
	s := NewScene()
	err := s.UpdateObject("nope", SceneObject{})
	if !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("UpdateObject = %v, want ErrObjectNotFound", err)
	}
}

func TestRemoveObject(t *testing.T) {
	s := NewScene()
	_ = s.AddObject(SceneObject{ID: "cube-1", Scale: [3]float64{1, 1, 1}})
	s.ClearDirty()

	if err := s.RemoveObject("cube-1"); err != nil {
		t.Fatalf("RemoveObject: %v", err)
	}

	if !s.IsDirty() {
		t.Error("scene should be dirty after RemoveObject")
	}

	_, err := s.GetObject("cube-1")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("GetObject after remove = %v, want ErrObjectNotFound", err)
	}
}

func TestRemoveObject_NotFound(t *testing.T) {
	s := NewScene()
	err := s.RemoveObject("nope")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("RemoveObject = %v, want ErrObjectNotFound", err)
	}
}

func TestGetObject_NotFound(t *testing.T) {
	s := NewScene()
	_, err := s.GetObject("nope")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("GetObject = %v, want ErrObjectNotFound", err)
	}
}

func TestGetObject_ReturnsCopy(t *testing.T) {
	s := NewScene()
	_ = s.AddObject(SceneObject{ID: "cube-1", Position: [3]float64{0, 0, 0}, Scale: [3]float64{1, 1, 1}})

	got, _ := s.GetObject("cube-1")
	got.Position = [3]float64{99, 99, 99}

	// Original should be unchanged.
	original, _ := s.GetObject("cube-1")
	if original.Position != [3]float64{0, 0, 0} {
		t.Error("GetObject should return a copy, not a reference to internal state")
	}
}

func TestObjects_Sorted(t *testing.T) {
	s := NewScene()
	_ = s.AddObject(SceneObject{ID: "charlie", Scale: [3]float64{1, 1, 1}})
	_ = s.AddObject(SceneObject{ID: "alpha", Scale: [3]float64{1, 1, 1}})
	_ = s.AddObject(SceneObject{ID: "bravo", Scale: [3]float64{1, 1, 1}})

	objs := s.Objects()
	if len(objs) != 3 {
		t.Fatalf("len(Objects) = %d, want 3", len(objs))
	}
	if objs[0].ID != "alpha" || objs[1].ID != "bravo" || objs[2].ID != "charlie" {
		t.Errorf("Objects not sorted: %v", []string{objs[0].ID, objs[1].ID, objs[2].ID})
	}
}

func TestAddLight(t *testing.T) {
	s := NewScene()
	s.ClearDirty()

	l := Light{
		ID:        "key-1",
		Type:      "point",
		Position:  [3]float64{0, 5, 0},
		Color:     [3]float64{1, 1, 1},
		Intensity: 100,
	}

	if err := s.AddLight(l); err != nil {
		t.Fatalf("AddLight: %v", err)
	}

	if !s.IsDirty() {
		t.Error("scene should be dirty after AddLight")
	}

	lights := s.Lights()
	if len(lights) != 1 {
		t.Fatalf("len(Lights) = %d, want 1", len(lights))
	}
	if lights[0].Type != "point" {
		t.Errorf("Type = %q, want %q", lights[0].Type, "point")
	}
	if lights[0].Intensity != 100 {
		t.Errorf("Intensity = %f, want 100", lights[0].Intensity)
	}
}

func TestAddLight_DuplicateID(t *testing.T) {
	s := NewScene()
	l := Light{ID: "key-1", Type: "point"}
	_ = s.AddLight(l)

	err := s.AddLight(l)
	if !errors.Is(err, ErrDuplicateID) {
		t.Errorf("second AddLight = %v, want ErrDuplicateID", err)
	}
}

func TestUpdateLight(t *testing.T) {
	s := NewScene()
	_ = s.AddLight(Light{ID: "key-1", Type: "point", Intensity: 50})
	s.ClearDirty()

	err := s.UpdateLight("key-1", Light{Type: "directional", Intensity: 200})
	if err != nil {
		t.Fatalf("UpdateLight: %v", err)
	}

	if !s.IsDirty() {
		t.Error("scene should be dirty after UpdateLight")
	}

	lights := s.Lights()
	if lights[0].ID != "key-1" {
		t.Error("UpdateLight should preserve original ID")
	}
	if lights[0].Intensity != 200 {
		t.Errorf("Intensity = %f, want 200", lights[0].Intensity)
	}
}

func TestUpdateLight_NotFound(t *testing.T) {
	s := NewScene()
	err := s.UpdateLight("nope", Light{})
	if !errors.Is(err, ErrLightNotFound) {
		t.Errorf("UpdateLight = %v, want ErrLightNotFound", err)
	}
}

func TestRemoveLight(t *testing.T) {
	s := NewScene()
	_ = s.AddLight(Light{ID: "key-1", Type: "point"})
	s.ClearDirty()

	if err := s.RemoveLight("key-1"); err != nil {
		t.Fatalf("RemoveLight: %v", err)
	}

	if !s.IsDirty() {
		t.Error("scene should be dirty after RemoveLight")
	}

	if len(s.Lights()) != 0 {
		t.Error("light should be removed")
	}
}

func TestRemoveLight_NotFound(t *testing.T) {
	s := NewScene()
	err := s.RemoveLight("nope")
	if !errors.Is(err, ErrLightNotFound) {
		t.Errorf("RemoveLight = %v, want ErrLightNotFound", err)
	}
}

func TestLights_Sorted(t *testing.T) {
	s := NewScene()
	_ = s.AddLight(Light{ID: "fill", Type: "area"})
	_ = s.AddLight(Light{ID: "back", Type: "point"})
	_ = s.AddLight(Light{ID: "key", Type: "directional"})

	lights := s.Lights()
	if len(lights) != 3 {
		t.Fatalf("len(Lights) = %d, want 3", len(lights))
	}
	if lights[0].ID != "back" || lights[1].ID != "fill" || lights[2].ID != "key" {
		t.Errorf("Lights not sorted: %v", []string{lights[0].ID, lights[1].ID, lights[2].ID})
	}
}

func TestMarkDirty(t *testing.T) {
	s := NewScene()
	s.ClearDirty()
	if s.IsDirty() {
		t.Error("scene should be clean after ClearDirty")
	}
	s.MarkDirty()
	if !s.IsDirty() {
		t.Error("scene should be dirty after MarkDirty")
	}
}

func TestDirtyFlag(t *testing.T) {
	s := NewScene()

	// New scene starts dirty.
	if !s.IsDirty() {
		t.Error("new scene should be dirty")
	}

	s.ClearDirty()
	if s.IsDirty() {
		t.Error("scene should be clean after ClearDirty")
	}

	// AddObject sets dirty.
	_ = s.AddObject(SceneObject{ID: "a", Scale: [3]float64{1, 1, 1}})
	if !s.IsDirty() {
		t.Error("AddObject should set dirty")
	}

	s.ClearDirty()

	// UpdateObject sets dirty.
	_ = s.UpdateObject("a", SceneObject{Scale: [3]float64{2, 2, 2}})
	if !s.IsDirty() {
		t.Error("UpdateObject should set dirty")
	}

	s.ClearDirty()

	// RemoveObject sets dirty.
	_ = s.RemoveObject("a")
	if !s.IsDirty() {
		t.Error("RemoveObject should set dirty")
	}

	s.ClearDirty()

	// AddLight sets dirty.
	_ = s.AddLight(Light{ID: "l1", Type: "point"})
	if !s.IsDirty() {
		t.Error("AddLight should set dirty")
	}

	s.ClearDirty()

	// UpdateLight sets dirty.
	_ = s.UpdateLight("l1", Light{Type: "directional"})
	if !s.IsDirty() {
		t.Error("UpdateLight should set dirty")
	}

	s.ClearDirty()

	// RemoveLight sets dirty.
	_ = s.RemoveLight("l1")
	if !s.IsDirty() {
		t.Error("RemoveLight should set dirty")
	}
}

func TestState(t *testing.T) {
	s := NewScene()
	_ = s.AddObject(SceneObject{ID: "obj-1", AssetID: "a1", Scale: [3]float64{1, 1, 1}, Visible: true})
	_ = s.AddObject(SceneObject{ID: "obj-2", AssetID: "a2", Scale: [3]float64{1, 1, 1}, Visible: false})
	_ = s.AddLight(Light{ID: "light-1", Type: "point", Intensity: 100})

	state := s.State()

	if len(state.Objects) != 2 {
		t.Errorf("len(Objects) = %d, want 2", len(state.Objects))
	}
	if len(state.Lights) != 1 {
		t.Errorf("len(Lights) = %d, want 1", len(state.Lights))
	}
	if state.Settings.Width != 1280 {
		t.Errorf("Settings.Width = %d, want 1280", state.Settings.Width)
	}
}

func TestScene_ConcurrentAccess(t *testing.T) {
	s := NewScene()
	const goroutines = 20
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				objID := fmt.Sprintf("obj-%d-%d", id, i)
				lightID := fmt.Sprintf("light-%d-%d", id, i)

				_ = s.AddObject(SceneObject{ID: objID, Scale: [3]float64{1, 1, 1}})
				_ = s.AddLight(Light{ID: lightID, Type: "point"})
				_ = s.Objects()
				_ = s.Lights()
				_ = s.IsDirty()
				_ = s.State()
				_, _ = s.GetObject(objID)
				_ = s.UpdateObject(objID, SceneObject{Scale: [3]float64{2, 2, 2}})
				_ = s.RemoveObject(objID)
				_ = s.RemoveLight(lightID)
			}
		}(g)
	}

	wg.Wait()
}

func TestDefaultRenderSettings(t *testing.T) {
	s := DefaultRenderSettings()
	if s.Width != 1280 {
		t.Errorf("Width = %d, want 1280", s.Width)
	}
	if s.Height != 720 {
		t.Errorf("Height = %d, want 720", s.Height)
	}
	if s.SPP != 2 {
		t.Errorf("SPP = %d, want 2", s.SPP)
	}
	if !s.Denoise {
		t.Error("Denoise should default to true")
	}
}
