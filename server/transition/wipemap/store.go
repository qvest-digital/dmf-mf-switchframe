package wipemap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Sentinel errors for WipeMapStore operations.
var (
	ErrPatternNotFound = errors.New("wipe pattern not found")
	ErrDeleteBuiltin   = errors.New("cannot delete builtin pattern")
)

type resolution struct{ w, h int }

// maxCacheBytes is the maximum total size of cached gradient maps before the
// entire cache is cleared. Maps regenerate cheaply so full eviction is fine.
const maxCacheBytes = 512 * 1024 * 1024 // 512 MB

// WipeMapStore manages wipe pattern registration, caching, and custom map storage.
type WipeMapStore struct {
	mu         sync.RWMutex
	patterns   map[string]*WipePattern
	generators map[string]GeneratorFunc
	cache      map[string]map[resolution][]byte
	cacheBytes int64
	customDir  string
}

// NewWipeMapStore creates a store with the given directory for custom maps.
func NewWipeMapStore(customDir string) (*WipeMapStore, error) {
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		return nil, fmt.Errorf("create custom wipe dir: %w", err)
	}
	return &WipeMapStore{
		patterns:   make(map[string]*WipePattern),
		generators: make(map[string]GeneratorFunc),
		cache:      make(map[string]map[resolution][]byte),
		customDir:  customDir,
	}, nil
}

// register adds a pattern with its generator.
func (s *WipeMapStore) register(id, name, category, description string, gen GeneratorFunc) {
	s.patterns[id] = &WipePattern{
		ID:          id,
		Name:        name,
		Category:    category,
		Description: description,
		Builtin:     true,
	}
	s.generators[id] = gen
}

