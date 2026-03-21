package stmap

import (
	"sort"
	"sync"
	"sync/atomic"
)

// STMapState is the serializable state snapshot for broadcasting to browsers.
type STMapState struct {
	Sources   map[string]string  `json:"sources"`           // source key -> map name
	Program   *STMapProgramState `json:"program,omitempty"` // nil if no program map
	Available []string           `json:"available"`         // all stored map names (sorted)
}

// STMapProgramState describes the current program ST map assignment.
type STMapProgramState struct {
	Map   string `json:"map"`
	Type  string `json:"type"`  // "static" or "animated"
	Frame int    `json:"frame"` // current frame index (animated only)
}

// Registry is the central store for all ST maps and their assignments.
type Registry struct {
	mu          sync.RWMutex
	maps        map[string]*STMap         // static maps by name
	animated    map[string]*AnimatedSTMap // animated maps by name
	perSource   map[string]string         // source key -> map name
	sourceProcs map[string]*Processor     // per-source cached processors
	programMap  string                    // current program map name (empty = none)
	programProc *Processor                // cached processor for static program map
	programAnim *AnimatedSTMap            // reference for animated program map

	hasProgramMap atomic.Bool      // fast lock-free check for pipeline node
	onStateChange func(STMapState) // callback for state broadcast
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		maps:        make(map[string]*STMap),
		animated:    make(map[string]*AnimatedSTMap),
		perSource:   make(map[string]string),
		sourceProcs: make(map[string]*Processor),
	}
}

// SetOnStateChange sets the callback invoked after any mutating operation.
func (r *Registry) SetOnStateChange(fn func(STMapState)) {
	r.mu.Lock()
	r.onStateChange = fn
	r.mu.Unlock()
}

// Store stores a static ST map (validates name). If a map with the same name
// already exists, it is replaced and any cached processors using that name are
// rebuilt.
func (r *Registry) Store(m *STMap) error {
	if err := ValidateName(m.Name); err != nil {
		return err
	}

	r.mu.Lock()
	r.maps[m.Name] = m

	// Rebuild cached processors for any source assigned to this map name.
	for srcKey, mapName := range r.perSource {
		if mapName == m.Name {
			r.sourceProcs[srcKey] = NewProcessor(m)
		}
	}

	// Rebuild program processor if the program map has this name.
	if r.programMap == m.Name && r.programProc != nil {
		r.programProc = NewProcessor(m)
	}

	fn := r.onStateChange
	state := r.stateLocked()
	r.mu.Unlock()

	if fn != nil {
		fn(state)
	}
	return nil
}

// StoreAnimated stores an animated ST map (validates name).
func (r *Registry) StoreAnimated(a *AnimatedSTMap) error {
	if err := ValidateName(a.Name); err != nil {
		return err
	}

	r.mu.Lock()
	r.animated[a.Name] = a

	fn := r.onStateChange
	state := r.stateLocked()
	r.mu.Unlock()

	if fn != nil {
		fn(state)
	}
	return nil
}

// Get returns a static map by name.
func (r *Registry) Get(name string) (*STMap, bool) {
	r.mu.RLock()
	m, ok := r.maps[name]
	r.mu.RUnlock()
	return m, ok
}

// GetAnimated returns an animated map by name.
func (r *Registry) GetAnimated(name string) (*AnimatedSTMap, bool) {
	r.mu.RLock()
	a, ok := r.animated[name]
	r.mu.RUnlock()
	return a, ok
}

// AssignSource assigns a map to a source. The map must exist (static or
// animated). A cached Processor is created for the source. Returns ErrNotFound
// if the map name is not stored.
func (r *Registry) AssignSource(sourceKey, mapName string) error {
	r.mu.Lock()

	// Look up static first, then animated.
	if m, ok := r.maps[mapName]; ok {
		r.perSource[sourceKey] = mapName
		r.sourceProcs[sourceKey] = NewProcessor(m)
	} else if a, ok := r.animated[mapName]; ok {
		r.perSource[sourceKey] = mapName
		r.sourceProcs[sourceKey] = NewProcessor(a.CurrentFrame())
	} else {
		r.mu.Unlock()
		return ErrNotFound
	}

	fn := r.onStateChange
	state := r.stateLocked()
	r.mu.Unlock()

	if fn != nil {
		fn(state)
	}
	return nil
}

