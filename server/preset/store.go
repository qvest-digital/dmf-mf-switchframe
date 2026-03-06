// Package preset provides save/load/recall of named production setups.
// Presets capture the program/preview source, transition type, audio channel
// settings, and master level. They are persisted as JSON to disk.
package preset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Preset represents a saved production setup that can be recalled.
type Preset struct {
	ID              string                       `json:"id"`
	Name            string                       `json:"name"`
	ProgramSource   string                       `json:"programSource"`
	PreviewSource   string                       `json:"previewSource"`
	TransitionType  string                       `json:"transitionType"`
	TransitionDurMs int                          `json:"transitionDurMs"`
	AudioChannels   map[string]AudioChannelPreset `json:"audioChannels"`
	MasterLevel     float64                      `json:"masterLevel"`
	CreatedAt       time.Time                    `json:"createdAt"`
}

// AudioChannelPreset captures audio settings for a single channel in a preset.
type AudioChannelPreset struct {
	Level float64 `json:"level"`
	Muted bool    `json:"muted"`
	AFV   bool    `json:"afv"`
}

// ControlRoomSnapshot captures the relevant state for creating a preset.
type ControlRoomSnapshot struct {
	ProgramSource        string
	PreviewSource        string
	TransitionType       string
	TransitionDurationMs int
	AudioChannels        map[string]AudioChannelSnapshot
	MasterLevel          float64
}

// AudioChannelSnapshot captures audio channel state for a snapshot.
type AudioChannelSnapshot struct {
	Level float64
	Muted bool
	AFV   bool
}

// PresetUpdate holds optional fields for updating a preset.
type PresetUpdate struct {
	Name *string `json:"name,omitempty"`
}

var (
	// ErrNotFound is returned when a preset with the given ID does not exist.
	ErrNotFound = errors.New("preset: not found")

	// ErrEmptyName is returned when a preset name is empty.
	ErrEmptyName = errors.New("preset: name must not be empty")
)

// PresetStore manages CRUD operations and file persistence for presets.
type PresetStore struct {
	mu       sync.RWMutex
	presets  []Preset
	filePath string
}

// NewPresetStore creates a PresetStore that persists to the given file path.
// If the file exists, presets are loaded from it. If it does not exist, the
// store starts empty and the file is created on first mutation.
func NewPresetStore(filePath string) (*PresetStore, error) {
	ps := &PresetStore{
		filePath: filePath,
		presets:  []Preset{},
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return ps, nil
		}
		return nil, fmt.Errorf("read presets file: %w", err)
	}

	if err := json.Unmarshal(data, &ps.presets); err != nil {
		return nil, fmt.Errorf("parse presets file: %w", err)
	}
	if ps.presets == nil {
		ps.presets = []Preset{}
	}

	return ps, nil
}

// List returns all presets, ordered by creation time (oldest first).
func (ps *PresetStore) List() []Preset {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	result := make([]Preset, len(ps.presets))
	copy(result, ps.presets)
	return result
}

// Get returns a preset by ID, or false if not found.
func (ps *PresetStore) Get(id string) (Preset, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	for _, p := range ps.presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Create saves a new preset from the given name and state snapshot.
// Returns the created preset with a generated UUID and timestamp.
func (ps *PresetStore) Create(name string, state ControlRoomSnapshot) (Preset, error) {
	if name == "" {
		return Preset{}, ErrEmptyName
	}

	channels := make(map[string]AudioChannelPreset, len(state.AudioChannels))
	for k, ch := range state.AudioChannels {
		channels[k] = AudioChannelPreset(ch)
	}

	p := Preset{
		ID:              uuid.New().String(),
		Name:            name,
		ProgramSource:   state.ProgramSource,
		PreviewSource:   state.PreviewSource,
		TransitionType:  state.TransitionType,
		TransitionDurMs: state.TransitionDurationMs,
		AudioChannels:   channels,
		MasterLevel:     state.MasterLevel,
		CreatedAt:       time.Now(),
	}

	ps.mu.Lock()
	ps.presets = append(ps.presets, p)
	err := ps.save()
	ps.mu.Unlock()

	if err != nil {
		return Preset{}, fmt.Errorf("save presets: %w", err)
	}
	return p, nil
}

// Update modifies a preset's mutable fields. Currently only Name can be updated.
func (ps *PresetStore) Update(id string, updates PresetUpdate) (Preset, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	for i := range ps.presets {
		if ps.presets[i].ID == id {
			if updates.Name != nil {
				if *updates.Name == "" {
					return Preset{}, ErrEmptyName
				}
				ps.presets[i].Name = *updates.Name
			}
			if err := ps.save(); err != nil {
				return Preset{}, fmt.Errorf("save presets: %w", err)
			}
			return ps.presets[i], nil
		}
	}
	return Preset{}, ErrNotFound
}

// Delete removes a preset by ID.
func (ps *PresetStore) Delete(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	for i, p := range ps.presets {
		if p.ID == id {
			ps.presets = append(ps.presets[:i], ps.presets[i+1:]...)
			if err := ps.save(); err != nil {
				return fmt.Errorf("save presets: %w", err)
			}
			return nil
		}
	}
	return ErrNotFound
}

// save writes the current presets to disk atomically (temp file + rename).
// Must be called with ps.mu held (either read or write lock).
func (ps *PresetStore) save() error {
	data, err := json.MarshalIndent(ps.presets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal presets: %w", err)
	}

	dir := filepath.Dir(ps.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, "presets-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, ps.filePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}
