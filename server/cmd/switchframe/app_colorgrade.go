package main

import (
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
	lutData   []float32
	lutSize   int
	lutGen    uint64
}

func newColorGradeController(store *colorgrade.Store) *colorGradeController {
	return &colorGradeController{
		store:     store,
		intensity: 1.0,
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
	c.enabled = true
	c.lookName = lookName
	c.intensity = intensity
	c.lutData = lut.Data
	c.lutSize = lut.Size
	c.lutGen++
	return nil
}

func (c *colorGradeController) DisableColorGrade() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enabled = false
	c.lookName = ""
	c.lutData = nil
	c.lutSize = 0
}

func (c *colorGradeController) GetState() internal.ColorGradeState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	looks := c.store.List()
	available := make([]string, len(looks))
	for i, l := range looks {
		available[i] = l.Name
	}

	return internal.ColorGradeState{
		Enabled:   c.enabled,
		LookName:  c.lookName,
		Intensity: c.intensity,
		Available: available,
	}
}

func (c *colorGradeController) UploadLUT(name string, data io.Reader) error {
	return c.store.Upload(name, data)
}

func (c *colorGradeController) DeleteLUT(name string) error {
	c.mu.Lock()
	// If the deleted look is currently active, disable.
	if c.lookName == name {
		c.enabled = false
		c.lookName = ""
		c.lutData = nil
		c.lutSize = 0
	}
	c.mu.Unlock()

	return c.store.Delete(name)
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
