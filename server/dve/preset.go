package dve

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Preset stores a complete DVE composition state for recall.
type Preset struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slots        []Slot `json:"slots"`
	TransitionMs int    `json:"transitionMs"` // default recall transition duration
	Easing       string `json:"easing"`       // default recall easing type
}

// PresetInfo is a summary for listing (no slots data).
type PresetInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PresetStore persists DVE presets to disk.
type PresetStore struct {
	dir string
	mu  sync.RWMutex
}

// NewPresetStore creates a new PresetStore, creating the directory if needed.
func NewPresetStore(dir string) (*PresetStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create preset store dir: %w", err)
	}
	return &PresetStore{dir: dir}, nil
}

// Save writes a preset to disk atomically (temp file + rename).
func (s *PresetStore) Save(p *Preset) error {
	if err := validatePresetID(p.ID); err != nil {
		return err
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal preset: %w", err)
	}

	target := filepath.Join(s.dir, p.ID+".json")
	tmp := target + ".tmp"

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// Load reads a preset from disk by ID.
func (s *PresetStore) Load(id string) (*Preset, error) {
	if err := validatePresetID(id); err != nil {
		return nil, err
	}

	path := filepath.Join(s.dir, id+".json")

	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("preset %q not found", id)
		}
		return nil, fmt.Errorf("read preset: %w", err)
	}

	var p Preset
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse preset: %w", err)
	}
	return &p, nil
}

// Delete removes a preset file by ID.
func (s *PresetStore) Delete(id string) error {
	if err := validatePresetID(id); err != nil {
		return err
	}

	path := filepath.Join(s.dir, id+".json")

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("preset %q not found", id)
		}
		return fmt.Errorf("delete preset: %w", err)
	}
	return nil
}

// List scans the store directory and returns all preset summaries sorted by name.
func (s *PresetStore) List() ([]PresetInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read preset dir: %w", err)
	}

	var infos []PresetInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(s.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var p Preset
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		infos = append(infos, PresetInfo{ID: p.ID, Name: p.Name})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})

	return infos, nil
}

// validatePresetID rejects IDs that could cause path traversal.
func validatePresetID(id string) error {
	if id == "" {
		return fmt.Errorf("preset ID must not be empty")
	}
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("preset ID %q contains path separator", id)
	}
	if id == "." || id == ".." || strings.Contains(id, "..") {
		return fmt.Errorf("preset ID %q is invalid", id)
	}
	return nil
}

// RecallPreset creates a transient Animation that interpolates from current
// to target state over the given duration. For each slot that exists in both
// current and target, it generates 2-keyframe tracks for every Transform
// parameter that differs.
func RecallPreset(current, target *Layout, durationMs int) *Animation {
	anim := &Animation{
		Name:       "preset-recall",
		DurationMs: durationMs,
	}

	// Process slots that exist in both layouts.
	n := len(current.Slots)
	if len(target.Slots) < n {
		n = len(target.Slots)
	}

	for i := 0; i < n; i++ {
		cs := current.Slots[i]
		ts := target.Slots[i]

		// Handle enable/disable transitions.
		if !cs.Enabled && ts.Enabled {
			// disabled -> enabled: fade in from 0 to target opacity.
			targetOpacity := ts.Transform.EffectiveOpacity()
			anim.Tracks = append(anim.Tracks, makeLinearTrack("opacity", i, 0, targetOpacity, durationMs))
		} else if cs.Enabled && !ts.Enabled {
			// enabled -> disabled: fade out from current opacity to 0.
			currentOpacity := cs.Transform.EffectiveOpacity()
			anim.Tracks = append(anim.Tracks, makeLinearTrack("opacity", i, currentOpacity, 0, durationMs))
		}

		// Generate tracks for differing transform parameters.
		type paramPair struct {
			name string
			cur  float64
			tgt  float64
		}
		pairs := []paramPair{
			{"positionX", cs.Transform.PositionX, ts.Transform.PositionX},
			{"positionY", cs.Transform.PositionY, ts.Transform.PositionY},
			{"width", cs.Transform.Width, ts.Transform.Width},
			{"height", cs.Transform.Height, ts.Transform.Height},
			{"rotationDeg", cs.Transform.RotationDeg, ts.Transform.RotationDeg},
			{"perspectiveX", cs.Transform.PerspectiveX, ts.Transform.PerspectiveX},
			{"perspectiveY", cs.Transform.PerspectiveY, ts.Transform.PerspectiveY},
			{"anchorX", cs.Transform.AnchorX, ts.Transform.AnchorX},
			{"anchorY", cs.Transform.AnchorY, ts.Transform.AnchorY},
			{"cropTop", cs.Transform.CropTop, ts.Transform.CropTop},
			{"cropBottom", cs.Transform.CropBottom, ts.Transform.CropBottom},
			{"cropLeft", cs.Transform.CropLeft, ts.Transform.CropLeft},
			{"cropRight", cs.Transform.CropRight, ts.Transform.CropRight},
			{"cropSoftness", cs.Transform.CropSoftness, ts.Transform.CropSoftness},
			{"defocusRadius", cs.Transform.DefocusRadius, ts.Transform.DefocusRadius},
			{"border.width", cs.Transform.Border.Width, ts.Transform.Border.Width},
			{"border.cornerRadius", cs.Transform.Border.CornerRadius, ts.Transform.Border.CornerRadius},
			{"border.softness", cs.Transform.Border.Softness, ts.Transform.Border.Softness},
			{"border.innerWidth", cs.Transform.Border.InnerWidth, ts.Transform.Border.InnerWidth},
			{"shadow.offsetX", cs.Transform.Shadow.OffsetX, ts.Transform.Shadow.OffsetX},
			{"shadow.offsetY", cs.Transform.Shadow.OffsetY, ts.Transform.Shadow.OffsetY},
			{"shadow.blur", cs.Transform.Shadow.Blur, ts.Transform.Shadow.Blur},
			{"shadow.opacity", cs.Transform.Shadow.Opacity, ts.Transform.Shadow.Opacity},
		}

		// Only add opacity track for same-enabled-state transitions (enable/disable handled above).
		if cs.Enabled == ts.Enabled {
			pairs = append(pairs, paramPair{"opacity", cs.Transform.Opacity, ts.Transform.Opacity})
		}

		for _, pp := range pairs {
			if !floatEqual(pp.cur, pp.tgt) {
				anim.Tracks = append(anim.Tracks, makeLinearTrack(pp.name, i, pp.cur, pp.tgt, durationMs))
			}
		}
	}

	return anim
}

