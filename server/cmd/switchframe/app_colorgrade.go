package main

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/zsiec/switchframe/server/colorgrade"
	"github.com/zsiec/switchframe/server/internal"
)

// colorGradeController manages color grading state and bridges between the
// REST API (control.ColorGradeManager) and the GPU pipeline (gpu.ColorGradeState).
type colorGradeController struct {
	store *colorgrade.Store

	mu        sync.RWMutex
	enabled   bool
	lookName  string
	intensity float32
	corrector colorgrade.CorrectorState // per-channel color corrector
	lookLUT   *colorgrade.LUT3D         // cached raw look LUT (before corrector combination)
	lutData   []float32                 // combined LUT (corrector + look)
	lutSize   int
	lutGen    uint64
}

func newColorGradeController(store *colorgrade.Store) *colorGradeController {
	return &colorGradeController{
		store:     store,
		intensity: 1.0,
		corrector: colorgrade.DefaultCorrectorState(),
	}
}

// --- control.ColorGradeManager interface ---

func (c *colorGradeController) IsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.enabled
}

func (c *colorGradeController) SetColorGrade(lookName string, intensity float32) error {
	lut, err := c.store.Load(lookName)
	if err != nil {
		return fmt.Errorf("look %q not found: %w", lookName, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.lookName = lookName
	c.intensity = intensity
	c.lookLUT = lut
	c.regenerateLUT()
	return nil
}

func (c *colorGradeController) DisableColorGrade() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enabled = false
	c.lookName = ""
	c.lookLUT = nil
	c.corrector = colorgrade.DefaultCorrectorState()
	c.regenerateLUT()
}

func (c *colorGradeController) GetState() internal.ColorGradeState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	looks := c.store.List()
	available := make([]internal.LookMetadata, len(looks))
	var lookGroup string
	for i, l := range looks {
		available[i] = internal.LookMetadata{
			Name:        l.Name,
			Group:       l.Group,
			Description: l.Description,
			Builtin:     l.Builtin,
		}
		if l.Name == c.lookName {
			lookGroup = l.Group
		}
	}

	// Convert colorgrade.CorrectorState → internal.CorrectorState.
	cs := &internal.CorrectorState{
		Lift:        c.corrector.Lift,
		Gamma:       c.corrector.Gamma,
		Gain:        c.corrector.Gain,
		Temperature: c.corrector.Temperature,
		Tint:        c.corrector.Tint,
		Saturation:  c.corrector.Saturation,
		Contrast:    c.corrector.Contrast,
		Shadows:     c.corrector.Shadows,
		Midtones:    c.corrector.Midtones,
		Highlights:  c.corrector.Highlights,
		Sharpness:   c.corrector.Sharpness,
	}
	// Copy curve slices.
	if len(c.corrector.CurveMaster) > 0 {
		cs.CurveMaster = make([]internal.CurvePoint, len(c.corrector.CurveMaster))
		for i, p := range c.corrector.CurveMaster {
			cs.CurveMaster[i] = internal.CurvePoint{X: p.X, Y: p.Y}
		}
	}
	if len(c.corrector.CurveR) > 0 {
		cs.CurveR = make([]internal.CurvePoint, len(c.corrector.CurveR))
		for i, p := range c.corrector.CurveR {
			cs.CurveR[i] = internal.CurvePoint{X: p.X, Y: p.Y}
		}
	}
	if len(c.corrector.CurveG) > 0 {
		cs.CurveG = make([]internal.CurvePoint, len(c.corrector.CurveG))
		for i, p := range c.corrector.CurveG {
			cs.CurveG[i] = internal.CurvePoint{X: p.X, Y: p.Y}
		}
	}
	if len(c.corrector.CurveB) > 0 {
		cs.CurveB = make([]internal.CurvePoint, len(c.corrector.CurveB))
		for i, p := range c.corrector.CurveB {
			cs.CurveB[i] = internal.CurvePoint{X: p.X, Y: p.Y}
		}
	}

	return internal.ColorGradeState{
		Enabled:   c.enabled,
		LookName:  c.lookName,
		LookGroup: lookGroup,
		Intensity: c.intensity,
		Available: available,
		Corrector: cs,
	}
}

