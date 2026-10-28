package colorgrade

import "sync"

type sourceEntry struct {
	state  CorrectorState
	lut    *LUT3D  // cached, nil = needs regeneration
	grader *Grader // cached CPU grader over lut, nil = needs regeneration
	gen    uint64  // incremented on each state change
}

// SourceCorrectorManager manages per-source color corrector state with LUT caching.
// Implements gpu.SourceColorGradeProvider.
type SourceCorrectorManager struct {
	mu      sync.RWMutex
	sources map[string]*sourceEntry
}

// NewSourceCorrectorManager creates a new per-source corrector state manager.
func NewSourceCorrectorManager() *SourceCorrectorManager {
	return &SourceCorrectorManager{
		sources: make(map[string]*sourceEntry),
	}
}

// Get returns the corrector state for a source. Returns default if not set.
func (m *SourceCorrectorManager) Get(key string) CorrectorState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.sources[key]; ok {
		return e.state
	}
	return DefaultCorrectorState()
}

// Set updates the corrector state for a source and invalidates the LUT cache.
func (m *SourceCorrectorManager) Set(key string, state CorrectorState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// If state is default, remove the entry to save memory.
	if state.IsDefault() {
		delete(m.sources, key)
		return
	}
	e, ok := m.sources[key]
	if !ok {
		e = &sourceEntry{}
		m.sources[key] = e
	}
	e.state = state
	e.lut = nil // invalidate cache
	e.grader = nil
	e.gen++
}

// Reset removes the corrector state for a source (back to default).
func (m *SourceCorrectorManager) Reset(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sources, key)
}

// HasCorrection returns true if a source has non-default correction.
func (m *SourceCorrectorManager) HasCorrection(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sources[key]
	return ok
}

// Remove cleans up state when a source is removed.
func (m *SourceCorrectorManager) Remove(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sources, key)
}

// AllCorrected returns the keys of all sources with non-default correction.
func (m *SourceCorrectorManager) AllCorrected() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.sources))
	for k := range m.sources {
		keys = append(keys, k)
	}
	return keys
}

// --- gpu.SourceColorGradeProvider implementation ---

// SourceLUTData returns the LUT float32 data for a source, or nil if default.
func (m *SourceCorrectorManager) SourceLUTData(sourceKey string) []float32 {
	m.mu.Lock() // full lock for lazy LUT generation
	defer m.mu.Unlock()
	e, ok := m.sources[sourceKey]
	if !ok {
		return nil
	}
	if e.lut == nil {
		e.lut = CombineLUT(e.state, nil) // no look LUT for per-source
	}
	return e.lut.Data
}

// SourceLUTSize returns the LUT cube dimension (33).
func (m *SourceCorrectorManager) SourceLUTSize(sourceKey string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.sources[sourceKey]; ok && e.lut != nil {
		return e.lut.Size
	}
	return 33 // default
}

// SourceLUTGen returns the generation counter for cache invalidation.
func (m *SourceCorrectorManager) SourceLUTGen(sourceKey string) uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.sources[sourceKey]; ok {
		return e.gen
	}
	return 0
}

// SourceGrader returns the CPU grader for a source's correction, or nil when
// the source has none. Implements switcher.SourceGrader.
func (m *SourceCorrectorManager) SourceGrader(sourceKey string) *Grader {
	m.mu.RLock()
	e, ok := m.sources[sourceKey]
	var g *Grader
	if ok {
		g = e.grader
	}
	m.mu.RUnlock()
	if !ok || g != nil {
		return g
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok = m.sources[sourceKey]
	if !ok {
		return nil
	}
	if e.grader == nil {
		if e.lut == nil {
			e.lut = CombineLUT(e.state, nil)
		}
		// CombineLUT always yields a well-formed cube.
		e.grader, _ = NewGrader(e.lut.Data, e.lut.Size)
	}
	return e.grader
}