// makeLinearTrack creates a 2-keyframe track with linear easing.
func makeLinearTrack(param string, slotIndex int, startVal, endVal float64, durationMs int) Track {
	return Track{
		Param:     param,
		SlotIndex: slotIndex,
		Keyframes: []Keyframe{
			{TimeMs: 0, Value: startVal, EaseOutX: 1, EaseOutY: 1},
			{TimeMs: durationMs, Value: endVal},
		},
	}
}

// floatEqual returns true if two float64 values are approximately equal.
func floatEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// BuiltinPresets generates the standard set of built-in DVE presets for
// the given frame dimensions.
func BuiltinPresets(frameW, frameH int) []*Preset {
	fw := float64(frameW)
	fh := float64(frameH)

	return []*Preset{
		builtinPIPTopRight(fw, fh),
		builtinPIPTopLeft(fw, fh),
		builtinPIPBottomRight(fw, fh),
		builtinPIPBottomLeft(fw, fh),
		builtinSideBySide(fw, fh),
		builtinQuad(fw, fh),
		builtinThreeBox(fw, fh),
		builtinOverShoulder(fw, fh),
	}
}

// ResolveBuiltinPreset returns a single built-in preset by name, or nil if not found.
func ResolveBuiltinPreset(name string, frameW, frameH int) *Preset {
	for _, p := range BuiltinPresets(frameW, frameH) {
		if p.ID == name {
			return p
		}
	}
	return nil
}

func whiteBorder(width, cornerRadius float64) BorderConfig {
	return BorderConfig{
		Width:        width,
		ColorY:       235,
		ColorCb:      128,
		ColorCr:      128,
		CornerRadius: cornerRadius,
	}
}

func pipRectTransform(x, y, w, h float64) Transform {
	t := DefaultTransform(x, y, w, h)
	t.RectMode = true
	t.OutputRect = Rect{X: x, Y: y, W: w, H: h}
	t.ScaleMode = ScaleModeFill
	t.CropAnchor = [2]float64{0.5, 0.5}
	return t
}

func builtinPIPTopRight(fw, fh float64) *Preset {
	pipW := fw * 0.25
	pipH := fh * 0.25
	margin := fw * 0.02
	x := fw - pipW - margin
	y := margin

	t := pipRectTransform(x, y, pipW, pipH)
	t.Border = whiteBorder(3, 6)

	return &Preset{
		ID:           "pip-top-right",
		Name:         "PIP Top Right",
		Slots:        []Slot{{Transform: t, ZOrder: 1, Enabled: true}},
		TransitionMs: 300,
		Easing:       "ease-in-out",
	}
}