// RemoveSource removes a source's map assignment and cached processor.
func (r *Registry) RemoveSource(sourceKey string) {
	r.mu.Lock()
	delete(r.perSource, sourceKey)
	delete(r.sourceProcs, sourceKey)

	fn := r.onStateChange
	state := r.stateLocked()
	r.mu.Unlock()

	if fn != nil {
		fn(state)
	}
}

// SourceMap returns the map name assigned to a source.
func (r *Registry) SourceMap(sourceKey string) (string, bool) {
	r.mu.RLock()
	name, ok := r.perSource[sourceKey]
	r.mu.RUnlock()
	return name, ok
}

// SourceProcessor returns the cached Processor for a source, or nil if none.
func (r *Registry) SourceProcessor(sourceKey string) *Processor {
	r.mu.RLock()
	p := r.sourceProcs[sourceKey]
	r.mu.RUnlock()
	return p
}

// AssignProgram assigns a map to the program output. Checks animated maps
// first, then static. Returns ErrNotFound if the map name is not stored.
func (r *Registry) AssignProgram(mapName string) error {
	r.mu.Lock()

	if a, ok := r.animated[mapName]; ok {
		r.programMap = mapName
		r.programProc = nil
		r.programAnim = a
		r.hasProgramMap.Store(true)
	} else if m, ok := r.maps[mapName]; ok {
		r.programMap = mapName
		r.programProc = NewProcessor(m)
		r.programAnim = nil
		r.hasProgramMap.Store(true)
	} else {
		r.mu.Unlock()
		return ErrNotFound
	}

	fn := r.onStateChange
	state := r.stateLocked()
	r.mu.Unlock()

	if fn != nil {
		fn(state)
	}
	return nil
}

// RemoveProgram clears the program map assignment.
func (r *Registry) RemoveProgram() {
	r.mu.Lock()
	r.programMap = ""
	r.programProc = nil
	r.programAnim = nil
	r.hasProgramMap.Store(false)

	fn := r.onStateChange
	state := r.stateLocked()
	r.mu.Unlock()

	if fn != nil {
		fn(state)
	}
}

// HasProgramMap returns true if a program map is assigned. This is a lock-free
// atomic check suitable for the hot pipeline path.
func (r *Registry) HasProgramMap() bool {
	return r.hasProgramMap.Load()
}

// ProgramProcessor returns the cached Processor for a static program map,
// or nil if no static program map is assigned.
func (r *Registry) ProgramProcessor() *Processor {
	r.mu.RLock()
	p := r.programProc
	r.mu.RUnlock()
	return p
}

// ProgramAnimatedFrame returns the AnimatedSTMap reference for an animated
// program map, or nil if no animated program map is assigned.
func (r *Registry) ProgramAnimatedFrame() *AnimatedSTMap {
	r.mu.RLock()
	a := r.programAnim
	r.mu.RUnlock()
	return a
}

// Delete removes a map (static or animated) and clears any source or program
// assignments referencing it. Returns ErrNotFound if the name is not stored.
func (r *Registry) Delete(name string) error {
	r.mu.Lock()

	_, hasStatic := r.maps[name]
	_, hasAnimated := r.animated[name]

	if !hasStatic && !hasAnimated {
		r.mu.Unlock()
		return ErrNotFound
	}

	delete(r.maps, name)
	delete(r.animated, name)

	// Clear source assignments referencing this map.
	for srcKey, mapName := range r.perSource {
		if mapName == name {
			delete(r.perSource, srcKey)
			delete(r.sourceProcs, srcKey)
		}
	}

	// Clear program assignment if it references this map.
	if r.programMap == name {
		r.programMap = ""
		r.programProc = nil
		r.programAnim = nil
		r.hasProgramMap.Store(false)
	}

	fn := r.onStateChange
	state := r.stateLocked()
	r.mu.Unlock()

	if fn != nil {
		fn(state)
	}
	return nil
}

