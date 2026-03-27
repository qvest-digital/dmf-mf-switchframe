package arrender

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Scene is a thread-safe scene graph that holds objects and lights.
// It tracks a dirty flag so consumers know when the scene has changed
// since the last render.
type Scene struct {
	mu       sync.RWMutex
	objects  map[string]*SceneObject
	lights   map[string]*Light
	dirty    atomic.Bool
	onChange func() // optional callback, called after mutations (outside lock)
}

// NewScene creates an empty scene with the dirty flag set.
func NewScene() *Scene {
	s := &Scene{
		objects: make(map[string]*SceneObject),
		lights:  make(map[string]*Light),
	}
	s.dirty.Store(true)
	return s
}

// SetOnChange registers a callback invoked after the scene is mutated
// (objects/lights added, updated, or removed). The callback is called
// outside of the scene's lock and must not call back into the scene.
func (s *Scene) SetOnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

// notifyChange calls the onChange callback if set. Must be called
// outside of the scene's write lock.
func (s *Scene) notifyChange() {
	s.mu.RLock()
	fn := s.onChange
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

// AddObject inserts an object into the scene. Returns ErrDuplicateID
// if an object with the same ID already exists.
func (s *Scene) AddObject(obj SceneObject) error {
	s.mu.Lock()
	if _, exists := s.objects[obj.ID]; exists {
		s.mu.Unlock()
		return ErrDuplicateID
	}
	cp := obj
	s.objects[obj.ID] = &cp
	s.dirty.Store(true)
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// UpdateObject replaces an existing object. The obj.ID field is used
// for lookup, not the id parameter. Returns ErrObjectNotFound if no
// object with the given id exists.
func (s *Scene) UpdateObject(id string, obj SceneObject) error {
	s.mu.Lock()
	if _, exists := s.objects[id]; !exists {
		s.mu.Unlock()
		return ErrObjectNotFound
	}
	cp := obj
	cp.ID = id // preserve original ID
	s.objects[id] = &cp
	s.dirty.Store(true)
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// RemoveObject removes an object by ID. Returns ErrObjectNotFound
// if no object with the given ID exists.
func (s *Scene) RemoveObject(id string) error {
	s.mu.Lock()
	if _, exists := s.objects[id]; !exists {
		s.mu.Unlock()
		return ErrObjectNotFound
	}
	delete(s.objects, id)
	s.dirty.Store(true)
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// GetObject returns a copy of the object with the given ID.
// Returns ErrObjectNotFound if no such object exists.
func (s *Scene) GetObject(id string) (*SceneObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, exists := s.objects[id]
	if !exists {
		return nil, ErrObjectNotFound
	}
	cp := *obj
	return &cp, nil
}

// Objects returns a sorted copy of all objects in the scene.
// Objects are sorted by ID for deterministic ordering.
func (s *Scene) Objects() []SceneObject {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]SceneObject, 0, len(s.objects))
	for _, obj := range s.objects {
		result = append(result, *obj)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// AddLight inserts a light into the scene. Returns ErrDuplicateID
// if a light with the same ID already exists.
func (s *Scene) AddLight(l Light) error {
	s.mu.Lock()
	if _, exists := s.lights[l.ID]; exists {
		s.mu.Unlock()
		return ErrDuplicateID
	}
	cp := l
	s.lights[l.ID] = &cp
	s.dirty.Store(true)
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// UpdateLight replaces an existing light. Returns ErrLightNotFound
// if no light with the given ID exists.
func (s *Scene) UpdateLight(id string, l Light) error {
	s.mu.Lock()
	if _, exists := s.lights[id]; !exists {
		s.mu.Unlock()
		return ErrLightNotFound
	}
	cp := l
	cp.ID = id // preserve original ID
	s.lights[id] = &cp
	s.dirty.Store(true)
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// RemoveLight removes a light by ID. Returns ErrLightNotFound
// if no light with the given ID exists.
func (s *Scene) RemoveLight(id string) error {
	s.mu.Lock()
	if _, exists := s.lights[id]; !exists {
		s.mu.Unlock()
		return ErrLightNotFound
	}
	delete(s.lights, id)
	s.dirty.Store(true)
	s.mu.Unlock()
	s.notifyChange()
	return nil
}

// Lights returns a sorted copy of all lights in the scene.
// Lights are sorted by ID for deterministic ordering.
func (s *Scene) Lights() []Light {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Light, 0, len(s.lights))
	for _, l := range s.lights {
		result = append(result, *l)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// MarkDirty sets the dirty flag, indicating the scene has been modified
// and needs to be re-rendered.
func (s *Scene) MarkDirty() {
	s.dirty.Store(true)
}

// IsDirty reports whether the scene has been modified since the last
// call to ClearDirty.
func (s *Scene) IsDirty() bool {
	return s.dirty.Load()
}

// ClearDirty resets the dirty flag. Called by the renderer after
// consuming scene changes.
func (s *Scene) ClearDirty() {
	s.dirty.Store(false)
}

// State returns a JSON-serializable snapshot of the entire scene.
func (s *Scene) State() SceneState {
	return SceneState{
		Objects:  s.Objects(),
		Lights:   s.Lights(),
		Settings: DefaultRenderSettings(),
	}
}