func builtinPIPTopLeft(fw, fh float64) *Preset {
	pipW := fw * 0.25
	pipH := fh * 0.25
	margin := fw * 0.02
	x := margin
	y := margin

	t := pipRectTransform(x, y, pipW, pipH)
	t.Border = whiteBorder(3, 6)

	return &Preset{
		ID:           "pip-top-left",
		Name:         "PIP Top Left",
		Slots:        []Slot{{Transform: t, ZOrder: 1, Enabled: true}},
		TransitionMs: 300,
		Easing:       "ease-in-out",
	}
}

func builtinPIPBottomRight(fw, fh float64) *Preset {
	pipW := fw * 0.25
	pipH := fh * 0.25
	margin := fw * 0.02
	x := fw - pipW - margin
	y := fh - pipH - margin

	t := pipRectTransform(x, y, pipW, pipH)
	t.Border = whiteBorder(3, 6)

	return &Preset{
		ID:           "pip-bottom-right",
		Name:         "PIP Bottom Right",
		Slots:        []Slot{{Transform: t, ZOrder: 1, Enabled: true}},
		TransitionMs: 300,
		Easing:       "ease-in-out",
	}
}

func builtinPIPBottomLeft(fw, fh float64) *Preset {
	pipW := fw * 0.25
	pipH := fh * 0.25
	margin := fw * 0.02
	x := margin
	y := fh - pipH - margin

	t := pipRectTransform(x, y, pipW, pipH)
	t.Border = whiteBorder(3, 6)

	return &Preset{
		ID:           "pip-bottom-left",
		Name:         "PIP Bottom Left",
		Slots:        []Slot{{Transform: t, ZOrder: 1, Enabled: true}},
		TransitionMs: 300,
		Easing:       "ease-in-out",
	}
}

func builtinSideBySide(fw, fh float64) *Preset {
	gap := 4.0
	slotW := (fw - gap) / 2
	slotH := fh

	t0 := pipRectTransform(0, 0, slotW, slotH)
	t1 := pipRectTransform(slotW+gap, 0, slotW, slotH)

	return &Preset{
		ID:   "side-by-side",
		Name: "Side by Side",
		Slots: []Slot{
			{Transform: t0, ZOrder: 0, Enabled: true},
			{Transform: t1, ZOrder: 0, Enabled: true},
		},
		TransitionMs: 300,
		Easing:       "ease-in-out",
	}
}

func builtinQuad(fw, fh float64) *Preset {
	gap := 4.0
	slotW := (fw - gap) / 2
	slotH := (fh - gap) / 2

	makeSlot := func(x, y float64) Slot {
		t := pipRectTransform(x, y, slotW, slotH)
		return Slot{Transform: t, ZOrder: 0, Enabled: true}
	}

	return &Preset{
		ID:   "quad",
		Name: "Quad",
		Slots: []Slot{
			makeSlot(0, 0),
			makeSlot(slotW+gap, 0),
			makeSlot(0, slotH+gap),
			makeSlot(slotW+gap, slotH+gap),
		},
		TransitionMs: 300,
		Easing:       "ease-in-out",
	}
}

func builtinThreeBox(fw, fh float64) *Preset {
	gap := 4.0
	slotW := (fw - 2*gap) / 3
	slotH := fh

	makeSlot := func(x float64) Slot {
		t := DefaultTransform(x, 0, slotW, slotH)
		t.ScaleMode = ScaleModeFill
		t.CropAnchor = [2]float64{0.5, 0.5}
		return Slot{Transform: t, ZOrder: 0, Enabled: true}
	}

	return &Preset{
		ID:   "three-box",
		Name: "Three Box",
		Slots: []Slot{
			makeSlot(0),
			makeSlot(slotW + gap),
			makeSlot(2*slotW + 2*gap),
		},
		TransitionMs: 300,
		Easing:       "ease-in-out",
	}
}

func builtinOverShoulder(fw, fh float64) *Preset {
	pipW := fw * 0.25
	pipH := fh * 0.25
	margin := fw * 0.02
	x := fw - pipW - margin
	y := margin

	t := DefaultTransform(x, y, pipW, pipH)
	t.Border = whiteBorder(3, 8)
	t.Shadow = ShadowConfig{
		Enabled: true,
		OffsetX: 4,
		OffsetY: 4,
		Blur:    8,
		Opacity: 0.6,
	}
	t.RotationDeg = -3

	return &Preset{
		ID:   "over-shoulder",
		Name: "Over Shoulder",
		Slots: []Slot{
			{Transform: t, ZOrder: 1, Enabled: true},
		},
		TransitionMs: 300,
		Easing:       "ease-in-out",
	}
}