// RegisterBuiltins registers all ~96 built-in wipe patterns.
func (s *WipeMapStore) RegisterBuiltins() {
	// === CatBasic ===
	s.register("h-left", "Horizontal Left", CatBasic, "Left to right wipe", GenHorizontal(false))
	s.register("h-right", "Horizontal Right", CatBasic, "Right to left wipe", GenHorizontal(true))
	s.register("v-top", "Vertical Top", CatBasic, "Top to bottom wipe", GenVertical(false))
	s.register("v-bottom", "Vertical Bottom", CatBasic, "Bottom to top wipe", GenVertical(true))
	s.register("diag-45", "Diagonal 45°", CatBasic, "Top-left to bottom-right diagonal wipe", GenDiagonal(45))
	s.register("diag-135", "Diagonal 135°", CatBasic, "Top-right to bottom-left diagonal wipe", GenDiagonal(135))
	s.register("diag-225", "Diagonal 225°", CatBasic, "Bottom-right to top-left diagonal wipe", GenDiagonal(225))
	s.register("diag-315", "Diagonal 315°", CatBasic, "Bottom-left to top-right diagonal wipe", GenDiagonal(315))
	s.register("barn-h-open", "Barn Door H Open", CatBasic, "Horizontal barn door opening", GenBarnDoor("h", false))
	s.register("barn-h-close", "Barn Door H Close", CatBasic, "Horizontal barn door closing", GenBarnDoor("h", true))
	s.register("barn-v-open", "Barn Door V Open", CatBasic, "Vertical barn door opening", GenBarnDoor("v", false))
	s.register("barn-v-close", "Barn Door V Close", CatBasic, "Vertical barn door closing", GenBarnDoor("v", true))

	// === CatIris ===
	s.register("box-center-out", "Box Center Out", CatIris, "Box iris opening from center", GenBoxIris(0.5, 0.5, false))
	s.register("box-edges-in", "Box Edges In", CatIris, "Box iris closing to center", GenBoxIris(0.5, 0.5, true))
	s.register("box-tl", "Box Top-Left", CatIris, "Box iris from top-left", GenBoxIris(0.0, 0.0, false))
	s.register("box-tr", "Box Top-Right", CatIris, "Box iris from top-right", GenBoxIris(1.0, 0.0, false))
	s.register("box-bl", "Box Bottom-Left", CatIris, "Box iris from bottom-left", GenBoxIris(0.0, 1.0, false))
	s.register("box-br", "Box Bottom-Right", CatIris, "Box iris from bottom-right", GenBoxIris(1.0, 1.0, false))
	s.register("circle-center-out", "Circle Center Out", CatIris, "Circular iris from center", GenCircleIris(0.5, 0.5, 1.0, 1.0, false))
	s.register("circle-center-in", "Circle Center In", CatIris, "Circular iris closing to center", GenCircleIris(0.5, 0.5, 1.0, 1.0, true))
	s.register("circle-tl", "Circle Top-Left", CatIris, "Circle iris from top-left", GenCircleIris(0.0, 0.0, 1.0, 1.0, false))
	s.register("circle-tr", "Circle Top-Right", CatIris, "Circle iris from top-right", GenCircleIris(1.0, 0.0, 1.0, 1.0, false))
	s.register("circle-bl", "Circle Bottom-Left", CatIris, "Circle iris from bottom-left", GenCircleIris(0.0, 1.0, 1.0, 1.0, false))
	s.register("circle-br", "Circle Bottom-Right", CatIris, "Circle iris from bottom-right", GenCircleIris(1.0, 1.0, 1.0, 1.0, false))
	s.register("oval-h", "Oval Horizontal", CatIris, "Horizontal oval iris", GenCircleIris(0.5, 0.5, 1.5, 1.0, false))
	s.register("oval-v", "Oval Vertical", CatIris, "Vertical oval iris", GenCircleIris(0.5, 0.5, 1.0, 1.5, false))
	s.register("diamond-center-out", "Diamond Center Out", CatIris, "Diamond iris from center", GenDiamondIris(0.5, 0.5, false))
	s.register("diamond-center-in", "Diamond Center In", CatIris, "Diamond iris closing to center", GenDiamondIris(0.5, 0.5, true))
	s.register("diamond-tl", "Diamond Top-Left", CatIris, "Diamond iris from top-left", GenDiamondIris(0.0, 0.0, false))
	s.register("diamond-tr", "Diamond Top-Right", CatIris, "Diamond iris from top-right", GenDiamondIris(1.0, 0.0, false))
	s.register("pinhole-center", "Pinhole Center", CatIris, "Pinhole iris from center", GenPinhole(0.5, 0.5))
	s.register("pinhole-tl", "Pinhole Top-Left", CatIris, "Pinhole iris from top-left", GenPinhole(0.0, 0.0))
	s.register("pinhole-tr", "Pinhole Top-Right", CatIris, "Pinhole iris from top-right", GenPinhole(1.0, 0.0))
	s.register("pinhole-bl", "Pinhole Bottom-Left", CatIris, "Pinhole iris from bottom-left", GenPinhole(0.0, 1.0))
	s.register("pinhole-br", "Pinhole Bottom-Right", CatIris, "Pinhole iris from bottom-right", GenPinhole(1.0, 1.0))

	// === CatClock ===
	s.register("clock-12-cw", "Clock 12 CW", CatClock, "Clock wipe from 12 o'clock clockwise", GenClockWipe(270, 1, true))
	s.register("clock-12-ccw", "Clock 12 CCW", CatClock, "Clock wipe from 12 o'clock counter-clockwise", GenClockWipe(270, 1, false))
	s.register("clock-3-cw", "Clock 3 CW", CatClock, "Clock wipe from 3 o'clock clockwise", GenClockWipe(0, 1, true))
	s.register("clock-6-cw", "Clock 6 CW", CatClock, "Clock wipe from 6 o'clock clockwise", GenClockWipe(90, 1, true))
	s.register("clock-9-cw", "Clock 9 CW", CatClock, "Clock wipe from 9 o'clock clockwise", GenClockWipe(180, 1, true))
	s.register("clock-2hand", "Clock 2-Hand", CatClock, "Two-handed clock wipe", GenClockWipe(270, 2, true))
	s.register("clock-3hand", "Clock 3-Hand", CatClock, "Three-handed clock wipe", GenClockWipe(270, 3, true))
	s.register("clock-4hand", "Clock 4-Hand", CatClock, "Four-handed clock wipe", GenClockWipe(270, 4, true))
	s.register("vclock-12", "V-Clock 12", CatClock, "V-shaped clock from 12 o'clock", GenVClockWipe(270))
	s.register("vclock-3", "V-Clock 3", CatClock, "V-shaped clock from 3 o'clock", GenVClockWipe(0))
	s.register("vclock-6", "V-Clock 6", CatClock, "V-shaped clock from 6 o'clock", GenVClockWipe(90))
	s.register("vclock-9", "V-Clock 9", CatClock, "V-shaped clock from 9 o'clock", GenVClockWipe(180))
	s.register("radial-4", "Radial Burst 4", CatClock, "4-segment radial burst", GenRadialBurst(4))
	s.register("radial-6", "Radial Burst 6", CatClock, "6-segment radial burst", GenRadialBurst(6))
	s.register("radial-8", "Radial Burst 8", CatClock, "8-segment radial burst", GenRadialBurst(8))
	s.register("radial-12", "Radial Burst 12", CatClock, "12-segment radial burst", GenRadialBurst(12))
	s.register("radial-16", "Radial Burst 16", CatClock, "16-segment radial burst", GenRadialBurst(16))

	// === CatMatrix ===
	s.register("blind-h-4", "Venetian H 4", CatMatrix, "4 horizontal venetian blinds", GenVenetianBlind(4, true))
	s.register("blind-h-8", "Venetian H 8", CatMatrix, "8 horizontal venetian blinds", GenVenetianBlind(8, true))
	s.register("blind-h-16", "Venetian H 16", CatMatrix, "16 horizontal venetian blinds", GenVenetianBlind(16, true))
	s.register("blind-v-4", "Venetian V 4", CatMatrix, "4 vertical venetian blinds", GenVenetianBlind(4, false))
	s.register("blind-v-8", "Venetian V 8", CatMatrix, "8 vertical venetian blinds", GenVenetianBlind(8, false))
	s.register("blind-v-16", "Venetian V 16", CatMatrix, "16 vertical venetian blinds", GenVenetianBlind(16, false))
	s.register("checker-4x3", "Checkerboard 4x3", CatMatrix, "4x3 checkerboard wipe", GenCheckerboard(4, 3))
	s.register("checker-8x6", "Checkerboard 8x6", CatMatrix, "8x6 checkerboard wipe", GenCheckerboard(8, 6))
	s.register("checker-16x9", "Checkerboard 16x9", CatMatrix, "16x9 checkerboard wipe", GenCheckerboard(16, 9))
	s.register("checker-diag-20", "Checker Diagonal 20", CatMatrix, "Diagonal checkerboard (20px)", GenCheckerboardDiagonal(20))
	s.register("checker-diag-40", "Checker Diagonal 40", CatMatrix, "Diagonal checkerboard (40px)", GenCheckerboardDiagonal(40))
	s.register("checker-diag-80", "Checker Diagonal 80", CatMatrix, "Diagonal checkerboard (80px)", GenCheckerboardDiagonal(80))
	s.register("random-1px", "Random Pixel", CatMatrix, "Per-pixel random dissolve", GenRandom(1, 42))
	s.register("random-4px", "Random 4px Block", CatMatrix, "4px block random dissolve", GenRandom(4, 42))
	s.register("random-8px", "Random 8px Block", CatMatrix, "8px block random dissolve", GenRandom(8, 42))
	s.register("random-16px", "Random 16px Block", CatMatrix, "16px block random dissolve", GenRandom(16, 42))
	s.register("matrix-cascade", "Matrix Cascade", CatMatrix, "Matrix-style column cascade", GenMatrixCascade())

	// === CatShape ===
	s.register("triangle-up", "Triangle Up", CatShape, "Triangle wipe pointing up", GenTriangle("up"))
	s.register("triangle-down", "Triangle Down", CatShape, "Triangle wipe pointing down", GenTriangle("down"))
	s.register("triangle-left", "Triangle Left", CatShape, "Triangle wipe pointing left", GenTriangle("left"))
	s.register("triangle-right", "Triangle Right", CatShape, "Triangle wipe pointing right", GenTriangle("right"))
	s.register("star-4", "4-Point Star", CatShape, "4-point star iris wipe", GenStar(4))
	s.register("star-5", "5-Point Star", CatShape, "5-point star iris wipe", GenStar(5))
	s.register("star-6", "6-Point Star", CatShape, "6-point star iris wipe", GenStar(6))
	s.register("star-8", "8-Point Star", CatShape, "8-point star iris wipe", GenStar(8))
	s.register("cross", "Cross", CatShape, "Cross-shaped wipe", GenCross(false))
	s.register("cross-rotated", "Cross Rotated", CatShape, "X-shaped rotated cross wipe", GenCross(true))
	s.register("polygon-3", "Triangle Iris", CatShape, "Triangular polygon iris", GenPolygon(3))
	s.register("polygon-5", "Pentagon Iris", CatShape, "Pentagon polygon iris", GenPolygon(5))
	s.register("polygon-6", "Hexagon Iris", CatShape, "Hexagon polygon iris", GenPolygon(6))
	s.register("polygon-8", "Octagon Iris", CatShape, "Octagon polygon iris", GenPolygon(8))
	s.register("heart", "Heart", CatShape, "Heart-shaped iris wipe", GenHeart())
	s.register("keyhole", "Keyhole", CatShape, "Keyhole-shaped iris wipe", GenKeyhole())

	// === CatSpecialty ===
	s.register("zigzag-h-4", "Zigzag H 4", CatSpecialty, "Horizontal zigzag (4 teeth)", GenZigzag("h", 4))
	s.register("zigzag-h-8", "Zigzag H 8", CatSpecialty, "Horizontal zigzag (8 teeth)", GenZigzag("h", 8))
	s.register("zigzag-v-4", "Zigzag V 4", CatSpecialty, "Vertical zigzag (4 teeth)", GenZigzag("v", 4))
	s.register("zigzag-v-8", "Zigzag V 8", CatSpecialty, "Vertical zigzag (8 teeth)", GenZigzag("v", 8))
	s.register("zigzag-diag-4", "Zigzag Diag 4", CatSpecialty, "Diagonal zigzag (4 teeth)", GenZigzag("diag", 4))
	s.register("zigzag-diag-8", "Zigzag Diag 8", CatSpecialty, "Diagonal zigzag (8 teeth)", GenZigzag("diag", 8))
	s.register("spiral-cw", "Spiral CW", CatSpecialty, "Clockwise spiral wipe", GenSpiral(true))
	s.register("spiral-ccw", "Spiral CCW", CatSpecialty, "Counter-clockwise spiral wipe", GenSpiral(false))
	s.register("wave-h-2", "Wave H 2", CatSpecialty, "Horizontal wave (2 cycles)", GenWave("h", 2))
	s.register("wave-h-4", "Wave H 4", CatSpecialty, "Horizontal wave (4 cycles)", GenWave("h", 4))
	s.register("wave-v-2", "Wave V 2", CatSpecialty, "Vertical wave (2 cycles)", GenWave("v", 2))
	s.register("wave-v-4", "Wave V 4", CatSpecialty, "Vertical wave (4 cycles)", GenWave("v", 4))
	s.register("curtain-open", "Curtain Open", CatSpecialty, "Curtain opening wipe", GenCurtain(false))
	s.register("curtain-close", "Curtain Close", CatSpecialty, "Curtain closing wipe", GenCurtain(true))

	// === CatMask ===
	s.register("binoculars", "Binoculars", CatMask, "Binocular mask wipe", GenBinoculars())
	s.register("letterbox-open", "Letterbox Open", CatMask, "Letterbox bars opening", GenLetterbox(false))
	s.register("letterbox-close", "Letterbox Close", CatMask, "Letterbox bars closing", GenLetterbox(true))

	// === CatDissolve ===
	s.register("dissolve-perlin", "Dissolve Perlin", CatDissolve, "Smooth noise dissolve", GenDissolve("perlin"))
	s.register("dissolve-blocks", "Dissolve Blocks", CatDissolve, "Block-based dissolve", GenDissolve("blocks"))
	s.register("dissolve-scanline", "Dissolve Scanline", CatDissolve, "Scanline dissolve", GenDissolve("scanline"))
	s.register("dissolve-burn", "Dissolve Burn", CatDissolve, "Edge-burn dissolve", GenDissolve("burn"))
}