// List returns all stored map names (static and animated), sorted.
func (r *Registry) List() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.maps)+len(r.animated))
	for name := range r.maps {
		names = append(names, name)
	}
	for name := range r.animated {
		names = append(names, name)
	}
	r.mu.RUnlock()

	sort.Strings(names)
	return names
}

// ProgramMapName returns the name of the current program map, or "" if none.
// Used by the GPU stmap node for cache invalidation.
func (r *Registry) ProgramMapName() string {
	r.mu.RLock()
	name := r.programMap
	r.mu.RUnlock()
	return name
}

// ProgramSTArrays returns the S and T float32 arrays for the current static
// program map. Returns (nil, nil) if no static program map is assigned.
// Used by the GPU stmap node to upload S/T data to GPU memory.
func (r *Registry) ProgramSTArrays() (s, t []float32) {
	r.mu.RLock()
	proc := r.programProc
	r.mu.RUnlock()
	if proc == nil {
		return nil, nil
	}
	return proc.STArrays()
}

// IsAnimated returns true if the current program map is animated.
func (r *Registry) IsAnimated() bool {
	r.mu.RLock()
	anim := r.programAnim
	r.mu.RUnlock()
	return anim != nil
}

// AdvanceAnimatedIndex advances the animated program map frame counter and
// returns the wrapped index. Returns -1 if no animated map is assigned.
func (r *Registry) AdvanceAnimatedIndex() int {
	r.mu.RLock()
	anim := r.programAnim
	r.mu.RUnlock()
	if anim == nil {
		return -1
	}
	return anim.AdvanceIndex()
}

// AnimatedSTArraysAt returns the S and T float32 arrays for the given
// animated frame index. Returns (nil, nil) if no animated map is assigned
// or the index is out of range.
func (r *Registry) AnimatedSTArraysAt(idx int) (s, t []float32) {
	r.mu.RLock()
	anim := r.programAnim
	r.mu.RUnlock()
	if anim == nil || idx < 0 || idx >= len(anim.Frames) {
		return nil, nil
	}
	frame := anim.Frames[idx]
	return frame.S, frame.T
}

// SourceMapName returns the map name assigned to a source, or "" if none.
// Used by the GPU source manager for per-source ST map cache invalidation.
func (r *Registry) SourceMapName(sourceKey string) string {
	r.mu.RLock()
	name := r.perSource[sourceKey]
	r.mu.RUnlock()
	return name
}

// SourceSTArrays returns the S and T float32 arrays for the source's assigned
// map. Returns (nil, nil) if no map is assigned or the cached processor is nil.
// Used by the GPU source manager to upload per-source S/T data to GPU memory.
func (r *Registry) SourceSTArrays(sourceKey string) (s, t []float32) {
	r.mu.RLock()
	proc := r.sourceProcs[sourceKey]
	r.mu.RUnlock()
	if proc == nil {
		return nil, nil
	}
	return proc.STArrays()
}

// State returns a snapshot of the current registry state for broadcasting.
func (r *Registry) State() STMapState {
	r.mu.RLock()
	state := r.stateLocked()
	r.mu.RUnlock()
	return state
}

// stateLocked builds the state snapshot. Caller must hold at least r.mu.RLock.
func (r *Registry) stateLocked() STMapState {
	sources := make(map[string]string, len(r.perSource))
	for k, v := range r.perSource {
		sources[k] = v
	}

	names := make([]string, 0, len(r.maps)+len(r.animated))
	for name := range r.maps {
		names = append(names, name)
	}
	for name := range r.animated {
		names = append(names, name)
	}
	sort.Strings(names)

	state := STMapState{
		Sources:   sources,
		Available: names,
	}

	if r.programMap != "" {
		ps := &STMapProgramState{
			Map: r.programMap,
		}
		if r.programAnim != nil {
			ps.Type = "animated"
			ps.Frame = int(r.programAnim.index.Load()) % len(r.programAnim.Frames)
		} else {
			ps.Type = "static"
		}
		state.Program = ps
	}

	return state
}