// DebugSnapshot implements debug.SnapshotProvider.
func (c *colorGradeController) DebugSnapshot() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return map[string]any{
		"enabled":   c.enabled,
		"lookName":  c.lookName,
		"intensity": c.intensity,
		"lutSize":   c.lutSize,
		"lutGen":    c.lutGen,
	}
}

func (c *colorGradeController) UploadLUT(name string, data io.Reader) error {
	return c.store.Upload(name, data)
}

func (c *colorGradeController) DeleteLUT(name string) error {
	c.mu.Lock()
	// If the deleted look is currently active, clear the look but keep corrector.
	if c.lookName == name {
		c.lookName = ""
		c.lookLUT = nil
		c.regenerateLUT()
	}
	c.mu.Unlock()

	return c.store.Delete(name)
}

// --- Corrector methods ---

// SetCorrector updates the color corrector state and regenerates the combined LUT.
func (c *colorGradeController) SetCorrector(state colorgrade.CorrectorState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.corrector = state
	c.regenerateLUT()
}

// GetCorrector returns the current corrector state.
func (c *colorGradeController) GetCorrector() colorgrade.CorrectorState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.corrector
}

// ResetCorrector resets the corrector to the neutral default and regenerates the LUT.
func (c *colorGradeController) ResetCorrector() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.corrector = colorgrade.DefaultCorrectorState()
	c.regenerateLUT()
}

// SaveCustomLook bakes the current corrector + look into a new .cube file.
func (c *colorGradeController) SaveCustomLook(name string) error {
	c.mu.RLock()
	combined := colorgrade.CombineLUT(c.corrector, c.lookLUT)
	c.mu.RUnlock()

	var buf bytes.Buffer
	if err := colorgrade.WriteCube(&buf, combined); err != nil {
		return fmt.Errorf("write cube: %w", err)
	}
	return c.store.Upload(name, &buf)
}

// regenerateLUT rebuilds the combined LUT from corrector + look. Caller must hold c.mu.
func (c *colorGradeController) regenerateLUT() {
	combined := colorgrade.CombineLUT(c.corrector, c.lookLUT)
	c.lutData = combined.Data
	c.lutSize = combined.Size
	c.lutGen++
	// Auto-enable if there's something to show.
	if c.lookName != "" || !c.corrector.IsDefault() {
		c.enabled = true
	}
}

// --- gpu.ColorGradeState interface (via adapter) ---

// colorGradeStateAdapter implements gpu.ColorGradeState for the pipeline node.
type colorGradeStateAdapter struct {
	ctrl *colorGradeController
}

func newColorGradeStateAdapter(ctrl *colorGradeController) *colorGradeStateAdapter {
	return &colorGradeStateAdapter{ctrl: ctrl}
}

func (a *colorGradeStateAdapter) HasGradingEnabled() bool {
	a.ctrl.mu.RLock()
	defer a.ctrl.mu.RUnlock()
	return a.ctrl.enabled
}

func (a *colorGradeStateAdapter) GradingLUTData() []float32 {
	a.ctrl.mu.RLock()
	defer a.ctrl.mu.RUnlock()
	if a.ctrl.lutData == nil {
		return nil
	}
	// Deep copy to prevent aliasing.
	data := make([]float32, len(a.ctrl.lutData))
	copy(data, a.ctrl.lutData)
	return data
}

func (a *colorGradeStateAdapter) GradingLUTSize() int {
	a.ctrl.mu.RLock()
	defer a.ctrl.mu.RUnlock()
	return a.ctrl.lutSize
}

func (a *colorGradeStateAdapter) GradingLUTGen() uint64 {
	a.ctrl.mu.RLock()
	defer a.ctrl.mu.RUnlock()
	return a.ctrl.lutGen
}

func (a *colorGradeStateAdapter) GradingIntensity() float32 {
	a.ctrl.mu.RLock()
	defer a.ctrl.mu.RUnlock()
	return a.ctrl.intensity
}