// GetMap returns the gradient map for the given pattern at the requested resolution.
// The result is cached; subsequent calls with the same pattern+resolution return
// the cached slice.
func (s *WipeMapStore) GetMap(patternID string, w, h int) ([]byte, error) {
	res := resolution{w, h}

	s.mu.RLock()
	if byRes, ok := s.cache[patternID]; ok {
		if m, ok := byRes[res]; ok {
			s.mu.RUnlock()
			return m, nil
		}
	}
	_, hasGen := s.generators[patternID]
	s.mu.RUnlock()

	if !hasGen {
		// Check if it's a custom pattern stored on disk.
		return nil, fmt.Errorf("%w: %s", ErrPatternNotFound, patternID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock.
	if byRes, ok := s.cache[patternID]; ok {
		if m, ok := byRes[res]; ok {
			return m, nil
		}
	}

	gen := s.generators[patternID]
	m := gen(w, h)

	if s.cache[patternID] == nil {
		s.cache[patternID] = make(map[resolution][]byte)
	}
	s.cache[patternID][res] = m
	s.cacheBytes += int64(len(m))
	if s.cacheBytes > maxCacheBytes {
		s.clearCacheLocked()
	}
	return m, nil
}

// List returns all registered patterns sorted by category then ID.
func (s *WipeMapStore) List() []WipePattern {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]WipePattern, 0, len(s.patterns))
	for _, p := range s.patterns {
		result = append(result, *p)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Category != result[j].Category {
			return result[i].Category < result[j].Category
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// ListByCategory returns patterns in the given category.
func (s *WipeMapStore) ListByCategory(category string) []WipePattern {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []WipePattern
	for _, p := range s.patterns {
		if p.Category == category {
			result = append(result, *p)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// GetPattern returns metadata for a single pattern.
func (s *WipeMapStore) GetPattern(id string) (*WipePattern, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.patterns[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPatternNotFound, id)
	}
	cp := *p
	return &cp, nil
}

// customMeta is the JSON metadata persisted for custom patterns.
type customMeta struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// ErrOverwriteBuiltin is returned when attempting to upload a custom pattern
// with an ID that conflicts with a builtin pattern.
var ErrOverwriteBuiltin = errors.New("cannot overwrite builtin pattern")

// UploadCustom saves a custom gradient map to disk and registers it.
// If a custom pattern with the same ID already exists, it is overwritten.
// Returns an error if the ID conflicts with a builtin pattern.
func (s *WipeMapStore) UploadCustom(id, name string, mapData []byte, w, h int) error {
	if len(mapData) != w*h {
		return fmt.Errorf("map data size %d does not match %dx%d=%d", len(mapData), w, h, w*h)
	}

	// Check for builtin conflict under write lock before writing files to disk.
	// This avoids a TOCTOU window between the check and file writes.
	s.mu.Lock()
	if existing, ok := s.patterns[id]; ok && existing.Builtin {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrOverwriteBuiltin, id)
	}
	s.mu.Unlock()

	// Write binary data.
	binPath := filepath.Join(s.customDir, id+".bin")
	if err := os.WriteFile(binPath, mapData, 0o644); err != nil {
		return fmt.Errorf("write custom map: %w", err)
	}

	// Write metadata.
	meta := customMeta{ID: id, Name: name, Width: w, Height: h}
	jsonData, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal custom metadata: %w", err)
	}
	jsonPath := filepath.Join(s.customDir, id+".json")
	if err := os.WriteFile(jsonPath, jsonData, 0o644); err != nil {
		return fmt.Errorf("write custom metadata: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.patterns[id] = &WipePattern{
		ID:       id,
		Name:     name,
		Category: CatCustom,
		Builtin:  false,
	}

	// Store a generator that returns a copy of the uploaded data at the native
	// resolution, or the raw data if the requested resolution matches.
	dataCopy := make([]byte, len(mapData))
	copy(dataCopy, mapData)
	nativeW, nativeH := w, h

	s.generators[id] = func(rw, rh int) []byte {
		if rw == nativeW && rh == nativeH {
			out := make([]byte, len(dataCopy))
			copy(out, dataCopy)
			return out
		}
		// Nearest-neighbor scale for different resolutions.
		out := make([]byte, rw*rh)
		for y := 0; y < rh; y++ {
			srcY := y * nativeH / rh
			for x := 0; x < rw; x++ {
				srcX := x * nativeW / rw
				out[y*rw+x] = dataCopy[srcY*nativeW+srcX]
			}
		}
		return out
	}

	// Cache at native resolution.
	if s.cache[id] == nil {
		s.cache[id] = make(map[resolution][]byte)
	}
	cached := make([]byte, len(dataCopy))
	copy(cached, dataCopy)
	s.cache[id][resolution{w, h}] = cached
	s.cacheBytes += int64(len(cached))
	if s.cacheBytes > maxCacheBytes {
		s.clearCacheLocked()
	}

	return nil
}

// DeleteCustom removes a custom pattern. Returns an error if the pattern is builtin.
func (s *WipeMapStore) DeleteCustom(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.patterns[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrPatternNotFound, id)
	}
	if p.Builtin {
		return fmt.Errorf("%w: %s", ErrDeleteBuiltin, id)
	}

	// Remove disk files (best-effort).
	os.Remove(filepath.Join(s.customDir, id+".bin"))
	os.Remove(filepath.Join(s.customDir, id+".json"))

	delete(s.patterns, id)
	delete(s.generators, id)
	delete(s.cache, id)
	return nil
}

// InvalidateCache clears all cached gradient maps.
func (s *WipeMapStore) InvalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCacheLocked()
}

// clearCacheLocked resets the cache and byte counter. Must be called with s.mu held.
func (s *WipeMapStore) clearCacheLocked() {
	s.cache = make(map[string]map[resolution][]byte)
	s.cacheBytes = 0
}
