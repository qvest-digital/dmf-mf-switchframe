package playout

import (
	"fmt"
	"sync"
)

// PodTemplate defines a reusable pod type with default behavior.
type PodTemplate struct {
	Type                  string      `json:"type"`
	DefaultDurationMs     int64       `json:"default_duration_ms"`
	AutoReturn            bool        `json:"auto_return"`
	AllowDumpOut          bool        `json:"allow_dump_out"`
	Priority              PodPriority `json:"priority"`
	Contractual           bool        `json:"contractual,omitempty"`
	Ephemeral             bool        `json:"ephemeral,omitempty"`
	HoldUntilManualReturn bool        `json:"hold_until_manual_return,omitempty"`
}

// Instantiate creates a new Pod from this template with the given ID.
func (t *PodTemplate) Instantiate(id string) *Pod {
	return &Pod{
		ID: id,
		Config: PodConfig{
			Type:                  t.Type,
			DurationMs:            t.DefaultDurationMs,
			AutoReturn:            t.AutoReturn,
			AllowDumpOut:          t.AllowDumpOut,
			Priority:              t.Priority,
			Contractual:           t.Contractual,
			Ephemeral:             t.Ephemeral,
			HoldUntilManualReturn: t.HoldUntilManualReturn,
		},
		State: PodScheduled,
	}
}

// builtinTemplates are the generic pod templates that ship by default.
// Playlist manifests can override or extend these via PodTemplateDef entries.
var builtinTemplates = map[string]PodTemplate{
	"short": {
		Type:              "short",
		DefaultDurationMs: 30000,
		AutoReturn:        true,
		AllowDumpOut:      true,
		Priority:          PriorityNormal,
	},
	"standard": {
		Type:              "standard",
		DefaultDurationMs: 120000,
		AutoReturn:        true,
		AllowDumpOut:      true,
		Priority:          PriorityNormal,
	},
	"long": {
		Type:              "long",
		DefaultDurationMs: 300000,
		AutoReturn:        true,
		AllowDumpOut:      true,
		Priority:          PriorityNormal,
	},
}

// BuiltinPodTemplates returns the generic default pod templates.
func BuiltinPodTemplates() map[string]PodTemplate {
	out := make(map[string]PodTemplate, len(builtinTemplates))
	for k, v := range builtinTemplates {
		out[k] = v
	}
	return out
}

// PodTemplateStore manages pod templates (builtins + custom).
type PodTemplateStore struct {
	mu        sync.Mutex
	templates map[string]PodTemplate
}

// NewPodTemplateStore creates a store seeded with builtin templates.
func NewPodTemplateStore() *PodTemplateStore {
	return &PodTemplateStore{
		templates: BuiltinPodTemplates(),
	}
}

// List returns all templates.
func (s *PodTemplateStore) List() []PodTemplate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PodTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		out = append(out, t)
	}
	return out
}

// Get returns a template by type name.
func (s *PodTemplateStore) Get(typeName string) (PodTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[typeName]
	if !ok {
		return PodTemplate{}, fmt.Errorf("playout: pod template %q not found", typeName)
	}
	return t, nil
}

// Put creates or updates a template.
func (s *PodTemplateStore) Put(t PodTemplate) error {
	if t.Type == "" {
		return fmt.Errorf("playout: pod template type is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates[t.Type] = t
	return nil
}

// LoadFromPlaylist replaces the template store contents with templates
// derived from the playlist's PodTemplateDef entries. Builtins are
// re-seeded first; manifest definitions can override them.
func (s *PodTemplateStore) LoadFromPlaylist(defs []PodTemplateDef) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear existing non-builtin templates.
	s.templates = make(map[string]PodTemplate)

	// Load builtins first.
	for k, v := range builtinTemplates {
		s.templates[k] = v
	}

	// Override/add from playlist.
	for _, d := range defs {
		s.templates[d.Label] = PodTemplate{
			Type:              d.Label,
			DefaultDurationMs: d.DurationMs,
			AutoReturn:        d.AutoReturn,
			Priority:          d.Priority,
			AllowDumpOut:      true,
		}
	}
}

// Delete removes a template by type name.
func (s *PodTemplateStore) Delete(typeName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[typeName]; !ok {
		return fmt.Errorf("playout: pod template %q not found", typeName)
	}
	delete(s.templates, typeName)
	return nil
}
