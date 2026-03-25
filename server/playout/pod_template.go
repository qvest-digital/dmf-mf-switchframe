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

// BuiltinPodTemplates returns the 4 standard pod templates.
func BuiltinPodTemplates() map[string]PodTemplate {
	return map[string]PodTemplate{
		"quarter": {
			Type:              "quarter",
			DefaultDurationMs: 180000,
			AutoReturn:        true,
			AllowDumpOut:      true,
			Priority:          PriorityNormal,
		},
		"timeout": {
			Type:              "timeout",
			DefaultDurationMs: 60000,
			AutoReturn:        true,
			AllowDumpOut:      true,
			Priority:          PriorityNormal,
			Ephemeral:         true,
		},
		"injury": {
			Type:                  "injury",
			DefaultDurationMs:     0,
			AutoReturn:            false,
			AllowDumpOut:          true,
			Priority:              PriorityHigh,
			HoldUntilManualReturn: true,
		},
		"mandatory_sponsor": {
			Type:              "mandatory_sponsor",
			DefaultDurationMs: 120000,
			AutoReturn:        true,
			AllowDumpOut:      false,
			Priority:          PriorityMandatory,
			Contractual:       true,
		},
	}
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
